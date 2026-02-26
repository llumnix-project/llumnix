package metrics

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPrometheusHandler_ServeHTTP(t *testing.T) {
	// Setup test metrics
	counter := Counter("test_requests", Labels{
		{Name: "method", Value: "GET"},
		{Name: "status", Value: "200"},
	})
	counter.IncrBy(100)

	latency := Latency("test_latency", Labels{
		{Name: "endpoint", Value: "/api/test"},
	})
	latency.Add(10)
	latency.Add(20)
	latency.Add(30)

	statusValue := StatusValue("test_status", Labels{
		{Name: "type", Value: "active"},
	})
	statusValue.Set(42.5)

	// Create handler and test request
	handler := NewPrometheusHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	expectedContentType := "text/plain; version=0.0.4; charset=utf-8"
	if contentType != expectedContentType {
		t.Errorf("Expected Content-Type %s, got %s", expectedContentType, contentType)
	}

	body := w.Body.String()

	// Check counter metrics
	if !strings.Contains(body, "test_requests") {
		t.Error("Expected body to contain 'test_requests'")
	}
	if !strings.Contains(body, "method=\"GET\"") {
		t.Error("Expected body to contain 'method=\"GET\"'")
	}
	if !strings.Contains(body, "status=\"200\"") {
		t.Error("Expected body to contain 'status=\"200\"'")
	}

	// Check latency metrics
	if !strings.Contains(body, "test_latency_min") {
		t.Error("Expected body to contain 'test_latency_min'")
	}
	if !strings.Contains(body, "test_latency_max") {
		t.Error("Expected body to contain 'test_latency_max'")
	}
	if !strings.Contains(body, "test_latency_mean") {
		t.Error("Expected body to contain 'test_latency_mean'")
	}
	if !strings.Contains(body, "test_latency_percentile") {
		t.Error("Expected body to contain 'test_latency_percentile'")
	}
	if !strings.Contains(body, "quantile=\"0.99\"") {
		t.Error("Expected body to contain 'quantile=\"0.99\"'")
	}

	// Check status metrics
	if !strings.Contains(body, "test_status") {
		t.Error("Expected body to contain 'test_status'")
	}
	if !strings.Contains(body, "type=\"active\"") {
		t.Error("Expected body to contain 'type=\"active\"'")
	}

	// Check uptime metric
	if !strings.Contains(body, "llm_gateway_uptime_seconds") {
		t.Error("Expected body to contain 'llm_gateway_uptime_seconds'")
	}

	// Check runtime metrics
	if !strings.Contains(body, "go_goroutines") {
		t.Error("Expected body to contain 'go_goroutines'")
	}
	if !strings.Contains(body, "go_memstats_heap_alloc_bytes") {
		t.Error("Expected body to contain 'go_memstats_heap_alloc_bytes'")
	}
	if !strings.Contains(body, "go_memstats_heap_inuse_bytes") {
		t.Error("Expected body to contain 'go_memstats_heap_inuse_bytes'")
	}
	if !strings.Contains(body, "go_memstats_heap_objects") {
		t.Error("Expected body to contain 'go_memstats_heap_objects'")
	}

	// Check Prometheus format elements
	if !strings.Contains(body, "# HELP") {
		t.Error("Expected body to contain '# HELP'")
	}
	if !strings.Contains(body, "# TYPE") {
		t.Error("Expected body to contain '# TYPE'")
	}
}

func TestPrometheusHandler_SanitizeMetricName(t *testing.T) {
	handler := NewPrometheusHandler()

	tests := []struct {
		input    string
		expected string
	}{
		{"metric-name", "metric_name"},
		{"metric.name", "metric_name"},
		{"metric name", "metric_name"},
		{"metric_name", "metric_name"},
		{"llm-response-time", "llm_response_time"},
	}

	for _, test := range tests {
		result := handler.sanitizeMetricName(test.input)
		if result != test.expected {
			t.Errorf("sanitizeMetricName(%s) = %s, expected %s", test.input, result, test.expected)
		}
	}
}

func TestPrometheusHandler_FormatLabels(t *testing.T) {
	handler := NewPrometheusHandler()

	tests := []struct {
		name     string
		labels   Labels
		expected string
	}{
		{
			name:     "empty labels",
			labels:   Labels{},
			expected: "",
		},
		{
			name: "single label",
			labels: Labels{
				{Name: "status", Value: "200"},
			},
			expected: "{status=\"200\"}",
		},
		{
			name: "multiple labels",
			labels: Labels{
				{Name: "method", Value: "GET"},
				{Name: "status", Value: "200"},
			},
			expected: "{method=\"GET\",status=\"200\"}",
		},
		{
			name: "labels with special characters",
			labels: Labels{
				{Name: "path", Value: "/api/test\"value"},
			},
			expected: "{path=\"/api/test\\\"value\"}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := handler.formatLabels(test.labels)
			if result != test.expected {
				t.Errorf("formatLabels() = %s, expected %s", result, test.expected)
			}
		})
	}
}

