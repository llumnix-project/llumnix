package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"llm-gateway/pkg/consts"
	loadbalancer "llm-gateway/pkg/load-balancer"
	"llm-gateway/pkg/structs"
)

// MockLoadBalancer is a mock implementation of the LoadBalancer interface
type MockLoadBalancer struct {
	mock.Mock
}

// Ensure MockLoadBalancer implements the LoadBalancer interface
var _ loadbalancer.LoadBalancer = &MockLoadBalancer{}

func (m *MockLoadBalancer) GetNextTokens(req *structs.Request) (*structs.NextTokens, error) {
	args := m.Called(req)
	return args.Get(0).(*structs.NextTokens), args.Error(1)
}

func (m *MockLoadBalancer) ReleaseToken(req *structs.Request, token *structs.Token) {
	m.Called(req, token)
}

func (m *MockLoadBalancer) ExcludeService(service string) {
	m.Called(service)
}

// Helper function to create a test request
func createTestRequest(model string) *structs.Request {
	return &structs.Request{
		Model: model,
	}
}

func TestNewServiceRouter(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*"},
	}
	routingPolicy := consts.RoutePolicyWeight

	sr := NewServiceRouter(mockLB, routingPolicy, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	assert.NotNil(t, sr)
	assert.Equal(t, mockLB, sr.lb)
	assert.Equal(t, routingConfigs, sr.routingConfigs)
	assert.Equal(t, routingPolicy, sr.routingPolicy)
}

func TestSetupFallbackConfigs(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", IsFallback: false},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*", IsFallback: true},
		{URL: "http://service3", Weight: 20, Prefix: "claude*", IsFallback: true},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs
	sr.setupFallbackConfigs(routingConfigs)

	// Check that fallback configs are added in the order they appear (no sorting by priority)
	assert.Equal(t, 2, len(sr.fallbackConfigs))
	assert.Equal(t, "http://service2", sr.fallbackConfigs[0].URL) // IsFallback: true (appears first in input after filtering)
	assert.Equal(t, "http://service3", sr.fallbackConfigs[1].URL) // IsFallback: true (appears second in input after filtering)
}

func TestSelectByWeight(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 30, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*"},
		{URL: "http://service3", Weight: 20, Prefix: "claude*"},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// Test that a config is always returned (weight-based selection)
	// We'll run this multiple times to verify the distribution
	configCounts := make(map[string]int)
	for i := 0; i < 1000; i++ {
		config, err := sr.selectByWeight()
		assert.NoError(t, err)
		assert.NotNil(t, config)
		configCounts[config.URL]++
	}

	// Verify that all configs were selected
	assert.Equal(t, 3, len(configCounts))

	// Verify approximate distribution (with some tolerance)
	// service2 should be selected most often (50% weight)
	// service1 should be selected next (30% weight)
	// service3 should be selected least (20% weight)
	assert.True(t, configCounts["http://service2"] > configCounts["http://service1"])
	assert.True(t, configCounts["http://service1"] > configCounts["http://service3"])
}

func TestSelectByWeight_SingleConfig(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*"},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// With only one config, it should always be selected
	for i := 0; i < 100; i++ {
		config, err := sr.selectByWeight()
		assert.NoError(t, err)
		assert.NotNil(t, config)
		assert.Equal(t, "http://service1", config.URL)
	}
}

func TestSelectByWeight_EmptyConfigs(t *testing.T) {
	mockLB := new(MockLoadBalancer)

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")

	// When there are no configs, it should return ErrorEndpointNotFound
	config, err := sr.selectByWeight()
	assert.Error(t, err)
	assert.Equal(t, consts.ErrorEndpointNotFound, err)
	assert.Nil(t, config)
}

func TestSelectByPrefix(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*"},
		{URL: "http://service3", Weight: 20, Prefix: "claude-2"},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyPrefix, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// Test exact match
	req := createTestRequest("claude-2")
	config, err := sr.selectByPrefix(req)

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service3", config.URL)

	// Test prefix match
	req = createTestRequest("gpt-3-turbo")
	config, err = sr.selectByPrefix(req)

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service1", config.URL)

	// Test longest prefix match
	routingConfigsLong := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-3*"},
	}

	srLong := NewServiceRouter(mockLB, consts.RoutePolicyPrefix, "")
	srLong.routingConfigs = routingConfigsLong

	req = createTestRequest("gpt-3-turbo")
	config, err = srLong.selectByPrefix(req)

	assert.NoError(t, err)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service2", config.URL) // Should match the longer prefix
}

