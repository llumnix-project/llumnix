package consts

import (
	"time"
)

const (
	MetricRecordDuration = 5 * time.Second
)

// llm inference role
const (
	NormalInferMode  = "normal"
	PrefillInferMode = "prefill"
	DecodeInferMode  = "decode"
)

// llm inference type for llumnix
const (
	LlumnixNeutralInstanceType = "neutral"
	LlumnixPrefillInstanceType = "prefill"
	LlumnixDecodeInstanceType  = "decode"
)

const (
	RoutePolicyWeight = "weight"
	RoutePolicyPrefix = "prefix"
	RouteInternalURL  = "local"
)

// llm scheduler policy with use a remote concertized scheduler
const (
	SchedulePolicyRoundRobin  = "round-robin"
	SchedulePolicyLoadBalance = "load-balance"
	SchedulePolicyFlood       = "flood"
)

const (
	LlumnixKvsBackendV6d      = "v6d"
	LlumnixKvsBackendMooncake = "mooncake"
)

const (
	LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills         = "kv_blocks_ratio_with_all_prefills"
	LlumnixSchedulingMetricDecodeBatchSize                      = "decode_batch_size"
	LlumnixSchedulingMetricNumWaitingRequests                   = "num_waiting_requests"
	LlumnixSchedulingMetricAllPrefillsKVBlocksNum               = "all_prefills_kv_blocks_num"
	LlumnixSchedulingMetricKVCacheHitLen                        = "kv_cache_hit_len"
	LlumnixSchedulingMetricCacheAwareAllPrefillsKVBlocksNum     = "cache_aware_all_prefills_kv_blocks_num"
	LlumnixSchedulingMetricAdaptiveDecodeBatchSize              = "adaptive_decode_batch_size"
	LlumnixSchedulingMetricNumRequests                          = "num_requests"
	LlumnixSchedulingMetricAllDecodesKVBlocksNumWithAllPrefills = "all_decodes_kv_blocks_num_with_all_prefills"
	LlumnixSchedulingMetricNumTokens                            = "num_tokens"
)

const (
	LlumnixMigrationReqSelectRuleNumReq = "NUM_REQ"
	LlumnixMigrationReqSelectRuleToken  = "TOKEN"
	LlumnixMigrationReqSelectRuleRatio  = "RATIO"

	LlumnixMigrationReqSelectOrderLCR   = "LCR"   // last running
	LlumnixMigrationReqSelectOrderFCR   = "FCR"   // first running
	LlumnixMigrationReqSelectOrderLR    = "LR"    // longest running
	LlumnixMigrationReqSelectOrderSR    = "SR"    // shortest running
	LlumnixMigrationReqSelectOrderFCW   = "FCW"   // first waiting
	LlumnixMigrationReqSelectOrderFCWSR = "FCWSR" // first waiting and shortest running
)

const (
	LlumnixReschedulePolicyNeutralLoad     = "neutral_load"
	LlumnixReschedulePolicyDecodeLoad      = "decode_load"
	LlumnixReschedulePolicyPrefillFailover = "prefill_failover"
	LlumnixReschedulePolicyDecodeFailover  = "decode_failover"
	LlumnixReschedulePolicyNeutralFailover = "neutral_failover"

	LlumnixReschedulePolicyCleanUpDecodeRequestsOnPrefill   = "clean_up_decode_requests_on_prefill"
	LlumnixReschedulePolicyAggregateDecodeRequestsOnPrefill = "aggregate_decode_requests_on_prefill"
	LlumnixReschedulePolicyEaseBusyDecodeWithFreePrefill    = "ease_busy_decode_with_free_prefill"
)

const (
	LlumnixRescheduleLoadBalanceScopeCluster = "cluster"
	LlumnixRescheduleLoadBalanceScopeUnit    = "unit"
)

// different pd-split mode
const (
	SplitModeVllmKvt        = "vllm-kvt"
	SplitModeSGlangMooncake = "sglang-mooncake"
	SplitModeVllmMooncake   = "vllm-mooncake"
)

// llm-gateway support different discovery mode
const (
	DiscoveryCacheServer = "cache-server"
	DiscoveryMessageBus  = "message-bus"
	DiscoveryRedis       = "redis"
)

const (
	// LlumnixFailoverScopeInstanceUnit failover instances sharing the unit with the unschedulable instances
	LlumnixFailoverScopeInstanceUnit = "instance-unit"
	// LlumnixFailoverScopeNodeUnit failover instances sharing units with instances on nodes requiring failover
	LlumnixFailoverScopeNodeUnit = "node-unit"
	// LlumnixFailoverScopeNode failover instances sharing the same node with the unschedulable instances
	LlumnixFailoverScopeNode = "node"
	// LlumnixFailoverScopeInstance When the failover scope is instance, it is equivalent to failover filter not being enabled.
	LlumnixFailoverScopeInstance = "instance"
)

