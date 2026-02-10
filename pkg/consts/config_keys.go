package consts

// Configuration key constants for dynamic configuration management.
// IMPORTANT: All config keys MUST be defined here to ensure consistency and avoid typos.

const (
	// Rate Limiter Configuration Keys
	ConfigKeyRateLimitPrefix                        = "llm_gateway.rate_limit."
	ConfigKeyRateLimitEnable                        = "llm_gateway.rate_limit.enable"
	ConfigKeyRateLimitScope                         = "llm_gateway.rate_limit.scope"
	ConfigKeyRateLimitAction                        = "llm_gateway.rate_limit.action"
	ConfigKeyRateLimitMaxWaitTimeout                = "llm_gateway.rate_limit.max_ratelimit_wait_timeout"
	ConfigKeyRateLimitRetryInterval                 = "llm_gateway.rate_limit.ratelimit_retry_interval"
	ConfigKeyRateLimitMaxRequestsPerInstance        = "llm_gateway.rate_limit.max_requests_per_instance"
	ConfigKeyRateLimitMaxTokensPerInstance          = "llm_gateway.rate_limit.max_tokens_per_instance"
	ConfigKeyRateLimitMaxPrefillRequestsPerInstance = "llm_gateway.rate_limit.max_prefill_requests_per_instance"
	ConfigKeyRateLimitMaxPrefillTokensPerInstance   = "llm_gateway.rate_limit.max_prefill_tokens_per_instance"
	ConfigKeyRateLimitMaxDecodeRequestsPerInstance  = "llm_gateway.rate_limit.max_decode_requests_per_instance"
	ConfigKeyRateLimitMaxDecodeTokensPerInstance    = "llm_gateway.rate_limit.max_decode_tokens_per_instance"

	// Service Router Configuration Keys
	ConfigKeyRoutePrefix = "llm_gateway.route_"
	ConfigKeyRoutePolicy = "llm_gateway.route_policy"
	ConfigKeyRouteConfig = "llm_gateway.route_config"

	// Traffic Mirror Configuration Keys
	ConfigKeyMirrorPrefix    = "llm_gateway.traffic_mirror."
	ConfigKeyMirrorEnable    = "llm_gateway.traffic_mirror.enable"
	ConfigKeyMirrorTarget    = "llm_gateway.traffic_mirror.target"
	ConfigKeyMirrorRatio     = "llm_gateway.traffic_mirror.ratio"
	ConfigKeyMirrorToken     = "llm_gateway.traffic_mirror.token"
	ConfigKeyMirrorTimeout   = "llm_gateway.traffic_mirror.timeout"
	ConfigKeyMirrorEnableLog = "llm_gateway.traffic_mirror.enable_log"
)
