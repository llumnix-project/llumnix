package consts

import (
	"time"
)

const (
	MetricRecordDuration = 5 * time.Second
)

// InferType represents the inference type of an instance or request.
type InferType string

const (
	InferTypeNeutral InferType = "neutral"
	InferTypePrefill InferType = "prefill"
	InferTypeDecode  InferType = "decode"
	InferTypeAll     InferType = "all" // Only for filtering purposes, will not appear in llm worker
)

func (m InferType) String() string {
	return string(m)
}

type SchedulingStage string

const (
	SchedulingStagePrefill SchedulingStage = "prefill"
	SchedulingStageDecode  SchedulingStage = "decode"
)

const (
	RoutePolicyWeight = "weight"
	RoutePolicyPrefix = "prefix"
	RouteInternalURL  = "local"
)

// llm scheduling policy with use a remote concertized scheduler
const (
	SchedulingPolicyRoundRobin  = "round-robin"
	SchedulingPolicyLoadBalance = "load-balance"
	SchedulingPolicyFlood       = "flood"
	SchedulingPolicySlo         = "slo"
)

const (
	KvsBackendV6d      = "v6d"
	KvsBackendMooncake = "mooncake"
)

const (
	SchedulingMetricKVCacheUsageRatioProjected     = "kv_cache_usage_ratio_projected"
	SchedulingMetricDecodeBatchSize                = "decode_batch_size"
	SchedulingMetricNumWaitingRequests             = "num_waiting_requests"
	SchedulingMetricAllPrefillsTokensNum           = "all_prefills_tokens_num"
	SchedulingMetricKVCacheHitLen                  = "kv_cache_hit_len"
	SchedulingMetricCacheAwareAllPrefillsTokensNum = "cache_aware_all_prefills_tokens_num"
	SchedulingMetricNumRequests                    = "num_requests"
	SchedulingMetricAllDecodesTokensNum            = "all_decodes_tokens_num"
	SchedulingMetricNumTokens                      = "num_tokens"

	SchedulingMetricPredictedTtft = "predicted_ttft"
	SchedulingMetricPredictedTpot = "predicted_tpot"
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
	ReschedulingPolicyNeutralLoad     = "neutral_load"
	ReschedulingPolicyDecodeLoad      = "decode_load"
	ReschedulingPolicyPrefillFailover = "prefill_failover"
	ReschedulingPolicyDecodeFailover  = "decode_failover"
	ReschedulingPolicyNeutralFailover = "neutral_failover"

	ReschedulingPolicyCleanUpDecodeRequestsOnPrefill   = "clean_up_decode_requests_on_prefill"
	ReschedulingPolicyAggregateDecodeRequestsOnPrefill = "aggregate_decode_requests_on_prefill"
	ReschedulingPolicyEaseBusyDecodeWithFreePrefill    = "ease_busy_decode_with_free_prefill"
)

const (
	ReschedulingLoadBalanceScopeCluster = "cluster"
	ReschedulingLoadBalanceScopeUnit    = "unit"
)

// different pd-disagg protocol
const (
	PDDisaggProtocolVllmKvt        = "vllm-kvt"
	PDDisaggProtocolSGlangMooncake = "sglang-mooncake"
	PDDisaggProtocolVllmMooncake   = "vllm-mooncake"
)

// forwarder type constants, mapping to config PDDisaggProtocol values
const (
	ForwarderTypeNeutral        = "neutral"
	ForwarderTypeVllmKvt        = PDDisaggProtocolVllmKvt
	ForwarderTypeSglangMooncake = PDDisaggProtocolSGlangMooncake
	ForwarderTypeVllmMooncake   = PDDisaggProtocolVllmMooncake
)

// gateway support different discovery mode
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

const (
	KvsHashAlgoSha256Hex  = "sha256_hex"
	KvsHashAlgoSha256CBOR = "sha256_cbor"
	KvsHashAlgoXxhash     = "xxhash"
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
	DefaultCacheAwareSchedulingMinTokens          = 1024
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
	DefaultKvsHashAlgo                            = KvsHashAlgoSha256CBOR

	// Scheduling defaults
	DefaultDispatchTopK                        = 1
	DefaultDispatchNeutralLoadMetric           = SchedulingMetricAllPrefillsTokensNum
	DefaultDispatchNeutralLoadThreshold        = 8192
	DefaultDispatchPrefillLoadMetric           = SchedulingMetricAllPrefillsTokensNum
	DefaultDispatchPrefillLoadThreshold        = 2048
	DefaultDispatchDecodeLoadMetric            = SchedulingMetricKVCacheUsageRatioProjected
	DefaultDispatchDecodeLoadThreshold         = 1.0
	DefaultDispatchPrefillCacheLocalityMetric  = SchedulingMetricCacheAwareAllPrefillsTokensNum
	DefaultEnableInstanceStatusLocalAccount    = true
	DefaultRequestLocalAccountStalenessSeconds = 10
	DefaultAllowConcurrentScheduling           = false
	DefaultEnablePredictorEnhancedScheduling   = false
	DefaultMaxNumBatchedTokens                 = 65536
	DefaultNumPredictorWarmupSamples           = 20

	DefaultTtftSlo                     = 20000
	DefaultTpotSlo                     = 125
	DefaultTtftSloDispatchThreshold    = 0.85
	DefaultTpotSloDispatchThreshold    = 0.85
	DefaultTpotMigrateOutCeilThreshold = 0.95

	// Adaptive PD defaults
	DefaultEnableAdaptivePD             = false
	DefaultTpotMigrateOutFloorThreshold = 0.50

	// Filter defaults
	DefaultFailoverScope            = FailoverScopeInstanceUnit
	DefaultInstanceStalenessSeconds = 60

	// Rescheduling defaults
	DefaultEnableRescheduling               = false
	DefaultReschedulingPolicies             = "decode_load,prefill_failover,decode_failover,neutral_failover"
	DefaultReschedulingIntervalMs           = 500
	DefaultReschedulingDecodeLoadMetric     = SchedulingMetricKVCacheUsageRatioProjected
	DefaultReschedulingDecodeLoadThreshold  = 1.0
	DefaultReschedulingPrefillLoadMetric    = SchedulingMetricKVCacheUsageRatioProjected
	DefaultReschedulingNeutralLoadMetric    = SchedulingMetricKVCacheUsageRatioProjected
	DefaultReschedulingNeutralLoadThreshold = 1.0
	DefaultReschedulingReqSelectOrder       = MigrationReqSelectOrderSR
	DefaultReschedulingReqSelectRule        = MigrationReqSelectRuleToken
	DefaultReschedulingReqSelectValue       = 1
	DefaultReschedulingLoadBalanceThreshold = 0.01
	DefaultReschedulingLoadBalanceScope     = ReschedulingLoadBalanceScopeCluster

	// Llumlet defaults
	DefaultLlumletGrpcConnectionPoolSize = -1
	DefaultLlumletGrpcTimeoutSeconds     = -1

	DefaultEnableMetrics = false
)