// default value
const (
	DefaultLlumnixEnableFullModeScheduling = true

	// CMS defaults
	DefaultLlumnixCmsRedisHost              = "redis.roles"
	DefaultLlumnixCmsRedisPort              = "10000"
	DefaultLlumnixCmsRedisUsername          = "default"
	DefaultLlumnixCmsRedisPassword          = ""
	DefaultLlumnixCmsRedisSocketTimeout     = 1.0
	DefaultLlumnixCmsRedisRetryTimes        = 1
	DefaultLlumnixCmsPullStatusIntervalMs   = 100
	DefaultLlumnixCmsPullMetadataIntervalMs = 10000
	DefaultLlumnixCmsRecordMetricsInterval  = 0

	// KvsMetaService defaults
	DefaultLlumnixEnableCacheAwareScheduling             = false
	DefaultLlumnixKvsBackend                             = "mooncake"
	DefaultLlumnixKvsMetadataServiceConfigPath           = ""
	DefaultLlumnixKvsChunkSize                           = 256
	DefaultLlumnixKvsEnableSaveUnfullChunk               = false
	DefaultLlumnixKvsIrisMetaPrefix                      = "iris."
	DefaultLlumnixKvsVLLMBlockPrefix                     = "block.hash.key."
	DefaultLlumnixKvsRetryIntervalMs                     = 100
	DefaultLlumnixKvsRetryTimes                          = 5
	DefaultLlumnixKvsMetadataServiceDownDurationS        = 30
	DefaultLlumnixKvsMetadataServiceRedisClusterHosts    = ""
	DefaultLlumnixKvsMetadataServiceRedisClusterPassword = ""
	DefaultLlumnixKvsMetadataServiceHttpServerHost       = "0.0.0.0"
	DefaultLlumnixKvsMetadataServiceHttpServerPort       = "9003"

	// Schedule defaults
	DefaultLlumnixDispatchTopK                        = 1
	DefaultLlumnixDispatchNeutralLoadMetric           = LlumnixSchedulingMetricAllPrefillsKVBlocksNum
	DefaultLlumnixDispatchNeutralLoadThreshold        = 2048
	DefaultLlumnixDispatchPrefillLoadMetric           = LlumnixSchedulingMetricAllPrefillsKVBlocksNum
	DefaultLlumnixDispatchPrefillLoadThreshold        = 2048
	DefaultLlumnixDispatchDecodeLoadMetric            = LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultLlumnixDispatchDecodeLoadThreshold         = 1.0
	DefaultLlumnixDispatchPrefillCacheLocalityMetric  = LlumnixSchedulingMetricCacheAwareAllPrefillsKVBlocksNum
	DefaultLlumnixEnableInstanceStatusLocalAccount    = true
	DefaultLlumnixKvCacheBlockSize                    = 64
	DefaultLlumnixRequestLocalAccountStalenessSeconds = 10
	DefaultLlumnixAllowConcurrentSchedule             = false
	DefaultLlumnixEnablePredictorEnhancedScheduling   = false
	DefaultLlumnixMaxNumBatchedTokens                 = 65536
	DefaultLlumnixNumPredictorWarmupSamples           = 20

	// Adaptive PD defaults
	DefaultLlumnixEnableAdaptivePD                     = false
	DefaultLlumnixDispatchPrefillAsDecodeLoadMetric    = LlumnixSchedulingMetricAdaptiveDecodeBatchSize
	DefaultLlumnixDispatchPrefillAsDecodeLoadThreshold = 256.0
	DefaultLlumnixDispatchDecodeAsPrefillLoadMetric    = LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultLlumnixDispatchDecodeAsPrefillLoadThreshold = 1.0
	DefaultLlumnixDecodeComputeBoundBatchSize          = 128

	// Filter defaults
	DefaultLlumnixFailoverScope            = LlumnixFailoverScopeInstanceUnit
	DefaultLlumnixInstanceStalenessSeconds = 60

	// Reschedule defaults
	DefaultLlumnixEnableRescheduling             = false
	DefaultLlumnixReschedulePolicies             = "decode_load,prefill_failover,decode_failover,neutral_failover"
	DefaultLlumnixRescheduleIntervalMs           = 500
	DefaultLlumnixRescheduleDecodeLoadMetric     = LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultLlumnixRescheduleDecodeLoadThreshold  = 1.0
	DefaultLlumnixReschedulePrefillLoadMetric    = LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultLlumnixRescheduleNeutralLoadMetric    = LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultLlumnixRescheduleNeutralLoadThreshold = 1.0
	DefaultLlumnixRescheduleReqSelectOrder       = LlumnixMigrationReqSelectOrderSR
	DefaultLlumnixRescheduleReqSelectRule        = LlumnixMigrationReqSelectRuleToken
	DefaultLlumnixRescheduleReqSelectValue       = 1
	DefaultLlumnixRescheduleLoadBalanceThreshold = 0.01
	DefaultLlumnixRescheduleLoadBalanceScope     = LlumnixRescheduleLoadBalanceScopeCluster

	// Llumlet defaults
	DefaultLlumnixLlumletGrpcConnectionPoolSize = -1
	DefaultLlumnixLlumletGrpcTimeoutSeconds     = -1

	DefaultLlumnixEnableMetrics = false

	DefaultLlumnixExtraArgs = ""
)