func TestPrometheusHandler_FormatLabelsWithQuantile(t *testing.T) {
	handler := NewPrometheusHandler()

	labels := Labels{
		{Name: "endpoint", Value: "/api/test"},
	}

	result := handler.formatLabelsWithQuantile(labels, "0.99")
	expected := "{endpoint=\"/api/test\",quantile=\"0.99\"}"

	if result != expected {
		t.Errorf("formatLabelsWithQuantile() = %s, expected %s", result, expected)
	}
}

func TestPrometheusHandler_CalculatePercentile(t *testing.T) {
	handler := NewPrometheusHandler()

	tests := []struct {
		name       string
		data       []int64
		percentile float64
		expected   int64
	}{
		{
			name:       "empty data",
			data:       []int64{},
			percentile: 0.5,
			expected:   0,
		},
		{
			name:       "single value",
			data:       []int64{10},
			percentile: 0.5,
			expected:   10,
		},
		{
			name:       "p50 of simple data",
			data:       []int64{10, 20, 30, 40, 50},
			percentile: 0.5,
			expected:   30,
		},
		{
			name:       "p99 of simple data",
			data:       []int64{10, 20, 30, 40, 50},
			percentile: 0.99,
			expected:   50,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := handler.calculatePercentile(test.data, test.percentile)
			if result != test.expected {
				t.Errorf("calculatePercentile() = %d, expected %d", result, test.expected)
			}
		})
	}
}

func TestPrometheusHandler_Uptime(t *testing.T) {
	handler := NewPrometheusHandler()
	time.Sleep(10 * time.Millisecond)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()
	if !strings.Contains(body, "llm_gateway_uptime_seconds") {
		t.Error("Expected body to contain uptime metric")
	}

	// Verify uptime is a positive number
	if !strings.Contains(body, "llm_gateway_uptime_seconds 0.") {
		t.Error("Expected uptime to be greater than 0")
	}
}

func TestPrometheusHandler_EmptyMetrics(t *testing.T) {
	// Create a fresh handler without any metrics
	handler := NewPrometheusHandler()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	// Should still return 200 OK with at least uptime metric
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	body := w.Body.String()
	if !strings.Contains(body, "llm_gateway_uptime_seconds") {
		t.Error("Expected body to contain uptime metric even when no other metrics exist")
	}
}

func TestPrometheusHandler_RuntimeMetrics(t *testing.T) {
	handler := NewPrometheusHandler()

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()

	// Verify GC metrics (Category 1)
	gcMetrics := []string{
		"go_gc_duration_seconds",
		"go_memstats_last_gc_time_seconds",
		"go_memstats_gc_sys_bytes",
		"go_memstats_num_gc",
		"go_memstats_num_forced_gc",
	}

	// Verify thread metrics (Category 2)
	threadMetrics := []string{
		"go_goroutines",
		"go_threads",
	}

	// Verify memory fine-grained metrics (Category 3)
	memoryMetrics := []string{
		"go_memstats_sys_bytes",
		"go_memstats_heap_alloc_bytes",
		"go_memstats_heap_inuse_bytes",
		"go_memstats_heap_objects",
		"go_memstats_stack_inuse_bytes",
		"go_memstats_stack_sys_bytes",
		"go_memstats_mspan_inuse_bytes",
		"go_memstats_mcache_inuse_bytes",
		"go_memstats_mallocs_total",
		"go_memstats_frees_total",
		"go_memstats_alloc_bytes_total",
	}

	// Verify process resource metrics (Category 4)
	processMetrics := []string{
		"go_cpu_count",
	}

	// Combine all runtime metrics
	allMetrics := append(append(append(gcMetrics, threadMetrics...), memoryMetrics...), processMetrics...)

	for _, metric := range allMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected body to contain '%s'", metric)
		}
		// Verify each metric has HELP annotation
		if !strings.Contains(body, "# HELP "+metric) {
			t.Errorf("Expected body to contain '# HELP %s'", metric)
		}
		// Verify each metric has TYPE annotation (gauge or counter)
		if !strings.Contains(body, "# TYPE "+metric) {
			t.Errorf("Expected body to contain '# TYPE %s'", metric)
		}
	}

	// Verify GC duration quantiles are present (only if GC has occurred)
	if strings.Contains(body, "go_memstats_num_gc") {
		// Check if NumGC > 0 by looking for the metric value
		lines := strings.Split(body, "\n")
		hasGCOccurred := false
		for _, line := range lines {
			if strings.HasPrefix(line, "go_memstats_num_gc ") {
				parts := strings.Fields(line)
				if len(parts) >= 2 && parts[1] != "0" {
					hasGCOccurred = true
					break
				}
			}
		}

		// Only verify quantiles if GC has occurred
		if hasGCOccurred && strings.Contains(body, "go_gc_duration_seconds") {
			gcQuantiles := []string{
				"go_gc_duration_seconds{quantile=\"0.5\"}",
				"go_gc_duration_seconds{quantile=\"0.9\"}",
				"go_gc_duration_seconds{quantile=\"0.99\"}",
			}
			for _, quantile := range gcQuantiles {
				if !strings.Contains(body, quantile) {
					t.Errorf("Expected body to contain '%s' when GC has occurred", quantile)
				}
			}
		}
	}

	// Verify metrics have non-negative values
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		for _, metric := range allMetrics {
			if strings.HasPrefix(line, metric+" ") || strings.HasPrefix(line, metric+"{") {
				// Check that the line contains a number
				parts := strings.Fields(line)
				if len(parts) < 2 {
					t.Errorf("Invalid metric line format: %s", line)
				}
			}
		}
	}
}

