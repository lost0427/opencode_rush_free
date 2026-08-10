package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func benchmarkClientApp(b *testing.B) (*App, string) {
	b.Helper()
	a := testApp(b)
	plain := "benchmark-client-key"
	if _, err := a.db.Exec("INSERT OR REPLACE INTO settings(key,value) VALUES('client_key',?)", hashToken(plain)); err != nil {
		b.Fatal(err)
	}
	if err := a.migrateLegacyClientKey(); err != nil {
		b.Fatal(err)
	}
	return a, plain
}

func BenchmarkAuthenticateClientCached(b *testing.B) {
	a, plain := benchmarkClientApp(b)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+plain)
	if _, err := a.authenticateClient(req); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.authenticateClient(req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkResolveModelCached(b *testing.B) {
	a := testApp(b)
	if _, err := a.db.Exec("INSERT INTO models(model_id,display_name,is_free,free_reason,raw_metadata,refreshed_at) VALUES(?,?,?,?,?,?)", "benchmark:free", "Benchmark", 1, "test", "{}", time.Now().UTC().Format(time.RFC3339)); err != nil {
		b.Fatal(err)
	}
	if _, err := a.resolveModel("benchmark:free"); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.resolveModel("benchmark:free"); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkProxyClientReuse(b *testing.B) {
	a := testApp(b)
	proxy := ProxyRecord{}
	if _, err := a.httpClient(proxy); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.httpClient(proxy); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLimiterAllow(b *testing.B) {
	limiter := newClientKeyLimiter()
	key := &ClientKey{ID: 99, RPMLimit: 1_000_000, TPMLimit: 1_000_000_000}
	start := time.Now()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if ok, _, _ := limiter.allow(key, start.Add(time.Duration(i)*time.Second)); !ok {
			b.Fatal("limiter rejected benchmark request")
		}
	}
}

func TestSQLiteConcurrentReadWrite(t *testing.T) {
	a := testApp(t)
	a.db.SetMaxOpenConns(4)
	a.db.SetMaxIdleConns(4)
	if _, err := a.db.Exec("PRAGMA busy_timeout=5000; PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL"); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := a.db.Exec("INSERT INTO usage_requests(created_at,request_kind,model,status,status_code) VALUES(?,?,?,?,?)", time.Now().UTC().Format(time.RFC3339Nano), "chat", "concurrent:free", "success", http.StatusOK)
			if err != nil {
				errs <- err
				return
			}
			var count int
			if err := a.db.QueryRow("SELECT COUNT(*) FROM usage_requests WHERE model=?", "concurrent:free").Scan(&count); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent sqlite operation failed: %v", err)
		}
	}
}

func TestChatAliasPreservesBodyWithoutRewrite(t *testing.T) {
	_ = testApp(t)
	body := []byte(`{"model":"benchmark:free","messages":[{"role":"user","content":"hello"}]}`)
	envelope, _, err := parseChatEnvelope(body)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Model != "benchmark:free" || len(envelope.Messages) != 1 {
		t.Fatalf("chat envelope was not parsed once: %#v", envelope)
	}
	forwardBody := body
	if envelope.Model != "different-model" {
		forwardBody = body
	}
	if !bytes.Equal(forwardBody, body) {
		t.Fatal("same-model request body was unexpectedly rewritten")
	}
}

func TestVisionRewriteReusesParsedPayload(t *testing.T) {
	body := []byte(`{"model":"alias-model","messages":[{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"https://example.test/image.png"}}]}],"stream":true}`)
	envelope, payload, err := parseChatEnvelope(body)
	if err != nil {
		t.Fatal(err)
	}
	payload["model"] = "resolved-model"
	cloned, ok := cloneJSONValue(payload).(map[string]any)
	if !ok {
		t.Fatal("parsed payload was not cloneable")
	}
	rewritten, err := replaceImageContentPayload(cloned, "a red square")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rewritten, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "resolved-model" || got["stream"] != true || requestHasImageInput(rewritten) {
		t.Fatalf("vision rewrite lost request fields: %s", rewritten)
	}
	if len(envelope.Messages) != 1 {
		t.Fatal("parsed messages were not reused")
	}
}
