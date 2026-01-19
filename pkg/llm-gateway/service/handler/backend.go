package handler

import (
	"easgo/pkg/llm-gateway/types"
	"fmt"
	"sync"
)

// StreamChunk represents a single chunk of data from a streaming inference response
// It contains either data bytes or an error that occurred during streaming
type StreamChunk struct {
	// err holds any error that occurred while processing this chunk
	err error

	// Data contains the actual response bytes for this chunk
	Data []byte
}

// InferenceBackend defines the interface for coordinating with backend inference engines
// It handles both regular inference and PD-separated inference modes
// The backend itself does not perform inference, but communicates with actual inference engines
type InferenceBackend interface {
	// StreamInference sends the request to backend inference engine and returns a channel
	// that streams response chunks back to the caller
	StreamInference(req *types.RequestContext) (<-chan StreamChunk, error)
}

// BackendFactory is a factory function that creates an InferenceBackend instance
// It takes a schedule mode as parameter and returns the corresponding backend
type BackendFactory func(scheduleMode types.ScheduleMode) (InferenceBackend, error)

// backendRegistry holds registered backend factories indexed by backend type key
var (
	backendRegistry = make(map[string]BackendFactory)
	registryMu      sync.RWMutex
)

// RegisterBackend registers a backend factory with the specified type key
// The key is typically the split mode identifier (e.g., "simple", "vllm-kvt", "sglang-mooncake")
func RegisterBackend(backendType string, factory BackendFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	backendRegistry[backendType] = factory
}

// BuildBackend creates an InferenceBackend instance based on the backend type and schedule mode
// Returns an error if the backend type is not registered
func BuildBackend(backendType string, scheduleMode types.ScheduleMode) (InferenceBackend, error) {
	registryMu.RLock()
	factory, exists := backendRegistry[backendType]
	registryMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown backend type: %s", backendType)
	}

	return factory(scheduleMode)
}
