package config

import (
	"strings"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
)

type DiscoveryConfig struct {
	// instead of relying on the active registration of inference workers, the
	// backend services are actively discovered through the scheduler, which can
	// only be used in the scenario where BackendService are set
	LLMBackendDiscovery string
	SchedulerDiscovery  string

	RedisDiscoveryConfig
	EndpointDiscoveryConfig
}

func (c *DiscoveryConfig) AddDiscoveryConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.LLMBackendDiscovery, "llm-backend-discovery", "redis", "use redis/endpoints to discovery backend services.")
	flags.StringVar(&c.SchedulerDiscovery, "scheduler-discovery", "endpoints", "use endpoints to discovery scheduler services.")

	c.RedisDiscoveryConfig.AddRedisDiscoveryConfigFlags(flags)
	c.EndpointDiscoveryConfig.AddEndpointDiscoveryConfigFlags(flags)
}

type EndpointDiscoveryConfig struct {
	LLMBackendEndpoints string
	SchedulerEndpoints  string
}

func (c *EndpointDiscoveryConfig) AddEndpointDiscoveryConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.LLMBackendEndpoints, "llm-backend-endpoints", "", "backend endpoints, example: 0.0.0.0:8090,0.0.0.0:8091")
	flags.StringVar(&c.SchedulerEndpoints, "scheduler-endpoints", "", "scheduler endpoints, example: 0.0.0.0:8090,0.0.0.0:8091")
}

type RedisDiscoveryConfig struct {
	DiscoveryRedisHost          string
	DiscoveryRedisPort          int
	DiscoveryRedisUsername      string
	DiscoveryRedisPassword      string
	DiscoveryRedisSocketTimeout float64
	DiscoveryRedisRetryTimes    int

	DiscoveryRedisRefreshIntervalMs int
	DiscoveryRedisStatusTTLMs       int
}

func (c *RedisDiscoveryConfig) AddRedisDiscoveryConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.DiscoveryRedisHost, "discovery-redis-host", "redis", "Redis discovery host")
	flags.IntVar(&c.DiscoveryRedisPort, "discovery-redis-port", 6379, "Redis discovery port")
	flags.StringVar(&c.DiscoveryRedisUsername, "discovery-redis-username", "", "Redis discovery username")
	flags.StringVar(&c.DiscoveryRedisPassword, "discovery-redis-password", "", "Redis discovery password")
	flags.Float64Var(&c.DiscoveryRedisSocketTimeout, "discovery-redis-socket-timeout", 1.0, "Redis discovery socket timeout")
	flags.IntVar(&c.DiscoveryRedisRetryTimes, "discovery-redis-retry-times", 1, "Redis discovery retry times")
	flags.IntVar(&c.DiscoveryRedisStatusTTLMs, "discovery-redis-status-ttl-ms", 10000, "Redis discovery status TTL milliseconds")
	flags.IntVar(&c.DiscoveryRedisRefreshIntervalMs, "discovery-redis-refresh-interval-ms", 1000, "Redis discovery refresh interval milliseconds")
}

type ProcessorConfig struct {
	// Tokenizer related configuration
	// builtin tokenizer name
	TokenizerName string
	// self defined tokenizer path, will overwrite the builtin tokenizer name when not empty
	TokenizerPath    string
	ChatTemplatePath string

	ToolCallParser  string
	ReasoningParser string
}

func (c *ProcessorConfig) AddProcessorConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.TokenizerName, "tokenizer-name", "", "builtin tokenizer name")
	flags.StringVar(&c.TokenizerPath, "tokenizer-path", "", "builtin tokenizer path")
	flags.StringVar(&c.ChatTemplatePath, "chat-template", "", "chat template path")
	flags.StringVar(&c.ToolCallParser, "tool-call-parser", "", "tool call parser type")
	flags.StringVar(&c.ReasoningParser, "reasoning-parser", "", "reasoning parser type")
}

