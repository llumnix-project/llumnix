package metrics

import (
	"easgo/pkg/llm-gateway/structs"
	"sync"
	"sync/atomic"
)

type StatusData struct {
	key    string
	labels structs.Labels
	mValue atomic.Int64
}

func NewStatusData(k string, l structs.Labels) *StatusData {
	return &StatusData{
		key:    k,
		labels: l,
	}
}

func (c *StatusData) Set(n float32) {
	c.mValue.Store(int64(n * 1000))
}

type StatusDataGroup struct {
	mu   sync.Mutex
	data map[string]*StatusData
}

func NewStatusDataGroup() *StatusDataGroup {
	return &StatusDataGroup{
		data: make(map[string]*StatusData),
	}
}

func (cg *StatusDataGroup) Get(k string, l structs.Labels) *StatusData {
	key := l.FlattenWithKey(k)

	cg.mu.Lock()
	defer cg.mu.Unlock()
	c, ok := cg.data[key]
	if ok {
		return c
	} else {
		c = NewStatusData(k, l)
		cg.data[key] = c
		return c
	}
}

func (cg *StatusDataGroup) Expose() []Metric {
	cg.mu.Lock()
	values := cg.data
	cg.data = make(map[string]*StatusData)
	cg.mu.Unlock()

	var metrics []Metric
	for _, v := range values {
		key := v.key
		current := v.mValue.Load()
		metrics = append(metrics,
			Metric{
				Name:  key,
				Tags:  v.labels.Convert(),
				Value: float32(current) / 1000,
			},
		)
	}
	return metrics
}

var (
	statusValueGroup *StatusDataGroup
)

func StatusValue(k string, l structs.Labels) *StatusData {
	return statusValueGroup.Get(k, l)
}

func ExposeStatusValue() []Metric {
	return statusValueGroup.Expose()
}

func init() {
	statusValueGroup = NewStatusDataGroup()
}
