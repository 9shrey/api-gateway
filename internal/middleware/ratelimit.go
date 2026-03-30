package middleware

import (
	"log/slog"
	"net"
	"net/http"

	"github.com/9shrey/api-gateway/internal/ratelimiter"
)

// RateLimit returns a middleware that enforces per-client rate limiting.
// It extracts the client identifier from the request (IP address by default)
// and checks with the RateLimiter whether the request should be allowed.
//
// If the rate limit is exceeded, it responds with 429 Too Many Requests
// and short-circuits the middleware chain (does not call the next handler).
//
// If the rate limiter backend (Redis) is unavailable, the request is allowed
// through (fail-open) to avoid blocking all traffic due to a Redis outage.
func RateLimit(limiter ratelimiter.RateLimiter) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract client identifier — use IP address
			clientKey := extractClientIP(r)

			allowed, err := limiter.Allow(r.Context(), clientKey)
			if err != nil {
				// Fail-open: if Redis is down, let the request through
				// but log the error so we can alert on it
				slog.Error("rate limiter error, failing open",
					"error", err,
					"client", clientKey,
					"path", r.URL.Path,
				)
				next.ServeHTTP(w, r)
				return
			}

			if !allowed {
				slog.Warn("rate limit exceeded",
					"client", clientKey,
					"path", r.URL.Path,
					"method", r.Method,
				)
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error": "rate limit exceeded", "retry_after": "1s"}`))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractClientIP gets the client's real IP address, checking
// X-Forwarded-For and X-Real-IP headers first (in case the gateway
// is behind a load balancer), then falling back to RemoteAddr.
func extractClientIP(r *http.Request) string {
	// Check X-Real-IP first (set by most reverse proxies)
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}

	// Check X-Forwarded-For (may contain comma-separated chain)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// First IP in the chain is the original client
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}

	// Fall back to direct connection IP
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
