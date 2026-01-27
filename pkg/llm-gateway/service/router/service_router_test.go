package router

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/types"
)

// Helper function to create a test request
func createTestRequest(model string) *types.LLMRequest {
	return &types.LLMRequest{
		Model: model,
	}
}

func TestNewServiceRouter(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*"},
	}
	routingPolicy := consts.RoutePolicyWeight

	sr := NewServiceRouter(routingPolicy, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	assert.NotNil(t, sr)
	assert.Equal(t, routingConfigs, sr.routingConfigs)
	assert.Equal(t, routingPolicy, sr.routingPolicy)
}

func TestSetupFallbackConfigs(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", IsFallback: false},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*", IsFallback: true},
		{URL: "http://service3", Weight: 20, Prefix: "claude*", IsFallback: true},
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs
	sr.setupFallbackConfigs(routingConfigs)

	// Check that fallback configs are added in the order they appear (no sorting by priority)
	assert.Equal(t, 2, len(sr.fallbackConfigs))
	assert.Equal(t, "http://service2", sr.fallbackConfigs[0].URL) // IsFallback: true (appears first in input after filtering)
	assert.Equal(t, "http://service3", sr.fallbackConfigs[1].URL) // IsFallback: true (appears second in input after filtering)
}

func TestSelectByWeight(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 30, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*"},
		{URL: "http://service3", Weight: 20, Prefix: "claude*"},
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// Test that a config is always returned (weight-based selection)
	// We'll run this multiple times to verify the distribution
	configCounts := make(map[string]int)
	for i := 0; i < 1000; i++ {
		config, routerType := sr.selectByWeight()
		assert.NotNil(t, config)
		assert.Equal(t, RouteExternal, routerType)
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

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// With only one config, it should always be selected
	for i := 0; i < 100; i++ {
		config, routerType := sr.selectByWeight()
		assert.Equal(t, RouteExternal, routerType)
		assert.NotNil(t, config)
		assert.Equal(t, "http://service1", config.URL)
	}
}

func TestSelectByWeight_EmptyConfigs(t *testing.T) {
	sr := NewServiceRouter(consts.RoutePolicyWeight, "")

	// When there are no configs, it should return ErrorEndpointNotFound
	config, routerType := sr.selectByWeight()
	assert.Nil(t, config)
	assert.Equal(t, RouteUnknown, routerType)
}

func TestSelectByPrefix(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*"},
		{URL: "http://service3", Weight: 20, Prefix: "claude-2"},
	}

	sr := NewServiceRouter(consts.RoutePolicyPrefix, "")

	// Manually set routingConfigs for testing
	sr.routingConfigs = routingConfigs

	// Test exact match
	req := createTestRequest("claude-2")
	config, routerType := sr.selectByPrefix(&types.RequestContext{LLMRequest: req})

	assert.Equal(t, RouteExternal, routerType)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service3", config.URL)

	// Test prefix match
	req = createTestRequest("gpt-3-turbo")
	config, routerType = sr.selectByPrefix(&types.RequestContext{LLMRequest: req})

	assert.Equal(t, RouteExternal, routerType)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service1", config.URL)

	// Test longest prefix match
	routingConfigsLong := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-3*"},
	}

	srLong := NewServiceRouter(consts.RoutePolicyPrefix, "")
	srLong.routingConfigs = routingConfigsLong

	req = createTestRequest("gpt-3-turbo")
	config, routerType = srLong.selectByPrefix(&types.RequestContext{LLMRequest: req})

	assert.Equal(t, RouteExternal, routerType)
	assert.NotNil(t, config)
	assert.Equal(t, "http://service2", config.URL) // Should match the longer prefix
}

func TestSelectByPrefix_NoModel(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
	}

	sr := NewServiceRouter(consts.RoutePolicyPrefix, "")
	sr.routingConfigs = routingConfigs

	// Test with empty model
	req := createTestRequest("")
	config, routerType := sr.selectByPrefix(&types.RequestContext{LLMRequest: req})

	assert.Equal(t, RouteInternal, routerType)
	assert.Nil(t, config)
}

func TestSelectByPrefix_EmptyConfigs(t *testing.T) {
	sr := NewServiceRouter(consts.RoutePolicyPrefix, "")

	// Test with empty configs
	req := createTestRequest("gpt-3-turbo")
	config, routerType := sr.selectByPrefix(&types.RequestContext{LLMRequest: req})

	assert.Equal(t, RouteUnknown, routerType)
	assert.Nil(t, config)
}

func TestSelectByPrefix_NoMatch(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*"},
		{URL: "http://service2", Weight: 30, Prefix: "gpt-4*"},
	}

	sr := NewServiceRouter(consts.RoutePolicyPrefix, "")
	sr.routingConfigs = routingConfigs

	// Test with model that doesn't match any prefix
	req := createTestRequest("claude-2")
	config, routerType := sr.selectByPrefix(&types.RequestContext{LLMRequest: req})

	assert.Equal(t, RouteUnknown, routerType)
	assert.Nil(t, config)
}