func TestSelectByPrefix_NoModel(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyPrefix, "")
	sr.routingConfigs = routingConfigs

	// Test with empty model
	req := createTestRequest("")
	config, err := sr.selectByPrefix(req)

	assert.Error(t, err)
	assert.Equal(t, consts.ErrorEndpointNotFound, err)
	assert.Nil(t, config)
}

func TestSelectByPrefix_EmptyConfigs(t *testing.T) {
	mockLB := new(MockLoadBalancer)

	sr := NewServiceRouter(mockLB, consts.RoutePolicyPrefix, "")

	// Test with empty configs
	req := createTestRequest("gpt-3-turbo")
	config, err := sr.selectByPrefix(req)

	assert.Error(t, err)
	assert.Equal(t, consts.ErrorEndpointNotFound, err)
	assert.Nil(t, config)
}

func TestSelectByPrefix_NoMatch(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*"},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyPrefix, "")
	sr.routingConfigs = routingConfigs

	// Test with model that doesn't match any prefix
	req := createTestRequest("claude-2")
	config, err := sr.selectByPrefix(req)

	assert.Error(t, err)
	assert.Equal(t, consts.ErrorEndpointNotFound, err)
	assert.Nil(t, config)
}

func TestGetTokens_PrefixPolicy(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model"},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyPrefix, "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-4-turbo")

	nextTokens, err := sr.getTokens(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.NotNil(t, nextTokens.ExternalEp)
	assert.Equal(t, "http://service2", nextTokens.ExternalEp.URL)
	assert.Equal(t, "gpt-4-model", nextTokens.ExternalEp.Model)
}

func TestGetTokens_NoURL(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
	}

	// Mock the load balancer to return tokens
	expectedTokens := &structs.NextTokens{
		Tokens: []structs.Token{{}},
	}
	mockLB.On("GetNextTokens", mock.Anything).Return(expectedTokens, nil)

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	nextTokens, err := sr.getTokens(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.Equal(t, expectedTokens, nextTokens)
	mockLB.AssertExpectations(t)
}

func TestGetTokens_UnsupportedPolicy(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
	}

	sr := NewServiceRouter(mockLB, "unsupported-policy", "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	// Should panic with unsupported policy
	assert.Panics(t, func() {
		sr.getTokens(req)
	})
}

func TestGetNextTokensByRouter_WithConfigs(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*", Model: "gpt-3-model"}, // Use 100% weight to ensure consistent selection
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	nextTokens, err := sr.GetNextTokensByRouter(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.NotNil(t, nextTokens.ExternalEp)
	assert.Equal(t, "http://service1", nextTokens.ExternalEp.URL)
}

func TestGetNextTokensByRouter_EmptyConfigs(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	// Mock the load balancer to return tokens
	expectedTokens := &structs.NextTokens{
		Tokens: []structs.Token{{}},
	}
	mockLB.On("GetNextTokens", mock.Anything).Return(expectedTokens, nil)

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")

	req := createTestRequest("gpt-3-turbo")

	nextTokens, err := sr.GetNextTokensByRouter(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.Equal(t, expectedTokens, nextTokens)
	mockLB.AssertExpectations(t)
}

func TestGetNextTokensByRouter_UnsupportedPolicy(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	// Mock the load balancer to return tokens
	expectedTokens := &structs.NextTokens{
		Tokens: []structs.Token{{}},
	}
	mockLB.On("GetNextTokens", mock.Anything).Return(expectedTokens, nil)

	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*", Model: "gpt-3-model"}, // Use 100% weight to ensure consistent selection
	}

	sr := NewServiceRouter(mockLB, "unsupported-policy", "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	nextTokens, err := sr.GetNextTokensByRouter(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.Equal(t, expectedTokens, nextTokens)
	mockLB.AssertExpectations(t)
}

func TestGetFallbackTokens(t *testing.T) {
	// Since the current implementation doesn't sort fallback configs by priority,
	// we need to arrange configs in the order we want them to be tried

	mockLB := new(MockLoadBalancer)
	// Only external services should be in fallback configs (internal services are filtered out)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model", IsFallback: true},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	// Manually set fallbackConfigs for testing
	sr.setupFallbackConfigs(routingConfigs)

	req := createTestRequest("gpt-3-turbo")
	req.FallbackAttempt = 0

	// Test fallback to external service (first in fallback order)
	nextTokens, err := sr.GetFallbackTokens(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.NotNil(t, nextTokens.ExternalEp)
	assert.Equal(t, "http://service1", nextTokens.ExternalEp.URL)
	assert.Equal(t, 1, req.FallbackAttempt) // Should increment the attempt counter

	req.FallbackAttempt = 1 // Access the second config (index 1)

	nextTokens2, err := sr.GetFallbackTokens(req)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens2)
	assert.NotNil(t, nextTokens2.ExternalEp)
	assert.Equal(t, "http://service2", nextTokens2.ExternalEp.URL)
	assert.Equal(t, 2, req.FallbackAttempt) // Should increment the attempt counter again
}

func TestGetFallbackTokens_NoMoreFallbacks(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	// Manually set fallbackConfigs for testing
	sr.setupFallbackConfigs(routingConfigs)

	req := createTestRequest("gpt-3-turbo")
	req.FallbackAttempt = 1 // Already at the end of fallback configs

	nextTokens, err := sr.GetFallbackTokens(req)

	assert.Error(t, err)
	assert.Equal(t, consts.ErrorNoAvailableEndpoint, err)
	assert.Nil(t, nextTokens)
}

func TestGetFallbackLength(t *testing.T) {
	mockLB := new(MockLoadBalancer)
	routingConfigs := []RouteConfig{
		{URL: "", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model", IsFallback: false},
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
		{URL: "http://service2", Weight: 30, Prefix: "claude*", Model: "claude-model", IsFallback: true},
	}

	sr := NewServiceRouter(mockLB, consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	// Manually set fallbackConfigs for testing
	sr.setupFallbackConfigs(routingConfigs)

	length := sr.GetFallbackLength()
	assert.Equal(t, 2, length) // Only configs with IsFallback: true
}

func TestSetupRoutingConfigs(t *testing.T) {
	// Test with valid JSON input
	routingConfigRaw := `[{"fallback":false,"prefix":"*"},{"api_key":"sk-xxxxxxxx","base_url":"https://dashscope.aliyuncs.com/compatible-mode/v1","fallback":true,"prefix":"qwen-*"},{"api_key":"xxxx-xxxx-xxxx","base_url":"https://ark.cn-beijing.volces.com/api/v3","fallback":false,"prefix":"doubao-*"}]`

	configs := setupRoutingConfigs(routingConfigRaw)

	// Should have 3 configs
	assert.Equal(t, 3, len(configs))

	// Check first config (* prefix)
	assert.Equal(t, "*", configs[0].Prefix)
	assert.Equal(t, false, configs[0].IsFallback)
	assert.Equal(t, "", configs[0].URL)
	assert.Equal(t, "", configs[0].APIKey)

	// Check second config (qwen-* prefix)
	assert.Equal(t, "qwen-*", configs[1].Prefix)
	assert.Equal(t, true, configs[1].IsFallback)
	assert.Equal(t, "https://dashscope.aliyuncs.com/compatible-mode/v1", configs[1].URL)
	assert.Equal(t, "sk-xxxxxxxx", configs[1].APIKey)

	// Check third config (doubao-* prefix)
	assert.Equal(t, "doubao-*", configs[2].Prefix)
	assert.Equal(t, false, configs[2].IsFallback)
	assert.Equal(t, "https://ark.cn-beijing.volces.com/api/v3", configs[2].URL)
	assert.Equal(t, "xxxx-xxxx-xxxx", configs[2].APIKey)
}

func TestSetupRoutingConfigs_EmptyInput(t *testing.T) {
	// Test with empty input
	configs := setupRoutingConfigs("")

	// Should have 0 configs
	assert.Equal(t, 0, len(configs))
}

func TestSetupRoutingConfigs_InvalidJSON(t *testing.T) {
	// Test with invalid JSON input
	routingConfigRaw := `[{"fallback":false,"prefix":"*"` // Invalid JSON, missing closing bracket

	configs := setupRoutingConfigs(routingConfigRaw)

	// Should have 0 configs due to parsing error
	assert.Equal(t, 0, len(configs))
}
