package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/9shrey/api-gateway/internal/metrics"
)

// Logging returns a middleware that logs every request with method, path,
// status code, latency, and request ID. It also records metrics into
// the provided Collector for the /metrics endpoint.
func Logging(collector *metrics.Collector) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Capture the original path before the router rewrites it
			originalPath := r.URL.Path

			// Wrap the ResponseWriter to capture the status code
			lrw := &loggingResponseWriter{
				ResponseWriter: w,
				statusCode:     http.StatusOK,
			}

			// Call the next handler in the chain
			next.ServeHTTP(lrw, r)

			// Calculate latency
			duration := time.Since(start)

			// Record into metrics collector
			collector.Record(r.Method, originalPath, lrw.statusCode, duration)

			// Log the request with request ID if present
			level := slog.LevelInfo
			if lrw.statusCode >= 500 {
				level = slog.LevelError
			} else if lrw.statusCode >= 400 {
				level = slog.LevelWarn
			}

			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", originalPath),
				slog.Int("status", lrw.statusCode),
				slog.Int64("latency_ms", duration.Milliseconds()),
				slog.String("latency", duration.String()),
				slog.String("client_ip", r.RemoteAddr),
			}

			// Include request ID if present
			if reqID := RequestIDFromContext(r.Context()); reqID != "" {
				attrs = append(attrs, slog.String("request_id", reqID))
			}

			// Include user agent for non-health-check paths
			if r.UserAgent() != "" {
				attrs = append(attrs, slog.String("user_agent", r.UserAgent()))
			}

			slog.LogAttrs(r.Context(), level, "request completed", attrs...)
		})
	}
}

// loggingResponseWriter wraps http.ResponseWriter to capture the status code.
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode  int
	wroteHeader bool
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	if !lrw.wroteHeader {
		lrw.statusCode = code
		lrw.wroteHeader = true
		lrw.ResponseWriter.WriteHeader(code)
	}
}

func (lrw *loggingResponseWriter) Write(b []byte) (int, error) {
	if !lrw.wroteHeader {
		lrw.WriteHeader(http.StatusOK)
	}
	return lrw.ResponseWriter.Write(b)
}

// Flush implements http.Flusher for streaming responses.
func (lrw *loggingResponseWriter) Flush() {
	if f, ok := lrw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
