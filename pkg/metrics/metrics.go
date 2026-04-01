package metrics

import (
	"log"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

var (
	// registry is the global prometheus registry for all llumnix metrics.
	// Using a custom registry instead of prometheus.DefaultRegisterer to:
	// 1. Avoid conflicts with other libraries using the default registry
	// 2. Support test isolation with ResetForTesting()
	registry *prometheus.Registry

	// startTime records service start time for the uptime gauge.
	startTime = time.Now()

	// MaxCardinalityPerMetric limits the number of unique label value combinations
	// per metric name. When exceeded, new combinations are replaced with "_overflow"
	// labels to cap time series growth. Default: 1000.
	MaxCardinalityPerMetric = 1000

	globalCardinality *cardinalityTracker

	// helpDescs stores human-readable descriptions for metric names.
	// Registered via RegisterHelp() at startup; looked up when creating new Vecs.
	helpDescs = make(map[string]string)
)

func init() {
	setupRegistry()
	globalCardinality = newCardinalityTracker()
}

// RegisterHelp associates a human-readable description with a metric name.
// Should be called at startup (e.g. in init()) before the metric is first recorded.
func RegisterHelp(name, help string) {
	helpDescs[name] = help
}

// getHelp returns the registered help string for a metric, or the metric name itself as fallback.
func getHelp(name string) string {
	if h, ok := helpDescs[name]; ok {
		return h
	}
	return name
}

// Registry returns the global prometheus registry.
func Registry() *prometheus.Registry {
	return registry
}

// ResetForTesting creates a fresh registry and metric groups for test isolation.
// Returns the new registry for assertions.
func ResetForTesting() *prometheus.Registry {
	setupRegistry()
	counterGroup = NewCounterGroup()
	histogramGroup = NewHistogramGroup()
	gaugeGroup = NewGaugeGroup()
	globalCardinality = newCardinalityTracker()
	helpDescs = make(map[string]string)
	metricBuckets = make(map[string][]float64)
	return registry
}

// setupRegistry initializes (or re-initializes) the prometheus registry
// with Go runtime, process, and uptime collectors.
func setupRegistry() {
	registry = prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector())
	registry.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	registry.MustRegister(prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: "uptime_seconds",
			Help: "Service uptime in seconds",
		},
		func() float64 {
			return time.Since(startTime).Seconds()
		},
	))
}

// sanitizeMetricName converts a metric name to Prometheus-compatible format.
// Prometheus metric names must match [a-zA-Z_:][a-zA-Z0-9_:]*.
func sanitizeMetricName(name string) string {
	name = strings.ReplaceAll(name, "-", "_")
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

// ---------- Cardinality Limiter ----------

type cardinalityTracker struct {
	mu     sync.RWMutex
	series map[string]map[string]struct{} // metric name -> set of flattened label keys
	warned map[string]bool
}

func newCardinalityTracker() *cardinalityTracker {
	return &cardinalityTracker{
		series: make(map[string]map[string]struct{}),
		warned: make(map[string]bool),
	}
}

// enforce returns labels unchanged if the metric is within cardinality limits.
// If the limit for this metric name is exceeded, all label values are replaced
// with "_overflow" to prevent unbounded time series growth.
//
// Performance: uses RWMutex with read-fast-path. The common case (already-tracked
// label combination) only takes a RLock + two map lookups.
func (ct *cardinalityTracker) enforce(name string, labels Labels) Labels {
	if MaxCardinalityPerMetric <= 0 {
		return labels // disabled
	}

	key := labels.Flatten()

	// Fast path: read-lock only for already-tracked combinations.
	ct.mu.RLock()
	if set, ok := ct.series[name]; ok {
		if _, exists := set[key]; exists {
			ct.mu.RUnlock()
			return labels
		}
	}
	ct.mu.RUnlock()

	// Slow path: write-lock for new label combinations.
	ct.mu.Lock()
	defer ct.mu.Unlock()

	set, ok := ct.series[name]
	if !ok {
		set = make(map[string]struct{})
		ct.series[name] = set
	}

	// Double-check after acquiring write lock.
	if _, exists := set[key]; exists {
		return labels
	}

	if len(set) >= MaxCardinalityPerMetric {
		if !ct.warned[name] {
			log.Printf("[metrics] WARNING: %q cardinality limit (%d) reached, new combinations overflow",
				name, MaxCardinalityPerMetric)
			ct.warned[name] = true
		}
		overflow := make(Labels, len(labels))
		for i, l := range labels {
			overflow[i] = Label{Name: l.Name, Value: "_overflow"}
		}
		overflowKey := overflow.Flatten()
		set[overflowKey] = struct{}{}
		return overflow
	}

	set[key] = struct{}{}
	return labels
}
