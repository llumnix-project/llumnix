package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// CounterValue wraps a prometheus.Counter for a specific label combination.
// It wraps prometheus.Counter with int-typed Add for caller convenience.
type CounterValue struct {
	counter prometheus.Counter
}

// Add adds the given value to the counter.
func (c *CounterValue) Add(n int) {
	c.counter.Add(float64(n))
}

// Inc increments the counter by 1.
func (c *CounterValue) Inc() {
	c.counter.Inc()
}

// CounterGroup manages prometheus CounterVec instances.
// Each unique metric name gets one CounterVec; different label value
// combinations share the same Vec but resolve to different Counter instances.
//
// Performance: uses sync.RWMutex with two-level caching.
// Hot path (already-seen name+labels) only takes a RLock + two map lookups,
// no prometheus vec.With() hash computation.
type CounterGroup struct {
	mu       sync.RWMutex
	vecs     map[string]*prometheus.CounterVec
	resolved map[string]*CounterValue // key: "name\x00" + labels.Flatten()
}

func NewCounterGroup() *CounterGroup {
	return &CounterGroup{
		vecs:     make(map[string]*prometheus.CounterVec),
		resolved: make(map[string]*CounterValue),
	}
}

// Get returns a CounterValue for the given metric name and labels.
// On first access for a metric name, a new CounterVec is registered with the global registry.
func (cg *CounterGroup) Get(name string, labels Labels) *CounterValue {
	labels = globalCardinality.enforce(name, labels)

	cacheKey := name + "\x00" + labels.Flatten()

	// Fast path: read-lock only, no allocation.
	cg.mu.RLock()
	if cv, ok := cg.resolved[cacheKey]; ok {
		cg.mu.RUnlock()
		return cv
	}
	cg.mu.RUnlock()

	// Slow path: write-lock, register vec if needed, resolve and cache.
	cg.mu.Lock()
	defer cg.mu.Unlock()

	// Double-check after acquiring write lock.
	if cv, ok := cg.resolved[cacheKey]; ok {
		return cv
	}

	vec, ok := cg.vecs[name]
	if !ok {
		vec = prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: sanitizeMetricName(name),
			Help: getHelp(name),
		}, labels.Names())
		registry.MustRegister(vec)
		cg.vecs[name] = vec
	}

	cv := &CounterValue{
		counter: vec.With(labels.ToPrometheusLabels()),
	}
	cg.resolved[cacheKey] = cv
	return cv
}

var counterGroup *CounterGroup

// Counter returns a CounterValue for the given metric name and labels.
// The underlying prometheus CounterVec is lazily created on first use.
func Counter(k string, l Labels) *CounterValue {
	return counterGroup.Get(k, l)
}

func init() {
	counterGroup = NewCounterGroup()
}
