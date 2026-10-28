package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	creditsPath        = "/api/v1/credits"
	maxUpstreamBody    = 1 << 20
	upstreamTimeout    = 15 * time.Second
	rateLimitInterval  = time.Second
	rateLimitBurst     = 5
	defaultContentType = "application/json"
)

type server struct {
	cfg        config
	keys       *apiKeys
	httpClient *http.Client

	mu    sync.Mutex
	cache *cachedCredits
}

type cachedCredits struct {
	body        []byte
	contentType string
	expiresAt   time.Time
}

func newServer(cfg config, keys *apiKeys) *server {
	return &server{
		cfg:  cfg,
		keys: keys,
		httpClient: &http.Client{
			Timeout: upstreamTimeout,
		},
	}
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != creditsPath {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	token, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "missing or malformed Authorization header")
		return
	}

	key, ok := s.keys.authenticate(token)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid API key")
		return
	}

	res := key.limiter.Reserve()
	if !res.OK() {
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}
	if delay := res.Delay(); delay > 0 {
		res.Cancel()
		w.Header().Set("Retry-After", retryAfter(delay))
		writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
		return
	}

	start := time.Now()
	status, body, contentType, err := s.credits(r.Context())
	if err != nil {
		slog.Error("upstream request failed", "error", err)
		writeError(w, http.StatusBadGateway, "upstream request failed")
		return
	}
	slog.Info("request served",
		"method", r.Method,
		"remote", r.RemoteAddr,
		"status", status,
		"duration", time.Since(start),
	)

	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		w.Write(body)
	}
}

// credits returns the cached upstream response if it is still fresh,
// otherwise it fetches from OpenRouter and refreshes the cache. The mutex
// is held across the upstream call so concurrent cache misses are served by
// a single request instead of hammering OpenRouter. The upstream status,
// body, and content type are forwarded transparently; only successful
// responses are cached.
func (s *server) credits(ctx context.Context) (int, []byte, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cache != nil && time.Now().Before(s.cache.expiresAt) {
		return http.StatusOK, s.cache.body, s.cache.contentType, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.upstreamURL, nil)
	if err != nil {
		return 0, nil, "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.managementKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return 0, nil, "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBody))
	if err != nil {
		return 0, nil, "", err
	}

	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = defaultContentType
	}

	if resp.StatusCode == http.StatusOK {
		s.cache = &cachedCredits{
			body:        body,
			contentType: contentType,
			expiresAt:   time.Now().Add(s.cfg.cacheTTL),
		}
	}

	return resp.StatusCode, body, contentType, nil
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

func retryAfter(delay time.Duration) string {
	seconds := int(delay.Seconds()) + 1
	if seconds < 1 {
		seconds = 1
	}
	return strconv.Itoa(seconds)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", defaultContentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{"message": message},
	}); err != nil {
		slog.Error("failed to write error response", "error", err)
	}
}
