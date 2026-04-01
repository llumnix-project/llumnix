package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// DefaultHistogramBuckets is the fallback bucket set used when no per-metric
// buckets are registered via RegisterBuckets().
// Covers a wide range (1 to 1,000,000) suitable as a safe default.
var DefaultHistogramBuckets = []float64{
	1, 2, 5, 10, 20, 50, 100, 200, 500,
	1000, 2000, 5000, 10000, 20000, 50000,
	100000, 200000, 500000, 1000000,
}

// metricBuckets stores per-metric histogram bucket overrides.
// Registered via RegisterBuckets() at startup; looked up by getBuckets().
var metricBuckets = make(map[string][]float64)

// RegisterBuckets associates a custom histogram bucket set with a metric name.
// Must be called at startup before the metric is first recorded.
func RegisterBuckets(name string, buckets []float64) {
	metricBuckets[name] = buckets
}

// getBuckets returns the bucket set for a metric: per-metric override if registered,
// otherwise DefaultHistogramBuckets.
func getBuckets(name string) []float64 {
	if b, ok := metricBuckets[name]; ok {
		return b
	}
	return DefaultHistogramBuckets
}

// HistogramValue wraps a prometheus.Observer (Histogram) for a specific label combination.
// It wraps prometheus.Observer with int-typed ObserveInt for caller convenience.
//
// Key improvement over the previous implementation:
//   - No more reservoir sampling (was capped at 50,000 samples)
//   - No more client-side percentile calculation (was sorting + interpolation)
//   - Percentiles are now computed by Prometheus server via histogram_quantile()
//   - Unlimited observation capacity with constant memory (fixed bucket count)
type HistogramValue struct {
	observer prometheus.Observer
}

// ObserveInt records a single integer observation value (e.g. milliseconds or microseconds).
func (l *HistogramValue) ObserveInt(value int64) {
	l.observer.Observe(float64(value))
}

// Observe records a single float64 observation value (e.g. seconds from time.Duration.Seconds()).
func (l *HistogramValue) Observe(value float64) {
	l.observer.Observe(value)
}

// ObserveManyInt records multiple integer observation values.
func (l *HistogramValue) ObserveManyInt(values []int64) {
	for _, v := range values {
		l.observer.Observe(float64(v))
	}
}

// HistogramGroup manages prometheus HistogramVec instances.
// Each unique metric name gets one HistogramVec; different label value
// combinations share the same Vec but resolve to different Histogram instances.
//
// Performance: uses sync.RWMutex with two-level caching.
// Hot path (already-seen name+labels) only takes a RLock + two map lookups,
// no prometheus vec.With() hash computation.
type HistogramGroup struct {
	mu       sync.RWMutex
	vecs     map[string]*prometheus.HistogramVec
	resolved map[string]*HistogramValue // key: "name\x00" + labels.Flatten()
}

func NewHistogramGroup() *HistogramGroup {
	return &HistogramGroup{
		vecs:     make(map[string]*prometheus.HistogramVec),
		resolved: make(map[string]*HistogramValue),
	}
}

// Get returns a HistogramValue for the given metric name and labels.
// On first access for a metric name, a new HistogramVec is registered with the global registry.
func (lg *HistogramGroup) Get(name string, labels Labels) *HistogramValue {
	labels = globalCardinality.enforce(name, labels)

	cacheKey := name + "\x00" + labels.Flatten()

	// Fast path: read-lock only, no allocation.
	lg.mu.RLock()
	if lv, ok := lg.resolved[cacheKey]; ok {
		lg.mu.RUnlock()
		return lv
	}
	lg.mu.RUnlock()

	// Slow path: write-lock, register vec if needed, resolve and cache.
	lg.mu.Lock()
	defer lg.mu.Unlock()

	// Double-check after acquiring write lock.
	if lv, ok := lg.resolved[cacheKey]; ok {
		return lv
	}

	vec, ok := lg.vecs[name]
	if !ok {
		vec = prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    sanitizeMetricName(name),
			Help:    getHelp(name),
			Buckets: getBuckets(name),
		}, labels.Names())
		registry.MustRegister(vec)
		lg.vecs[name] = vec
	}

	lv := &HistogramValue{
		observer: vec.With(labels.ToPrometheusLabels()),
	}
	lg.resolved[cacheKey] = lv
	return lv
}

var histogramGroup *HistogramGroup

// Histogram returns a HistogramValue for the given metric name and labels.
// The underlying prometheus HistogramVec is lazily created on first use.
func Histogram(k string, l Labels) *HistogramValue {
	return histogramGroup.Get(k, l)
}

func init() {
	histogramGroup = NewHistogramGroup()
}
