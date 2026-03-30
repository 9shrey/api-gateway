package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/9shrey/api-gateway/internal/auth"
)

// claimsContextKey is an unexported type used as the context key for JWT claims.
// Using a custom type prevents collisions with keys from other packages.
type claimsContextKey struct{}

// Auth returns a middleware that validates JWT tokens from the Authorization header.
//
// It expects: Authorization: Bearer <token>
//
// On success, the validated claims are injected into the request context
// and can be retrieved by downstream handlers via ClaimsFromContext().
//
// On failure, it responds with 401 Unauthorized and short-circuits the chain.
func Auth(validator *auth.JWTValidator) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Extract the token from the Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				writeAuthError(w, "missing Authorization header")
				return
			}

			// Must be "Bearer <token>"
			if !strings.HasPrefix(authHeader, "Bearer ") {
				writeAuthError(w, "invalid Authorization format, expected: Bearer <token>")
				return
			}

			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")
			if tokenStr == "" {
				writeAuthError(w, "empty token")
				return
			}

			// Validate the JWT
			claims, err := validator.Validate(tokenStr)
			if err != nil {
				slog.Warn("auth: token validation failed",
					"error", err,
					"path", r.URL.Path,
					"client_ip", r.RemoteAddr,
				)
				writeAuthError(w, err.Error())
				return
			}

			slog.Debug("auth: token validated",
				"subject", claims.Subject,
				"path", r.URL.Path,
			)

			// Inject claims into request context for downstream use
			ctx := context.WithValue(r.Context(), claimsContextKey{}, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ClaimsFromContext retrieves the JWT claims from the request context.
// Returns nil if no claims are present (e.g., auth middleware was skipped).
func ClaimsFromContext(ctx context.Context) *auth.Claims {
	claims, _ := ctx.Value(claimsContextKey{}).(*auth.Claims)
	return claims
}

// writeAuthError sends a 401 JSON response.
func writeAuthError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="api-gateway"`)
	w.WriteHeader(http.StatusUnauthorized)
	w.Write([]byte(`{"error": "unauthorized", "message": "` + message + `"}`))
}
