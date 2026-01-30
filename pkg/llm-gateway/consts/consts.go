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
	NeutralInstanceType = "neutral"
	PrefillInstanceType = "prefill"
	DecodeInstanceType  = "decode"
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
	KvsBackendV6d      = "v6d"
	KvsBackendMooncake = "mooncake"
)

const (
	SchedulingMetricKVBlocksRatioWithAllPrefills         = "kv_blocks_ratio_with_all_prefills"
	SchedulingMetricDecodeBatchSize                      = "decode_batch_size"
	SchedulingMetricNumWaitingRequests                   = "num_waiting_requests"
	SchedulingMetricAllPrefillsKVBlocksNum               = "all_prefills_kv_blocks_num"
	SchedulingMetricKVCacheHitLen                        = "kv_cache_hit_len"
	SchedulingMetricCacheAwareAllPrefillsKVBlocksNum     = "cache_aware_all_prefills_kv_blocks_num"
	SchedulingMetricAdaptiveDecodeBatchSize              = "adaptive_decode_batch_size"
	SchedulingMetricNumRequests                          = "num_requests"
	SchedulingMetricAllDecodesKVBlocksNumWithAllPrefills = "all_decodes_kv_blocks_num_with_all_prefills"
	SchedulingMetricNumTokens                            = "num_tokens"
)

const (
	MigrationReqSelectRuleNumReq = "NUM_REQ"
	MigrationReqSelectRuleToken  = "TOKEN"
	MigrationReqSelectRuleRatio  = "RATIO"

	MigrationReqSelectOrderLCR   = "LCR"   // last running
	MigrationReqSelectOrderFCR   = "FCR"   // first running
	MigrationReqSelectOrderLR    = "LR"    // longest running
	MigrationReqSelectOrderSR    = "SR"    // shortest running
	MigrationReqSelectOrderFCW   = "FCW"   // first waiting
	MigrationReqSelectOrderFCWSR = "FCWSR" // first waiting and shortest running
)

const (
	ReschedulePolicyNeutralLoad     = "neutral_load"
	ReschedulePolicyDecodeLoad      = "decode_load"
	ReschedulePolicyPrefillFailover = "prefill_failover"
	ReschedulePolicyDecodeFailover  = "decode_failover"
	ReschedulePolicyNeutralFailover = "neutral_failover"

	ReschedulePolicyCleanUpDecodeRequestsOnPrefill   = "clean_up_decode_requests_on_prefill"
	ReschedulePolicyAggregateDecodeRequestsOnPrefill = "aggregate_decode_requests_on_prefill"
	ReschedulePolicyEaseBusyDecodeWithFreePrefill    = "ease_busy_decode_with_free_prefill"
)

const (
	RescheduleLoadBalanceScopeCluster = "cluster"
	RescheduleLoadBalanceScopeUnit    = "unit"
)

// different pd-split mode
const (
	SplitModeVllmKvt        = "vllm-kvt"
	SplitModeSGlangMooncake = "sglang-mooncake"
	SplitModeVllmMooncake   = "vllm-mooncake"
)

// llm-gateway support different discovery mode
const (
	DiscoveryEndpoints = "endpoints"
	DiscoveryRedis     = "redis"
)

const (
	// FailoverScopeInstanceUnit failover instances sharing the unit with the unschedulable instances
	FailoverScopeInstanceUnit = "instance-unit"
	// FailoverScopeNodeUnit failover instances sharing units with instances on nodes requiring failover
	FailoverScopeNodeUnit = "node-unit"
	// FailoverScopeNode failover instances sharing the same node with the unschedulable instances
	FailoverScopeNode = "node"
	// FailoverScopeInstance When the failover scope is instance, it is equivalent to failover filter not being enabled.
	FailoverScopeInstance = "instance"
)

