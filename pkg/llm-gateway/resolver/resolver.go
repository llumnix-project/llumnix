package resolver

import (
	"context"
	"easgo/pkg/llm-gateway/types"
	"fmt"
	"strings"
	"sync"

	"k8s.io/klog/v2"
)

// BuildArgs is a map of string keys to arbitrary values
type BuildArgs map[string]interface{}

// ResolverBuilder is an interface for creating Resolver instances.
type ResolverBuilder interface {
	// Build creates a new Resolver instance using the provided arguments.
	Build(uri string, args BuildArgs) (Resolver, error)

	// Schema returns a unique identifier for this builder type.
	// This is used to register and lookup builders in the registry.
	Schema() string
}

// LLMResolverBuilder is an interface for creating LLMResolver instances.
type LLMResolverBuilder interface {
	// Build creates a new LLMResolver instance using the provided arguments.
	Build(uri string, args BuildArgs) (LLMResolver, error)

	// Schema returns a unique identifier for this builder type.
	// This is used to register and lookup builders in the registry.
	Schema() string
}

var (
	// rMutex protects access to the resolverBuilders map
	rMutex sync.Mutex
	// resolverBuilders is a registry of available resolver builders
	// keyed by their schema identifier
	resolverBuilders map[string]ResolverBuilder

	// rLlmMutex protects access to the llmResolverBuilders map
	rLlmMutex sync.Mutex

	// llmResolverBuilders is a registry of available llm resolver builders
	// keyed by their schema identifier
	llmResolverBuilders map[string]LLMResolverBuilder
)

// Register adds a ResolverBuilder to the global registry.
func Register(builder ResolverBuilder) {
	rMutex.Lock()
	defer rMutex.Unlock()

	if resolverBuilders == nil {
		resolverBuilders = make(map[string]ResolverBuilder)
	}

	schema := builder.Schema()
	if _, ok := resolverBuilders[schema]; ok {
		klog.Warningf("register resolver failed: %s already registered", schema)
		return
	}

	resolverBuilders[schema] = builder
	klog.V(2).Infof("resolver builder registered: %s", schema)
}

// RegisterLLM adds a LLMResolverBuilder to the global registry.
func RegisterLLM(builder LLMResolverBuilder) {
	rLlmMutex.Lock()
	defer rLlmMutex.Unlock()

	if llmResolverBuilders == nil {
		llmResolverBuilders = make(map[string]LLMResolverBuilder)
	}

	schema := builder.Schema()
	if _, ok := llmResolverBuilders[schema]; ok {
		klog.Warningf("register llm resolver failed: %s already registered", schema)
		return
	}

	llmResolverBuilders[schema] = builder
	klog.V(2).Infof("llm resolver builder registered: %s", schema)
}

func getBuilder(schema string) (ResolverBuilder, bool) {
	rMutex.Lock()
	defer rMutex.Unlock()

	builder, ok := resolverBuilders[schema]
	return builder, ok
}

func getLlmBuilder(schema string) (LLMResolverBuilder, bool) {
	rLlmMutex.Lock()
	defer rLlmMutex.Unlock()

	builder, ok := llmResolverBuilders[schema]
	return builder, ok
}

// BuildResolver creates a new Resolver instance using the specified schema and arguments.
func BuildResolver(uri string, args BuildArgs) (Resolver, error) {
	parts := strings.SplitN(uri, "://", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("resolver uri is not correct: %s", uri)
	}
	schema := parts[0]
	builder, ok := getBuilder(schema)
	if !ok {
		return nil, fmt.Errorf("no resolver builder registered for schema: %s", schema)
	}
	return builder.Build(uri, args)
}

// BuildLlmResolver creates a new LLMResolver instance using the specified schema and arguments.
func BuildLlmResolver(uri string, args BuildArgs) (LLMResolver, error) {
	parts := strings.SplitN(uri, "://", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("llm resolver uri is not correct: %s", uri)
	}
	schema := parts[0]
	builder, ok := getLlmBuilder(schema)
	if !ok {
		return nil, fmt.Errorf("no llm resolver builder registered for schema: %s", schema)
	}
	return builder.Build(uri, args)
}

// ListSchemas returns a list of all registered resolver builder schemas.
// This function is thread-safe.
func ListSchemas() []string {
	rMutex.Lock()
	defer rMutex.Unlock()

	schemas := make([]string, 0, len(resolverBuilders))
	for schema := range resolverBuilders {
		schemas = append(schemas, schema)
	}
	return schemas
}

// Resolver is an interface for discovering service endpoints.
// Implementations of this interface provide mechanisms to get current
// endpoint information for load balancing and service discovery.
type Resolver interface {
	// GetEndpoints returns the current list of available endpoints.
	// This provides a snapshot of the endpoint state at the time of calling.
	GetEndpoints() ([]types.Endpoint, error)
}

// LLMResolver is an interface for discovering and monitoring LLM workers.
// Implementations of this interface provide mechanisms to get current worker
// state and watch for changes over time.
type LLMResolver interface {
	// GetLLMWorkers returns the current list of available LLM workers.
	// This provides a snapshot of the worker state at the time of calling.
	GetLLMWorkers() (types.LLMWorkerSlice, error)

	// Watch returns two channels for monitoring worker changes.
	// The first channel receives slices of workers that have been added.
	// The second channel receives slices of workers that have been removed.
	// Both channels will be closed when the context is cancelled or the resolver stops.
	// This method supports multiple concurrent observers.
	Watch(ctx context.Context) (<-chan types.LLMWorkerSlice, <-chan types.LLMWorkerSlice, error)
}
