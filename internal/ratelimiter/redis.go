package ratelimiter

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// luaTokenBucket is a Lua script executed atomically in Redis.
// It implements the token bucket algorithm:
//   - Tokens refill at a steady rate up to the burst capacity.
//   - Each request consumes one token.
//   - If no tokens are available, the request is denied.
//
// Keys[1]: the bucket key (e.g. "ratelimit:<client_ip>")
// ARGV[1]: burst capacity (max tokens)
// ARGV[2]: refill rate (tokens per second)
// ARGV[3]: current timestamp in microseconds (for precision)
//
// Returns 1 if allowed, 0 if denied.
var luaTokenBucket = redis.NewScript(`
local key = KEYS[1]
local capacity = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local now = tonumber(ARGV[3])

-- Get current bucket state
local bucket = redis.call("HMGET", key, "tokens", "last_refill")
local tokens = tonumber(bucket[1])
local last_refill = tonumber(bucket[2])

-- Initialize bucket on first request
if tokens == nil then
    tokens = capacity
    last_refill = now
end

-- Calculate token refill since last request
local elapsed = (now - last_refill) / 1000000  -- convert microseconds to seconds
local refill = elapsed * rate
tokens = math.min(capacity, tokens + refill)

-- Try to consume one token
if tokens >= 1 then
    tokens = tokens - 1
    redis.call("HMSET", key, "tokens", tokens, "last_refill", now)
    redis.call("EXPIRE", key, math.ceil(capacity / rate) + 1)
    return 1
else
    redis.call("HMSET", key, "tokens", tokens, "last_refill", now)
    redis.call("EXPIRE", key, math.ceil(capacity / rate) + 1)
    return 0
end
`)

// Config holds the token bucket parameters.
type Config struct {
	Rate  int // tokens refilled per second
	Burst int // maximum tokens (bucket capacity)
}

// RedisLimiter implements RateLimiter using a Redis-backed token bucket.
// It is safe for use across multiple gateway instances (distributed).
type RedisLimiter struct {
	client    *redis.Client
	cfg       Config
	keyPrefix string
}

// NewRedisLimiter creates a new Redis-backed rate limiter.
func NewRedisLimiter(client *redis.Client, cfg Config) *RedisLimiter {
	return &RedisLimiter{
		client:    client,
		cfg:       cfg,
		keyPrefix: "ratelimit:",
	}
}

// Allow checks if the request identified by key is within the rate limit.
// The key is typically the client IP or a user ID extracted from a JWT.
func (rl *RedisLimiter) Allow(ctx context.Context, key string) (bool, error) {
	bucketKey := rl.keyPrefix + key

	// Use microseconds for more precise token refill calculation
	now := time.Now().UnixMicro()

	result, err := luaTokenBucket.Run(ctx, rl.client,
		[]string{bucketKey},
		rl.cfg.Burst,
		rl.cfg.Rate,
		now,
	).Int()

	if err != nil {
		return false, fmt.Errorf("ratelimiter: redis eval: %w", err)
	}

	return result == 1, nil
}
