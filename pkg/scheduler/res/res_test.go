package res

import (
	"testing"
	"time"
)

func TestParseAllMetrics(t *testing.T) {
	tests := []struct {
		name     string
		data     string
		wantKeys []string
		wantVals map[string]float64
	}{
		{
			name: "vLLM metrics",
			data: `# HELP vllm metrics
vllm:num_requests_running 5
vllm:num_requests_waiting 3
vllm:gpu_cache_usage_perc 0.85
vllm:prompt_tokens_total 1000
`,
			wantKeys: []string{
				"vllm:num_requests_running",
				"vllm:num_requests_waiting",
				"vllm:gpu_cache_usage_perc",
				"vllm:prompt_tokens_total",
			},
			wantVals: map[string]float64{
				"vllm:num_requests_running": 5,
				"vllm:num_requests_waiting": 3,
				"vllm:gpu_cache_usage_perc": 0.85,
				"vllm:prompt_tokens_total":  1000,
			},
		},
		{
			name: "SGLang metrics",
			data: `sglang:num_running_reqs 2
sglang:num_queue_reqs 5
sglang:token_usage 0.75
sglang:gen_throughput 150.5
`,
			wantKeys: []string{
				"sglang:num_running_reqs",
				"sglang:num_queue_reqs",
				"sglang:token_usage",
				"sglang:gen_throughput",
			},
			wantVals: map[string]float64{
				"sglang:num_running_reqs": 2,
				"sglang:num_queue_reqs":   5,
				"sglang:token_usage":      0.75,
				"sglang:gen_throughput":   150.5,
			},
		},
		{
			name: "metrics with labels",
			data: `vllm:num_requests_running{model="llama"} 5
vllm:gpu_cache_usage_perc{instance="gpu0"} 0.85
`,
			wantKeys: []string{
				"vllm:num_requests_running",
				"vllm:gpu_cache_usage_perc",
			},
			wantVals: map[string]float64{
				"vllm:num_requests_running": 5,
				"vllm:gpu_cache_usage_perc": 0.85,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metrics := ParseAllMetrics(tt.data)

			// Check that all expected keys exist
			for _, key := range tt.wantKeys {
				if _, ok := metrics[key]; !ok {
					t.Errorf("Missing metric %s", key)
				}
			}

			// Check values
			for key, wantVal := range tt.wantVals {
				if gotVal, ok := metrics[key]; !ok {
					t.Errorf("Missing metric %s", key)
				} else if gotVal != wantVal {
					t.Errorf("Metric %s = %f, want %f", key, gotVal, wantVal)
				}
			}
		})
	}
}

func TestVLLMExtractMetrics(t *testing.T) {
	metrics := map[string]float64{
		"vllm:num_requests_running":    5,
		"vllm:num_requests_swapped":    2,
		"vllm:num_requests_waiting":    3,
		"vllm:gpu_cache_usage_perc":    0.85,
		"vllm:cpu_cache_usage_perc":    0.25,
		"vllm:prompt_tokens_total":     1000,
		"vllm:generation_tokens_total": 500,
	}

	parser := GetParserByType(BackendVLLM)
	if parser == nil {
		t.Fatal("GetParserByType(BackendVLLM) returned nil")
	}
	llmMetric := parser.ExtractMetrics(metrics)

	if llmMetric.RunningRequests != 5 {
		t.Errorf("RunningRequests = %d, want 5", llmMetric.RunningRequests)
	}
	if llmMetric.SwappedRequests != 2 {
		t.Errorf("SwappedRequests = %d, want 2", llmMetric.SwappedRequests)
	}
	if llmMetric.WaitingRequests != 3 {
		t.Errorf("WaitingRequests = %d, want 3", llmMetric.WaitingRequests)
	}
	if llmMetric.GPUCacheUsage != 85.0 {
		t.Errorf("GPUCacheUsage = %f, want 85.0", llmMetric.GPUCacheUsage)
	}
	if llmMetric.CPUCacheUsage != 25.0 {
		t.Errorf("CPUCacheUsage = %f, want 25.0", llmMetric.CPUCacheUsage)
	}
	if llmMetric.PromptTokensTotal != 1000 {
		t.Errorf("PromptTokensTotal = %d, want 1000", llmMetric.PromptTokensTotal)
	}
	if llmMetric.GenerationTokensTotal != 500 {
		t.Errorf("GenerationTokensTotal = %d, want 500", llmMetric.GenerationTokensTotal)
	}
}