type RouteConfig struct {
	RoutePolicy    string
	RouteConfigRaw string
}

func (c *RouteConfig) AddRouteConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.RoutePolicy, "route-policy", "", "route policy, support weight and prefix")
	flags.StringVar(&c.RouteConfigRaw, "route-config", "", "route config, include api key, base url, weight/prefix and fallback priority")
}

type PDSplitConfig struct {
	// The configuration of pd split
	// LLM Gateway currently supports a variety of separate implementations of the prefill
	// and decode phases, which can be distinguished by this configuration
	PDSplitMode string
	// separate scheduling for p and d or not
	SeparatePDSchedule bool
}

func (c *PDSplitConfig) AddPDSplitConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.PDSplitMode, "pd-split-mode", "", "pd split mode, this configuration only takes effect under the pd-split policy, now support vllm-mooncake, vllm-kvt, sglang-mooncake")
	flags.BoolVar(&c.SeparatePDSchedule, "separate-pd-schedule", false, "Specify whether to separate pd schedule")
}

type BatchServiceConfig struct {
	BatchOSSPath           string
	BatchOSSEndpoint       string
	BatchParallel          int
	BatchLinesPerShard     int
	BatchRequestTimeout    time.Duration
	BatchRequestRetryTimes int

	BatchServiceRedisAddrs      string
	BatchServiceRedisUsername   string
	BatchServiceRedisPassword   string
	BatchServiceRedisRetryTimes int
}

func (c *BatchServiceConfig) AddBatchServiceConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.BatchOSSPath, "batch-oss-path", "", "OSS path for batch API")
	flags.StringVar(&c.BatchOSSEndpoint, "batch-oss-endpoint", "", "OSS endpoint (default is current region vpc endpoint)")
	flags.StringVar(&c.BatchServiceRedisAddrs, "batch-redis-addrs", "redis.roles:10000", "Redis addresses for batch API")
	flags.StringVar(&c.BatchServiceRedisUsername, "batch-redis-username", "default", "Redis username")
	flags.StringVar(&c.BatchServiceRedisPassword, "batch-redis-password", "default", "Redis password")
	flags.IntVar(&c.BatchServiceRedisRetryTimes, "batch-redis-retry-times", 3, "Redis retry times")

	flags.IntVar(&c.BatchParallel, "batch-parallel", 8, "The parallel of shard process")
	flags.IntVar(&c.BatchLinesPerShard, "batch-lines-per-shard", 1000, "The number of lines per shard file")
	flags.DurationVar(&c.BatchRequestTimeout, "batch-request-timeout", 3*time.Minute, "HTTP request timeout duration")
	flags.IntVar(&c.BatchRequestRetryTimes, "batch-request-retry-times", 3, "HTTP retry times")
}

type ScheduleBaseConfig struct {
	// lite-mode scheduling: When not enabling full mode scheduling (lite-mode scheduling), llumnix does not
	// intrusively modify inference engine, and only support basic load balance scheduling.
	// In lite mode scheduling, LLM gateway collect update realtime request token states and report these data to
	// LLM scheduler periodically. LLM scheduler perform load balance scheduling based on these local realtime states,
	// supporting num-requests and num-tokens scheduling metric.
	// full-mode scheduling: When enable full mode scheduling, llumnix intrusively modifies inference engine to
	// collect accurate load information from inference engine and support kv cache migration, and therefore can
	// support advanced scheduling feature like rescheduling, adaptive pd, etc.
	EnableFullModeScheduling bool

	SchedulePolicy string
}

func (c *ScheduleBaseConfig) AddScheduleBaseConfigFlags(flags *pflag.FlagSet) {
	flags.BoolVar(&c.EnableFullModeScheduling, "enable-full-mode-scheduling", consts.DefaultEnableFullModeScheduling, "Enable full mode scheduling")
	flags.StringVar(&c.SchedulePolicy, "schedule-policy", "load-balance", "schedule policy, now support round-robin, load-balance, flood")
}

