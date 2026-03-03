package registry

import (
	"llm-gateway/pkg/types"
	"sync"
)

// PreProcessorFactory is a function that creates a PreProcessor instance with params.
type PreProcessorFactory func(params map[string]interface{}) PreProcessor

// PostProcessorFactory is a function that creates a PostProcessor instance with params.
type PostProcessorFactory func(params map[string]interface{}) PostProcessor

// PreProcessor defines the interface for pre-processing requests.
// Pre-processors are executed before proxy processing logic.
//
// IMPORTANT: All implementations MUST be stateless and functional.
// DO NOT store any request state in processor implementations.
// All request state MUST be stored in RequestContext.
type PreProcessor interface {
	// Name returns the name of the pre-processor.
	Name() string
	// PreProcess processes the request and returns an error.
	PreProcess(request *types.RequestContext) error
}

// PostProcessor defines the interface for post-processing requests.
// Post-processors are executed after the main processing logic.
//
// IMPORTANT: All implementations MUST be stateless and functional.
// DO NOT store any request state in processor implementations.
// All request state MUST be stored in RequestContext.
type PostProcessor interface {
	// Name returns the name of the post-processor.
	Name() string
	// PostProcess processes the request and returns an error.
	PostProcess(request *types.RequestContext) error
	// PostStreamProcess processes the request stream and returns an error.
	PostStreamProcess(request *types.RequestContext, done bool) error
}

var (
	preProcessorFactories  = make(map[string]PreProcessorFactory)
	postProcessorFactories = make(map[string]PostProcessorFactory)
	mu                     sync.RWMutex
)

// RegisterPreProcessor registers a pre-processor factory with the given name.
func RegisterPreProcessor(name string, factory PreProcessorFactory) {
	mu.Lock()
	defer mu.Unlock()
	preProcessorFactories[name] = factory
}

// RegisterPostProcessor registers a post-processor factory with the given name.
func RegisterPostProcessor(name string, factory PostProcessorFactory) {
	mu.Lock()
	defer mu.Unlock()
	postProcessorFactories[name] = factory
}

// BuildPreProcessor creates a pre-processor instance by name with params.
func BuildPreProcessor(name string, params map[string]interface{}) PreProcessor {
	mu.RLock()
	defer mu.RUnlock()
	if factory, ok := preProcessorFactories[name]; ok {
		return factory(params)
	}
	return nil
}

// BuildPostProcessor creates a post-processor instance by name with params.
func BuildPostProcessor(name string, params map[string]interface{}) PostProcessor {
	mu.RLock()
	defer mu.RUnlock()
	if factory, ok := postProcessorFactories[name]; ok {
		return factory(params)
	}
	return nil
}