func TestSGLangExtractMetrics(t *testing.T) {
	metrics := map[string]float64{
		"sglang:num_running_reqs":    5,
		"sglang:num_queue_reqs":      3,
		"sglang:token_usage":         0.75,
		"sglang:prompt_tokens_total": 1000,
		"sglang:gen_throughput":      150.5,
	}

	parser := GetParserByType(BackendSGLang)
	if parser == nil {
		t.Fatal("GetParserByType(BackendSGLang) returned nil")
	}
	llmMetric := parser.ExtractMetrics(metrics)

	if llmMetric.RunningRequests != 5 {
		t.Errorf("RunningRequests = %d, want 5", llmMetric.RunningRequests)
	}
	if llmMetric.WaitingRequests != 3 {
		t.Errorf("WaitingRequests = %d, want 3", llmMetric.WaitingRequests)
	}
	if llmMetric.GPUCacheUsage != 75.0 {
		t.Errorf("GPUCacheUsage = %f, want 75.0", llmMetric.GPUCacheUsage)
	}
	if llmMetric.PromptTokensTotal != 1000 {
		t.Errorf("PromptTokensTotal = %d, want 1000", llmMetric.PromptTokensTotal)
	}
	if llmMetric.TokenPerSecondOut != 150.0 {
		t.Errorf("TokenPerSecondOut = %f, want 150.0", llmMetric.TokenPerSecondOut)
	}
}

func TestCalculateDerivedMetrics(t *testing.T) {
	prev := &EngineState{
		PromptTokensTotal:     1000,
		GenerationTokensTotal: 500,
	}

	current := &EngineState{
		PromptTokensTotal:     2000,
		GenerationTokensTotal: 1000,
	}

	durationMs := int64(10000) // 10 seconds

	calculateDerivedMetrics(current, prev, durationMs)

	// Expected: (2000-1000) * 1000 / 10000 = 100 tokens/sec
	if current.TokenPerSecondIn != 100.0 {
		t.Errorf("TokenPerSecondIn = %f, want 100.0", current.TokenPerSecondIn)
	}

	// Expected: (1000-500) * 1000 / 10000 = 50 tokens/sec
	if current.TokenPerSecondOut != 50.0 {
		t.Errorf("TokenPerSecondOut = %f, want 50.0", current.TokenPerSecondOut)
	}
}

func TestCalculateDerivedMetrics_NoPositiveDiff(t *testing.T) {
	prev := &EngineState{
		PromptTokensTotal:     2000,
		GenerationTokensTotal: 1000,
	}

	current := &EngineState{
		PromptTokensTotal:     1000, // Decreased, not increased
		GenerationTokensTotal: 500,
	}

	durationMs := int64(10000)

	calculateDerivedMetrics(current, prev, durationMs)

	// Should not calculate when diff is not positive
	if current.TokenPerSecondIn != 0 {
		t.Errorf("TokenPerSecondIn = %f, want 0 (no positive diff)", current.TokenPerSecondIn)
	}
	if current.TokenPerSecondOut != 0 {
		t.Errorf("TokenPerSecondOut = %f, want 0 (no positive diff)", current.TokenPerSecondOut)
	}
}

