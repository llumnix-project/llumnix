package metrics

import (
	"bytes"
	"easgo/pkg/llm-gateway/consts"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

const (
	kMetricURL = "http://127.0.0.1:8080/api/builtin/realtime_metrics?internal=true"
)

type Metric struct {
	Name  string            `json:"name"`
	Tags  map[string]string `json:"tags,omitempty"`
	Value float32           `json:"value"`
}

type MetricContext struct {
	client *http.Client
}

func newMetricContext() *MetricContext {
	return &MetricContext{
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (m *MetricContext) submitMetrics(metrics []Metric) {
	if len(metrics) == 0 {
		return
	}
	data, err := json.Marshal(metrics)
	if err != nil {
		klog.Warningf("failed to marshal metrics: %v", err)
		return
	}
	req, _ := http.NewRequest("POST", kMetricURL, bytes.NewBuffer(data))

	resp, err := m.client.Do(req)
	if err != nil {
		klog.Warningf("submit metric failed: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respData, _ := io.ReadAll(resp.Body)
		klog.Warningf("submit metric failed, response code is %v, %s", resp.StatusCode, string(respData))
	}
}

func (m *MetricContext) run() {
	start := time.Now()
	for {
		time.Sleep(consts.MetricRecordDuration)
		var metrics []Metric
		latencyMetrics := ExposeLatency()
		statusMetrics := ExposeStatusValue()
		duration := time.Since(start)
		start = time.Now()
		counterMetrics := ExposeCounter(duration)

		metrics = append(metrics, latencyMetrics...)
		metrics = append(metrics, counterMetrics...)
		metrics = append(metrics, statusMetrics...)

		m.submitMetrics(metrics)
	}
}

func init() {
	// TODO: enable metric collect
	// metricContext := newMetricContext()
	// go metricContext.run()
}
