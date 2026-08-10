package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type alertSettings struct {
	Enabled            bool     `json:"enabled"`
	WebhookURL         string   `json:"webhook_url"`
	HasWebhookSecret   bool     `json:"has_webhook_secret"`
	Events             []string `json:"events"`
	LowProxyThreshold  int      `json:"low_proxy_threshold"`
	SuccessRatePercent int      `json:"success_rate_percent"`
}

type alertDispatcher struct {
	app    *App
	client *http.Client
	mu     sync.Mutex
}

func newAlertDispatcher(app *App) *alertDispatcher {
	return &alertDispatcher{app: app, client: &http.Client{Timeout: 5 * time.Second}}
}

func defaultAlertSettings() alertSettings {
	return alertSettings{Enabled: false, Events: []string{"resin_unavailable", "proxy_pool_empty", "proxy_availability_low", "success_rate_low", "model_refresh_failed", "client_key_rate_limited"}, LowProxyThreshold: 3, SuccessRatePercent: 80}
}

func (a *App) loadAlertSettings() (alertSettings, string, error) {
	settings := defaultAlertSettings()
	var enabled, webhook, encryptedSecret, events, low, rate string
	for _, item := range []struct {
		key  string
		dest *string
	}{{"alerts_enabled", &enabled}, {"alerts_webhook_url", &webhook}, {"alerts_webhook_secret", &encryptedSecret}, {"alerts_events", &events}, {"alerts_low_proxy_threshold", &low}, {"alerts_success_rate_percent", &rate}} {
		value, err := settingValue(a.db, item.key)
		if err != nil {
			return settings, "", err
		}
		*item.dest = value
	}
	if enabled != "" {
		settings.Enabled, _ = strconv.ParseBool(enabled)
	}
	settings.WebhookURL = webhook
	settings.HasWebhookSecret = encryptedSecret != ""
	if events != "" {
		_ = json.Unmarshal([]byte(events), &settings.Events)
	}
	if n, err := strconv.Atoi(low); err == nil && n >= 1 && n <= 1000 {
		settings.LowProxyThreshold = n
	}
	if n, err := strconv.Atoi(rate); err == nil && n >= 1 && n <= 100 {
		settings.SuccessRatePercent = n
	}
	secret := ""
	if encryptedSecret != "" {
		var decryptErr error
		secret, decryptErr = a.decrypt(encryptedSecret)
		if decryptErr != nil {
			return settings, "", decryptErr
		}
	}
	return settings, secret, nil
}

func (a *App) getAlertSettings(w http.ResponseWriter, _ *http.Request) {
	settings, _, err := a.loadAlertSettings()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not load alert settings"})
		return
	}
	writeJSON(w, 200, settings)
}

func validAlertEvent(event string) bool {
	for _, candidate := range defaultAlertSettings().Events {
		if event == candidate {
			return true
		}
	}
	return false
}

func (a *App) putAlertSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled            bool     `json:"enabled"`
		WebhookURL         string   `json:"webhook_url"`
		WebhookSecret      *string  `json:"webhook_secret"`
		Events             []string `json:"events"`
		LowProxyThreshold  int      `json:"low_proxy_threshold"`
		SuccessRatePercent int      `json:"success_rate_percent"`
	}
	if readJSON(r, &in) != nil || in.LowProxyThreshold < 1 || in.LowProxyThreshold > 1000 || in.SuccessRatePercent < 1 || in.SuccessRatePercent > 100 {
		writeJSON(w, 400, map[string]string{"error": "invalid alert settings"})
		return
	}
	parsed, err := url.Parse(strings.TrimSpace(in.WebhookURL))
	if in.Enabled && (err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "") {
		writeJSON(w, 400, map[string]string{"error": "an http or https webhook_url is required when alerts are enabled"})
		return
	}
	for _, event := range in.Events {
		if !validAlertEvent(event) {
			writeJSON(w, 400, map[string]string{"error": "unsupported alert event"})
			return
		}
	}
	if len(in.Events) == 0 {
		in.Events = defaultAlertSettings().Events
	}
	_, currentSecret, err := a.loadAlertSettings()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not load alert settings"})
		return
	}
	if in.WebhookSecret != nil {
		currentSecret = strings.TrimSpace(*in.WebhookSecret)
	}
	encryptedSecret := ""
	if currentSecret != "" {
		encryptedSecret, err = a.encrypt(currentSecret)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": "could not encrypt webhook secret"})
			return
		}
	}
	events, _ := json.Marshal(in.Events)
	_, err = a.db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('alerts_enabled',?),('alerts_webhook_url',?),('alerts_webhook_secret',?),('alerts_events',?),('alerts_low_proxy_threshold',?),('alerts_success_rate_percent',?)", strconv.FormatBool(in.Enabled), strings.TrimSpace(in.WebhookURL), encryptedSecret, string(events), strconv.Itoa(in.LowProxyThreshold), strconv.Itoa(in.SuccessRatePercent))
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not save alert settings"})
		return
	}
	settings, _, _ := a.loadAlertSettings()
	writeJSON(w, 200, settings)
}