type LiteModeScheduleConfig struct {
	// request token state report (report to llm scheduler) interval (seconds)
	RequestStateReportInterval int
}

func (c *LiteModeScheduleConfig) AddLiteModeScheduleConfigFlags(flags *pflag.FlagSet) {
	flags.IntVar(&c.RequestStateReportInterval, "requests-report-duration", 0, "Specify requests reporter duration")
}

type FullModeScheduleConfig struct {
	// cms
	CmsRedisHost              string
	CmsRedisPort              string
	CmsRedisUsername          string
	CmsRedisPassword          string
	CmsRedisSocketTimeout     float64
	CmsRedisRetryTimes        int
	CmsPullStatusIntervalMs   int32
	CmsPullMetadataIntervalMs int32

	// KvsMetadataService
	EnableCacheAwareScheduling             bool
	KvsBackend                             string
	KvsMetadataServiceConfigPath           string
	KvsChunkSize                           int
	KvsEnableSaveUnfullChunk               bool
	KvsIrisMetaPrefix                      string
	KvsVLLMBlockPrefix                     string
	KvsRetryTimes                          int
	KvsRetryIntervalMs                     int
	KvsMetadataServiceDownDurationS        int
	KvsMetadataServiceRedisClusterHosts    string
	KvsMetadataServiceRedisClusterPassword string
	KvsMetadataServiceHttpServerHost       string
	KvsMetadataServiceHttpServerPort       string

	// schedule
	DispatchTopK                        int
	DispatchNeutralLoadMetric           string
	DispatchNeutralLoadThreshold        float32
	DispatchPrefillLoadMetric           string
	DispatchPrefillLoadThreshold        float32
	DispatchDecodeLoadMetric            string
	DispatchDecodeLoadThreshold         float32
	DispatchPrefillCacheLocalityMetric  string
	EnableInstanceStatusLocalAccount    bool
	RequestLocalAccountStalenessSeconds int32
	AllowConcurrentSchedule             bool
	EnablePredictorEnhancedScheduling   bool
	MaxNumBatchedTokens                 int
	NumPredictorWarmupSamples           int

	// Adaptive PD
	EnableAdaptivePD                     bool
	DispatchPrefillAsDecodeLoadMetric    string
	DispatchPrefillAsDecodeLoadThreshold float32
	DispatchDecodeAsPrefillLoadMetric    string
	DispatchDecodeAsPrefillLoadThreshold float32
	DecodeComputeBoundBatchSize          int32

	// filter
	FailoverScope            string
	InstanceStalenessSeconds int64

	// reschedule
	EnableRescheduling             bool
	ReschedulePolicies             string
	RescheduleIntervalMs           int32
	RescheduleDecodeLoadMetric     string
	RescheduleDecodeLoadThreshold  float32
	ReschedulePrefillLoadMetric    string
	RescheduleNeutralLoadMetric    string
	RescheduleNeutralLoadThreshold float32
	RescheduleReqSelectOrder       string
	RescheduleReqSelectRule        string
	RescheduleReqSelectValue       float32
	RescheduleLoadBalanceThreshold float32
	RescheduleLoadBalanceScope     string

	// llumlet
	LlumletGrpcConnectionPoolSize int
	LlumletGrpcTimeoutSeconds     int

	// metrics
	EnableMetrics            bool
	CmsRecordMetricsInterval int32
}

