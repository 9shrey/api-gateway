package loadbalancer

import "net/url"

// LoadBalancer selects the next backend server for a request.
// Implementations must be safe for concurrent use.
type LoadBalancer interface {
	// Next returns the next backend in the rotation.
	Next() *url.URL

	// Backends returns all backend URLs managed by this balancer.
	Backends() []*url.URL
}

// HealthChecker is an interface for checking backend health.
// Used by load balancers to skip unhealthy backends.
type HealthChecker interface {
	IsHealthy(backend *url.URL) bool
}
