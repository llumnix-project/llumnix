package router

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/protocol/anthropic"
	"llm-gateway/pkg/types"
)

// mockRouterConfig is a lightweight mock for testing
type mockRouterConfig struct {
	routePolicy    atomic.Value // stores string
	routeConfigRaw atomic.Value // stores string
	needRebuild    atomic.Bool
}

func newMockRouterConfig(policy, configRaw string) *mockRouterConfig {
	m := &mockRouterConfig{}
	m.routePolicy.Store(policy)
	m.routeConfigRaw.Store(configRaw)
	return m
}

func (m *mockRouterConfig) RoutePolicy() string {
	return m.routePolicy.Load().(string)
}

func (m *mockRouterConfig) RouteConfigRaw() string {
	return m.routeConfigRaw.Load().(string)
}

func (m *mockRouterConfig) NeedRebuild() bool {
	return m.needRebuild.Load()
}

func (m *mockRouterConfig) MarkRebuilt() {
	m.needRebuild.Store(false)
}

// Helper function to create a test request context with a given model
func createTestRequest(model string) *types.RequestContext {
	return &types.RequestContext{
		RequestType: consts.AnthropicHandlerName,
		AnthropicRequest: &types.AnthropicRequest{
			Request: &anthropic.Request{Model: model},
		},
		RequestStats: &types.RequestStats{},
	}
}

func TestNewServiceRouter(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*"},
	}
	routingPolicy := consts.RoutePolicyWeight

	mockCfg := newMockRouterConfig(routingPolicy, "")
	sr := newServiceRouter(mockCfg)

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	assert.NotNil(t, sr)
	assert.Equal(t, routingPolicy, sr.routingPolicy)
	assert.Equal(t, routingConfigs, sr.routingConfigs)
}

func TestSetupFallbackConfigs(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", IsFallback: false},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*", IsFallback: true},
		{URL: "http://service3", Weight: 20, Prefix: "claude*", IsFallback: true},
		{URL: consts.RouteInternalURL, Weight: 10, Prefix: "internal*", IsFallback: true},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs
	sr.setupFallbackConfigs(routingConfigs)

	// Check that fallback configs are added in the order they appear (no sorting by priority)
	// and that internal services are filtered out
	assert.Equal(t, 2, len(sr.fallbackConfigs))
	assert.Equal(t, "http://service2", sr.fallbackConfigs[0].URL)
	assert.Equal(t, "http://service3", sr.fallbackConfigs[1].URL)
}

func TestSelectByWeight(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 30, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*"},
		{URL: "http://service3", Weight: 20, Prefix: "claude*"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// Test that a config is always returned (weight-based selection)
	// We'll run this multiple times to verify the distribution
	configCounts := make(map[string]int)
	for i := 0; i < 1000; i++ {
		config, routeType := sr.selectByWeight()
		assert.Equal(t, RouteExternal, routeType)
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
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// With only one config, it should always be selected
	for i := 0; i < 100; i++ {
		config, routeType := sr.selectByWeight()
		assert.Equal(t, RouteExternal, routeType)
		assert.NotNil(t, config)
		assert.Equal(t, "http://service1", config.URL)
	}
}

func TestSelectByWeight_EmptyConfigs(t *testing.T) {
	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)

	// When there are no configs, selectByWeight should panic due to rand.Intn(0)
	assert.Panics(t, func() {
		sr.selectByWeight()
	})
}

func TestSelectByPrefix(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*"},
		{URL: "http://service3", Weight: 20, Prefix: "claude-2"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyPrefix, "")
	sr := newServiceRouter(mockCfg)

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// Test exact match
	req := createTestRequest("claude-2")
	config, routeType := sr.selectByPrefix(req)

	assert.Equal(t, RouteExternal, routeType)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service3", config.URL)

	// Test prefix match
	req = createTestRequest("gpt-3-turbo")
	config, routeType = sr.selectByPrefix(req)

	assert.Equal(t, RouteExternal, routeType)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service1", config.URL)

	// Test longest prefix match
	routingConfigsLong := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-3*"},
	}

	srLong := newServiceRouter(newMockRouterConfig(consts.RoutePolicyPrefix, ""))
	srLong.routingConfigs = routingConfigsLong

	req = createTestRequest("gpt-3-turbo")
	config, routeType = srLong.selectByPrefix(req)

	assert.Equal(t, RouteExternal, routeType)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service2", config.URL) // Should match the longer prefix
}

