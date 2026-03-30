package proxy

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/9shrey/api-gateway/internal/circuitbreaker"
)

// Config holds proxy behaviour settings for a single route.
type Config struct {
	MaxAttempts    int
	Timeout        time.Duration
	RetryBaseDelay time.Duration // base delay for exponential backoff
}

// ReverseProxy wraps the standard library reverse proxy and adds
// timeout handling, retry logic with backoff, circuit breaker integration,
// and header injection.
type ReverseProxy struct {
	cfg       Config
	transport *http.Transport
}

// New creates a ReverseProxy with the given configuration.
func New(cfg Config) *ReverseProxy {
	if cfg.RetryBaseDelay == 0 {
		cfg.RetryBaseDelay = 100 * time.Millisecond
	}
	return &ReverseProxy{
		cfg: cfg,
		transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   5 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 10,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

// Forward sends the incoming request to the target backend URL.
// If a circuit breaker is provided, it checks/updates its state.
func (rp *ReverseProxy) Forward(target *url.URL, cb *circuitbreaker.CircuitBreaker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rp.forwardWithRetry(w, r, target, cb)
	})
}

// forwardWithRetry attempts the request up to MaxAttempts times with
// exponential backoff between retries. It only retries on connection
// errors and 5xx responses, never on 4xx.
func (rp *ReverseProxy) forwardWithRetry(w http.ResponseWriter, r *http.Request, target *url.URL, cb *circuitbreaker.CircuitBreaker) {
	var lastErr error

	for attempt := 1; attempt <= rp.cfg.MaxAttempts; attempt++ {
		// Check circuit breaker before attempting
		if cb != nil {
			if err := cb.Allow(); err != nil {
				slog.Warn("circuit breaker open, skipping backend",
					"target", target.String(),
					"attempt", attempt,
				)
				http.Error(w, `{"error": "service unavailable", "reason": "circuit breaker open"}`, http.StatusServiceUnavailable)
				return
			}
		}

		// Create a per-attempt timeout context
		ctx, cancel := context.WithTimeout(r.Context(), rp.cfg.Timeout)
		reqWithTimeout := r.WithContext(ctx)

		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}

		err := rp.doProxy(recorder, reqWithTimeout, target)
		cancel()

		if err != nil {
			lastErr = err
			if cb != nil {
				cb.RecordFailure()
			}
			slog.Warn("proxy attempt failed",
				"attempt", attempt,
				"target", target.String(),
				"error", err,
			)
			if attempt < rp.cfg.MaxAttempts && isRetryable(err) {
				rp.backoff(r.Context(), attempt)
				continue
			}
			http.Error(w, `{"error": "bad gateway"}`, http.StatusBadGateway)
			return
		}

		// Retry on 5xx from the backend
		if recorder.statusCode >= 500 {
			if cb != nil {
				cb.RecordFailure()
			}
			if attempt < rp.cfg.MaxAttempts {
				slog.Warn("backend returned 5xx, retrying",
					"attempt", attempt,
					"status", recorder.statusCode,
					"target", target.String(),
				)
				rp.backoff(r.Context(), attempt)
				continue
			}
			// Last attempt — response already written
			return
		}

		// Success — record in circuit breaker and return
		if cb != nil {
			cb.RecordSuccess()
		}
		return
	}

	slog.Error("all proxy attempts exhausted",
		"target", target.String(),
		"last_error", lastErr,
	)
	http.Error(w, `{"error": "bad gateway"}`, http.StatusBadGateway)
}

// backoff waits with exponential backoff: baseDelay * 2^(attempt-1)
// capped at 2 seconds. Respects context cancellation.
func (rp *ReverseProxy) backoff(ctx context.Context, attempt int) {
	delay := time.Duration(float64(rp.cfg.RetryBaseDelay) * math.Pow(2, float64(attempt-1)))
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}

	slog.Debug("retry backoff", "delay", delay, "attempt", attempt)

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// doProxy performs a single reverse-proxy pass to the target.
func (rp *ReverseProxy) doProxy(w http.ResponseWriter, r *http.Request, target *url.URL) error {
	var proxyErr error

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host

			// Preserve the original path after the route prefix.
			// The route prefix is stripped by the router before we get here.
			if req.URL.Path == "" {
				req.URL.Path = "/"
			}

			// Standard proxy headers
			if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
				prior := req.Header.Get("X-Forwarded-For")
				if prior != "" {
					clientIP = prior + ", " + clientIP
				}
				req.Header.Set("X-Forwarded-For", clientIP)
			}
			req.Header.Set("X-Forwarded-Host", req.Host)
			req.Header.Set("X-Forwarded-Proto", "http")
		},
		ErrorHandler: func(_ http.ResponseWriter, _ *http.Request, err error) {
			proxyErr = err
		},
		Transport: rp.transport,
	}

	proxy.ServeHTTP(w, r)
	return proxyErr
}

// isRetryable returns true for errors that indicate a connection-level
// failure (worth retrying), not a logical error from the backend.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	// Connection refused, timeout, DNS errors
	var netErr net.Error
	if ok := errors.As(err, &netErr); ok {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "i/o timeout")
}

// responseRecorder captures the status code written by the reverse proxy
// so we can decide whether to retry.
type responseRecorder struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (rr *responseRecorder) WriteHeader(code int) {
	if !rr.wroteHeader {
		rr.statusCode = code
		rr.wroteHeader = true
		rr.ResponseWriter.WriteHeader(code)
	}
}

func (rr *responseRecorder) Write(b []byte) (int, error) {
	if !rr.wroteHeader {
		rr.WriteHeader(http.StatusOK)
	}
	return rr.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for streaming responses.
func (rr *responseRecorder) Flush() {
	if f, ok := rr.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
