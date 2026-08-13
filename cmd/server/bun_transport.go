package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const bunBridgeURL = "http://127.0.0.1:8787/request"

type bunTransport struct {
	proxy  ProxyRecord
	client *http.Client
}

func (t *bunTransport) CloseIdleConnections() { t.client.CloseIdleConnections() }

func (t *bunTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		var err error
		body, err = io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
	}
	bridgeReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, getenv("BUN_BRIDGE_URL", bunBridgeURL), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	encodedHeaders, err := json.Marshal(r.Header)
	if err != nil {
		return nil, err
	}
	bridgeReq.Header.Set("X-Bun-Target-URL", r.URL.String())
	bridgeReq.Header.Set("X-Bun-Method", r.Method)
	bridgeReq.Header.Set("X-Bun-Headers", base64.StdEncoding.EncodeToString(encodedHeaders))
	bridgeReq.Header.Set("X-Bun-Proxy-URL", t.proxy.URI)
	bridgeReq.Header.Set("X-Bun-Proxy-Username", t.proxy.Username)
	bridgeReq.Header.Set("X-Bun-Proxy-Password", t.proxy.Password)
	bridgeReq.Header.Set("Content-Type", "application/octet-stream")
	resp, err := t.client.Do(bridgeReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusBadGateway && resp.Header.Get("X-Bun-Bridge-Error") != "" {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("bun bridge: %s", strings.TrimSpace(string(message)))
	}
	return resp, nil
}

func newBunTransport(p ProxyRecord) *bunTransport {
	return &bunTransport{proxy: p, client: &http.Client{Transport: &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     true,
		MaxConnsPerHost:       64,
		MaxIdleConns:          256,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       upstreamRequestTimeout,
		ResponseHeaderTimeout: upstreamRequestTimeout,
	}}}
}
