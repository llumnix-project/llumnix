package handler

import (
	"easgo/pkg/llm-gateway/types"
	"time"
)

const (
	// connectRetry defines the maximum number of retry attempts for backend connection failures
	connectRetry = 2

	// ReadBufferSize specifies the initial buffer size (8KB) for reading streaming responses
	ReadBufferSize = 8 * 1024

	// ReadTimeout sets the maximum duration (5 minutes) to wait for reading from backend stream
	ReadTimeout = 5 * time.Minute
)

// RequestHandler defines the interface for handling LLM inference requests
// Implementations should provide request parsing and processing logic
type RequestHandler interface {
	// ParseRequest validates and parses the incoming request into internal format
	ParseRequest(req *types.RequestContext) error

	// Handle processes the request and sends the response back to the client
	Handle(req *types.RequestContext)
}

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