func TestGetTokens_PrefixPolicy(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model"},
	}

	sr := NewServiceRouter(consts.RoutePolicyPrefix, "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-4-turbo")

	nextTokens, routeType := sr.Route(&types.RequestContext{LLMRequest: req})

	assert.NotNil(t, nextTokens)
	assert.Equal(t, RouteExternal, routeType)
	assert.Equal(t, "http://service2", nextTokens.URL)
	assert.Equal(t, "gpt-4-model", nextTokens.Model)
}

func TestGetTokens_NoURL(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	nextTokens, routeType := sr.Route(&types.RequestContext{LLMRequest: req})
	assert.Nil(t, nextTokens)
	assert.Equal(t, RouteInternal, routeType)
}

func TestGetTokens_UnsupportedPolicy(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model"},
	}

	sr := NewServiceRouter("unsupported-policy", "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	// Should panic with unsupported policy
	assert.Panics(t, func() {
		sr.Route(&types.RequestContext{LLMRequest: req})
	})
}

func TestGetNextTokensByRouter_WithConfigs(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*", Model: "gpt-3-model"}, // Use 100% weight to ensure consistent selection
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	nextTokens, routeType := sr.Route(&types.RequestContext{LLMRequest: req})

	assert.NotNil(t, nextTokens)
	assert.Equal(t, RouteExternal, routeType)
	assert.Equal(t, "http://service1", nextTokens.URL)
}

func TestGetNextTokensByRouter_EmptyConfigs(t *testing.T) {
	sr := NewServiceRouter(consts.RoutePolicyWeight, "")

	req := createTestRequest("gpt-3-turbo")

	nextTokens, routeType := sr.Route(&types.RequestContext{LLMRequest: req})

	assert.Nil(t, nextTokens)
	assert.Equal(t, RouteInternal, routeType)
}

func TestGetNextTokensByRouter_UnsupportedPolicy(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 100, Prefix: "gpt-3*", Model: "gpt-3-model"}, // Use 100% weight to ensure consistent selection
	}

	sr := NewServiceRouter("unsupported-policy", "")
	sr.routingConfigs = routingConfigs

	req := createTestRequest("gpt-3-turbo")

	assert.Panics(t, func() {
		sr.Route(&types.RequestContext{LLMRequest: req})
	})
}

func TestGetFallbackTokens(t *testing.T) {
	// Since the current implementation doesn't sort fallback configs by priority,
	// we need to arrange configs in the order we want them to be tried

	// Only external services should be in fallback configs (internal services are filtered out)
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
		{URL: "http://service2", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model", IsFallback: true},
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	// Manually set fallbackConfigs for testing
	sr.setupFallbackConfigs(routingConfigs)

	req := createTestRequest("gpt-3-turbo")
	context := &types.RequestContext{RequestStats: &types.RequestStats{FallbackAttempt: 0}, LLMRequest: req}

	// Test fallback to external service (first in fallback order)
	nextTokens, err := sr.Fallback(context)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens)
	assert.Equal(t, "http://service1", nextTokens.URL)
	assert.Equal(t, 1, context.RequestStats.FallbackAttempt) // Should increment the attempt counter

	nextTokens2, err := sr.Fallback(context)

	assert.NoError(t, err)
	assert.NotNil(t, nextTokens2)
	assert.Equal(t, "http://service2", nextTokens2.URL)
	assert.Equal(t, 2, context.RequestStats.FallbackAttempt) // Should increment the attempt counter again
}

func TestGetFallbackTokens_NoMoreFallbacks(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	// Manually set fallbackConfigs for testing
	sr.setupFallbackConfigs(routingConfigs)

	req := createTestRequest("gpt-3-turbo")
	context := &types.RequestContext{RequestStats: &types.RequestStats{FallbackAttempt: 1}, LLMRequest: req}
	nextTokens, err := sr.Fallback(context)

	assert.Error(t, err)
	assert.Equal(t, consts.ErrorNoAvailableEndpoint, err)
	assert.Nil(t, nextTokens)
}

func TestGetFallbackLength(t *testing.T) {
	routingConfigs := []RouteConfig{
		{URL: "", Weight: 50, Prefix: "gpt-3*", Model: "gpt-3-model", IsFallback: false},
		{URL: "http://service1", Weight: 50, Prefix: "gpt-4*", Model: "gpt-4-model", IsFallback: true},
		{URL: "http://service2", Weight: 30, Prefix: "claude*", Model: "claude-model", IsFallback: true},
	}

	sr := NewServiceRouter(consts.RoutePolicyWeight, "")
	sr.routingConfigs = routingConfigs

	// Manually set fallbackConfigs for testing
	sr.setupFallbackConfigs(routingConfigs)

	length := len(sr.fallbackConfigs)
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
