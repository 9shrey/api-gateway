package metrics

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Collector gathers request metrics broken down by route, method, and status.
// It exposes data in Prometheus text exposition format via ServeHTTP.
// All operations are safe for concurrent use.
type Collector struct {
	// Global counters
	totalRequests atomic.Int64
	totalErrors   atomic.Int64 // status >= 500

	// Per-key counters: "method:path:status_class" → count
	// status_class is "2xx", "3xx", "4xx", "5xx"
	requestCounts sync.Map // map[string]*atomic.Int64

	// Latency tracking per route
	latencies sync.Map // map[string]*latencyBucket (key = "method:path")

	startTime time.Time
}

// latencyBucket tracks latency distribution for a route.
type latencyBucket struct {
	count    atomic.Int64
	totalMs  atomic.Int64
	buckets  [7]atomic.Int64 // <=5ms, <=10ms, <=25ms, <=50ms, <=100ms, <=500ms, >500ms
}

var bucketBounds = [6]int64{5, 10, 25, 50, 100, 500} // milliseconds

// NewCollector creates a new metrics collector.
func NewCollector() *Collector {
	return &Collector{
		startTime: time.Now(),
	}
}

// Record logs a completed request into the metrics.
func (c *Collector) Record(method, path string, statusCode int, latency time.Duration) {
	c.totalRequests.Add(1)
	if statusCode >= 500 {
		c.totalErrors.Add(1)
	}

	// Per-route+method+status counter
	statusClass := statusClassOf(statusCode)
	countKey := method + ":" + path + ":" + statusClass
	counter := c.getOrCreateCounter(countKey)
	counter.Add(1)

	// Latency tracking
	latencyKey := method + ":" + path
	bucket := c.getOrCreateLatency(latencyKey)
	ms := latency.Milliseconds()
	bucket.count.Add(1)
	bucket.totalMs.Add(ms)

	// Histogram bucket
	placed := false
	for i, bound := range bucketBounds {
		if ms <= bound {
			bucket.buckets[i].Add(1)
			placed = true
			break
		}
	}
	if !placed {
		bucket.buckets[6].Add(1) // >500ms
	}
}

// Snapshot returns a human-readable summary of all metrics.
func (c *Collector) Snapshot() map[string]interface{} {
	total := c.totalRequests.Load()
	errors := c.totalErrors.Load()

	var errorRate float64
	if total > 0 {
		errorRate = float64(errors) / float64(total) * 100.0
	}

	result := map[string]interface{}{
		"uptime_seconds": int64(time.Since(c.startTime).Seconds()),
		"total_requests": total,
		"total_errors":   errors,
		"error_rate_pct": errorRate,
	}

	return result
}

// ServeHTTP exposes metrics in Prometheus text exposition format.
// Mount this on a /metrics endpoint.
func (c *Collector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	var b strings.Builder

	// Uptime
	uptime := time.Since(c.startTime).Seconds()
	fmt.Fprintf(&b, "# HELP gateway_uptime_seconds Time since the gateway started.\n")
	fmt.Fprintf(&b, "# TYPE gateway_uptime_seconds gauge\n")
	fmt.Fprintf(&b, "gateway_uptime_seconds %.1f\n\n", uptime)

	// Total requests
	total := c.totalRequests.Load()
	fmt.Fprintf(&b, "# HELP gateway_requests_total Total number of requests processed.\n")
	fmt.Fprintf(&b, "# TYPE gateway_requests_total counter\n")
	fmt.Fprintf(&b, "gateway_requests_total %d\n\n", total)

	// Total errors
	errors := c.totalErrors.Load()
	fmt.Fprintf(&b, "# HELP gateway_errors_total Total number of 5xx responses.\n")
	fmt.Fprintf(&b, "# TYPE gateway_errors_total counter\n")
	fmt.Fprintf(&b, "gateway_errors_total %d\n\n", errors)

	// Per-route request counts
	fmt.Fprintf(&b, "# HELP gateway_route_requests_total Requests broken down by method, path, and status class.\n")
	fmt.Fprintf(&b, "# TYPE gateway_route_requests_total counter\n")
	c.requestCounts.Range(func(key, val interface{}) bool {
		k := key.(string)
		v := val.(*atomic.Int64)
		parts := strings.SplitN(k, ":", 3)
		if len(parts) == 3 {
			fmt.Fprintf(&b, "gateway_route_requests_total{method=%q,path=%q,status=%q} %d\n",
				parts[0], parts[1], parts[2], v.Load())
		}
		return true
	})
	b.WriteString("\n")

	// Latency histograms per route
	fmt.Fprintf(&b, "# HELP gateway_request_duration_ms Request latency histogram buckets.\n")
	fmt.Fprintf(&b, "# TYPE gateway_request_duration_ms histogram\n")

	// Collect and sort latency keys for deterministic output
	var latencyKeys []string
	c.latencies.Range(func(key, _ interface{}) bool {
		latencyKeys = append(latencyKeys, key.(string))
		return true
	})
	sort.Strings(latencyKeys)

	bucketLabels := []string{"5", "10", "25", "50", "100", "500", "+Inf"}
	for _, k := range latencyKeys {
		val, ok := c.latencies.Load(k)
		if !ok {
			continue
		}
		bucket := val.(*latencyBucket)
		parts := strings.SplitN(k, ":", 2)
		if len(parts) != 2 {
			continue
		}
		method, path := parts[0], parts[1]

		var cumulative int64
		for i := range bucket.buckets {
			cumulative += bucket.buckets[i].Load()
			fmt.Fprintf(&b, "gateway_request_duration_ms_bucket{method=%q,path=%q,le=%q} %d\n",
				method, path, bucketLabels[i], cumulative)
		}

		count := bucket.count.Load()
		totalMs := bucket.totalMs.Load()
		fmt.Fprintf(&b, "gateway_request_duration_ms_sum{method=%q,path=%q} %d\n", method, path, totalMs)
		fmt.Fprintf(&b, "gateway_request_duration_ms_count{method=%q,path=%q} %d\n", method, path, count)
	}

	w.Write([]byte(b.String()))
}

func (c *Collector) getOrCreateCounter(key string) *atomic.Int64 {
	if val, ok := c.requestCounts.Load(key); ok {
		return val.(*atomic.Int64)
	}
	counter := &atomic.Int64{}
	actual, _ := c.requestCounts.LoadOrStore(key, counter)
	return actual.(*atomic.Int64)
}

func (c *Collector) getOrCreateLatency(key string) *latencyBucket {
	if val, ok := c.latencies.Load(key); ok {
		return val.(*latencyBucket)
	}
	bucket := &latencyBucket{}
	actual, _ := c.latencies.LoadOrStore(key, bucket)
	return actual.(*latencyBucket)
}

func statusClassOf(code int) string {
	switch {
	case code < 200:
		return "1xx"
	case code < 300:
		return "2xx"
	case code < 400:
		return "3xx"
	case code < 500:
		return "4xx"
	default:
		return "5xx"
	}
}
