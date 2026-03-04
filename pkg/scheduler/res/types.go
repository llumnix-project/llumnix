package res

import "time"

// InstanceConfig represents the configuration for an instance to collect metrics from.
type InstanceConfig struct {
	InstanceID string
	Endpoint   string
	Backend    BackendType // Detected backend type (empty means not detected yet)
	Model      string      // Model name served by this instance

	// ConsecutiveFailures tracks the number of consecutive metric collection failures
	// When this reaches MaxConsecutiveFailures, the instance will be skipped
	ConsecutiveFailures int
}

// EngineState represents the metrics collected from a remote LLM engine instance.
// It contains resource usage, load, and throughput metrics.
type EngineState struct {
	// Instance identification
	InstanceID string
	Endpoint   string
	Model      string // Model name served by this instance
	Timestamp  time.Time

	// Request load metrics
	RunningRequests int // Number of requests currently being processed
	SwappedRequests int // Number of swapped requests (vLLM specific)
	WaitingRequests int // Number of requests waiting in queue

	// Cache usage metrics (percentage, 0-100)
	GPUCacheUsage float64 // GPU cache usage percentage
	CPUCacheUsage float64 // CPU cache usage percentage (vLLM specific)

	// Token throughput metrics
	PromptTokensTotal     int64 // Total prompt tokens processed
	GenerationTokensTotal int64 // Total generation tokens produced

	// Derived throughput metrics (tokens per second)
	TokenPerSecondIn  float64 // Input token throughput
	TokenPerSecondOut float64 // Output token throughput
}
