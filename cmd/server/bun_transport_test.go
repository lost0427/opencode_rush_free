package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testBunBridge(t testing.TB) {
	t.Helper()
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Bun-Target-URL")
		method := r.Header.Get("X-Bun-Method")
		encodedHeaders, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Bun-Headers"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var headers http.Header
		if err := json.Unmarshal(encodedHeaders, &headers); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := io.ReadAll(r.Body)
		upstream, err := http.NewRequestWithContext(r.Context(), method, target, strings.NewReader(string(body)))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		upstream.Header = headers
		transport := http.DefaultTransport.(*http.Transport).Clone()
		if rawProxy := r.Header.Get("X-Bun-Proxy-URL"); rawProxy != "" {
			proxyURL, err := url.Parse(rawProxy)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if username := r.Header.Get("X-Bun-Proxy-Username"); username != "" {
				proxyURL.User = url.UserPassword(username, r.Header.Get("X-Bun-Proxy-Password"))
			}
			transport.Proxy = http.ProxyURL(proxyURL)
		} else {
			transport.Proxy = nil
		}
		response, err := (&http.Client{Transport: transport}).Do(upstream)
		if err != nil {
			w.Header().Set("X-Bun-Bridge-Error", "1")
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		for name, values := range response.Header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	t.Cleanup(bridge.Close)
	t.Setenv("BUN_BRIDGE_URL", bridge.URL)
}

func TestBunTransportForwardsUpstreamRequestAndProxyCredentials(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/request" {
			t.Fatalf("unexpected bridge request: %s %s", r.Method, r.URL)
		}
		if got := r.Header.Get("X-Bun-Target-URL"); got != "https://upstream.example.test/v1/chat/completions" {
			t.Fatalf("target URL = %q", got)
		}
		if got := r.Header.Get("X-Bun-Method"); got != http.MethodPost {
			t.Fatalf("upstream method = %q", got)
		}
		if got := r.Header.Get("X-Bun-Proxy-URL"); got != "http://proxy.example.test:7899" {
			t.Fatalf("proxy URL = %q", got)
		}
		if got := r.Header.Get("X-Bun-Proxy-Username"); got != "Default.account" {
			t.Fatalf("proxy username = %q", got)
		}
		if got := r.Header.Get("X-Bun-Proxy-Password"); got != "proxy-token" {
			t.Fatalf("proxy password = %q", got)
		}
		encodedHeaders, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Bun-Headers"))
		if err != nil {
			t.Fatal(err)
		}
		var headers http.Header
		if err := json.Unmarshal(encodedHeaders, &headers); err != nil || headers.Get("Authorization") != "Bearer token" {
			t.Fatalf("upstream headers = %#v, err=%v", headers, err)
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != `{"model":"free"}` {
			t.Fatalf("upstream body = %q", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("data: error\n\n"))
	}))
	defer bridge.Close()
	t.Setenv("BUN_BRIDGE_URL", bridge.URL+"/request")

	transport := newBunTransport(ProxyRecord{URI: "http://proxy.example.test:7899", Username: "Default.account", Password: "proxy-token"})
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://upstream.example.test/v1/chat/completions", io.NopCloser(strings.NewReader(`{"model":"free"}`)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer token")
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusTooManyRequests || response.Header.Get("Content-Type") != "text/event-stream" || response.Header.Get("Retry-After") != "1" {
		t.Fatalf("response metadata was not preserved: %#v", response)
	}
	if body, _ := io.ReadAll(response.Body); string(body) != "data: error\n\n" {
		t.Fatalf("response body = %q", body)
	}
}

func TestBunTransportPropagatesBridgeErrors(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Bun-Bridge-Error", "1")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("proxy connect failed"))
	}))
	defer bridge.Close()
	t.Setenv("BUN_BRIDGE_URL", bridge.URL)

	req := httptest.NewRequest(http.MethodGet, "https://upstream.example.test/models", nil)
	_, err := newBunTransport(ProxyRecord{}).RoundTrip(req)
	if err == nil || err.Error() != "bun bridge: proxy connect failed" {
		t.Fatalf("bridge error = %v", err)
	}
}
