package main

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type dimensionStat struct {
	Name            string  `json:"name"`
	Requests        int64   `json:"requests"`
	Success         int64   `json:"success"`
	ExternalErrors  int64   `json:"external_errors"`
	UserErrors      int64   `json:"user_errors"`
	Tokens          int64   `json:"tokens"`
	SuccessRate     float64 `json:"success_rate"`
	P50LatencyMS    int64   `json:"p50_latency_ms"`
	P95LatencyMS    int64   `json:"p95_latency_ms"`
	P50FirstTokenMS int64   `json:"p50_first_token_latency_ms"`
	P95FirstTokenMS int64   `json:"p95_first_token_latency_ms"`
	latencies       []int64
	firstTokens     []int64
}

func statsSince(r *http.Request) (string, error) {
	window := strings.TrimSpace(r.URL.Query().Get("window"))
	if window == "" || window == "all" {
		return "", nil
	}
	durations := map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}
	duration, ok := durations[window]
	if !ok {
		return "", strconv.ErrSyntax
	}
	return time.Now().UTC().Add(-duration).Format(time.RFC3339), nil
}

func percentile(values []int64, fraction float64) int64 {
	if len(values) == 0 {
		return 0
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	index := int(float64(len(values)-1)*fraction + 0.5)
	return values[index]
}

func (a *App) statsByDimension(w http.ResponseWriter, r *http.Request, dimension string) {
	since, err := statsSince(r)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": "window must be all, 1h, 24h, 7d, or 30d"})
		return
	}
	field := "model"
	if dimension == "proxy" {
		field = "COALESCE(NULLIF(proxy_uri,''),'direct')"
	}
	query := "SELECT " + field + ",status,COALESCE(NULLIF(error_origin,''),'user'),COALESCE(total_tokens,0),latency_ms,first_token_latency_ms FROM usage_requests WHERE request_kind='chat'"
	args := []any{}
	if since != "" {
		query += " AND created_at>=?"
		args = append(args, since)
	}
	rows, err := a.db.Query(query, args...)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": "could not query statistics"})
		return
	}
	defer rows.Close()
	groups := map[string]*dimensionStat{}
	for rows.Next() {
		var name, status, origin string
		var tokens, latency int64
		var first *int64
		if err := rows.Scan(&name, &status, &origin, &tokens, &latency, &first); err != nil {
			writeJSON(w, 500, map[string]string{"error": "could not read statistics"})
			return
		}
		entry := groups[name]
		if entry == nil {
			entry = &dimensionStat{Name: name}
			groups[name] = entry
		}
		entry.Requests++
		entry.Tokens += tokens
		if status == "success" {
			entry.Success++
		} else if origin == "external" {
			entry.ExternalErrors++
		} else {
			entry.UserErrors++
		}
		entry.latencies = append(entry.latencies, latency)
		if first != nil {
			entry.firstTokens = append(entry.firstTokens, *first)
		}
	}
	out := make([]dimensionStat, 0, len(groups))
	for _, entry := range groups {
		entry.SuccessRate = rate(entry.Success, entry.Requests-entry.ExternalErrors)
		entry.P50LatencyMS = percentile(entry.latencies, .50)
		entry.P95LatencyMS = percentile(entry.latencies, .95)
		entry.P50FirstTokenMS = percentile(entry.firstTokens, .50)
		entry.P95FirstTokenMS = percentile(entry.firstTokens, .95)
		entry.latencies = nil
		entry.firstTokens = nil
		out = append(out, *entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Requests > out[j].Requests })
	writeJSON(w, 200, out)
}

func (a *App) statsProxies(w http.ResponseWriter, r *http.Request) {
	a.statsByDimension(w, r, "proxy")
}
