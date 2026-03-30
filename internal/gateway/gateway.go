package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/9shrey/api-gateway/internal/auth"
	"github.com/9shrey/api-gateway/internal/config"
	"github.com/9shrey/api-gateway/internal/healthcheck"
	"github.com/9shrey/api-gateway/internal/metrics"
	"github.com/9shrey/api-gateway/internal/middleware"
)

// Gateway is the main application struct. It owns the HTTP server,
// router, health checker, and the middleware chain.
type Gateway struct {
	server       *http.Server
	router       *Router
	cfg          *config.Config
	healthChecker *healthcheck.Checker
}

// Options holds optional dependencies for the gateway.
type Options struct {
	JWTValidator  *auth.JWTValidator     // nil = auth disabled
	Metrics       *metrics.Collector     // nil = no /metrics endpoint
	HealthChecker *healthcheck.Checker   // nil = no health checks
}

// New creates a Gateway from the given config.
// The middlewares are applied in order around the router (global chain).
// Per-route middlewares (like auth) are applied inside the router.
func New(cfg *config.Config, opts Options, middlewares ...middleware.Middleware) (*Gateway, error) {
	router, err := NewRouter(cfg.Routes, opts.JWTValidator)
	if err != nil {
		return nil, fmt.Errorf("gateway: %w", err)
	}

	// Build a top-level mux that handles internal endpoints (/metrics)
	// and delegates everything else to the middleware-wrapped router.
	mux := http.NewServeMux()

	// Mount metrics endpoint if collector is provided
	if opts.Metrics != nil {
		mux.Handle("GET /metrics", opts.Metrics)
		slog.Info("/metrics endpoint registered")
	}

	// Mount a gateway health endpoint showing backend statuses
	if opts.HealthChecker != nil {
		mux.HandleFunc("GET /gateway/health", func(w http.ResponseWriter, r *http.Request) {
			statuses := opts.HealthChecker.Statuses()
			w.Header().Set("Content-Type", "application/json")

			allHealthy := true
			for _, s := range statuses {
				if !s.Healthy {
					allHealthy = false
					break
				}
			}

			if !allHealthy {
				w.WriteHeader(http.StatusServiceUnavailable)
			}

			result := map[string]interface{}{
				"status":   "ok",
				"backends": statuses,
			}
			if !allHealthy {
				result["status"] = "degraded"
			}
			json.NewEncoder(w).Encode(result)
		})
		slog.Info("/gateway/health endpoint registered")
	}

	// Wrap the router with the global middleware chain
	var routerHandler http.Handler = router
	if len(middlewares) > 0 {
		chain := middleware.Chain(middlewares...)
		routerHandler = chain(router)
	}

	// All other paths go through the middleware chain → router
	mux.Handle("/", routerHandler)

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      mux,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	return &Gateway{
		server:        srv,
		router:        router,
		cfg:           cfg,
		healthChecker: opts.HealthChecker,
	}, nil
}

// Run starts the HTTP server and blocks until a shutdown signal is received.
// It performs graceful shutdown, giving in-flight requests time to complete.
func (g *Gateway) Run() error {
	// Start health checker if configured
	if g.healthChecker != nil {
		g.healthChecker.Start()
		defer g.healthChecker.Stop()
	}

	// Channel to catch OS signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// Start server in a goroutine
	errCh := make(chan error, 1)
	go func() {
		slog.Info("gateway started", "addr", g.server.Addr)
		if err := g.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Block until signal or server error
	select {
	case sig := <-quit:
		slog.Info("shutdown signal received", "signal", sig)
	case err := <-errCh:
		return fmt.Errorf("gateway: server error: %w", err)
	}

	// Graceful shutdown with a 15-second deadline
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	slog.Info("shutting down gracefully...")
	if err := g.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("gateway: shutdown error: %w", err)
	}

	slog.Info("gateway stopped")
	return nil
}
