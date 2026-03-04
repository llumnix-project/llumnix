package res

import (
	"fmt"
	"io"
	"strings"

	"github.com/prometheus/common/expfmt"
)

// BackendType represents the type of LLM inference backend.
type BackendType string

const (
	BackendVLLM   BackendType = "vllm"
	BackendSGLang BackendType = "sglang"
)

// BackendParser defines the interface for backend-specific metric extraction.
type BackendParser interface {
	// MetricURL returns the metrics endpoint URL for the given endpoint.
	MetricURL(endpoint string) string

	// CanHandle checks if this parser can handle the given metrics.
	// It returns true if the metrics contain the expected metric names for this backend.
	CanHandle(metrics map[string]float64) bool

	// ExtractMetrics extracts EngineState from parsed Prometheus metrics map.
	ExtractMetrics(metrics map[string]float64) EngineState

	// BackendType returns the backend type identifier.
	BackendType() BackendType
}

// vLLM parser implementation
type vllmParser struct{}

var vllmMetricNames = []string{
	"vllm:num_requests_running",
	"vllm:num_requests_swapped",
	"vllm:num_requests_waiting",
	"vllm:gpu_cache_usage_perc",
	"vllm:cpu_cache_usage_perc",
	"vllm:prompt_tokens_total",
	"vllm:generation_tokens_total",
}

func (p *vllmParser) MetricURL(endpoint string) string {
	return fmt.Sprintf("http://%s/metrics/", endpoint)
}

func (p *vllmParser) CanHandle(metrics map[string]float64) bool {
	// Check if at least one vLLM specific metric exists
	for _, name := range vllmMetricNames {
		if _, exists := metrics[name]; exists {
			return true
		}
	}
	return false
}

func (p *vllmParser) BackendType() BackendType {
	return BackendVLLM
}

func (p *vllmParser) ExtractMetrics(metrics map[string]float64) EngineState {
	var m EngineState

	if val, ok := metrics["vllm:num_requests_running"]; ok {
		m.RunningRequests = int(val)
	}
	if val, ok := metrics["vllm:num_requests_swapped"]; ok {
		m.SwappedRequests = int(val)
	}
	if val, ok := metrics["vllm:num_requests_waiting"]; ok {
		m.WaitingRequests = int(val)
	}
	if val, ok := metrics["vllm:gpu_cache_usage_perc"]; ok {
		m.GPUCacheUsage = val * 100
	}
	if val, ok := metrics["vllm:cpu_cache_usage_perc"]; ok {
		m.CPUCacheUsage = val * 100
	}
	if val, ok := metrics["vllm:prompt_tokens_total"]; ok {
		m.PromptTokensTotal = int64(val)
	}
	if val, ok := metrics["vllm:generation_tokens_total"]; ok {
		m.GenerationTokensTotal = int64(val)
	}

	return m
}

// SGLang parser implementation
type sglangParser struct{}

var sglangMetricNames = []string{
	"sglang:num_running_reqs",
	"sglang:num_queue_reqs",
	"sglang:token_usage",
	"sglang:prompt_tokens_total",
	"sglang:gen_throughput",
}

func (p *sglangParser) MetricURL(endpoint string) string {
	return fmt.Sprintf("http://%s/metrics", endpoint)
}

func (p *sglangParser) CanHandle(metrics map[string]float64) bool {
	// Check if at least one SGLang specific metric exists
	for _, name := range sglangMetricNames {
		if _, exists := metrics[name]; exists {
			return true
		}
	}
	return false
}

func (p *sglangParser) BackendType() BackendType {
	return BackendSGLang
}

func (p *sglangParser) ExtractMetrics(metrics map[string]float64) EngineState {
	var m EngineState

	if val, ok := metrics["sglang:num_running_reqs"]; ok {
		m.RunningRequests = int(val)
	}
	if val, ok := metrics["sglang:num_queue_reqs"]; ok {
		m.WaitingRequests = int(val)
	}
	if val, ok := metrics["sglang:token_usage"]; ok {
		m.GPUCacheUsage = val * 100
	}
	if val, ok := metrics["sglang:prompt_tokens_total"]; ok {
		m.PromptTokensTotal = int64(val)
	}
	if val, ok := metrics["sglang:gen_throughput"]; ok {
		m.TokenPerSecondOut = float64(int(val))
	}

	return m
}

// backendRegistry stores all registered backend parsers in order.
var backendRegistry = []BackendParser{
	&vllmParser{},
	&sglangParser{},
}

// DetectBackend analyzes the metrics and returns the appropriate parser.
// It iterates through all registered parsers and returns the first one that can handle the metrics.
// Returns nil if no parser can handle the metrics.
func DetectBackend(metrics map[string]float64) BackendParser {
	for _, parser := range backendRegistry {
		if parser.CanHandle(metrics) {
			return parser
		}
	}
	return nil
}

// GetParserByType returns the parser for the given backend type.
// Returns nil if the backend type is not supported.
func GetParserByType(backendType BackendType) BackendParser {
	for _, parser := range backendRegistry {
		if parser.BackendType() == backendType {
			return parser
		}
	}
	return nil
}

// ParseAllMetrics parses Prometheus format metrics text and returns all metrics as a map.
// Uses the official Prometheus expfmt parser for robust parsing.
func ParseAllMetrics(data string) map[string]float64 {
	metrics := make(map[string]float64)

	// Create a text parser
	parser := expfmt.TextParser{}

	// Parse the metrics text
	families, err := parser.TextToMetricFamilies(io.NopCloser(strings.NewReader(data)))
	if err != nil {
		return metrics
	}

	// Extract individual metrics from metric families
	for name, family := range families {
		// Iterate over all metrics in this family
		for _, metric := range family.Metric {
			var value float64

			// Extract value based on metric type
			switch {
			case metric.Counter != nil:
				value = metric.Counter.GetValue()
			case metric.Gauge != nil:
				value = metric.Gauge.GetValue()
			case metric.Untyped != nil:
				value = metric.Untyped.GetValue()
			default:
				// Skip histograms and summaries
				continue
			}

			metrics[name] = value
		}
	}

	return metrics
}
