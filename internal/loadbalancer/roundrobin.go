package loadbalancer

import (
	"net/url"
	"sync/atomic"
)

// RoundRobin distributes requests evenly across backends.
// It uses an atomic counter — no mutex needed on the hot path.
// If a HealthChecker is provided, unhealthy backends are skipped.
type RoundRobin struct {
	backends []*url.URL
	counter  atomic.Uint64
	health   HealthChecker // optional — nil means all backends are assumed healthy
}

// NewRoundRobin creates a round-robin balancer from the given backend URLs.
// It panics if backends is empty (caller must validate config).
func NewRoundRobin(backends []*url.URL) *RoundRobin {
	if len(backends) == 0 {
		panic("loadbalancer: round-robin requires at least one backend")
	}
	return &RoundRobin{
		backends: backends,
	}
}

// SetHealthChecker attaches a health checker for health-aware load balancing.
func (rr *RoundRobin) SetHealthChecker(hc HealthChecker) {
	rr.health = hc
}

// Next returns the next healthy backend in round-robin order.
// If all backends are unhealthy, falls back to the next in rotation
// (better to try than to reject immediately).
// Safe for concurrent use from multiple goroutines.
func (rr *RoundRobin) Next() *url.URL {
	n := uint64(len(rr.backends))
	start := rr.counter.Add(1) - 1

	// If no health checker, pure round-robin
	if rr.health == nil {
		return rr.backends[start%n]
	}

	// Try to find a healthy backend within one full rotation
	for i := uint64(0); i < n; i++ {
		idx := (start + i) % n
		backend := rr.backends[idx]
		if rr.health.IsHealthy(backend) {
			return backend
		}
	}

	// All unhealthy — fall back to round-robin (best effort)
	return rr.backends[start%n]
}

// Backends returns all backend URLs managed by this balancer.
func (rr *RoundRobin) Backends() []*url.URL {
	return rr.backends
}