func (c *FullModeScheduleConfig) AddFullModeScheduleConfigFlags(flags *pflag.FlagSet) {
	flags.StringVar(&c.CmsRedisHost, "cms-redis-host", consts.DefaultCmsRedisHost, "Llumnix CMS redis host")
	flags.StringVar(&c.CmsRedisPort, "cms-redis-port", consts.DefaultCmsRedisPort, "Llumnix CMS redis port")
	flags.StringVar(&c.CmsRedisUsername, "cms-redis-username", consts.DefaultCmsRedisUsername, "Llumnix CMS redis username")
	flags.StringVar(&c.CmsRedisPassword, "cms-redis-password", consts.DefaultCmsRedisPassword, "Llumnix CMS redis password")
	flags.Float64Var(&c.CmsRedisSocketTimeout, "cms-redis-timeout", consts.DefaultCmsRedisSocketTimeout, "Llumnix CMS redis socket timeout")
	flags.IntVar(&c.CmsRedisRetryTimes, "cms-redis-retry-times", consts.DefaultCmsRedisRetryTimes, "Llumnix CMS redis retry times")
	flags.Int32Var(&c.CmsPullStatusIntervalMs, "cms-pull-status-interval-ms", consts.DefaultCmsPullStatusIntervalMs, "Llumnix CMS pull status interval in milliseconds")
	flags.Int32Var(&c.CmsPullMetadataIntervalMs, "cms-pull-metadata-interval-ms", consts.DefaultCmsPullMetadataIntervalMs, "Llumnix CMS pull metadata interval in milliseconds")
	flags.Int32Var(&c.CmsRecordMetricsInterval, "cms-record-metrics-interval", consts.DefaultCmsRecordMetricsInterval, "Llumnix CMS record metrics interval")

	flags.BoolVar(&c.EnableCacheAwareScheduling, "enable-cache-aware-scheduling", consts.DefaultEnableCacheAwareScheduling, "Llumnix enable cache aware scheduling")
	flags.StringVar(&c.KvsBackend, "kvs-backend", consts.DefaultKvsBackend, "Llumnix KVS backend")
	flags.StringVar(&c.KvsMetadataServiceConfigPath, "kvs-metadata-service-config-path", consts.DefaultKvsMetadataServiceConfigPath, "Llumnix KVS MetadataService config path")
	flags.IntVar(&c.KvsChunkSize, "kvs-chunk-size", consts.DefaultKvsChunkSize, "Llumnix KVS chunk size")
	flags.BoolVar(&c.KvsEnableSaveUnfullChunk, "kvs-enable-save-unfull-chunk", consts.DefaultKvsEnableSaveUnfullChunk, "Llumnix KVS enable save unfull chunk")
	flags.StringVar(&c.KvsIrisMetaPrefix, "kvs-iris-meta-prefix", consts.DefaultKvsIrisMetaPrefix, "Llumnix KVS iris meta prefix")
	flags.StringVar(&c.KvsVLLMBlockPrefix, "kvs-vllm-block-prefix", consts.DefaultKvsVLLMBlockPrefix, "Llumnix KVS vllm block prefix")
	flags.IntVar(&c.KvsRetryIntervalMs, "kvs-retry-interval-ms", consts.DefaultKvsRetryIntervalMs, "Llumnix KVS retry interval in milliseconds")
	flags.IntVar(&c.KvsRetryTimes, "kvs-retry-times", consts.DefaultKvsRetryTimes, "Llumnix KVS retry times")
	flags.IntVar(&c.KvsMetadataServiceDownDurationS, "kvs-metadata-service-down-duration-s", consts.DefaultKvsMetadataServiceDownDurationS, "Llumnix KVS metadata service down duration in seconds")
	flags.StringVar(&c.KvsMetadataServiceRedisClusterHosts, "kvs-metadata-service-redis-cluster-hosts", consts.DefaultKvsMetadataServiceRedisClusterHosts, "Llumnix KVS metadata service redis cluster hosts")
	flags.StringVar(&c.KvsMetadataServiceRedisClusterPassword, "kvs-metadata-service-redis-cluster-password", consts.DefaultKvsMetadataServiceRedisClusterPassword, "Llumnix KVS metadata service redis cluster password")
	flags.StringVar(&c.KvsMetadataServiceHttpServerHost, "kvs-metadata-service-http-server-host", consts.DefaultKvsMetadataServiceHttpServerHost, "Llumnix KVS metadata service http server host")
	flags.StringVar(&c.KvsMetadataServiceHttpServerPort, "kvs-metadata-service-http-server-port", consts.DefaultKvsMetadataServiceHttpServerPort, "Llumnix KVS metadata service http server port")

	flags.IntVar(&c.DispatchTopK, "dispatch-top-k", consts.DefaultDispatchTopK, "Llumnix dispatch top K")
	flags.StringVar(&c.DispatchNeutralLoadMetric, "dispatch-neutral-load-metric", consts.DefaultDispatchNeutralLoadMetric, "Llumnix dispatch neutral load metric")
	flags.Float32Var(&c.DispatchNeutralLoadThreshold, "dispatch-neutral-load-threshold", consts.DefaultDispatchNeutralLoadThreshold, "Llumnix dispatch neutral load threshold")
	flags.StringVar(&c.DispatchPrefillLoadMetric, "dispatch-prefill-load-metric", consts.DefaultDispatchPrefillLoadMetric, "Llumnix dispatch prefill load metric")
	flags.Float32Var(&c.DispatchPrefillLoadThreshold, "dispatch-prefill-load-threshold", consts.DefaultDispatchPrefillLoadThreshold, "Llumnix dispatch prefill load threshold")
	flags.StringVar(&c.DispatchDecodeLoadMetric, "dispatch-decode-load-metric", consts.DefaultDispatchDecodeLoadMetric, "Llumnix dispatch decode load metric")
	flags.Float32Var(&c.DispatchDecodeLoadThreshold, "dispatch-decode-load-threshold", consts.DefaultDispatchDecodeLoadThreshold, "Llumnix dispatch decode load threshold")
	flags.StringVar(&c.DispatchPrefillCacheLocalityMetric, "dispatch-prefill-cache-locality-metric", consts.DefaultDispatchPrefillCacheLocalityMetric, "Llumnix dispatch prefill cache locality metric")
	flags.BoolVar(&c.EnableInstanceStatusLocalAccount, "enable-instance-status-local-account", consts.DefaultEnableInstanceStatusLocalAccount, "Llumnix enable instance status local account")
	flags.Int32Var(&c.RequestLocalAccountStalenessSeconds, "request-local-account-staleness-seconds", consts.DefaultRequestLocalAccountStalenessSeconds, "Llumnix request local account staleness seconds")
	flags.BoolVar(&c.AllowConcurrentSchedule, "allow-concurrent-schedule", consts.DefaultAllowConcurrentSchedule, "Llumnix allow concurrent schedule")
	flags.BoolVar(&c.EnablePredictorEnhancedScheduling, "enable-predictor-enhanced-scheduling", consts.DefaultEnablePredictorEnhancedScheduling, "Llumnix enable predictor enhanced scheduling")
	flags.IntVar(&c.MaxNumBatchedTokens, "max-num-batched-tokens", consts.DefaultMaxNumBatchedTokens, "Llumnix max num batched tokens")
	flags.IntVar(&c.NumPredictorWarmupSamples, "num-predictor-warmup-samples", consts.DefaultNumPredictorWarmupSamples, "Llumnix num predictor warmup samples")

	flags.BoolVar(&c.EnableAdaptivePD, "enable-adaptive-pd", consts.DefaultEnableAdaptivePD, "Llumnix enable adaptive pd")
	flags.StringVar(&c.DispatchPrefillAsDecodeLoadMetric, "dispatch-prefill-as-decode-load-metric", consts.DefaultDispatchPrefillAsDecodeLoadMetric, "Llumnix dispatch prefill as decode load metric")
	flags.Float32Var(&c.DispatchPrefillAsDecodeLoadThreshold, "dispatch-prefill-as-decode-load-threshold", consts.DefaultDispatchPrefillAsDecodeLoadThreshold, "Llumnix dispatch prefill as decode load threshold")
	flags.StringVar(&c.DispatchDecodeAsPrefillLoadMetric, "dispatch-decode-as-prefill-load-metric", consts.DefaultDispatchDecodeAsPrefillLoadMetric, "Llumnix dispatch decode as prefill load metric")
	flags.Float32Var(&c.DispatchDecodeAsPrefillLoadThreshold, "dispatch-decode-as-prefill-load-threshold", consts.DefaultDispatchDecodeAsPrefillLoadThreshold, "Llumnix dispatch decode as prefill load threshold")
	flags.Int32Var(&c.DecodeComputeBoundBatchSize, "decode-compute-bound-batch-size", consts.DefaultDecodeComputeBoundBatchSize, "Llumnix decode compute bound batch size for adaptive decode batch size metric")

	flags.StringVar(&c.FailoverScope, "failover-scope", consts.DefaultFailoverScope, "Llumnix failover scope")
	flags.Int64Var(&c.InstanceStalenessSeconds, "instance-staleness-seconds", consts.DefaultInstanceStalenessSeconds, "Llumnix instance staleness seconds")

	flags.BoolVar(&c.EnableRescheduling, "enable-rescheduling", consts.DefaultEnableRescheduling, "Llumnix enable rescheduling")
	flags.StringVar(&c.ReschedulePolicies, "reschedule-policies", consts.DefaultReschedulePolicies, "Llumnix reschedule policies, comma separated")
	flags.Int32Var(&c.RescheduleIntervalMs, "reschedule-interval-ms", consts.DefaultRescheduleIntervalMs, "Llumnix reschedule interval milliseconds")
	flags.StringVar(&c.RescheduleDecodeLoadMetric, "reschedule-decode-load-metric", consts.DefaultRescheduleDecodeLoadMetric, "Llumnix reschedule decode load metric")
	flags.Float32Var(&c.RescheduleDecodeLoadThreshold, "reschedule-decode-load-threshold", consts.DefaultRescheduleDecodeLoadThreshold, "Llumnix reschedule decode load threshold")
	flags.StringVar(&c.ReschedulePrefillLoadMetric, "reschedule-prefill-load-metric", consts.DefaultReschedulePrefillLoadMetric, "Llumnix reschedule prefill load metric")
	flags.StringVar(&c.RescheduleNeutralLoadMetric, "reschedule-neutral-load-metric", consts.DefaultRescheduleNeutralLoadMetric, "Llumnix reschedule neutral load metric")
	flags.Float32Var(&c.RescheduleNeutralLoadThreshold, "reschedule-neutral-load-threshold", consts.DefaultRescheduleNeutralLoadThreshold, "Llumnix reschedule neutral load threshold")
	flags.StringVar(&c.RescheduleReqSelectOrder, "reschedule-req-select-order", consts.DefaultRescheduleReqSelectOrder, "Llumnix reschedule req selection order")
	flags.StringVar(&c.RescheduleReqSelectRule, "reschedule-req-select-rule", consts.DefaultRescheduleReqSelectRule, "Llumnix reschedule req selection rule")
	flags.Float32Var(&c.RescheduleReqSelectValue, "reschedule-req-select-value", consts.DefaultRescheduleReqSelectValue, "Llumnix reschedule req selection value")
	flags.Float32Var(&c.RescheduleLoadBalanceThreshold, "reschedule-load-balance-threshold", consts.DefaultRescheduleLoadBalanceThreshold, "Llumnix reschedule load balance threshold")
	flags.StringVar(&c.RescheduleLoadBalanceScope, "reschedule-load-balance-scope", consts.DefaultRescheduleLoadBalanceScope, "Llumnix reschedule load balance scope")

	flags.IntVar(&c.LlumletGrpcConnectionPoolSize, "llumlet-grpc-connection-pool-size", consts.DefaultLlumletGrpcConnectionPoolSize, "Llumnix llumlet grpc connection pool size")
	flags.IntVar(&c.LlumletGrpcTimeoutSeconds, "llumlet-grpc-timeout-seconds", consts.DefaultLlumletGrpcTimeoutSeconds, "Llumnix llumlet grpc timeout seconds")

	flags.BoolVar(&c.EnableMetrics, "enable-metrics", consts.DefaultEnableMetrics, "Llumnix enable recording metrics")
}

