package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/metrics"
)

func TestGatewayService_PrometheusMetricsEndpoint(t *testing.T) {
	// Create test configuration
	config := &options.Config{
		Host:             "localhost",
		Port:             8080,
		MaxQueueSize:     10,
		WaitQueueThreads: 2,
		LocalTestIPs:     "127.0.0.1:9000",
		RetryCount:       1,
	}

	// Create gateway service
	gateway := NewGatewayService(config)
	if gateway == nil {
		t.Fatal("Failed to create gateway service")
	}

	// Add some test metrics
	metrics.Counter("test_requests", metrics.Labels{
		{Name: "status", Value: "200"},
	}).IncrByOne()

	metrics.Latency("test_latency", metrics.Labels{
		{Name: "endpoint", Value: "/test"},
	}).Add(100)

	// Create test server with the metrics handler
	prometheusHandler := metrics.NewPrometheusHandler()
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	// Serve the request
	prometheusHandler.ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Expected Content-Type to contain 'text/plain', got %s", contentType)
	}

	body := w.Body.String()

	// Verify Prometheus format
	if !strings.Contains(body, "# HELP") {
		t.Error("Expected response to contain '# HELP' annotations")
	}

	if !strings.Contains(body, "# TYPE") {
		t.Error("Expected response to contain '# TYPE' annotations")
	}

	// Verify our test metrics are present
	if !strings.Contains(body, "test_requests") {
		t.Error("Expected response to contain 'test_requests' metric")
	}

	if !strings.Contains(body, "test_latency") {
		t.Error("Expected response to contain 'test_latency' metric")
	}

	// Verify uptime metric
	if !strings.Contains(body, "llm_gateway_uptime_seconds") {
		t.Error("Expected response to contain 'llm_gateway_uptime_seconds' metric")
	}

	t.Logf("Metrics response (%d bytes):\n%s", len(body), body)
}

func TestGatewayService_MetricsEndpointFormat(t *testing.T) {
	// Test that metrics output is valid Prometheus format
	prometheusHandler := metrics.NewPrometheusHandler()

	// Add various metric types
	metrics.Counter("http_requests_total", metrics.Labels{
		{Name: "method", Value: "POST"},
		{Name: "endpoint", Value: "/v1/chat/completions"},
	}).IncrBy(50)

	metrics.Latency("request_duration_ms", metrics.Labels{
		{Name: "handler", Value: "openai"},
	}).Add(250)

	metrics.StatusValue("active_connections", metrics.Labels{}).Set(15.5)

	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	prometheusHandler.ServeHTTP(w, req)

	body := w.Body.String()
	lines := strings.Split(body, "\n")

	var hasHelp, hasType, hasMetricValue bool

	for _, line := range lines {
		if strings.HasPrefix(line, "# HELP") {
			hasHelp = true
		}
		if strings.HasPrefix(line, "# TYPE") {
			hasType = true
		}
		// Check for metric values (lines without # prefix and not empty)
		if line != "" && !strings.HasPrefix(line, "#") {
			hasMetricValue = true
		}
	}

	if !hasHelp {
		t.Error("Expected at least one '# HELP' line")
	}
	if !hasType {
		t.Error("Expected at least one '# TYPE' line")
	}
	if !hasMetricValue {
		t.Error("Expected at least one metric value line")
	}
}

func TestScheduleService_PrometheusMetricsEndpoint(t *testing.T) {
	// Create test configuration
	config := &options.Config{
		Host:             "localhost",
		Port:             8081,
		SchedulePolicy:   "prefix-cache",
		LocalTestIPs:     "127.0.0.1:9000",
		UseDiscovery:     "eas",
		EnableAccessLog:  true,
		MaxQueueSize:     10,
		WaitQueueThreads: 2,
	}

	// Create schedule service
	scheduleService := NewScheduleService(config)
	if scheduleService == nil {
		t.Fatal("Failed to create schedule service")
	}

	// Add some test metrics
	metrics.Counter("schedule_requests", metrics.Labels{
		{Name: "status", Value: "success"},
	}).IncrByOne()

	metrics.Latency("schedule_latency", metrics.Labels{
		{Name: "policy", Value: "prefix-cache"},
	}).Add(50)

	// Create test request
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()

	// Serve the request
	scheduleService.ServeHTTP(w, req)

	// Verify response
	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, got %d", http.StatusOK, w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("Expected Content-Type to contain 'text/plain', got %s", contentType)
	}

	body := w.Body.String()

	// Verify Prometheus format
	if !strings.Contains(body, "# HELP") {
		t.Error("Expected response to contain '# HELP' annotations")
	}

	if !strings.Contains(body, "# TYPE") {
		t.Error("Expected response to contain '# TYPE' annotations")
	}

	// Verify our test metrics are present
	if !strings.Contains(body, "schedule_requests") {
		t.Error("Expected response to contain 'schedule_requests' metric")
	}

	if !strings.Contains(body, "schedule_latency") {
		t.Error("Expected response to contain 'schedule_latency' metric")
	}

	// Verify runtime metrics (all categories)
	runtimeMetrics := []string{
		// Category 1: GC metrics
		"go_gc_duration_seconds",
		"go_memstats_last_gc_time_seconds",
		"go_memstats_gc_sys_bytes",
		"go_memstats_num_gc",
		"go_memstats_num_forced_gc",
		// Category 2: Thread metrics
		"go_goroutines",
		"go_threads",
		// Category 3: Memory fine-grained metrics
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
		// Category 4: Process resource metrics
		"go_cpu_count",
	}

	for _, metric := range runtimeMetrics {
		if !strings.Contains(body, metric) {
			t.Errorf("Expected response to contain '%s'", metric)
		}
	}

	// Verify uptime metric
	if !strings.Contains(body, "llm_gateway_uptime_seconds") {
		t.Error("Expected response to contain 'llm_gateway_uptime_seconds' metric")
	}

	t.Logf("Metrics response (%d bytes)", len(body))
}

func TestScheduleService_MetricsRouting(t *testing.T) {
	// Create test configuration
	config := &options.Config{
		Host:             "localhost",
		Port:             8081,
		SchedulePolicy:   "prefix-cache",
		LocalTestIPs:     "127.0.0.1:9000",
		UseDiscovery:     "eas",
		EnableAccessLog:  false,
		MaxQueueSize:     10,
		WaitQueueThreads: 2,
	}

	// Create schedule service
	scheduleService := NewScheduleService(config)
	if scheduleService == nil {
		t.Fatal("Failed to create schedule service")
	}

	// Test metrics endpoint exists
	req := httptest.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	scheduleService.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected /metrics endpoint to return 200, got %d", w.Code)
	}

	// Test healthz endpoint still works
	req = httptest.NewRequest("GET", "/healthz", nil)
	w = httptest.NewRecorder()
	scheduleService.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected /healthz endpoint to return 200, got %d", w.Code)
	}

	// Test unknown endpoint returns 404
	req = httptest.NewRequest("GET", "/unknown", nil)
	w = httptest.NewRecorder()
	scheduleService.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("Expected /unknown endpoint to return 404, got %d", w.Code)
	}
}