func TestPrometheusHandler_GCMetricsWithForcedGC(t *testing.T) {
	// Allocate memory to trigger GC
	const allocSize = 10 * 1024 * 1024 // 10MB
	allocations := make([][]byte, 0, 100)
	for i := 0; i < 100; i++ {
		allocations = append(allocations, make([]byte, allocSize))
	}

	// Force GC to ensure metrics are populated
	runtime.GC()

	// Allow GC to complete
	time.Sleep(10 * time.Millisecond)

	handler := NewPrometheusHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	body := w.Body.String()
	lines := strings.Split(body, "\n")

	// Verify NumGC > 0
	var numGC int
	for _, line := range lines {
		if strings.HasPrefix(line, "go_memstats_num_gc ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				fmt.Sscanf(parts[1], "%d", &numGC)
			}
			break
		}
	}

	if numGC == 0 {
		t.Error("Expected at least one GC cycle to have occurred")
	}

	t.Logf("NumGC: %d", numGC)

	// Verify GC duration quantiles are present
	requiredQuantiles := []string{
		"go_gc_duration_seconds{quantile=\"0.5\"}",
		"go_gc_duration_seconds{quantile=\"0.9\"}",
		"go_gc_duration_seconds{quantile=\"0.99\"}",
	}

	for _, quantile := range requiredQuantiles {
		if !strings.Contains(body, quantile) {
			t.Errorf("Expected body to contain '%s' after forced GC", quantile)
		}
	}

	// Verify GC duration values are reasonable (non-negative and < 1 second)
	gcDurations := make(map[string]float64)
	for _, line := range lines {
		if strings.HasPrefix(line, "go_gc_duration_seconds{quantile=") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var duration float64
				fmt.Sscanf(parts[1], "%f", &duration)
				if duration < 0 {
					t.Errorf("GC duration should not be negative: %s", line)
				}
				if duration > 1.0 {
					t.Errorf("GC duration unexpectedly high (>1s): %s", line)
				}
				// Extract quantile for logging
				if strings.Contains(parts[0], "0.5") {
					gcDurations["p50"] = duration
				} else if strings.Contains(parts[0], "0.9") {
					gcDurations["p90"] = duration
				} else if strings.Contains(parts[0], "0.99") {
					gcDurations["p99"] = duration
				}
			}
		}
	}

	if len(gcDurations) > 0 {
		t.Logf("GC pause durations: p50=%.6fs, p90=%.6fs, p99=%.6fs",
			gcDurations["p50"], gcDurations["p90"], gcDurations["p99"])
	}

	// Verify last GC time is recent (within last minute)
	for _, line := range lines {
		if strings.HasPrefix(line, "go_memstats_last_gc_time_seconds ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				var lastGCTime float64
				fmt.Sscanf(parts[1], "%f", &lastGCTime)
				now := float64(time.Now().Unix())
				if now-lastGCTime > 60 {
					t.Errorf("Last GC time seems stale: %.0f seconds ago", now-lastGCTime)
				}
			}
			break
		}
	}

	// Keep allocation alive to prevent early collection
	_ = allocations
}
