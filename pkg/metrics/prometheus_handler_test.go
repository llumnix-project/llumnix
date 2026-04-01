package metrics

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// collectOutput is a test helper that scrapes the registry and returns the text output.
func collectOutput(t *testing.T) string {
	t.Helper()
	handler := promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}
	return w.Body.String()
}

func TestCounterMetric(t *testing.T) {
	ResetForTesting()

	counter := Counter("test_requests_total", Labels{
		{Name: "method", Value: "GET"},
		{Name: "status", Value: "200"},
	})
	counter.Add(100)

	body := collectOutput(t)

	// Verify counter metric is exposed with correct name and labels
	if !strings.Contains(body, `test_requests_total{method="GET",status="200"} 100`) {
		t.Errorf("Expected counter metric with value 100, got:\n%s", body)
	}
	// Verify HELP and TYPE annotations
	if !strings.Contains(body, "# HELP test_requests_total") {
		t.Error("Expected HELP annotation for test_requests_total")
	}
	if !strings.Contains(body, "# TYPE test_requests_total counter") {
		t.Error("Expected TYPE counter annotation")
	}
}

func TestCounterInc(t *testing.T) {
	ResetForTesting()

	counter := Counter("test_incr_total", Labels{
		{Name: "type", Value: "click"},
	})
	counter.Inc()
	counter.Inc()
	counter.Inc()

	body := collectOutput(t)

	if !strings.Contains(body, `test_incr_total{type="click"} 3`) {
		t.Errorf("Expected counter value 3, got:\n%s", body)
	}
}

func TestHistogramMetric(t *testing.T) {
	ResetForTesting()

	latency := Histogram("test_latency_us", Labels{
		{Name: "endpoint", Value: "/api/test"},
	})
	latency.ObserveInt(10)
	latency.ObserveInt(20)
	latency.ObserveInt(30)

	body := collectOutput(t)

	// Verify histogram metric components
	if !strings.Contains(body, "test_latency_us_bucket{") {
		t.Error("Expected histogram bucket metrics")
	}
	if !strings.Contains(body, "test_latency_us_sum{") {
		t.Error("Expected histogram sum metric")
	}
	if !strings.Contains(body, "test_latency_us_count{") {
		t.Error("Expected histogram count metric")
	}
	// Verify count = 3
	if !strings.Contains(body, `test_latency_us_count{endpoint="/api/test"} 3`) {
		t.Errorf("Expected count=3, got:\n%s", body)
	}
	// Verify sum = 60
	if !strings.Contains(body, `test_latency_us_sum{endpoint="/api/test"} 60`) {
		t.Errorf("Expected sum=60, got:\n%s", body)
	}
	// Verify +Inf bucket = 3
	if !strings.Contains(body, `test_latency_us_bucket{endpoint="/api/test",le="+Inf"} 3`) {
		t.Errorf("Expected +Inf bucket=3, got:\n%s", body)
	}
	// Verify TYPE annotation
	if !strings.Contains(body, "# TYPE test_latency_us histogram") {
		t.Error("Expected TYPE histogram annotation")
	}
}

func TestHistogramAddMany(t *testing.T) {
	ResetForTesting()

	latency := Histogram("test_batch_latency", Labels{
		{Name: "op", Value: "read"},
	})
	latency.ObserveManyInt([]int64{100, 200, 300, 400, 500})

	body := collectOutput(t)

	if !strings.Contains(body, `test_batch_latency_count{op="read"} 5`) {
		t.Errorf("Expected count=5, got:\n%s", body)
	}
	if !strings.Contains(body, `test_batch_latency_sum{op="read"} 1500`) {
		t.Errorf("Expected sum=1500, got:\n%s", body)
	}
}

func TestGaugeMetric(t *testing.T) {
	ResetForTesting()

	statusValue := Gauge("test_status", Labels{
		{Name: "type", Value: "active"},
	})
	statusValue.Set(42.5)

	body := collectOutput(t)

	// Verify gauge metric
	if !strings.Contains(body, "test_status{") {
		t.Error("Expected status gauge metric")
	}
	if !strings.Contains(body, `type="active"`) {
		t.Error("Expected label type=active")
	}
	// Verify TYPE annotation
	if !strings.Contains(body, "# TYPE test_status gauge") {
		t.Error("Expected TYPE gauge annotation")
	}
}

