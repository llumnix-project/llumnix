package handler

import (
	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/types"
	"fmt"
	"sync"
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

// HandlerFactory is a factory function that creates a RequestHandler instance
// It takes a config as parameter and returns the corresponding handler
type HandlerFactory func(config *options.Config) (RequestHandler, error)

// handlerRegistry holds registered handler factories indexed by protocol type
var (
	handlerRegistry = make(map[string]HandlerFactory)
	handlerMu       sync.RWMutex
)

// RegisterHandler registers a request handler factory with the specified protocol type
// The protocol type is typically "openai", "anthropic", etc.
func RegisterHandler(protocol string, factory HandlerFactory) {
	handlerMu.Lock()
	defer handlerMu.Unlock()
	handlerRegistry[protocol] = factory
}

// BuildHandler creates a RequestHandler instance based on the protocol type
// Returns an error if the protocol type is not registered
func BuildHandler(protocol string, config *options.Config) (RequestHandler, error) {
	handlerMu.RLock()
	factory, exists := handlerRegistry[protocol]
	handlerMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown protocol type: %s", protocol)
	}

	return factory(config)
}
