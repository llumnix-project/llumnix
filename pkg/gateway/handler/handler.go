package handler

import (
	"fmt"
	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/types"
	"sync"
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
type HandlerFactory func(config *options.GatewayConfig) (RequestHandler, error)

// handlerRegistry holds registered handler factories indexed by protocol type
var (
	handlerRegistry = make(map[string]HandlerFactory)
	handlerMu       sync.RWMutex
)

// registerHandler registers a request handler factory with the specified protocol type
// The protocol type is typically "openai", "anthropic", etc.
func registerHandler(protocol string, factory HandlerFactory) {
	handlerMu.Lock()
	defer handlerMu.Unlock()
	handlerRegistry[protocol] = factory
}

// BuildHandler creates a RequestHandler instance based on the protocol type
// Returns an error if the protocol type is not registered
func BuildHandler(protocol string, config *options.GatewayConfig) (RequestHandler, error) {
	handlerMu.RLock()
	factory, exists := handlerRegistry[protocol]
	handlerMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown protocol type: %s", protocol)
	}

	return factory(config)
}