func TestGetMetricURL(t *testing.T) {
	tests := []struct {
		name     string
		backend  BackendType
		endpoint string
		want     string
	}{
		{
			name:     "vLLM backend",
			backend:  BackendVLLM,
			endpoint: "192.168.1.1:8000",
			want:     "http://192.168.1.1:8000/metrics/",
		},
		{
			name:     "SGLang backend",
			backend:  BackendSGLang,
			endpoint: "192.168.1.2:8000",
			want:     "http://192.168.1.2:8000/metrics",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := GetParserByType(tt.backend)
			if parser == nil {
				t.Fatalf("GetParserByType(%s) returned nil", tt.backend)
			}
			got := parser.MetricURL(tt.endpoint)
			if got != tt.want {
				t.Errorf("MetricURL() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestBackendParserRegistry(t *testing.T) {
	if GetParserByType(BackendVLLM) == nil {
		t.Error("GetParserByType(BackendVLLM) returned nil")
	}
	if GetParserByType(BackendSGLang) == nil {
		t.Error("GetParserByType(BackendSGLang) returned nil")
	}
	if GetParserByType("unknown") != nil {
		t.Error("GetParserByType(unknown) should return nil")
	}
}

func TestExtractMetricsWithMissingMetrics(t *testing.T) {
	// Test with empty metrics map
	emptyMetrics := map[string]float64{}

	vllmParser := GetParserByType(BackendVLLM)
	if vllmParser == nil {
		t.Fatal("GetParserByType(BackendVLLM) returned nil")
	}
	m1 := vllmParser.ExtractMetrics(emptyMetrics)
	if m1.RunningRequests != 0 {
		t.Errorf("RunningRequests should be 0 for empty metrics, got %d", m1.RunningRequests)
	}

	sglangParser := GetParserByType(BackendSGLang)
	if sglangParser == nil {
		t.Fatal("GetParserByType(BackendSGLang) returned nil")
	}
	m2 := sglangParser.ExtractMetrics(emptyMetrics)
	if m2.RunningRequests != 0 {
		t.Errorf("RunningRequests should be 0 for empty metrics, got %d", m2.RunningRequests)
	}

	// Test with partial metrics
	partialMetrics := map[string]float64{
		"vllm:num_requests_running": 5,
		// Missing other metrics
	}
	m3 := vllmParser.ExtractMetrics(partialMetrics)
	if m3.RunningRequests != 5 {
		t.Errorf("RunningRequests = %d, want 5", m3.RunningRequests)
	}
	if m3.WaitingRequests != 0 {
		t.Errorf("WaitingRequests should be 0 for missing metric, got %d", m3.WaitingRequests)
	}
}

func TestDetectBackend(t *testing.T) {
	tests := []struct {
		name         string
		metrics      map[string]float64
		expectedType BackendType
		expectNil    bool
	}{
		{
			name: "detect vLLM",
			metrics: map[string]float64{
				"vllm:num_requests_running": 5,
			},
			expectedType: BackendVLLM,
		},
		{
			name: "detect SGLang",
			metrics: map[string]float64{
				"sglang:num_running_reqs": 5,
			},
			expectedType: BackendSGLang,
		},
		{
			name: "vLLM takes priority (registered first)",
			metrics: map[string]float64{
				"vllm:num_requests_running": 5,
				"sglang:num_running_reqs":   3,
			},
			expectedType: BackendVLLM,
		},
		{
			name: "unknown backend",
			metrics: map[string]float64{
				"unknown_metric": 1,
			},
			expectNil: true,
		},
		{
			name:      "empty metrics",
			metrics:   map[string]float64{},
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parser := DetectBackend(tt.metrics)
			if tt.expectNil {
				if parser != nil {
					t.Errorf("DetectBackend() should return nil, got %v", parser)
				}
				return
			}

			if parser == nil {
				t.Fatalf("DetectBackend() returned nil, expected %s", tt.expectedType)
			}
			if parser.BackendType() != tt.expectedType {
				t.Errorf("BackendType() = %s, want %s", parser.BackendType(), tt.expectedType)
			}
		})
	}
}

func TestCanHandle(t *testing.T) {
	vllmParser := GetParserByType(BackendVLLM)
	sglangParser := GetParserByType(BackendSGLang)

	// vLLM can handle vLLM metrics
	vllmMetrics := map[string]float64{
		"vllm:num_requests_running": 5,
	}
	if !vllmParser.CanHandle(vllmMetrics) {
		t.Error("vllmParser should handle vLLM metrics")
	}
	if sglangParser.CanHandle(vllmMetrics) {
		t.Error("sglangParser should not handle vLLM metrics")
	}

	// SGLang can handle SGLang metrics
	sglangMetrics := map[string]float64{
		"sglang:num_running_reqs": 5,
	}
	if !sglangParser.CanHandle(sglangMetrics) {
		t.Error("sglangParser should handle SGLang metrics")
	}
	if vllmParser.CanHandle(sglangMetrics) {
		t.Error("vllmParser should not handle SGLang metrics")
	}

	// Neither can handle unknown metrics
	unknownMetrics := map[string]float64{
		"unknown_metric": 1,
	}
	if vllmParser.CanHandle(unknownMetrics) {
		t.Error("vllmParser should not handle unknown metrics")
	}
	if sglangParser.CanHandle(unknownMetrics) {
		t.Error("sglangParser should not handle unknown metrics")
	}
}

func TestNewCollector(t *testing.T) {
	c := NewCollector()
	if c == nil {
		t.Fatal("NewCollector() returned nil")
	}
	if c.client == nil {
		t.Error("collector.client is nil")
	}
	if c.previousMetrics == nil {
		t.Error("collector.previousMetrics is nil")
	}
}

func TestNewCollectorWithTimeout(t *testing.T) {
	timeout := 10 * time.Second
	c := NewCollectorWithTimeout(timeout)
	if c == nil {
		t.Fatal("NewCollectorWithTimeout() returned nil")
	}
	if c.client == nil {
		t.Error("collector.client is nil")
	}
	if c.client.Timeout != timeout {
		t.Errorf("client.Timeout = %v, want %v", c.client.Timeout, timeout)
	}
}

func TestCollector_RemoveInstance(t *testing.T) {
	c := NewCollector()

	// Add some previous metrics
	c.previousMetrics["instance-1"] = &EngineState{InstanceID: "instance-1"}
	c.previousMetrics["instance-2"] = &EngineState{InstanceID: "instance-2"}

	// Remove one instance
	c.RemoveInstance("instance-1")

	if _, exists := c.previousMetrics["instance-1"]; exists {
		t.Error("instance-1 should be removed")
	}
	if _, exists := c.previousMetrics["instance-2"]; !exists {
		t.Error("instance-2 should still exist")
	}
}
