# API Gateway

A lightweight, config-driven API gateway built from scratch in Go using only the standard library (plus Redis for distributed rate limiting).

Built this to understand how production gateways like Kong and AWS API Gateway work under the hood — reverse proxying, load balancing, auth, circuit breaking, the whole deal.

## What it does

- **Reverse proxy** — forwards client requests to backend services, rewrites headers (`X-Forwarded-For`, `X-Request-ID`, `Host`)
- **Round-robin load balancing** — lock-free (`atomic.Uint64`), health-aware (skips unhealthy backends, falls back if all are down)
- **Rate limiting** — distributed token bucket backed by Redis, uses a Lua script for atomic check-and-decrement
- **JWT authentication** — HMAC-SHA256, per-route toggle (`auth_required: true/false`), no external JWT library
- **Circuit breaker** — per-backend, three-state (closed → open → half-open), configurable failure/success thresholds
- **Active health checks** — periodic `/health` polling with concurrent checks across all backends
- **Retry with backoff** — exponential backoff per attempt (base_delay × 2^attempt, capped at 2s)
- **Middleware chain** — composable `func(http.Handler) http.Handler` pattern: request ID → logging → rate limit → (per-route auth) → proxy
- **Metrics** — Prometheus-compatible `/metrics` endpoint with per-route counters and latency histograms (7 buckets)
- **Health endpoint** — `/gateway/health` returns live backend status
- **Graceful shutdown** — listens for SIGINT/SIGTERM signals

Everything is driven by a single YAML config file.

## Project structure

```
cmd/gateway/           → entry point
internal/
  config/              → YAML config loader + validation
  gateway/             → HTTP server, router, route-to-backend wiring
  proxy/               → reverse proxy, retry logic, header rewriting
  loadbalancer/        → round-robin (interface + implementation)
  middleware/           → chain, logging, rate-limit, auth, request-id
  ratelimiter/         → token bucket interface + Redis implementation
  auth/                → JWT validation + token generation
  circuitbreaker/      → three-state circuit breaker
  healthcheck/         → periodic backend health polling
  metrics/             → request counters + latency histograms
config/                → gateway.yaml (local), gateway.docker.yaml (Docker)
examples/
  backend/             → simple echo server for testing
  tokengen/            → CLI to generate JWT tokens
```

## Quick start

### Local

You need Go 1.22+ and Redis running on `localhost:6379`.

```bash
# start Redis (if not already running)
redis-server

# start two backend servers
go run examples/backend/main.go -port 8081 -name backend-1 &
go run examples/backend/main.go -port 8082 -name backend-2 &

# start the gateway
go run cmd/gateway/main.go -config config/gateway.yaml
```

### Docker

```bash
docker-compose up --build
```

This spins up Redis, 3 backend instances, and the gateway. The gateway listens on `:8080`.

## Usage

**Public route (no auth):**

```bash
curl http://localhost:8080/api/health
```

**Protected route (needs JWT):**

```bash
# generate a token
go run examples/tokengen/main.go

# use it
curl -H "Authorization: Bearer <token>" http://localhost:8080/api/users
```

**Check metrics:**

```bash
curl http://localhost:8080/metrics
```

**Check backend health:**

```bash
curl http://localhost:8080/gateway/health
```

## Configuration

All routing, auth, rate limiting, and retry behaviour is controlled via `config/gateway.yaml`:

```yaml
server:
  port: 8080

redis:
  addr: "localhost:6379"

rate_limit:
  enabled: true
  rate: 10      # tokens per second
  burst: 20     # max burst size

auth:
  enabled: true
  jwt_secret: "change-me"
  issuer: "api-gateway"

routes:
  - path: "/api/users"
    auth_required: true
    backends:
      - "http://localhost:8081"
      - "http://localhost:8082"
    retry:
      max_attempts: 3
      timeout: 5s
      base_delay: 100ms
```

For Docker, use `config/gateway.docker.yaml` which references container hostnames instead of `localhost`.

## Design decisions

- **No framework** — pure `net/http`. Makes it easy to reason about the request lifecycle and keeps the binary small (~15MB).
- **Middleware as decorators** — `func(http.Handler) http.Handler` is idiomatic Go and composes cleanly. Auth is applied per-route inside the router, not globally, since different routes have different auth requirements.
- **Lua for rate limiting** — the token bucket runs as a single Lua script in Redis, so the read-check-decrement is atomic. No race conditions even with multiple gateway instances.
- **No external JWT library** — parsing a JWT is just base64 + HMAC + JSON. Doing it by hand avoids a dependency and makes it easier to explain in interviews.
- **Circuit breaker per backend** — if one backend starts failing, only that backend gets tripped. The load balancer routes around it while health checks wait for recovery.
- **Fail-open rate limiting** — if Redis goes down, requests are allowed through. Better to serve traffic without rate limits than to reject everything.

## Tech stack

- Go 1.22
- Redis 7 (via `go-redis/v9`)
- Docker + Docker Compose
- No web frameworks

## License

MIT
