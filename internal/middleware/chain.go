package middleware

import "net/http"

// Middleware is a function that wraps an http.Handler with additional behaviour.
// This is the standard Go middleware signature used by Chi, Alice, and others.
type Middleware func(http.Handler) http.Handler

// Chain composes multiple middlewares into a single Middleware.
// Middlewares are applied in the order given: the first middleware in the
// slice is the outermost wrapper (executes first on the way in, last on the way out).
//
// Example:
//
//	chain := middleware.Chain(logging, rateLimit, auth)
//	handler := chain(router)
//
// Request flow: Logging → RateLimit → Auth → Router → Auth → RateLimit → Logging
func Chain(middlewares ...Middleware) Middleware {
	return func(final http.Handler) http.Handler {
		// Apply in reverse order so the first middleware wraps outermost
		for i := len(middlewares) - 1; i >= 0; i-- {
			final = middlewares[i](final)
		}
		return final
	}
}
