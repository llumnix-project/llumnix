package resolver

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

const (
	EndpointsUriPrefix    = "endpoints://"
	EndpointsLlmUriPrefix = "llm+endpoints://"
)

// EndpointsResolver implements both Resolver and LLMResolver interfaces
// for static endpoint lists specified in a URI.
type EndpointsResolver struct {
	inferType consts.InferType
	mu        sync.RWMutex
	endpoints types.EndpointSlice
	watcher   *Watcher
}

// newEndpointsResolver creates a new EndpointsResolver from a URI.
// If no port is specified, default port 80 is used.
func newEndpointsResolver(uri string, inferType consts.InferType) (*EndpointsResolver, error) {
	// Split by comma to get endpoint strings
	endpointStrs := strings.Split(uri, ",")
	if len(endpointStrs) == 0 {
		return nil, fmt.Errorf("no endpoints specified in URI")
	}

	// Parse each endpoint
	endpoints := make(types.EndpointSlice, 0, len(endpointStrs))
	for i, endpointStr := range endpointStrs {
		endpointStr = strings.TrimSpace(endpointStr)
		if endpointStr == "" {
			continue
		}

		// Parse endpoint
		ep, err := parseEndpoint(endpointStr)
		if err != nil {
			return nil, fmt.Errorf("invalid endpoint at position %d '%s': %v", i, endpointStr, err)
		}
		endpoints = append(endpoints, ep)
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no valid endpoints found in URI")
	}

	return &EndpointsResolver{
		inferType: inferType,
		endpoints: endpoints,
		watcher:   NewWatcher(),
	}, nil
}

// parseEndpoint parses a single endpoint string into an types.Endpoint.
// Supports formats: "host:port", "ip:port".
// If no port is specified, uses default port 80.
func parseEndpoint(endpointStr string) (types.Endpoint, error) {
	// Check if port is specified
	if !strings.Contains(endpointStr, ":") {
		// No port specified, use default port 80
		return types.Endpoint{
			Host: endpointStr,
			Port: 80,
		}, nil
	}
	// Use the existing NewEndpoint function which expects "ip:port" format
	return types.NewEndpoint(endpointStr)
}

// GetEndpoints implements the Resolver interface.
// It returns the current list of endpoints for this resolver.
func (er *EndpointsResolver) GetEndpoints() ([]types.Endpoint, error) {
	er.mu.RLock()
	defer er.mu.RUnlock()

	return er.endpoints, nil
}

// GetLLMInstances implements the LLMResolver interface.
// It converts the current endpoints to types.LLMInstance objects.
func (er *EndpointsResolver) GetLLMInstances() (types.LLMInstanceSlice, error) {
	er.mu.RLock()
	defer er.mu.RUnlock()

	instances := make(types.LLMInstanceSlice, 0, len(er.endpoints))
	for _, ep := range er.endpoints {
		instances = append(instances, types.LLMInstance{
			Endpoint: ep,
			InferType: er.inferType,
			// endpoints discovery for backend is just used for testing, hack it here!
			AuxIp:   ep.Host,
			AuxPort: 20000 + ep.Port,
		})
	}
	return instances, nil
}

// Watch implements the LLMResolver interface.
// It returns channels for monitoring added and removed LLM instances.
func (er *EndpointsResolver) Watch(ctx context.Context) (<-chan types.LLMInstanceSlice, <-chan types.LLMInstanceSlice, error) {
	return er.watcher.Watch(ctx, er.GetLLMInstances)
}

// EndpointsResolverBuilder implements ResolverBuilder for creating EndpointsResolver instances.
type EndpointsResolverBuilder struct{}

// Schema returns the schema identifier for this builder: "endpoints".
func (b *EndpointsResolverBuilder) Schema() string {
	return "endpoints"
}

// Build creates a new EndpointsResolver instance from the provided arguments.
// The args map must contain a "uri" key with a valid endpoints URI string.
func (b *EndpointsResolverBuilder) Build(uri string, args BuildArgs) (Resolver, error) {
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}
	// Ensure the URI has the correct prefix
	if !strings.HasPrefix(uri, EndpointsUriPrefix) {
		return nil, fmt.Errorf("invalid endpoints URI format: must start with '%s'", EndpointsUriPrefix)
	}
	uri = strings.TrimPrefix(uri, EndpointsUriPrefix)
	return newEndpointsResolver(uri, consts.InferTypeNeutral)
}

// EndpointsLlmResolverBuilder implements LLMResolverBuilder for creating EndpointsResolver instances.
type EndpointsLlmResolverBuilder struct{}

// Schema returns the schema identifier for this builder: "endpoints+llm".
func (b *EndpointsLlmResolverBuilder) Schema() string {
	return "llm+endpoints"
}

// Build creates a new EndpointsResolver instance from the provided arguments.
// The args map must contain a "uri" key with a valid endpoints URI string.
func (b *EndpointsLlmResolverBuilder) Build(uri string, args BuildArgs) (LLMResolver, error) {
	// Ensure the URI has the correct prefix
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}
	if !strings.HasPrefix(uri, EndpointsLlmUriPrefix) {
		return nil, fmt.Errorf("invalid endpoints URI format: must start with '%s'", EndpointsLlmUriPrefix)
	}
	// Remove the endpoints+llm:// prefix and treat it as regular endpoints URI
	uri = strings.TrimPrefix(uri, EndpointsLlmUriPrefix)

	// Read instance type build args
	instanceTypeStr, ok := args["instance_type"].(string)
	if !ok || instanceTypeStr == "" {
		return nil, fmt.Errorf("missing instance_type or invalid instance_type build args: %v", instanceTypeStr)
	}

	return newEndpointsResolver(uri, consts.InferType(instanceTypeStr))
}

func init() {
	Register(&EndpointsResolverBuilder{})
	RegisterLLM(&EndpointsLlmResolverBuilder{})
}
