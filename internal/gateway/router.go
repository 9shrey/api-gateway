package gateway

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/9shrey/api-gateway/internal/auth"
	"github.com/9shrey/api-gateway/internal/circuitbreaker"
	"github.com/9shrey/api-gateway/internal/config"
	"github.com/9shrey/api-gateway/internal/loadbalancer"
	"github.com/9shrey/api-gateway/internal/middleware"
	"github.com/9shrey/api-gateway/internal/proxy"
)

// route is an internal representation of a configured route
// with its load balancer and proxy handler wired up.
type route struct {
	pathPrefix string
	handler    http.Handler // the final handler (proxy, possibly wrapped with auth)
}

// Router maps incoming request paths to backend handlers.
type Router struct {
	routes []route
}

// NewRouter builds a Router from the provided route configurations.
// Each route gets its own round-robin load balancer, reverse proxy,
// and per-backend circuit breakers.
func NewRouter(routes []config.RouteConfig, jwtValidator *auth.JWTValidator) (*Router, error) {
	var built []route

	for _, rc := range routes {
		// Parse backend URLs
		backends := make([]*url.URL, 0, len(rc.Backends))
		for _, raw := range rc.Backends {
			u, err := url.Parse(raw)
			if err != nil {
				return nil, fmt.Errorf("router: invalid backend URL %q: %w", raw, err)
			}
			backends = append(backends, u)
		}

		lb := loadbalancer.NewRoundRobin(backends)

		p := proxy.New(proxy.Config{
			MaxAttempts:    rc.Retry.MaxAttempts,
			Timeout:        rc.Retry.Timeout,
			RetryBaseDelay: rc.Retry.BaseDelay,
		})

		// Create a circuit breaker per backend
		cbMap := make(map[string]*circuitbreaker.CircuitBreaker, len(backends))
		cbCfg := circuitbreaker.DefaultConfig()
		for _, b := range backends {
			cbMap[b.String()] = circuitbreaker.New(b.String(), cbCfg)
		}

		// Build the per-route handler: load balancer + proxy + circuit breaker
		var handler http.Handler = &lbProxyHandler{lb: lb, proxy: p, circuitBreakers: cbMap}

		// Wrap with auth middleware if required for this route
		if rc.AuthRequired && jwtValidator != nil {
			handler = middleware.Auth(jwtValidator)(handler)
		}

		built = append(built, route{
			pathPrefix: rc.Path,
			handler:    handler,
		})

		slog.Info("route registered",
			"path", rc.Path,
			"backends", rc.Backends,
			"auth_required", rc.AuthRequired,
		)
	}

	return &Router{routes: built}, nil
}

// lbProxyHandler selects a backend via the load balancer and forwards the request,
// using the per-backend circuit breaker.
type lbProxyHandler struct {
	lb              loadbalancer.LoadBalancer
	proxy           *proxy.ReverseProxy
	circuitBreakers map[string]*circuitbreaker.CircuitBreaker
}

func (h *lbProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := h.lb.Next()
	cb := h.circuitBreakers[target.String()]
	h.proxy.Forward(target, cb).ServeHTTP(w, r)
}

// ServeHTTP implements http.Handler. It matches the request path
// against registered route prefixes and forwards to the appropriate backend.
func (rt *Router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for _, route := range rt.routes {
		if strings.HasPrefix(r.URL.Path, route.pathPrefix) {
			// Strip the route prefix so the backend sees the sub-path.
			originalPath := r.URL.Path
			r.URL.Path = strings.TrimPrefix(r.URL.Path, route.pathPrefix)
			if r.URL.Path == "" || r.URL.Path[0] != '/' {
				r.URL.Path = "/" + r.URL.Path
			}

			slog.Debug("routing request",
				"original_path", originalPath,
				"rewritten_path", r.URL.Path,
			)

			route.handler.ServeHTTP(w, r)
			return
		}
	}

	http.Error(w, `{"error": "no route matched"}`, http.StatusNotFound)
}