func TestSelectByPrefix_NoModel(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyPrefix, "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs

	// Test with empty model
	req := createTestRequest("")
	config, routeType := sr.selectByPrefix(req)

	assert.Nil(t, config)
	assert.Equal(t, RouteInternal, routeType)
}

func TestSelectByPrefix_EmptyConfigs(t *testing.T) {
	mockCfg := newMockRouterConfig(consts.RoutePolicyPrefix, "")
	sr := newServiceRouter(mockCfg)

	// Test with empty configs
	req := createTestRequest("gpt-3-turbo")
	config, routeType := sr.selectByPrefix(req)

	assert.Nil(t, config)
	assert.Equal(t, RouteUnknown, routeType)
}

func TestSelectByPrefix_NoMatch(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyPrefix, "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs

	// Test with model that doesn't match any prefix
	req := createTestRequest("claude-2")
	config, routeType := sr.selectByPrefix(req)

	assert.Nil(t, config)
	assert.Equal(t, RouteUnknown, routeType)
}

func TestRoute_WeightPolicy_EmptyConfigs(t *testing.T) {
	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)

	req := createTestRequest("gpt-3-turbo")

	endpoint, routeType := sr.Route(req)

	assert.Nil(t, endpoint)
	assert.Equal(t, RouteInternal, routeType)
}

func TestRoute_WeightPolicy_External(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*", Model: "gpt-3-model"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	endpoint, routeType := sr.Route(req)

	assert.Equal(t, RouteExternal, routeType)
	if assert.NotNil(t, endpoint) {
		assert.Equal(t, "http://service1", endpoint.URL)
		assert.Equal(t, "gpt-3-model", endpoint.Model)
	}
}

func TestRoute_PrefixPolicy_External(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model"},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyPrefix, "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-4-turbo")

	endpoint, routeType := sr.Route(req)

	assert.Equal(t, RouteExternal, routeType)
	if assert.NotNil(t, endpoint) {
		assert.Equal(t, "http://service2", endpoint.URL)
		assert.Equal(t, "gpt-4-model", endpoint.Model)
	}
}

func TestRoute_UnsupportedPolicy(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*", Model: "gpt-3-model"},
	}

	mockCfg := newMockRouterConfig("unsupported-policy", "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	// Should panic with unsupported policy
	assert.Panics(t, func() {
		_, _ = sr.Route(req)
	})
}

func TestFallback_Basic(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model", IsFallback: true},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs
	sr.setupFallbackConfigs(routingConfigs)

	req := createTestRequest("gpt-3-turbo")

	// Test fallback to external service (first in fallback order)
	endpoint1, err := sr.Fallback(req)

	assert.NoError(t, err)
	if assert.NotNil(t, endpoint1) {
		assert.Equal(t, "http://service1", endpoint1.URL)
	}
	assert.Equal(t, 1, req.RequestStats.FallbackAttempt) // Should increment the attempt counter

	// Access the second config (index 1)
	endpoint2, err := sr.Fallback(req)

	assert.NoError(t, err)
	if assert.NotNil(t, endpoint2) {
		assert.Equal(t, "http://service2", endpoint2.URL)
	}
	assert.Equal(t, 2, req.RequestStats.FallbackAttempt) // Should increment the attempt counter again
}

func TestFallback_NoMoreFallbacks(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
	}

	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)
	sr.routingConfigs = routingConfigs
	sr.setupFallbackConfigs(routingConfigs)

	req := createTestRequest("gpt-3-turbo")
	req.RequestStats.FallbackAttempt = 1 // Already at the end of fallback configs

	endpoint, err := sr.Fallback(req)

	assert.Error(t, err)
	assert.Nil(t, endpoint)
	assert.EqualError(t, err, "no available fallback endpoint")
}

func TestFallback_NoFallbackConfigs(t *testing.T) {
	mockCfg := newMockRouterConfig(consts.RoutePolicyWeight, "")
	sr := newServiceRouter(mockCfg)

	req := createTestRequest("gpt-3-turbo")

	endpoint, err := sr.Fallback(req)

	assert.Error(t, err)
	assert.Nil(t, endpoint)
	assert.EqualError(t, err, "no available fallback endpoint")
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
