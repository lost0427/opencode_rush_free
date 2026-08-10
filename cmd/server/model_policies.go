package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type ModelAlias struct {
	ID            int64     `json:"id"`
	Alias         string    `json:"alias"`
	TargetModelID string    `json:"target_model_id"`
	Enabled       bool      `json:"enabled"`
	CreatedAt     time.Time `json:"created_at"`
}

func (a *App) modelEnabled(modelID string) (bool, error) {
	models, _, err := a.loadModelRuntime()
	if err != nil {
		return false, err
	}
	model, ok := models[modelID]
	return ok && model.Record.AdminEnabled, nil
}

func (a *App) resolveModel(requested string) (string, error) {
	models, aliases, err := a.loadModelRuntime()
	if err != nil {
		return "", err
	}
	if model, ok := models[requested]; ok && model.Record.AdminEnabled {
		return requested, nil
	}
	target, ok := aliases[requested]
	if !ok {
		return "", errors.New("model is not an available enabled free model")
	}
	targetModel, exists := models[target]
	if !exists {
		return "", errors.New("model alias target is not currently available")
	}
	if !targetModel.Record.AdminEnabled {
		return "", errors.New("model is not an available enabled free model")
	}
	return target, nil
}

func rewriteRequestedModel(body []byte, target string) ([]byte, error) {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	payload["model"] = target
	return json.Marshal(payload)
}

func (a *App) patchModelPolicy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid model id"})
		return
	}
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if readJSON(r, &in) != nil || in.Enabled == nil {
		writeJSON(w, 400, map[string]string{"error": "enabled is required"})
		return
	}
	var modelID string
	if err := a.db.QueryRow("SELECT model_id FROM models WHERE id=? AND is_free=1", id).Scan(&modelID); errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "free model not found"})
		return
	} else if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not load model"})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := a.db.Exec("INSERT INTO model_policies(model_id,enabled,updated_at) VALUES(?,?,?) ON CONFLICT(model_id) DO UPDATE SET enabled=excluded.enabled,updated_at=excluded.updated_at", modelID, boolInt(*in.Enabled), now); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not update model policy"})
		return
	}
	a.invalidateModelRuntimeCache()
	writeJSON(w, 200, map[string]any{"model_id": modelID, "enabled": *in.Enabled})
}

func scanModelAlias(row interface{ Scan(...any) error }) (*ModelAlias, error) {
	var alias ModelAlias
	var enabled int
	var created string
	if err := row.Scan(&alias.ID, &alias.Alias, &alias.TargetModelID, &enabled, &created); err != nil {
		return nil, err
	}
	alias.Enabled = enabled == 1
	alias.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &alias, nil
}

func (a *App) listModelAliases(w http.ResponseWriter, _ *http.Request) {
	rows, err := a.db.Query("SELECT id,alias,target_model_id,enabled,created_at FROM model_aliases ORDER BY alias")
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not query model aliases"})
		return
	}
	defer rows.Close()
	out := []*ModelAlias{}
	for rows.Next() {
		item, scanErr := scanModelAlias(rows)
		if scanErr != nil {
			writeJSON(w, 500, map[string]string{"error": "could not read model aliases"})
			return
		}
		out = append(out, item)
	}
	writeJSON(w, 200, out)
}

func validAlias(value string) bool {
	return value != "" && len(value) <= 128 && !strings.ContainsAny(value, "\r\n\t ")
}

func (a *App) validateAliasTarget(alias, target string) error {
	if !validAlias(alias) {
		return errors.New("alias must be 1 to 128 characters without whitespace")
	}
	if alias == target {
		return errors.New("alias cannot target itself")
	}
	var count int
	if err := a.db.QueryRow("SELECT COUNT(*) FROM models WHERE model_id=?", alias).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return errors.New("alias conflicts with an upstream model id")
	}
	if err := a.db.QueryRow("SELECT COUNT(*) FROM models WHERE model_id=? AND is_free=1", target).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return errors.New("target must be an available free model")
	}
	return nil
}

