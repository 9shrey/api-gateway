package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// requestIDKey is the context key for the request ID.
type requestIDKey struct{}

// RequestID returns a middleware that assigns a unique request ID to every
// incoming request. If the client sends an X-Request-ID header, it is reused;
// otherwise a new random ID is generated.
//
// The ID is:
//   - Stored in the request context (retrieve via RequestIDFromContext)
//   - Set as the X-Request-ID response header
//   - Available for downstream logging and tracing
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := r.Header.Get("X-Request-ID")
			if id == "" {
				id = generateID()
			}

			// Set on response so the client can correlate
			w.Header().Set("X-Request-ID", id)

			// Inject into context for downstream use
			ctx := context.WithValue(r.Context(), requestIDKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequestIDFromContext retrieves the request ID from the context.
// Returns empty string if not present.
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// generateID produces a 16-character hex string (8 random bytes).
// This is fast and provides enough entropy for request tracing.
func generateID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
