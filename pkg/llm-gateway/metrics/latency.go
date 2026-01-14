package metrics

import (
	"math"
	"math/rand"
	"sort"
	"sync"
)

const (
	kLatencyValueCapacity = 50000
)

type LatencyValue struct {
	key    string
	labels Labels

	data        []int64
	sampleCount int64
	capacity    int
	mu          sync.Mutex
}

type LatencyResult struct {
	min  int64
	max  int64
	mean int64

	p99Value int64
	p90Value int64
	p75Value int64
	p50Value int64
}

func NewLatencyValue(k string, l Labels, cap int) *LatencyValue {
	return &LatencyValue{
		key:      k,
		labels:   l,
		data:     make([]int64, 0, 1000),
		capacity: cap,
	}
}

// Uniform Sample
func (l *LatencyValue) Add(value int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sampleCount++
	if len(l.data) < l.capacity {
		l.data = append(l.data, value)
	} else {
		r := rand.Int63n(l.sampleCount)
		if r < int64(len(l.data)) {
			l.data[int(r)] = value
		}
	}
}

func (l *LatencyValue) AddMany(values []int64) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sampleCount += int64(len(values))
	if l.sampleCount < int64(l.capacity) {
		l.data = append(l.data, values...)
	} else {
		lastValues := values
		x := l.sampleCount - int64(len(values))
		if x < int64(l.capacity) {
			n := l.capacity - int(x)
			v := values[0:n]
			lastValues = values[n:]
			l.data = append(l.data, v...)
		}
		for _, v := range lastValues {
			r := rand.Int63n(l.sampleCount)
			if r < int64(len(l.data)) {
				l.data[int(r)] = v
			}
		}
	}
}

func (l *LatencyValue) clear() {
	l.sampleCount = 0
	l.data = make([]int64, 0, 1000)
}

func (l *LatencyValue) Calculate() *LatencyResult {
	var values []int64
	l.mu.Lock()
	values = l.data
	l.clear()
	l.mu.Unlock()

	result := &LatencyResult{}
	length := len(values)
	if length == 0 {
		return result
	}

	sort.Slice(values, func(i, j int) bool {
		return values[i] < values[j]
	})

	sum := int64(0)
	for _, v := range values {
		sum += v
	}
	result.min = values[0]
	result.max = values[length-1]
	result.mean = sum / int64(length)

	percentValues := make([]int64, 4)
	for i, p := range []float64{0.99, 0.90, 0.75, 0.50} {
		pos := p * float64(length+1)
		if pos < 1.0 {
			percentValues[i] = values[0]
		} else if pos >= float64(length) {
			percentValues[i] = values[length-1]
		} else {
			lower := float64(values[int(pos)-1])
			upper := float64(values[int(pos)])
			percentValues[i] = int64(lower + (pos-math.Floor(pos))*(upper-lower))
		}
	}
	result.p99Value = percentValues[0]
	result.p90Value = percentValues[1]
	result.p75Value = percentValues[2]
	result.p50Value = percentValues[3]
	return result
}

type LatencyGroup struct {
	mu   sync.Mutex
	data map[string]*LatencyValue
}

func NewLatencyGroup() *LatencyGroup {
	return &LatencyGroup{
		data: make(map[string]*LatencyValue),
	}
}

func (lg *LatencyGroup) Get(k string, l Labels) *LatencyValue {
	key := l.FlattenWithKey(k)

	lg.mu.Lock()
	defer lg.mu.Unlock()
	g, ok := lg.data[key]
	if ok {
		return g
	} else {
		g = NewLatencyValue(k, l, kLatencyValueCapacity)
		lg.data[key] = g
		return g
	}
}

func (lg *LatencyGroup) Expose() []Metric {
	lg.mu.Lock()
	latencies := make(map[string]*LatencyValue)
	for k, v := range lg.data {
		latencies[k] = v
	}
	lg.mu.Unlock()

	var metrics []Metric
	for _, l := range latencies {
		key := l.key
		result := l.Calculate()
		if result == nil {
			continue
		}
		metrics = append(metrics,
			Metric{
				Name:  key + "_max",
				Tags:  l.labels.Convert(),
				Value: float32(result.max),
			},
			Metric{
				Name:  key + "_min",
				Tags:  l.labels.Convert(),
				Value: float32(result.min),
			},
			Metric{
				Name:  key + "_mean",
				Tags:  l.labels.Convert(),
				Value: float32(result.mean),
			},
			Metric{
				Name:  key + "_percent",
				Tags:  l.labels.Append(Label{Name: "percent_line", Value: "99"}).Convert(),
				Value: float32(result.p99Value),
			},
			Metric{
				Name:  key + "_percent",
				Tags:  l.labels.Append(Label{Name: "percent_line", Value: "90"}).Convert(),
				Value: float32(result.p90Value),
			},
			Metric{
				Name:  key + "_percent",
				Tags:  l.labels.Append(Label{Name: "percent_line", Value: "75"}).Convert(),
				Value: float32(result.p75Value),
			},
			Metric{
				Name:  key + "_percent",
				Tags:  l.labels.Append(Label{Name: "percent_line", Value: "50"}).Convert(),
				Value: float32(result.p50Value),
			},
		)
	}
	return metrics
}

var (
	latencyGroup *LatencyGroup
)

func Latency(k string, l Labels) *LatencyValue {
	return latencyGroup.Get(k, l)
}

func ExposeLatency() []Metric {
	return latencyGroup.Expose()
}

func init() {
	latencyGroup = NewLatencyGroup()
}

// var latencyGroup []*LatencyGroup
// var current atomic.Int32
// var once sync.Once

// func getFrontendLatencyGroupAndSwap() *LatencyGroup {
// 	front := current.Load()
// 	current.Store((front + 1) % 2)
// 	return latencyGroup[front]
// }

// func getFrontendLatencyGroup() *LatencyGroup {
// 	return latencyGroup[current.Load()]
// }
