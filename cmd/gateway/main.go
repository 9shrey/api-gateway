package main

import (
	"context"
	"flag"
	"log/slog"
	"net/url"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/9shrey/api-gateway/internal/auth"
	"github.com/9shrey/api-gateway/internal/config"
	"github.com/9shrey/api-gateway/internal/gateway"
	"github.com/9shrey/api-gateway/internal/healthcheck"
	"github.com/9shrey/api-gateway/internal/metrics"
	"github.com/9shrey/api-gateway/internal/middleware"
	"github.com/9shrey/api-gateway/internal/ratelimiter"
)

func main() {
	// Parse command-line flags
	configPath := flag.String("config", "config/gateway.yaml", "path to gateway config file")
	flag.Parse()

	// Set up structured logging (JSON in production, text for dev)
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}
	slog.Info("config loaded",
		"port", cfg.Server.Port,
		"routes", len(cfg.Routes),
	)

	// Build the middleware chain
	collector := metrics.NewCollector()
	chain := []middleware.Middleware{
		middleware.RequestID(),        // outermost — assigns request ID first
		middleware.Logging(collector),  // logs every request + records metrics
	}

	// Set up rate limiting if enabled
	if cfg.RateLimit.Enabled {
		rdb := redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})

		// Verify Redis connectivity at startup
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer pingCancel()
		if err := rdb.Ping(pingCtx).Err(); err != nil {
			slog.Error("failed to connect to Redis", "addr", cfg.Redis.Addr, "error", err)
			os.Exit(1)
		}
		slog.Info("redis connected", "addr", cfg.Redis.Addr)

		limiter := ratelimiter.NewRedisLimiter(rdb, ratelimiter.Config{
			Rate:  cfg.RateLimit.Rate,
			Burst: cfg.RateLimit.Burst,
		})
		chain = append(chain, middleware.RateLimit(limiter))
		slog.Info("rate limiting enabled", "rate", cfg.RateLimit.Rate, "burst", cfg.RateLimit.Burst)
	}

	// Set up JWT authentication if enabled
	var gwOpts gateway.Options
	gwOpts.Metrics = collector
	if cfg.Auth.Enabled {
		gwOpts.JWTValidator = auth.NewJWTValidator(cfg.Auth.JWTSecret, cfg.Auth.Issuer)
		slog.Info("jwt auth enabled", "issuer", cfg.Auth.Issuer)
	}

	// Set up health checker for all unique backends
	allBackends := collectBackends(cfg.Routes)
	if len(allBackends) > 0 {
		hc := healthcheck.NewChecker(allBackends, healthcheck.DefaultConfig())
		gwOpts.HealthChecker = hc
		slog.Info("health checker configured", "backends", len(allBackends))
	}

	slog.Info("middleware chain built", "count", len(chain))

	// Build and run the gateway
	gw, err := gateway.New(cfg, gwOpts, chain...)
	if err != nil {
		slog.Error("failed to create gateway", "error", err)
		os.Exit(1)
	}

	if err := gw.Run(); err != nil {
		slog.Error("gateway error", "error", err)
		os.Exit(1)
	}
}

// collectBackends gathers all unique backend URLs across all routes.
func collectBackends(routes []config.RouteConfig) []*url.URL {
	seen := make(map[string]struct{})
	var backends []*url.URL

	for _, rc := range routes {
		for _, raw := range rc.Backends {
			if _, ok := seen[raw]; ok {
				continue
			}
			seen[raw] = struct{}{}
			u, err := url.Parse(raw)
			if err != nil {
				continue
			}
			backends = append(backends, u)
		}
	}
	return backends
}
