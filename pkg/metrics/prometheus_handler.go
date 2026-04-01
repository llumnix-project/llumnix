package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// PrometheusHandler exports metrics in standard Prometheus text format.
// It delegates to promhttp.HandlerFor() which handles all formatting, encoding,
// and content negotiation automatically.
//
// This replaces the previous 426-line manual implementation that:
//   - Manually formatted metric names and labels
//   - Manually calculated percentiles from reservoir samples
//   - Manually exported 140+ lines of runtime metrics via runtime.ReadMemStats
//
// Now all of that is handled by the prometheus client library:
//   - Counter/Histogram/Gauge metrics: auto-exposed by registered collectors
//   - Runtime metrics: exported by collectors.NewGoCollector()
//   - Process metrics: exported by collectors.NewProcessCollector()
//
// Usage:
//
//	handler := metrics.NewPrometheusHandler()
//	http.Handle("/metrics", handler)
type PrometheusHandler struct {
	handler http.Handler
}

// NewPrometheusHandler creates a new Prometheus metrics handler.
// The handler serves metrics from the global custom registry.
func NewPrometheusHandler() *PrometheusHandler {
	return &PrometheusHandler{
		handler: promhttp.HandlerFor(registry, promhttp.HandlerOpts{
			// EnableOpenMetrics enables OpenMetrics content negotiation.
			EnableOpenMetrics: true,
		}),
	}
}

// ServeHTTP implements the http.Handler interface.
func (h *PrometheusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}