func TestGaugeUpdate(t *testing.T) {
	ResetForTesting()

	sv := Gauge("test_gauge", Labels{
		{Name: "name", Value: "cpu"},
	})
	sv.Set(10.0)
	sv.Set(20.0) // overwrite

	body := collectOutput(t)

	// Should contain latest value (20), not old (10)
	if !strings.Contains(body, `test_gauge{name="cpu"} 20`) {
		t.Errorf("Expected gauge value 20, got:\n%s", body)
	}
}

func TestPrometheusHandler_ServeHTTP(t *testing.T) {
	ResetForTesting()

	// Setup test metrics
	Counter("handler_test_counter", Labels{
		{Name: "method", Value: "POST"},
	}).Add(50)

	Histogram("handler_test_latency", Labels{
		{Name: "path", Value: "/api"},
	}).ObserveInt(100)

	Gauge("handler_test_gauge", Labels{
		{Name: "kind", Value: "memory"},
	}).Set(8.5)

	// Use the actual PrometheusHandler
	handler := NewPrometheusHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Verify all three metric types are present
	if !strings.Contains(body, "handler_test_counter") {
		t.Error("Expected counter metric")
	}
	if !strings.Contains(body, "handler_test_latency_bucket") {
		t.Error("Expected histogram bucket metric")
	}
	if !strings.Contains(body, "handler_test_gauge") {
		t.Error("Expected gauge metric")
	}

	// Verify runtime metrics from GoCollector
	if !strings.Contains(body, "go_goroutines") {
		t.Error("Expected go_goroutines from GoCollector")
	}

	// Verify process metrics from ProcessCollector
	if !strings.Contains(body, "process_") {
		t.Error("Expected process_* metrics from ProcessCollector")
	}

	// Verify uptime
	if !strings.Contains(body, "uptime_seconds") {
		t.Error("Expected uptime_seconds gauge")
	}

	// Verify standard Prometheus format
	if !strings.Contains(body, "# HELP") {
		t.Error("Expected # HELP annotations")
	}
	if !strings.Contains(body, "# TYPE") {
		t.Error("Expected # TYPE annotations")
	}
}

func TestSanitizeMetricName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"metric-name", "metric_name"},
		{"metric.name", "metric_name"},
		{"metric name", "metric_name"},
		{"metric_name", "metric_name"},
		{"already_valid", "already_valid"},
	}

	for _, test := range tests {
		result := sanitizeMetricName(test.input)
		if result != test.expected {
			t.Errorf("sanitizeMetricName(%q) = %q, expected %q", test.input, result, test.expected)
		}
	}
}

func TestResetForTesting_Isolation(t *testing.T) {
	// Create metrics in first registry
	ResetForTesting()
	Counter("isolation_counter", Labels{{Name: "a", Value: "1"}}).Add(999)

	body1 := collectOutput(t)
	if !strings.Contains(body1, "isolation_counter") {
		t.Error("Expected isolation_counter in first registry")
	}

	// Reset and verify old metrics are gone
	ResetForTesting()
	body2 := collectOutput(t)
	if strings.Contains(body2, "isolation_counter") {
		t.Error("Expected isolation_counter to be gone after reset")
	}
}

func TestMultipleLabelsPerMetric(t *testing.T) {
	ResetForTesting()

	// Same metric name, different label values
	Counter("multi_label_total", Labels{
		{Name: "method", Value: "GET"},
		{Name: "status", Value: "200"},
	}).Add(10)

	Counter("multi_label_total", Labels{
		{Name: "method", Value: "POST"},
		{Name: "status", Value: "201"},
	}).Add(5)

	body := collectOutput(t)

	if !strings.Contains(body, `multi_label_total{method="GET",status="200"} 10`) {
		t.Errorf("Expected GET/200 counter=10, got:\n%s", body)
	}
	if !strings.Contains(body, `multi_label_total{method="POST",status="201"} 5`) {
		t.Errorf("Expected POST/201 counter=5, got:\n%s", body)
	}
}

