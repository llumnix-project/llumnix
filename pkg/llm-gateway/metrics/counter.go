package metrics

import (
	"easgo/pkg/llm-gateway/structs"
	"sync"
	"sync/atomic"
	"time"
)

type CounterValue struct {
	key     string
	labels  structs.Labels
	counter atomic.Int64
	pre     int64
}

func NewCounter(k string, l structs.Labels) *CounterValue {
	return &CounterValue{
		key:    k,
		labels: l,
	}
}

func (c *CounterValue) IncrBy(n int) {
	c.counter.Add(int64(n))
}

func (c *CounterValue) IncrByOne() {
	c.counter.Add(int64(1))
}

type CounterGroup struct {
	mu   sync.Mutex
	data map[string]*CounterValue
}

func NewCounterGroup() *CounterGroup {
	return &CounterGroup{
		data: make(map[string]*CounterValue),
	}
}

func (cg *CounterGroup) Get(k string, l structs.Labels) *CounterValue {
	key := l.FlattenWithKey(k)

	cg.mu.Lock()
	defer cg.mu.Unlock()
	c, ok := cg.data[key]
	if ok {
		return c
	} else {
		c = NewCounter(k, l)
		cg.data[key] = c
		return c
	}
}

func (cg *CounterGroup) Expose(duration time.Duration) []Metric {
	cg.mu.Lock()
	counters := make(map[string]*CounterValue)
	for k, v := range cg.data {
		counters[k] = v
	}
	cg.mu.Unlock()

	var metrics []Metric
	for _, c := range counters {
		current := c.counter.Load()
		if current == 0 {
			continue
		}
		key := c.key
		incrValue := current - c.pre
		c.pre = current

		qps := float32(incrValue) * 1000 / float32(duration.Milliseconds())

		metrics = append(metrics,
			Metric{
				Name:  key,
				Tags:  c.labels.Convert(),
				Value: float32(current),
			},
			Metric{
				Name:  key + "_qps",
				Tags:  c.labels.Convert(),
				Value: float32(qps),
			},
		)
	}
	return metrics
}

var (
	counterGroup *CounterGroup
)

func Counter(k string, l structs.Labels) *CounterValue {
	return counterGroup.Get(k, l)
}

func ExposeCounter(duration time.Duration) []Metric {
	return counterGroup.Expose(duration)
}

func init() {
	counterGroup = NewCounterGroup()
}
