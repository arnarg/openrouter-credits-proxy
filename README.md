# openrouter-credits-proxy

A minimal HTTP proxy that exposes OpenRouter's account credits endpoint
behind your own throwaway API keys, so less secure devices never need to
hold the (highly privileged) OpenRouter management key.

The proxy accepts `GET /api/v1/credits` with `Authorization: Bearer <local
key>` and forwards the request upstream with the management key it holds
privately. Everything else returns 404/405.

## Configuration (environment variables)

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `OPENROUTER_MANAGEMENT_KEY` | yes | - | OpenRouter management key. Kept server-side only, never sent to clients. |
| `PROXY_API_KEYS` | yes | - | Comma-separated local API keys, read once at startup. |
| `LISTEN_ADDR` | no | `:8080` | Listen address. |
| `CREDITS_CACHE_TTL` | no | `30s` | How long an upstream response is cached. |

## Running

```sh
export OPENROUTER_MANAGEMENT_KEY="sk-or-m-..."
export PROXY_API_KEYS="$(openssl rand -hex 32),$(openssl rand -hex 32)"
go run .
```

```sh
curl -H "Authorization: Bearer <local key>" http://localhost:8080/api/v1/credits
```

## Behavior

- Local keys are stored hashed (SHA-256) in memory and compared via digest
  lookup; plaintext keys are never retained after startup.
- Responses are cached for `CREDITS_CACHE_TTL` (default 30s); a cache miss
  triggers exactly one upstream request even under concurrent load.
- Each key is rate limited (1 req/s, burst 5); exceeded requests get 429
  with `Retry-After`.
- Non-200 upstream responses are forwarded as-is and never cached.
- Access logs contain no key material.
