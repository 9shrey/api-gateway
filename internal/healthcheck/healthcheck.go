package healthcheck

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Config holds health check parameters.
type Config struct {
	Interval time.Duration // how often to check each backend
	Timeout  time.Duration // per-backend check timeout
	Path     string        // health check endpoint path (e.g. "/health")
}

// DefaultConfig returns sensible health check defaults.
func DefaultConfig() Config {
	return Config{
		Interval: 10 * time.Second,
		Timeout:  3 * time.Second,
		Path:     "/health",
	}
}

// Status represents whether a backend is healthy.
type Status struct {
	Healthy    bool
	LastCheck  time.Time
	LastError  string
	StatusCode int
}

// Checker performs periodic health checks against backend servers
// and tracks their health status.
type Checker struct {
	cfg      Config
	client   *http.Client
	backends []*url.URL

	mu       sync.RWMutex
	statuses map[string]Status // keyed by backend URL string

	cancel context.CancelFunc
}

// NewChecker creates a health checker for the given backends.
func NewChecker(backends []*url.URL, cfg Config) *Checker {
	return &Checker{
		cfg: cfg,
		client: &http.Client{
			Timeout: cfg.Timeout,
		},
		backends: backends,
		statuses: make(map[string]Status),
	}
}

// Start begins periodic health checking in the background.
// Call Stop() to shut it down gracefully.
func (hc *Checker) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	hc.cancel = cancel

	// Run an initial check immediately
	hc.checkAll(ctx)

	go func() {
		ticker := time.NewTicker(hc.cfg.Interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				hc.checkAll(ctx)
			}
		}
	}()

	slog.Info("health checker started",
		"backends", len(hc.backends),
		"interval", hc.cfg.Interval,
		"path", hc.cfg.Path,
	)
}

// Stop terminates the health check loop.
func (hc *Checker) Stop() {
	if hc.cancel != nil {
		hc.cancel()
	}
}

// IsHealthy returns whether the given backend URL is considered healthy.
// Returns true if the backend has never been checked (optimistic default).
func (hc *Checker) IsHealthy(backend *url.URL) bool {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	status, ok := hc.statuses[backend.String()]
	if !ok {
		return true // never checked = assume healthy
	}
	return status.Healthy
}

// Statuses returns a snapshot of all backend health statuses.
func (hc *Checker) Statuses() map[string]Status {
	hc.mu.RLock()
	defer hc.mu.RUnlock()

	result := make(map[string]Status, len(hc.statuses))
	for k, v := range hc.statuses {
		result[k] = v
	}
	return result
}

// checkAll runs health checks against all backends concurrently.
func (hc *Checker) checkAll(ctx context.Context) {
	var wg sync.WaitGroup
	for _, backend := range hc.backends {
		wg.Add(1)
		go func(b *url.URL) {
			defer wg.Done()
			hc.checkOne(ctx, b)
		}(backend)
	}
	wg.Wait()
}

// checkOne performs a single health check against a backend.
func (hc *Checker) checkOne(ctx context.Context, backend *url.URL) {
	checkURL := *backend
	checkURL.Path = hc.cfg.Path

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL.String(), nil)
	if err != nil {
		hc.updateStatus(backend, Status{
			Healthy:   false,
			LastCheck: time.Now(),
			LastError: err.Error(),
		})
		return
	}

	resp, err := hc.client.Do(req)
	if err != nil {
		slog.Debug("health check failed",
			"backend", backend.String(),
			"error", err,
		)
		hc.updateStatus(backend, Status{
			Healthy:   false,
			LastCheck: time.Now(),
			LastError: err.Error(),
		})
		return
	}
	resp.Body.Close()

	healthy := resp.StatusCode >= 200 && resp.StatusCode < 300
	status := Status{
		Healthy:    healthy,
		LastCheck:  time.Now(),
		StatusCode: resp.StatusCode,
	}
	if !healthy {
		status.LastError = "unhealthy status code"
		slog.Warn("health check unhealthy",
			"backend", backend.String(),
			"status", resp.StatusCode,
		)
	}

	hc.updateStatus(backend, status)
}

func (hc *Checker) updateStatus(backend *url.URL, status Status) {
	hc.mu.Lock()
	defer hc.mu.Unlock()
	hc.statuses[backend.String()] = status
}
