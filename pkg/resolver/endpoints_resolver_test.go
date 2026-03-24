package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func TestEndpointsResolverBuilder_Schema(t *testing.T) {
	builder := &EndpointsResolverBuilder{}
	assert.Equal(t, "endpoints", builder.Schema())
}

func TestEndpointsLlmResolverBuilder_Schema(t *testing.T) {
	builder := &EndpointsLlmResolverBuilder{}
	assert.Equal(t, "llm+endpoints", builder.Schema())
}

func TestEndpointsResolverBuilder_Build_InvalidURI(t *testing.T) {
	builder := &EndpointsResolverBuilder{}

	// Test empty URI
	_, err := builder.Build("", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing or invalid")

	// Test wrong prefix
	_, err = builder.Build("redis://localhost:8080", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must start with")
}

func TestEndpointsLlmResolverBuilder_Build_InvalidURI(t *testing.T) {
	builder := &EndpointsLlmResolverBuilder{}

	// Test empty URI
	_, err := builder.Build("", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing or invalid")

	// Test wrong prefix
	_, err = builder.Build("redis://localhost:8080", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must start with")
}

func TestEndpointsLlmResolverBuilder_Build_MissingInstanceType(t *testing.T) {
	builder := &EndpointsLlmResolverBuilder{}
	args := BuildArgs{}

	_, err := builder.Build("llm+endpoints://localhost:8080", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance_type")
}

func TestEndpointsResolver_GetEndpoints(t *testing.T) {
	builder := &EndpointsResolverBuilder{}
	
	// Test with valid URI
	resolver, err := builder.Build("endpoints://localhost:8080,localhost:8081", BuildArgs{})
	assert.NoError(t, err)
	
	endpoints, err := resolver.GetEndpoints()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(endpoints))
	assert.Equal(t, "localhost", endpoints[0].Host)
	assert.Equal(t, 8080, endpoints[0].Port)
	assert.Equal(t, "localhost", endpoints[1].Host)
	assert.Equal(t, 8081, endpoints[1].Port)
}

func TestEndpointsResolver_GetLLMInstances(t *testing.T) {
	builder := &EndpointsLlmResolverBuilder{}
	args := BuildArgs{
		"instance_type": string(consts.InferTypePrefill),
	}
	
	// Test with valid URI
	resolver, err := builder.Build("llm+endpoints://localhost:8080,localhost:8081", args)
	assert.NoError(t, err)
	
	instances, err := resolver.GetLLMInstances()
	assert.NoError(t, err)
	assert.Equal(t, 2, len(instances))
	assert.Equal(t, consts.InferTypePrefill, instances[0].InferType)
	assert.Equal(t, consts.InferTypePrefill, instances[1].InferType)
	assert.Equal(t, "localhost", instances[0].Endpoint.Host)
	assert.Equal(t, 8080, instances[0].Endpoint.Port)
	assert.Equal(t, "localhost", instances[1].Endpoint.Host)
	assert.Equal(t, 8081, instances[1].Endpoint.Port)
	// Check AuxIp and AuxPort are set correctly
	assert.Equal(t, "localhost", instances[0].AuxIp)
	assert.Equal(t, 28080, instances[0].AuxPort)
	assert.Equal(t, "localhost", instances[1].AuxIp)
	assert.Equal(t, 28081, instances[1].AuxPort)
}

func TestEndpointsUriPrefix(t *testing.T) {
	assert.Equal(t, "endpoints://", EndpointsUriPrefix)
	assert.Equal(t, "llm+endpoints://", EndpointsLlmUriPrefix)
}

func TestEndpointsResolver_ParseEndpoint(t *testing.T) {
	testCases := []struct {
		name      string
		input     string
		expected  types.Endpoint
		expectErr bool
	}{
		{
			name:  "valid endpoint with port",
			input: "localhost:8080",
			expected: types.Endpoint{
				Host: "localhost",
				Port: 8080,
			},
			expectErr: false,
		},
		{
			name:  "valid endpoint without port",
			input: "localhost",
			expected: types.Endpoint{
				Host: "localhost",
				Port: 80,
			},
			expectErr: false,
		},
		{
			name:      "invalid endpoint format",
			input:     "invalid:port:extra",
			expectErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := parseEndpoint(tc.input)
			if tc.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expected, result)
			}
		})
	}
}