// safeSplitArgs safely splits a string by comma, handling quotes and square brackets.
// Example: "a=1,b=[1,2,3],c='hello,world'" -> ["a=1", "b=[1,2,3]", "c='hello,world'"]
func safeSplitArgs(input string) []string {
	if input == "" {
		return nil
	}

	var result []string
	var current strings.Builder
	var inQuote rune     // 0 if not in quote, otherwise the quote char (' or ")
	var bracketDepth int // Track nesting depth of square brackets

	for i, ch := range input {
		// Handle escape sequences
		if i > 0 && input[i-1] == '\\' {
			current.WriteRune(ch)
			continue
		}

		// Handle quotes
		if (ch == '\'' || ch == '"') && bracketDepth == 0 {
			if inQuote == 0 {
				inQuote = ch
			} else if inQuote == ch {
				inQuote = 0
			}
			current.WriteRune(ch)
			continue
		}

		// Skip processing if inside quotes
		if inQuote != 0 {
			current.WriteRune(ch)
			continue
		}

		// Handle square brackets
		if ch == '[' {
			bracketDepth++
			current.WriteRune(ch)
			continue
		}

		if ch == ']' {
			if bracketDepth > 0 {
				bracketDepth--
			}
			current.WriteRune(ch)
			continue
		}

		// Handle comma separator (only when not in quotes or brackets)
		if ch == ',' && bracketDepth == 0 {
			if current.Len() > 0 {
				result = append(result, strings.TrimSpace(current.String()))
				current.Reset()
			}
			continue
		}

		current.WriteRune(ch)
	}

	// Add the last segment
	if current.Len() > 0 {
		result = append(result, strings.TrimSpace(current.String()))
	}

	// Remove surrounding quotes from each segment
	for i, segment := range result {
		// Split by first '=' to check if it's a key=value pair
		parts := strings.SplitN(segment, "=", 2)
		if len(parts) == 2 {
			value := parts[1]
			// Remove quotes (not brackets)
			if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
				result[i] = parts[0] + "=" + value[1:len(value)-1]
			} else if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
				result[i] = parts[0] + "=" + value[1:len(value)-1]
			}
		}
	}

	return result
}