// default value
const (
	DefaultEnableFullModeScheduling = true

	// CMS defaults
	DefaultCmsRedisHost              = "redis.roles"
	DefaultCmsRedisPort              = "10000"
	DefaultCmsRedisUsername          = ""
	DefaultCmsRedisPassword          = ""
	DefaultCmsRedisSocketTimeout     = 1.0
	DefaultCmsRedisRetryTimes        = 1
	DefaultCmsPullStatusIntervalMs   = 50
	DefaultCmsPullMetadataIntervalMs = 10000
	DefaultCmsRecordMetricsInterval  = 0

	// KvsMetaService defaults
	DefaultEnableCacheAwareScheduling             = false
	DefaultKvsBackend                             = "mooncake"
	DefaultKvsMetadataServiceConfigPath           = ""
	DefaultKvsChunkSize                           = 256
	DefaultKvsEnableSaveUnfullChunk               = false
	DefaultKvsIrisMetaPrefix                      = "iris."
	DefaultKvsVLLMBlockPrefix                     = "block.hash.key."
	DefaultKvsRetryIntervalMs                     = 100
	DefaultKvsRetryTimes                          = 5
	DefaultKvsMetadataServiceDownDurationS        = 30
	DefaultKvsMetadataServiceRedisClusterHosts    = ""
	DefaultKvsMetadataServiceRedisClusterPassword = ""
	DefaultKvsMetadataServiceHttpServerHost       = "0.0.0.0"
	DefaultKvsMetadataServiceHttpServerPort       = "9003"

	// Schedule defaults
	DefaultDispatchTopK                        = 1
	DefaultDispatchNeutralLoadMetric           = SchedulingMetricAllPrefillsKVBlocksNum
	DefaultDispatchNeutralLoadThreshold        = 8192
	DefaultDispatchPrefillLoadMetric           = SchedulingMetricAllPrefillsKVBlocksNum
	DefaultDispatchPrefillLoadThreshold        = 2048
	DefaultDispatchDecodeLoadMetric            = SchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultDispatchDecodeLoadThreshold         = 1.0
	DefaultDispatchPrefillCacheLocalityMetric  = SchedulingMetricCacheAwareAllPrefillsKVBlocksNum
	DefaultEnableInstanceStatusLocalAccount    = true
	DefaultKvCacheBlockSize                    = 64
	DefaultRequestLocalAccountStalenessSeconds = 10
	DefaultAllowConcurrentSchedule             = false
	DefaultEnablePredictorEnhancedScheduling   = false
	DefaultMaxNumBatchedTokens                 = 65536
	DefaultNumPredictorWarmupSamples           = 20

	// Adaptive PD defaults
	DefaultEnableAdaptivePD                     = false
	DefaultDispatchPrefillAsDecodeLoadMetric    = SchedulingMetricAdaptiveDecodeBatchSize
	DefaultDispatchPrefillAsDecodeLoadThreshold = 256.0
	DefaultDispatchDecodeAsPrefillLoadMetric    = SchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultDispatchDecodeAsPrefillLoadThreshold = 1.0
	DefaultDecodeComputeBoundBatchSize          = 128

	// Filter defaults
	DefaultFailoverScope            = FailoverScopeInstanceUnit
	DefaultInstanceStalenessSeconds = 60

	// Reschedule defaults
	DefaultEnableRescheduling             = false
	DefaultReschedulePolicies             = "decode_load,prefill_failover,decode_failover,neutral_failover"
	DefaultRescheduleIntervalMs           = 500
	DefaultRescheduleDecodeLoadMetric     = SchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultRescheduleDecodeLoadThreshold  = 1.0
	DefaultReschedulePrefillLoadMetric    = SchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultRescheduleNeutralLoadMetric    = SchedulingMetricKVBlocksRatioWithAllPrefills
	DefaultRescheduleNeutralLoadThreshold = 1.0
	DefaultRescheduleReqSelectOrder       = MigrationReqSelectOrderSR
	DefaultRescheduleReqSelectRule        = MigrationReqSelectRuleToken
	DefaultRescheduleReqSelectValue       = 1
	DefaultRescheduleLoadBalanceThreshold = 0.01
	DefaultRescheduleLoadBalanceScope     = RescheduleLoadBalanceScopeCluster

	// Llumlet defaults
	DefaultLlumletGrpcConnectionPoolSize = -1
	DefaultLlumletGrpcTimeoutSeconds     = -1

	DefaultEnableMetrics = false
)