func eventEnabled(settings alertSettings, event string) bool {
	for _, candidate := range settings.Events {
		if candidate == event {
			return true
		}
	}
	return false
}

func (a *App) emitAlert(eventType, severity, summary string, details map[string]any) {
	if a.alerts == nil {
		return
	}
	settings, _, err := a.loadAlertSettings()
	if err != nil || !settings.Enabled || !eventEnabled(settings, eventType) {
		return
	}
	now := time.Now().UTC()
	var existing int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM alert_events WHERE dedupe_key=? AND created_at>=?", eventType, now.Add(-15*time.Minute).Format(time.RFC3339)).Scan(&existing); err != nil || existing > 0 {
		return
	}
	payload, _ := json.Marshal(map[string]any{"version": 1, "event_id": randomEventID(), "type": eventType, "severity": severity, "occurred_at": now.Format(time.RFC3339), "summary": summary, "data": details})
	if _, err := a.db.Exec("INSERT INTO alert_events(dedupe_key,event_type,severity,payload,status,attempts,next_attempt_at,created_at) VALUES(?,?,?,?, 'pending',0,?,?)", eventType, eventType, severity, string(payload), now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		log.Printf("queue alert failed: %v", err)
		return
	}
	// Delivery runs outside the request path. This performs the initial
	// immediate attempt while retries remain durable in the outbox.
	go a.alerts.deliverDue()
}

func randomEventID() string {
	key, err := randomKey()
	if err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return key
}

func (d *alertDispatcher) run() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		d.deliverDue()
	}
}

func (d *alertDispatcher) deliverDue() {
	d.mu.Lock()
	defer d.mu.Unlock()
	settings, secret, err := d.app.loadAlertSettings()
	if err != nil || !settings.Enabled || settings.WebhookURL == "" {
		return
	}
	rows, err := d.app.db.Query("SELECT id,payload,attempts FROM alert_events WHERE status='pending' AND next_attempt_at<=? ORDER BY id LIMIT 20", time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var payload string
		var attempts int
		if rows.Scan(&id, &payload, &attempts) != nil {
			continue
		}
		req, err := http.NewRequest(http.MethodPost, settings.WebhookURL, bytes.NewBufferString(payload))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			mac := hmac.New(sha256.New, []byte(secret))
			_, _ = mac.Write([]byte(payload))
			req.Header.Set("X-RelayDesk-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
			resp, requestErr := d.client.Do(req)
			if resp != nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
				_ = resp.Body.Close()
			}
			if requestErr == nil && resp != nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
				_, _ = d.app.db.Exec("UPDATE alert_events SET status='delivered',attempts=?,delivered_at=?,last_error='' WHERE id=?", attempts+1, time.Now().UTC().Format(time.RFC3339), id)
				continue
			}
			if requestErr != nil {
				err = requestErr
			} else if resp != nil {
				err = errors.New("webhook returned " + resp.Status)
			}
		}
		attempts++
		if attempts >= 4 {
			_, _ = d.app.db.Exec("UPDATE alert_events SET status='failed',attempts=?,last_error=? WHERE id=?", attempts, truncateError(errString(err)), id)
			continue
		}
		delays := []time.Duration{time.Minute, 5 * time.Minute, 30 * time.Minute}
		_, _ = d.app.db.Exec("UPDATE alert_events SET attempts=?,next_attempt_at=?,last_error=? WHERE id=?", attempts, time.Now().UTC().Add(delays[attempts-1]).Format(time.RFC3339), truncateError(errString(err)), id)
	}
}

func errString(err error) string {
	if err == nil {
		return "webhook delivery failed"
	}
	return err.Error()
}

func (a *App) modelRefreshJanitor() {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		interval := 6 * time.Hour
		if raw, _ := settingValue(a.db, "model_refresh_minutes"); raw != "" {
			if minutes, err := strconv.Atoi(raw); err == nil {
				if minutes == 0 {
					continue
				}
				if minutes >= 60 && minutes <= 10080 {
					interval = time.Duration(minutes) * time.Minute
				}
			}
		}
		cfg, err := a.loadUpstream()
		if err != nil || cfg.BaseURL == "" || (cfg.LastRefresh != nil && time.Since(*cfg.LastRefresh) < interval) {
			continue
		}
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/settings/models/refresh", nil)
		a.refreshModels(recorder, req)
	}
}
