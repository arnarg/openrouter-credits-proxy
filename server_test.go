package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

const (
	testLocalKey = "sk-proxy-test-token"
	testMgmtKey  = "mgmt-secret-do-not-leak"
)

func newTestServer(t *testing.T, upstreamHandler http.HandlerFunc) *server {
	t.Helper()
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	return &server{
		cfg: config{
			managementKey: testMgmtKey,
			cacheTTL:      30 * time.Second,
			upstreamURL:   upstream.URL + "/api/v1/credits",
		},
		keys:       newAPIKeys([]string{testLocalKey}),
		httpClient: upstream.Client(),
	}
}

func doRequest(t *testing.T, s *server, method, path, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

func TestSuccessfulProxy(t *testing.T) {
	var gotAuth atomic.Value
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{"total_credits":10.5,"total_usage":4.2}}`)
	})

	rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "Bearer "+testLocalKey)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if auth := gotAuth.Load(); auth != "Bearer "+testMgmtKey {
		t.Fatalf("upstream Authorization = %q, want %q", auth, "Bearer "+testMgmtKey)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not JSON: %v", err)
	}
}

func TestInvalidKey(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called with an invalid local key")
	})
	rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "Bearer wrong-key")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestMissingAuthHeader(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called without credentials")
	})
	rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWrongPath(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called for unknown paths")
	})
	for _, path := range []string{"/", "/api/v1/credits/", "/api/v1/credits/extra"} {
		rec := doRequest(t, s, http.MethodGet, path, "Bearer "+testLocalKey)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %q: status = %d, want %d", path, rec.Code, http.StatusNotFound)
		}
	}
}

func TestWrongMethod(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("upstream should not be called for unsupported methods")
	})
	rec := doRequest(t, s, http.MethodPost, "/api/v1/credits", "Bearer "+testLocalKey)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
	if allow := rec.Header().Get("Allow"); allow != "GET, HEAD" {
		t.Fatalf("Allow header = %q, want %q", allow, "GET, HEAD")
	}
}

func TestHeadRequest(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"data":{}}`)
	})
	rec := doRequest(t, s, http.MethodHead, "/api/v1/credits", "Bearer "+testLocalKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("HEAD response must have no body, got %q", rec.Body.String())
	}
}

func TestCaching(t *testing.T) {
	var hits atomic.Int64
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, `{"data":{}}`)
	})

	for range 3 {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "Bearer "+testLocalKey)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("upstream hits = %d, want 1 (response should be cached)", hits.Load())
	}
}

func TestRateLimit(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":{}}`)
	})

	var rateLimited int
	for range rateLimitBurst + 5 {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "Bearer "+testLocalKey)
		if rec.Code == http.StatusTooManyRequests {
			rateLimited++
			if ra := rec.Header().Get("Retry-After"); ra == "" {
				t.Error("429 response must include Retry-After")
			}
		}
	}
	if rateLimited == 0 {
		t.Fatal("expected at least one 429 after exhausting the burst")
	}
}

func TestUpstreamErrorForwarded(t *testing.T) {
	var hits atomic.Int64
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	})

	for range 2 {
		rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "Bearer "+testLocalKey)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusInternalServerError)
		}
	}
	if hits.Load() != 2 {
		t.Fatalf("upstream hits = %d, want 2 (errors must not be cached)", hits.Load())
	}
}

func TestUpstreamNetworkError(t *testing.T) {
	s := newTestServer(t, func(w http.ResponseWriter, r *http.Request) {})
	s.cfg.upstreamURL = "http://127.0.0.1:1/api/v1/credits"

	rec := doRequest(t, s, http.MethodGet, "/api/v1/credits", "Bearer "+testLocalKey)
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}
