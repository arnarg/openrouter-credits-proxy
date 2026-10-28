package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	defaultUpstreamURL = "https://openrouter.ai/api/v1/credits"
	defaultCacheTTL    = 30 * time.Second
	defaultListenAddr  = ":8080"
)

type config struct {
	listenAddr    string
	managementKey string
	apiKeys       []string
	cacheTTL      time.Duration
	upstreamURL   string
}

func loadConfig() (config, error) {
	cfg := config{
		listenAddr:  defaultListenAddr,
		upstreamURL: defaultUpstreamURL,
		cacheTTL:    defaultCacheTTL,
	}

	managementKey := os.Getenv("OPENROUTER_MANAGEMENT_KEY")
	if managementKey == "" {
		return config{}, errors.New("OPENROUTER_MANAGEMENT_KEY must be set")
	}
	cfg.managementKey = managementKey

	rawKeys := os.Getenv("PROXY_API_KEYS")
	if rawKeys == "" {
		return config{}, errors.New("PROXY_API_KEYS must be set (comma-separated local API keys)")
	}
	for _, part := range strings.Split(rawKeys, ",") {
		if part = strings.TrimSpace(part); part != "" {
			cfg.apiKeys = append(cfg.apiKeys, part)
		}
	}
	if len(cfg.apiKeys) == 0 {
		return config{}, errors.New("PROXY_API_KEYS must contain at least one key")
	}

	if addr := os.Getenv("LISTEN_ADDR"); addr != "" {
		cfg.listenAddr = addr
	}

	if raw := os.Getenv("CREDITS_CACHE_TTL"); raw != "" {
		ttl, err := time.ParseDuration(raw)
		if err != nil {
			return config{}, fmt.Errorf("invalid CREDITS_CACHE_TTL: %w", err)
		}
		if ttl < 0 {
			return config{}, errors.New("CREDITS_CACHE_TTL must not be negative")
		}
		cfg.cacheTTL = ttl
	}

	return cfg, nil
}
