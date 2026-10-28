package main

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"

	"golang.org/x/time/rate"
)

// apiKeys holds accepted local tokens hashed, so plaintext keys are never
// kept in memory. Lookup on the SHA-256 digest makes token comparison
// constant-time: an attacker cannot learn anything from partial matches of
// the digest, and Go map hashing is seeded per-map.
type apiKeys struct {
	mu   sync.RWMutex
	keys map[string]*apiKey
}

type apiKey struct {
	limiter *rate.Limiter
}

func newAPIKeys(tokens []string) *apiKeys {
	keys := make(map[string]*apiKey, len(tokens))
	for _, token := range tokens {
		keys[hashToken(token)] = &apiKey{
			limiter: rate.NewLimiter(rate.Limit(rateLimitInterval.Seconds()), rateLimitBurst),
		}
	}
	return &apiKeys{keys: keys}
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (a *apiKeys) authenticate(token string) (*apiKey, bool) {
	digest := hashToken(token)
	a.mu.RLock()
	defer a.mu.RUnlock()
	key, ok := a.keys[digest]
	return key, ok
}