func TestSchedulerMetrics_Integration(t *testing.T) {
	ResetForTesting()

	// Test scheduler counter
	Counter("scheduler_rescheduling_total", Labels{
		{Name: "model", Value: "gpt-4"},
	}).Inc()

	// Test scheduler latency
	Histogram("request_full_mode_schedule_duration_milliseconds", Labels{
		{Name: "model", Value: "gpt-4"},
	}).ObserveInt(500)

	// Test instance status value
	Gauge("instance_cms_used_gpu_tokens", Labels{
		{Name: "instance", Value: "i-001"},
	}).Set(0.75)

	body := collectOutput(t)

	if !strings.Contains(body, "scheduler_rescheduling_total") {
		t.Error("Expected scheduler_rescheduling_total metric")
	}
	if !strings.Contains(body, "request_full_mode_schedule_duration_milliseconds") {
		t.Error("Expected request_full_mode_schedule_duration_milliseconds metric")
	}
	if !strings.Contains(body, "instance_cms_used_gpu_tokens") {
		t.Error("Expected instance_cms_used_gpu_tokens metric")
	}
}

func TestEmptyMetrics(t *testing.T) {
	ResetForTesting()

	handler := NewPrometheusHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	body := w.Body.String()

	// Should still have runtime metrics and uptime
	if !strings.Contains(body, "uptime_seconds") {
		t.Error("Expected uptime_seconds even with no custom metrics")
	}
	if !strings.Contains(body, "go_goroutines") {
		t.Error("Expected go_goroutines even with no custom metrics")
	}
}

func TestCardinalityLimit(t *testing.T) {
	ResetForTesting()
	oldLimit := MaxCardinalityPerMetric
	MaxCardinalityPerMetric = 3
	defer func() { MaxCardinalityPerMetric = oldLimit }()

	// Create 3 unique label combos (within limit)
	Counter("cardinality_test", Labels{{Name: "id", Value: "1"}}).Inc()
	Counter("cardinality_test", Labels{{Name: "id", Value: "2"}}).Inc()
	Counter("cardinality_test", Labels{{Name: "id", Value: "3"}}).Inc()

	// 4th combo should overflow
	Counter("cardinality_test", Labels{{Name: "id", Value: "4"}}).Inc()
	Counter("cardinality_test", Labels{{Name: "id", Value: "5"}}).Inc()

	body := collectOutput(t)

	// First 3 should exist
	if !strings.Contains(body, `id="1"`) {
		t.Error("Expected id=1")
	}
	if !strings.Contains(body, `id="2"`) {
		t.Error("Expected id=2")
	}
	if !strings.Contains(body, `id="3"`) {
		t.Error("Expected id=3")
	}
	// 4th and 5th should be overflowed
	if !strings.Contains(body, `id="_overflow"`) {
		t.Errorf("Expected overflow label, got:\n%s", body)
	}
	// id=4 and id=5 should NOT appear as separate series
	if strings.Contains(body, `id="4"`) {
		t.Error("id=4 should have been overflowed")
	}
	if strings.Contains(body, `id="5"`) {
		t.Error("id=5 should have been overflowed")
	}
}

func TestCardinalityLimit_DifferentMetrics(t *testing.T) {
	ResetForTesting()
	oldLimit := MaxCardinalityPerMetric
	MaxCardinalityPerMetric = 2
	defer func() { MaxCardinalityPerMetric = oldLimit }()

	// Each metric has its own cardinality budget
	Counter("metric_a", Labels{{Name: "x", Value: "1"}}).Inc()
	Counter("metric_a", Labels{{Name: "x", Value: "2"}}).Inc()
	Counter("metric_a", Labels{{Name: "x", Value: "3"}}).Inc() // overflow

	Counter("metric_b", Labels{{Name: "y", Value: "a"}}).Inc()
	Counter("metric_b", Labels{{Name: "y", Value: "b"}}).Inc()

	body := collectOutput(t)

	// metric_a should have overflow
	if !strings.Contains(body, `metric_a{x="_overflow"}`) {
		t.Errorf("metric_a should have overflow, got:\n%s", body)
	}
	// metric_b should be fine (at limit, not over)
	if !strings.Contains(body, `metric_b{y="a"}`) {
		t.Error("metric_b y=a should exist")
	}
	if !strings.Contains(body, `metric_b{y="b"}`) {
		t.Error("metric_b y=b should exist")
	}
}
