package metrics

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// GaugeValue wraps a prometheus.Gauge for a specific label combination.
// It preserves the original API (Set) for zero-change caller compatibility.
//
// Key improvement over the previous implementation:
//   - No more int64 * 1000 precision hack (was: Store(int64(n * 1000)), Load()/1000)
//   - Direct float64 storage via prometheus.Gauge
//   - Automatic Prometheus exposition (no manual Expose() needed)
type GaugeValue struct {
	gauge prometheus.Gauge
}

// Set sets the current gauge value.
func (c *GaugeValue) Set(n float64) {
	c.gauge.Set(n)
}

// GaugeGroup manages prometheus GaugeVec instances.
// Each unique metric name gets one GaugeVec; different label value
// combinations share the same Vec but resolve to different Gauge instances.
//
// Performance: uses sync.RWMutex with two-level caching.
// Hot path (already-seen name+labels) only takes a RLock + two map lookups,
// no prometheus vec.With() hash computation.
type GaugeGroup struct {
	mu       sync.RWMutex
	vecs     map[string]*prometheus.GaugeVec
	resolved map[string]*GaugeValue // key: "name\x00" + labels.Flatten()
}

func NewGaugeGroup() *GaugeGroup {
	return &GaugeGroup{
		vecs:     make(map[string]*prometheus.GaugeVec),
		resolved: make(map[string]*GaugeValue),
	}
}

// Get returns a GaugeValue for the given metric name and labels.
// On first access for a metric name, a new GaugeVec is registered with the global registry.
func (cg *GaugeGroup) Get(k string, l Labels) *GaugeValue {
	l = globalCardinality.enforce(k, l)

	cacheKey := k + "\x00" + l.Flatten()

	// Fast path: read-lock only, no allocation.
	cg.mu.RLock()
	if sd, ok := cg.resolved[cacheKey]; ok {
		cg.mu.RUnlock()
		return sd
	}
	cg.mu.RUnlock()

	// Slow path: write-lock, register vec if needed, resolve and cache.
	cg.mu.Lock()
	defer cg.mu.Unlock()

	// Double-check after acquiring write lock.
	if sd, ok := cg.resolved[cacheKey]; ok {
		return sd
	}

	vec, ok := cg.vecs[k]
	if !ok {
		vec = prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: sanitizeMetricName(k),
			Help: getHelp(k),
		}, l.Names())
		registry.MustRegister(vec)
		cg.vecs[k] = vec
	}

	sd := &GaugeValue{
		gauge: vec.With(l.ToPrometheusLabels()),
	}
	cg.resolved[cacheKey] = sd
	return sd
}

var gaugeGroup *GaugeGroup

// Gauge returns a GaugeValue for the given metric name and labels.
// The underlying prometheus GaugeVec is lazily created on first use.
func Gauge(k string, l Labels) *GaugeValue {
	return gaugeGroup.Get(k, l)
}

func init() {
	gaugeGroup = NewGaugeGroup()
}