func (a *App) createModelAlias(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Alias         string `json:"alias"`
		TargetModelID string `json:"target_model_id"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid model alias"})
		return
	}
	in.Alias = strings.TrimSpace(in.Alias)
	in.TargetModelID = strings.TrimSpace(in.TargetModelID)
	if err := a.validateAliasTarget(in.Alias, in.TargetModelID); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	result, err := a.db.Exec("INSERT INTO model_aliases(alias,target_model_id,enabled,created_at,updated_at) VALUES(?,?,1,?,?)", in.Alias, in.TargetModelID, now, now)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			writeJSON(w, 409, map[string]string{"error": "alias already exists"})
		} else {
			writeJSON(w, 500, map[string]string{"error": "could not create model alias"})
		}
		return
	}
	id, _ := result.LastInsertId()
	a.invalidateModelRuntimeCache()
	writeJSON(w, 201, map[string]any{"id": id, "alias": in.Alias, "target_model_id": in.TargetModelID, "enabled": true})
}

func (a *App) patchModelAlias(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid alias id"})
		return
	}
	var current ModelAlias
	var enabled int
	var created, updated string
	if err := a.db.QueryRow("SELECT id,alias,target_model_id,enabled,created_at,updated_at FROM model_aliases WHERE id=?", id).Scan(&current.ID, &current.Alias, &current.TargetModelID, &enabled, &created, &updated); errors.Is(err, sql.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "model alias not found"})
		return
	} else if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not load model alias"})
		return
	}
	current.Enabled = enabled == 1
	var in struct {
		TargetModelID *string `json:"target_model_id"`
		Enabled       *bool   `json:"enabled"`
	}
	if readJSON(r, &in) != nil {
		writeJSON(w, 400, map[string]string{"error": "invalid model alias update"})
		return
	}
	if in.TargetModelID != nil {
		current.TargetModelID = strings.TrimSpace(*in.TargetModelID)
	}
	if err := a.validateAliasTarget(current.Alias, current.TargetModelID); err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}
	if in.Enabled != nil {
		current.Enabled = *in.Enabled
	}
	if _, err := a.db.Exec("UPDATE model_aliases SET target_model_id=?,enabled=?,updated_at=? WHERE id=?", current.TargetModelID, boolInt(current.Enabled), time.Now().UTC().Format(time.RFC3339), id); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not update model alias"})
		return
	}
	a.invalidateModelRuntimeCache()
	writeJSON(w, 200, current)
}

func (a *App) deleteModelAlias(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, 400, map[string]string{"error": "invalid alias id"})
		return
	}
	result, err := a.db.Exec("DELETE FROM model_aliases WHERE id=?", id)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not delete model alias"})
		return
	}
	if n, _ := result.RowsAffected(); n == 0 {
		writeJSON(w, 404, map[string]string{"error": "model alias not found"})
		return
	}
	a.invalidateModelRuntimeCache()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *App) getModelRefreshSettings(w http.ResponseWriter, _ *http.Request) {
	minutes := 360
	if raw, err := settingValue(a.db, "model_refresh_minutes"); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not load model refresh settings"})
		return
	} else if raw != "" {
		if parsed, parseErr := strconv.Atoi(raw); parseErr == nil {
			minutes = parsed
		}
	}
	writeJSON(w, 200, map[string]int{"refresh_minutes": minutes})
}

func (a *App) putModelRefreshSettings(w http.ResponseWriter, r *http.Request) {
	var in struct {
		RefreshMinutes int `json:"refresh_minutes"`
	}
	if readJSON(r, &in) != nil || (in.RefreshMinutes != 0 && (in.RefreshMinutes < 60 || in.RefreshMinutes > 10080)) {
		writeJSON(w, 400, map[string]string{"error": "refresh_minutes must be 0 or between 60 and 10080"})
		return
	}
	if _, err := a.db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('model_refresh_minutes',?)", strconv.Itoa(in.RefreshMinutes)); err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not save model refresh settings"})
		return
	}
	writeJSON(w, 200, map[string]int{"refresh_minutes": in.RefreshMinutes})
}
