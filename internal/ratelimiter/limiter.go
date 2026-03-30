package ratelimiter

import "context"

// RateLimiter decides whether a request identified by key should be allowed.
// Implementations must be safe for concurrent use.
type RateLimiter interface {
	// Allow checks if the request identified by key is within the rate limit.
	// Returns true if the request is allowed, false if it should be rejected.
	Allow(ctx context.Context, key string) (bool, error)
}