// ParseLlumnixExtraArgs parses llumnix-extra-args and overrides corresponding flag values.
// Supports two formats for array values:
//   - Quoted strings: key='value1,value2' -> parsed as: key=value1,value2 (quotes removed)
//   - Bracket arrays: key=[value1,value2] -> parsed as: key=[value1,value2] (brackets kept)
//
// Example: "dispatch-top-k=5,policies=[p1,p2],timeout='10,20'" ->
//
//	"dispatch-top-k=5", "policies=[p1,p2]", "timeout=10,20"
func ParseLlumnixExtraArgs(flags *pflag.FlagSet, extraArgs string) {
	if extraArgs == "" {
		return
	}

	klog.Infof("Parsing extra-args: %s", extraArgs)

	// Use safeSplitArgs to safely split by comma, handling quotes, brackets and arrays
	args := safeSplitArgs(extraArgs)

	for _, arg := range args {
		if arg == "" {
			continue
		}

		// Split by first '=' to get key-value pair
		parts := strings.SplitN(arg, "=", 2)
		if len(parts) != 2 {
			klog.Warningf("Invalid extra arg format (expected key=value): %s", arg)
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Try to set the flag value
		flag := flags.Lookup(key)
		if flag == nil {
			klog.Warningf("Flag not found, skipping: %s", key)
			continue
		}

		if err := flags.Set(key, value); err != nil {
			klog.Warningf("Failed to set flag %s=%s: %v", key, value, err)
			continue
		}

		klog.Infof("Successfully set flag from extra-args: %s=%s", key, value)
	}
}
