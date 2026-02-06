package options

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"llm-gateway/pkg/utils"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
	prop "llm-gateway/pkg/property"
)

// TODO(sunbiao.sun): Group the parameters in the config.
type Config struct {
	Port int
	Host string

	configManager *prop.ConfigManager

	Service              string // service name
	LlmScheduler         string // llm scheduler service
	LlmGateway           string // llm gateway service
	Redis                string // redis service
	LocalTestIPs         string // local test ips
	LocalTestSchedulerIP string // local test ip for scheduler
	EnablePprof          bool   // enable pprof

	ServiceToken        string
	DiscoveryEndpoint   string
	BackendService      string // backend service
	BackendServiceToken string // backend service token

	ScheduleMode bool
	// ColocatedRescheduleMode means whether to start a rescheduler inside a scheduler process
	// (so that they can share the cms client).
	ColocatedRescheduleMode bool
	// StandaloneRescheduleMode means whether to start a rescheduler as a separate process.
	StandaloneRescheduleMode bool

	// schedule policy
	SchedulePolicy string

	// The configuration of pd splits
	// LLM Gateway currently supports a variety of separate implementations of the prefill
	// and decode phases, which can be distinguished by this configuration
	PDSplitMode string

	// separate scheduling for p and d or not
	SeparatePDSchedule bool

	// number of retries when forwarding fails
	RetryCount int

	// number of coroutines which read the requests from queue
	WaitQueueThreads int

	// waiting timeout if no free token, unit is milliseconds, 0 means that drop request
	WaitScheduleTimeout int
	// retry period while waiting free tokens, unit is milliseconds
	WaitScheduleTryPeriod int

	// max queue size
	MaxQueueSize int

	// deprecated, inference backend type, support vllm, blade
	InferBackend string

	// enable access log
	EnableAccessLog bool

	// enable input log
	EnableLogInput bool

	// run on serverless
	ServerlessMode bool

	// instead of relying on the active registration of inference workers, the
	// backend services are actively discovered through the scheduler, which can
	// only be used in the scenario where BackendService are set
	UseDiscovery string

	// time to live of the prefix cache, unit is second
	PrefixCacheTTL int
	// max size of the prefix cache, unit is byte
	PrefixCacheLimit int64
	// load tolerance factor for prefix cache worker selection (0.0-1.0)
	// determines acceptable load deviation above mean when selecting cache-hit workers
	// e.g., 0.5 means workers with load <= (mean * 1.5) are acceptable
	PrefixCacheLoadTolerance float64
	// if the number of requests for an instance exceeds this value when the prefix
	// cache is satisfied, a new instance will be selected to be a more suitable one
	RequestsThreshold int

	// Tokenizer related configuration
	// builtin tokenizer name
	TokenizerName string
	// self defined tokenizer path, will overwrite the builtin tokenizer name when not empty
	TokenizerPath    string
	ChatTemplatePath string
	ToolCallParser   string
	ReasoningParser  string
	TokenizerMode    string

	// requests token reporter duration (seconds)
	RequestsReporterDuration int

	// TODO(sunbiao.sun): remove

	// max requests for every instance
	LimitRequests        int
	PrefillLimitRequests int
	DecodeLimitRequests  int

	// the limit of every instance tokens, used to limit the number of tokens in the instance
	LimitTokens        int
	PrefillLimitTokens int
	DecodeLimitTokens  int

	// the scale of threshold, used to calculate the threshold of every decode tokens
	LimitTokensThresholdScale float32

	// the threshold of request prompt length
	PromptLengthThreshold int

	// ProcessorType，used to select the processor
	ProcessorType string

	// route config for llm gateway
	RoutePolicy    string
	RouteConfigRaw string

	RedisAddrs      string
	RedisUsername   string
	RedisPassword   string
	RedisRetryTimes int

	RedisDiscoveryRefreshIntervalMs int
	RedisDiscoveryStatusTTLMs       int

	BatchOSSPath           string
	BatchOSSEndpoint       string
	BatchParallel          int
	BatchLinesPerShard     int
	BatchRequestTimeout    time.Duration
	BatchRequestRetryTimes int

	// TODO(sunbiao.sun): remove
	PrefillPolicy string
	DecodePolicy  string

	LlumnixConfig LlumnixConfig
}

type LlumnixConfig struct {
	EnableFullModeScheduling bool

	// cms
	CmsRedisHost              string
	CmsRedisPort              string
	CmsRedisUsername          string
	CmsRedisPassword          string
	CmsRedisSocketTimeout     float64
	CmsRedisRetryTimes        int
	CmsPullStatusIntervalMs   int32
	CmsPullMetadataIntervalMs int32

	// KvsMetaService
	EnableCacheAwareScheduling         bool
	KvsMetaServiceConfigPath           string
	KvsChunkSize                       int
	KvsEnableSaveUnfullChunk           bool
	KvsIrisMetaPrefix                  string
	KvsVLLMBlockPrefix                 string
	KvsRetryTimes                      int
	KvsRetryIntervalMs                 int
	KvsMetaServiceDownDurationS        int
	KvsMetaServiceRedisClusterHosts    string
	KvsMetaServiceRedisClusterPassword string

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
	KvCacheBlockSize                    int32
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

	ExtraArgs string

	// TODO(sunbiao.sun): remove
	DispatchPolicy string
}

func (c *Config) AddServerFlags(flags *pflag.FlagSet) {
	flags.IntVar(&c.Port, "port", 8001, "http service listen port")
	flags.StringVar(&c.Host, "host", "0.0.0.0", "http service listen host")
}

func (c *Config) AddConfigFlags(flags *pflag.FlagSet) {
	flags.BoolVar(&c.EnablePprof, "enable-pprof", false, "enable pprof")

	flags.StringVar(&c.BackendService, "backend-service", "", "backend services")
	flags.StringVar(&c.BackendServiceToken, "backend-service-token", "", "backend service token")

	flags.StringVar(&c.ServiceToken, "token", "", "service token")
	flags.StringVar(&c.LlmScheduler, "llm-scheduler", "", "llm scheduler service")

	flags.StringVar(&c.LocalTestIPs, "local-test-ip", "", "local test")
	flags.StringVar(&c.LocalTestSchedulerIP, "local-test-scheduler-ip", "", "local test scheduler ip")

	flags.StringVar(&c.DiscoveryEndpoint, "discovery-endpoint", "", "discovery endpoint")
	flags.BoolVar(&c.ScheduleMode, "schedule-mode", false, "work as a scheduler")
	flags.BoolVar(&c.ColocatedRescheduleMode, "colocated-reschedule-mode", false, "work as a scheduler with a colocated rescheduler")
	flags.BoolVar(&c.StandaloneRescheduleMode, "standalone-reschedule-mode", false, "work as a standalone rescheduler")
	flags.StringVar(&c.SchedulePolicy, "schedule-policy", "least-token", "schedule policy, now support round-robin, load-balance, flood")
	flags.IntVar(&c.PrefixCacheTTL, "prefix-cache-ttl", 1800, "prefix cache time to live")
	flags.Float64Var(&c.PrefixCacheLoadTolerance, "prefix-cache-load-tolerance", 0.3, "load tolerance factor (0.0-1.0) for prefix cache worker selection")
	flags.IntVar(&c.RequestsThreshold, "requests-threshold", 10, "the threshold of try to choose other instance, 0 indicates the average value of usage requests")

	flags.StringVar(&c.PDSplitMode, "pdsplit-mode", "", "pd split mode, this configuration only takes effect under the pd-split policy, now support vllm-vineyard, vllm-kvt, sglang-mooncake")

	flags.BoolVar(&c.EnableAccessLog, "enable-access-log", true, "enable access log or not")
	flags.BoolVar(&c.EnableLogInput, "enable-log-input", false, "enable log input or not")
	flags.BoolVar(&c.ServerlessMode, "serverless-mode", false, "run on serverless")

	flags.IntVar(&c.RetryCount, "retry-count", 0, "gateway forwarding retry count")
	flags.IntVar(&c.WaitQueueThreads, "wait-queue-threads", 5, "number of coroutines which read the requests from queue")
	flags.IntVar(&c.WaitScheduleTimeout, "wait-schedule-timeout", 10000, "waiting timeout if no free token, unit(milliseconds)")
	flags.IntVar(&c.WaitScheduleTryPeriod, "wait-schedule-try-period", 1000, "retry period while waiting free tokens, unit(milliseconds)")

	flags.IntVar(&c.MaxQueueSize, "max-queue-size", 512, "max buffer queue size")

	flags.StringVar(&c.InferBackend, "infer-backend", "vllm", "deprecated, inference backend type, support vllm, blade")

	flags.StringVar(&c.UseDiscovery, "use-discovery", "", "the scheduler use cache-server/message-bus to discovery backend services.")

	flags.StringVar(&c.ProcessorType, "processor-type", "", "use processor to modify request")

	flags.StringVar(&c.TokenizerName, "tokenizer-name", "", "builtin tokenizer name")
	flags.StringVar(&c.TokenizerPath, "tokenizer-path", "", "builtin tokenizer path")
	flags.StringVar(&c.ChatTemplatePath, "chat-template", "", "chat template path")
	flags.StringVar(&c.ToolCallParser, "tool-call-parser", "", "tool call parser type")
	flags.StringVar(&c.ReasoningParser, "reasoning-parser", "", "reasoning parser type")
	flags.StringVar(&c.TokenizerMode, "tokenizer-mode", "", "builtin tokenizer mode, support formatter or leave null as vllmv0")

	flags.IntVar(&c.RequestsReporterDuration, "requests-report-duration", 0, "Specify requests reporter duration")

	// TODO(sunbiao.sun): remove
	flags.IntVar(&c.LimitRequests, "limit-requests", 0, "max requests for every backend llm instances.")
	flags.IntVar(&c.PrefillLimitRequests, "prefill-limit-requests", 0, "max requests for every backend llm instances.")
	flags.IntVar(&c.DecodeLimitRequests, "decode-limit-requests", 0, "max requests for every backend llm instances.")

	flags.IntVar(&c.LimitTokens, "limit-tokens", 0, "max tokens for every backend llm instances.")
	flags.IntVar(&c.PrefillLimitTokens, "prefill-limit-tokens", 0, "max tokens for every backend for prefill llm instances.")
	flags.IntVar(&c.DecodeLimitTokens, "decode-limit-tokens", 0, "max tokens for every backend for decode llm instances.")
	flags.Float32Var(&c.LimitTokensThresholdScale, "limit-tokens-threshold-scale", 0.0, "the scale of threshold, used to calculate the threshold of every decode tokens.")
	flags.IntVar(&c.PromptLengthThreshold, "prompt-length-threshold", 0, "the threshold of request prompt length, used to determine whether the request is a long request")
	flags.BoolVar(&c.SeparatePDSchedule, "separate-pd-schedule", false, "Specify whether to separate pd schedule")

	// TODO(sunbiao.sun): remove
	flags.StringVar(&c.PrefillPolicy, "prefill-policy", "load-balance", "prefill schedule policy, the same value as the schedule policy")
	flags.StringVar(&c.DecodePolicy, "decode-policy", "load-balance", "decode schedule policy, the same value as the schedule policy")

	flags.BoolVar(&c.LlumnixConfig.EnableFullModeScheduling, "enable-full-mode-scheduling", false, "Enable full mode scheduling")

	flags.StringVar(&c.LlumnixConfig.CmsRedisHost, "llumnix-cms-redis-host", consts.DefaultLlumnixCmsRedisHost, "Llumnix CMS redis host")
	flags.StringVar(&c.LlumnixConfig.CmsRedisPort, "llumnix-cms-redis-port", consts.DefaultLlumnixCmsRedisPort, "Llumnix CMS redis port")
	flags.StringVar(&c.LlumnixConfig.CmsRedisUsername, "llumnix-cms-redis-username", consts.DefaultLlumnixCmsRedisUsername, "Llumnix CMS redis username")
	flags.StringVar(&c.LlumnixConfig.CmsRedisPassword, "llumnix-cms-redis-password", consts.DefaultLlumnixCmsRedisPassword, "Llumnix CMS redis password")
	flags.Float64Var(&c.LlumnixConfig.CmsRedisSocketTimeout, "llumnix-cms-redis-timeout", consts.DefaultLlumnixCmsRedisSocketTimeout, "Llumnix CMS redis socket timeout")
	flags.IntVar(&c.LlumnixConfig.CmsRedisRetryTimes, "llumnix-cms-redis-retry-times", consts.DefaultLlumnixCmsRedisRetryTimes, "Llumnix CMS redis retry times")
	flags.Int32Var(&c.LlumnixConfig.CmsPullStatusIntervalMs, "llumnix-cms-pull-status-interval-ms", consts.DefaultLlumnixCmsPullStatusIntervalMs, "Llumnix CMS pull status interval in milliseconds")
	flags.Int32Var(&c.LlumnixConfig.CmsPullMetadataIntervalMs, "llumnix-cms-pull-metadata-interval-ms", consts.DefaultLlumnixCmsPullMetadataIntervalMs, "Llumnix CMS pull metadata interval in milliseconds")
	flags.Int32Var(&c.LlumnixConfig.CmsRecordMetricsInterval, "llumnix-cms-record-metrics-interval", consts.DefaultLlumnixCmsRecordMetricsInterval, "Llumnix CMS record metrics interval")

	flags.BoolVar(&c.LlumnixConfig.EnableCacheAwareScheduling, "llumnix-enable-cache-aware-scheduling", consts.DefaultLlumnixEnableCacheAwareScheduling, "Llumnix enable cache aware scheduling")
	flags.StringVar(&c.LlumnixConfig.KvsMetaServiceConfigPath, "llumnix-kvs-meta-service-config-path", consts.DefaultLlumnixKvsMetaServiceConfigPath, "Llumnix KVS MetaService config path")
	flags.IntVar(&c.LlumnixConfig.KvsChunkSize, "llumnix-kvs-chunk-size", consts.DefaultLlumnixKvsChunkSize, "Llumnix KVS chunk size")
	flags.BoolVar(&c.LlumnixConfig.KvsEnableSaveUnfullChunk, "llumnix-kvs-enable-save-unfull-chunk", consts.DefaultLlumnixKvsEnableSaveUnfullChunk, "Llumnix KVS enable save unfull chunk")
	flags.StringVar(&c.LlumnixConfig.KvsIrisMetaPrefix, "llumnix-kvs-iris-meta-prefix", consts.DefaultLlumnixKvsIrisMetaPrefix, "Llumnix KVS iris meta prefix")
	flags.StringVar(&c.LlumnixConfig.KvsVLLMBlockPrefix, "llumnix-kvs-vllm-block-prefix", consts.DefaultLlumnixKvsVLLMBlockPrefix, "Llumnix KVS vllm block prefix")
	flags.IntVar(&c.LlumnixConfig.KvsRetryIntervalMs, "llumnix-kvs-retry-interval-ms", consts.DefaultLlumnixKvsRetryIntervalMs, "Llumnix KVS retry interval in milliseconds")
	flags.IntVar(&c.LlumnixConfig.KvsRetryTimes, "llumnix-kvs-retry-times", consts.DefaultLlumnixKvsRetryTimes, "Llumnix KVS retry times")
	flags.IntVar(&c.LlumnixConfig.KvsMetaServiceDownDurationS, "llumnix-kvs-meta-service-down-duration-s", consts.DefaultLlumnixKvsMetaServiceDownDurationS, "Llumnix KVS meta service down duration in seconds")
	flags.StringVar(&c.LlumnixConfig.KvsMetaServiceRedisClusterHosts, "llumnix-kvs-meta-service-redis-cluster-hosts", consts.DefaultLlumnixKvsMetaServiceRedisClusterHosts, "Llumnix KVS meta service redis cluster hosts")
	flags.StringVar(&c.LlumnixConfig.KvsMetaServiceRedisClusterPassword, "llumnix-kvs-meta-service-redis-cluster-password", consts.DefaultLlumnixKvsMetaServiceRedisClusterPassword, "Llumnix KVS meta service redis cluster password")

	flags.IntVar(&c.LlumnixConfig.DispatchTopK, "llumnix-dispatch-top-k", consts.DefaultLlumnixDispatchTopK, "Llumnix dispatch top K")
	flags.StringVar(&c.LlumnixConfig.DispatchNeutralLoadMetric, "llumnix-dispatch-neutral-load-metric", consts.DefaultLlumnixDispatchNeutralLoadMetric, "Llumnix dispatch neutral load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchNeutralLoadThreshold, "llumnix-dispatch-neutral-load-threshold", consts.DefaultLlumnixDispatchNeutralLoadThreshold, "Llumnix dispatch neutral load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchPrefillLoadMetric, "llumnix-dispatch-prefill-load-metric", consts.DefaultLlumnixDispatchPrefillLoadMetric, "Llumnix dispatch prefill load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchPrefillLoadThreshold, "llumnix-dispatch-prefill-load-threshold", consts.DefaultLlumnixDispatchPrefillLoadThreshold, "Llumnix dispatch prefill load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchDecodeLoadMetric, "llumnix-dispatch-decode-load-metric", consts.DefaultLlumnixDispatchDecodeLoadMetric, "Llumnix dispatch decode load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchDecodeLoadThreshold, "llumnix-dispatch-decode-load-threshold", consts.DefaultLlumnixDispatchDecodeLoadThreshold, "Llumnix dispatch decode load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchPrefillCacheLocalityMetric, "llumnix-dispatch-prefill-cache-locality-metric", consts.DefaultLlumnixDispatchPrefillCacheLocalityMetric, "Llumnix dispatch prefill cache locality metric")
	// TODO(sunbiao.sun): delete it
	flags.BoolVar(&c.LlumnixConfig.EnableInstanceStatusLocalAccount, "llumnix-enable-scheduling-account", consts.DefaultLlumnixEnableInstanceStatusLocalAccount, "Llumnix enable instance status local account")
	flags.BoolVar(&c.LlumnixConfig.EnableInstanceStatusLocalAccount, "llumnix-enable-instance-status-local-account", consts.DefaultLlumnixEnableInstanceStatusLocalAccount, "Llumnix enable instance status local account")
	flags.Int32Var(&c.LlumnixConfig.KvCacheBlockSize, "llumnix-kv-cache-block-size", consts.DefaultLlumnixKvCacheBlockSize, "Llumnix KV cache block size")
	// TODO(sunbiao.sun): delete it
	flags.Int32Var(&c.LlumnixConfig.RequestLocalAccountStalenessSeconds, "llumnix-scheduling-account-request-staleness-seconds", consts.DefaultLlumnixRequestLocalAccountStalenessSeconds, "Llumnix request local account staleness seconds")
	flags.Int32Var(&c.LlumnixConfig.RequestLocalAccountStalenessSeconds, "llumnix-request-local-account-staleness-seconds", consts.DefaultLlumnixRequestLocalAccountStalenessSeconds, "Llumnix request local account staleness seconds")
	flags.BoolVar(&c.LlumnixConfig.AllowConcurrentSchedule, "llumnix-allow-concurrent-schedule", consts.DefaultLlumnixAllowConcurrentSchedule, "Llumnix allow concurrent schedule")
	flags.BoolVar(&c.LlumnixConfig.EnablePredictorEnhancedScheduling, "llumnix-enable-predictor-enhanced-scheduling", consts.DefaultLlumnixEnablePredictorEnhancedScheduling, "Llumnix enable predictor enhanced scheduling")
	flags.IntVar(&c.LlumnixConfig.MaxNumBatchedTokens, "llumnix-max-num-batched-tokens", consts.DefaultLlumnixMaxNumBatchedTokens, "Llumnix max num batched tokens")
	flags.IntVar(&c.LlumnixConfig.NumPredictorWarmupSamples, "llumnix-num-predictor-warmup-samples", consts.DefaultLlumnixNumPredictorWarmupSamples, "Llumnix num predictor warmup samples")

	flags.BoolVar(&c.LlumnixConfig.EnableAdaptivePD, "llumnix-enable-adaptive-pd", consts.DefaultLlumnixEnableAdaptivePD, "Llumnix enable adaptive pd")
	flags.StringVar(&c.LlumnixConfig.DispatchPrefillAsDecodeLoadMetric, "llumnix-dispatch-prefill-as-decode-load-metric", consts.DefaultLlumnixDispatchPrefillAsDecodeLoadMetric, "Llumnix dispatch prefill as decode load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchPrefillAsDecodeLoadThreshold, "llumnix-dispatch-prefill-as-decode-load-threshold", consts.DefaultLlumnixDispatchPrefillAsDecodeLoadThreshold, "Llumnix dispatch prefill as decode load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchDecodeAsPrefillLoadMetric, "llumnix-dispatch-decode-as-prefill-load-metric", consts.DefaultLlumnixDispatchDecodeAsPrefillLoadMetric, "Llumnix dispatch decode as prefill load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchDecodeAsPrefillLoadThreshold, "llumnix-dispatch-decode-as-prefill-load-threshold", consts.DefaultLlumnixDispatchDecodeAsPrefillLoadThreshold, "Llumnix dispatch decode as prefill load threshold")
	flags.Int32Var(&c.LlumnixConfig.DecodeComputeBoundBatchSize, "llumnix-decode-compute-bound-batch-size", consts.DefaultLlumnixDecodeComputeBoundBatchSize, "Llumnix decode compute bound batch size for adaptive decode batch size metric")

	flags.StringVar(&c.LlumnixConfig.FailoverScope, "llumnix-failover-scope", consts.DefaultLlumnixFailoverScope, "Llumnix failover scope")
	flags.Int64Var(&c.LlumnixConfig.InstanceStalenessSeconds, "llumnix-instance-staleness-seconds", consts.DefaultLlumnixInstanceStalenessSeconds, "Llumnix instance staleness seconds")

	flags.BoolVar(&c.LlumnixConfig.EnableRescheduling, "llumnix-enable-rescheduling", consts.DefaultLlumnixEnableRescheduling, "Llumnix enable rescheduling")
	flags.StringVar(&c.LlumnixConfig.ReschedulePolicies, "llumnix-reschedule-policies", consts.DefaultLlumnixReschedulePolicies, "Llumnix reschedule policies, comma separated")
	flags.Int32Var(&c.LlumnixConfig.RescheduleIntervalMs, "llumnix-reschedule-interval-ms", consts.DefaultLlumnixRescheduleIntervalMs, "Llumnix reschedule interval milliseconds")
	flags.StringVar(&c.LlumnixConfig.RescheduleDecodeLoadMetric, "llumnix-reschedule-decode-load-metric", consts.DefaultLlumnixRescheduleDecodeLoadMetric, "Llumnix reschedule decode load metric")
	flags.Float32Var(&c.LlumnixConfig.RescheduleDecodeLoadThreshold, "llumnix-reschedule-decode-load-threshold", consts.DefaultLlumnixRescheduleDecodeLoadThreshold, "Llumnix reschedule decode load threshold")
	flags.StringVar(&c.LlumnixConfig.ReschedulePrefillLoadMetric, "llumnix-reschedule-prefill-load-metric", consts.DefaultLlumnixReschedulePrefillLoadMetric, "Llumnix reschedule prefill load metric")
	flags.StringVar(&c.LlumnixConfig.RescheduleNeutralLoadMetric, "llumnix-reschedule-neutral-load-metric", consts.DefaultLlumnixRescheduleNeutralLoadMetric, "Llumnix reschedule neutral load metric")
	flags.Float32Var(&c.LlumnixConfig.RescheduleNeutralLoadThreshold, "llumnix-reschedule-neutral-load-threshold", consts.DefaultLlumnixRescheduleNeutralLoadThreshold, "Llumnix reschedule neutral load threshold")
	flags.IntVar(&c.LlumnixConfig.LlumletGrpcConnectionPoolSize, "llumnix-llumlet-grpc-connection-pool-size", consts.DefaultLlumnixLlumletGrpcConnectionPoolSize, "Llumnix llumlet grpc connection pool size")
	flags.StringVar(&c.LlumnixConfig.RescheduleReqSelectOrder, "llumnix-reschedule-req-select-order", consts.DefaultLlumnixRescheduleReqSelectOrder, "Llumnix reschedule req selection order")
	flags.StringVar(&c.LlumnixConfig.RescheduleReqSelectRule, "llumnix-reschedule-req-select-rule", consts.DefaultLlumnixRescheduleReqSelectRule, "Llumnix reschedule req selection rule")
	flags.Float32Var(&c.LlumnixConfig.RescheduleReqSelectValue, "llumnix-reschedule-req-select-value", consts.DefaultLlumnixRescheduleReqSelectValue, "Llumnix reschedule req selection value")
	flags.Float32Var(&c.LlumnixConfig.RescheduleLoadBalanceThreshold, "llumnix-reschedule-load-balance-threshold", consts.DefaultLlumnixRescheduleLoadBalanceThreshold, "Llumnix reschedule load balance threshold")
	flags.StringVar(&c.LlumnixConfig.RescheduleLoadBalanceScope, "llumnix-reschedule-load-balance-scope", consts.DefaultLlumnixRescheduleLoadBalanceScope, "Llumnix reschedule load balance scope")
	flags.IntVar(&c.LlumnixConfig.LlumletGrpcTimeoutSeconds, "llumnix-llumlet-grpc-timeout-seconds", consts.DefaultLlumnixLlumletGrpcTimeoutSeconds, "Llumnix llumlet grpc timeout seconds")

	flags.BoolVar(&c.LlumnixConfig.EnableMetrics, "llumnix-enable-metrics", consts.DefaultLlumnixEnableMetrics, "Llumnix enable recording metrics")
	flags.StringVar(&c.LlumnixConfig.ExtraArgs, "llumnix-extra-args", consts.DefaultLlumnixExtraArgs, "Llumnix extra args")

	flags.StringVar(&c.RoutePolicy, "route-policy", "", "route policy, support weight and prefix")
	flags.StringVar(&c.RouteConfigRaw, "route-config", "", "route config, include api key, base url, weight/prefix and fallback priority")

	flags.StringVar(&c.BatchOSSPath, "batch-oss-path", "", "OSS path for batch API")
	flags.StringVar(&c.BatchOSSEndpoint, "batch-oss-endpoint", "", "OSS endpoint (default is current region vpc endpoint)")
	flags.StringVar(&c.RedisAddrs, "redis-addrs", "redis.roles:10000", "Redis addresses for batch API")
	flags.StringVar(&c.RedisUsername, "redis-username", "default", "Redis username")
	flags.StringVar(&c.RedisPassword, "redis-password", "default", "Redis password")
	flags.IntVar(&c.RedisRetryTimes, "redis-retry-times", 3, "Redis retry times")

	flags.IntVar(&c.RedisDiscoveryStatusTTLMs, "redis-discovery-status-ttl-ms", 10000, "Redis discovery status TTL milliseconds")
	flags.IntVar(&c.RedisDiscoveryRefreshIntervalMs, "redis-discovery-refresh-interval-ms", 5000, "Redis discovery refresh interval milliseconds")

	flags.IntVar(&c.BatchParallel, "batch-parallel", 8, "The parallel of shard process")
	flags.IntVar(&c.BatchLinesPerShard, "batch-lines-per-shard", 1000, "The number of lines per shard file")
	flags.DurationVar(&c.BatchRequestTimeout, "batch-request-timeout", 3*time.Minute, "HTTP request timeout duration")
	flags.IntVar(&c.BatchRequestRetryTimes, "batch-request-retry-times", 3, "HTTP retry times")

	// TODO(sunbiao.sun): remove
	flags.StringVar(&c.LlumnixConfig.DispatchPolicy, "llumnix-dispatch-policy", consts.DefaultLlumnixDispatchPolicy, "Llumnix dispatch policy")
}

func (c *Config) AddFlags(flags *pflag.FlagSet) {
	c.AddServerFlags(flags)
	c.AddConfigFlags(flags)
	flags.AddGoFlagSet(flag.CommandLine)
}

func (c *Config) GetConfigManager() *prop.ConfigManager {
	return c.configManager
}

func (c *Config) IsPDSplitMode() bool {
	return c.PDSplitMode != ""
}

func (c *Config) IsPDRoundRobin() bool {
	return c.IsPDSplitMode() && c.SchedulePolicy == consts.SchedulePolicyRoundRobin
}

func (c *Config) IsVllmV6dSplitMode() bool {
	return c.PDSplitMode == consts.SplitModeVllmV6d
}

func (c *Config) IsVllmKvtSplitMode() bool {
	return c.PDSplitMode == consts.SplitModeVllmKvt
}

func (c *Config) IsSGLangMooncakeSplitMode() bool {
	return c.PDSplitMode == consts.SplitModeSGlangMooncake
}

func (c *Config) IsVllmMooncakeSplitMode() bool {
	return c.PDSplitMode == consts.SplitModeVllmMooncake
}

func (c *Config) IsPDProxySplitMode() bool {
	return c.IsPDSplitMode() && c.IsSGLangMooncakeSplitMode()
}

func (c *Config) TokenizerEnabled() bool {
	return c.TokenizerName != "" || c.TokenizerPath != ""
}

func (c *Config) EnableRequestReport() bool {
	return c.RequestsReporterDuration > 0 && !c.LlumnixConfig.EnableFullModeScheduling
}

// getPrefixCacheLimit configures the prefix cache size based on available memory.
// It reserves a fixed amount (consts.DefaultPrefixCacheReservedSize = 1GB) for program runtime,
// and allocates the remaining memory to the prefix cache.
//
// Memory calculation:
//
//	PrefixCacheLimit = (TotalMemory - ReservedSize) / MB
func (c *Config) getPrefixCacheLimit() (int64, error) {
	memLimit := utils.GetLimitMemory()

	if memLimit == 0 {
		klog.Infof("No memory limit detected, using default value: %d MB", c.PrefixCacheLimit/consts.MB)
		return consts.DefaultPrefixCacheMaxSize, nil
	}

	// Reserve memory for program runtime (heap, stack, goroutines, OS buffers, etc.)
	cacheSize := memLimit - consts.DefaultPrefixCacheReservedSize
	if cacheSize < 0 {
		klog.Errorf("Memory limit (%d bytes) is smaller than reserved size (%d bytes), prefix cache cannot be initialized",
			memLimit, consts.DefaultPrefixCacheReservedSize)
		return 0, fmt.Errorf("memory limit is smaller than reserved size")
	}

	// Convert bytes to MB for storage
	klog.Infof("Prefix cache size configured: %d MB (total memory: %d bytes, reserved: %d bytes)",
		c.PrefixCacheLimit/consts.MB, memLimit, consts.DefaultPrefixCacheReservedSize)
	return cacheSize, nil
}

// setupPrefixCacheSize configures the prefix cache size based on available memory.
func (c *Config) setupPrefixCacheSize() error {
	cacheSize, err := c.getPrefixCacheLimit()
	if err != nil {
		return err
	}
	if c.SchedulePolicy != consts.SchedulePolicyPDSplit {
		c.PrefixCacheLimit = cacheSize
	} else {
		if c.PrefillPolicy == consts.SchedulePolicyPrefixCache && c.DecodePolicy == consts.SchedulePolicyPrefixCache {
			c.PrefixCacheLimit = c.PrefixCacheLimit / 2
			klog.Infof("Prefix cache size divided by 2, as both prefill and decode use prefix cache: %d MB", c.PrefixCacheLimit/consts.MB)
		}
		c.PrefixCacheLimit = consts.DefaultPrefixCacheMaxSize
	}
	return nil
}

// createConfigManager initializes the dynamic property manager for runtime config updates.
func (c *Config) createConfigManager() error {
	prefetchKeys := []prop.PrefetchKey{
		{Key: "llm_gateway.traffic_mirror.enable", Type: prop.BoolType},
		{Key: "llm_gateway.traffic_mirror.target", Type: prop.StringType},
		{Key: "llm_gateway.traffic_mirror.ratio", Type: prop.FloatType},
		{Key: "llm_gateway.traffic_mirror.token", Type: prop.StringType},
		{Key: "llm_gateway.traffic_mirror.timeout", Type: prop.FloatType},
		{Key: "llm_gateway.traffic_mirror.enable_log", Type: prop.BoolType},
	}
	c.configManager = prop.NewConfigManager([]string{consts.PropertyFile}, prefetchKeys)
	return nil
}

// updateDiscoveryEndpoint overrides the discovery endpoint from the DISCOVERY_ENDPOINT environment variable.
func (c *Config) updateDiscoveryEndpoint() error {
	discoveryEndpoint := os.Getenv("DISCOVERY_ENDPOINT")
	if len(discoveryEndpoint) > 0 {
		c.DiscoveryEndpoint = discoveryEndpoint
	}
	return nil
}

// Complete performs post-initialization configuration setup in a well-defined sequence.
// This function MUST be called after flag parsing and before the application starts.
func (c *Config) Complete(flags *pflag.FlagSet) error {
	// Load configuration from property files
	c.loadCfgFromProperties()

	// Initialize dynamic property manager for runtime config updates
	if err := c.createConfigManager(); err != nil {
		return fmt.Errorf("failed to create config manager: %w", err)
	}

	// Override discovery endpoint from environment variable
	if err := c.updateDiscoveryEndpoint(); err != nil {
		return fmt.Errorf("failed to update discovery endpoint: %w", err)
	}

	// Calculate prefix cache size based on system memory
	if err := c.setupPrefixCacheSize(); err != nil {
		return fmt.Errorf("failed to setup prefix cache size: %w", err)
	}

	// Parse llumnix-extra-args to override flag values (highest priority)
	if err := c.ParseLlumnixExtraArgs(flags); err != nil {
		return fmt.Errorf("failed to parse llumnix extra args: %w", err)
	}

	// Print comprehensive configuration summary for debugging and auditing
	c.printConfigSummary()

	return nil
}

// printConfigSummary logs all configuration values for debugging and auditing purposes.
// Configuration is grouped into logical sections for better readability:
// - Basic service settings (host, port, service names)
// - Network and connection settings (retry, timeout, queue)
// - Scheduling policies and algorithms
// - Tokenizer and processor configuration
// - Prefix cache settings
// - Redis and batch processing configuration
// - Llumnix full-mode scheduling configuration
// - Feature flags and operational modes
func (c *Config) printConfigSummary() {
	// Helper function to log values (only skips empty strings)
	logIfNotEmpty := func(format string, value interface{}) {
		switch v := value.(type) {
		case string:
			if v != "" {
				klog.Infof(format, v)
			}
		default:
			klog.Infof(format, v) // Always print numbers and booleans
		}
	}

	klog.Infof("========== Configuration Summary ==========")

	// Basic service settings
	klog.Infof("[Service]")
	klog.Infof("  listen: %s:%d", c.Host, c.Port)
	logIfNotEmpty("  service: %s", c.Service)
	logIfNotEmpty("  llm-gateway: %s", c.LlmGateway)
	logIfNotEmpty("  llm-scheduler: %s", c.LlmScheduler)
	logIfNotEmpty("  backend-service: %s", c.BackendService)
	logIfNotEmpty("  redis: %s", c.Redis)

	// Discovery and test settings
	klog.Infof("[Discovery]")
	logIfNotEmpty("  use-discovery: %s", c.UseDiscovery)
	logIfNotEmpty("  discovery-endpoint: %s", c.DiscoveryEndpoint)
	logIfNotEmpty("  local-test-ips: %s", c.LocalTestIPs)
	logIfNotEmpty("  local-test-scheduler-ip: %s", c.LocalTestSchedulerIP)

	// Network and connection settings
	klog.Infof("[Network]")
	logIfNotEmpty("  retry-count: %d", c.RetryCount)
	logIfNotEmpty("  wait-queue-threads: %d", c.WaitQueueThreads)
	logIfNotEmpty("  wait-schedule-timeout: %dms", c.WaitScheduleTimeout)
	logIfNotEmpty("  wait-schedule-try-period: %dms", c.WaitScheduleTryPeriod)
	logIfNotEmpty("  max-queue-size: %d", c.MaxQueueSize)

	// Scheduling policies
	klog.Infof("[Scheduling]")
	logIfNotEmpty("  schedule-policy: %s", c.SchedulePolicy)
	logIfNotEmpty("  pd-split-mode: %s", c.PDSplitMode)
	logIfNotEmpty("  separate-pd-schedule: %v", c.SeparatePDSchedule)
	logIfNotEmpty("  infer-backend: %s", c.InferBackend)

	// Legacy limit settings (TODO: remove)
	if c.LimitRequests > 0 || c.PrefillLimitRequests > 0 || c.DecodeLimitRequests > 0 {
		klog.Infof("[Legacy Limits - Requests]")
		logIfNotEmpty("  limit-requests: %d", c.LimitRequests)
		logIfNotEmpty("  prefill-limit-requests: %d", c.PrefillLimitRequests)
		logIfNotEmpty("  decode-limit-requests: %d", c.DecodeLimitRequests)
	}
	if c.LimitTokens > 0 || c.PrefillLimitTokens > 0 || c.DecodeLimitTokens > 0 {
		klog.Infof("[Legacy Limits - Tokens]")
		logIfNotEmpty("  limit-tokens: %d", c.LimitTokens)
		logIfNotEmpty("  prefill-limit-tokens: %d", c.PrefillLimitTokens)
		logIfNotEmpty("  decode-limit-tokens: %d", c.DecodeLimitTokens)
		logIfNotEmpty("  limit-tokens-threshold-scale: %.3f", c.LimitTokensThresholdScale)
	}
	logIfNotEmpty("  prompt-length-threshold: %d", c.PromptLengthThreshold)

	// Tokenizer and processor
	klog.Infof("[Tokenizer & Processor]")
	logIfNotEmpty("  tokenizer-name: %s", c.TokenizerName)
	logIfNotEmpty("  tokenizer-path: %s", c.TokenizerPath)
	logIfNotEmpty("  tokenizer-mode: %s", c.TokenizerMode)
	logIfNotEmpty("  chat-template: %s", c.ChatTemplatePath)
	logIfNotEmpty("  tool-call-parser: %s", c.ToolCallParser)
	logIfNotEmpty("  reasoning-parser: %s", c.ReasoningParser)
	logIfNotEmpty("  processor-type: %s", c.ProcessorType)

	// Prefix cache
	klog.Infof("[Prefix Cache]")
	logIfNotEmpty("  prefix-cache-ttl: %ds", c.PrefixCacheTTL)
	logIfNotEmpty("  prefix-cache-limit: %d MB", c.PrefixCacheLimit/consts.MB)
	logIfNotEmpty("  prefix-cache-load-tolerance: %.2f", c.PrefixCacheLoadTolerance)
	logIfNotEmpty("  requests-threshold: %d", c.RequestsThreshold)

	// Redis and batch processing
	if c.RedisAddrs != "" || c.BatchOSSPath != "" {
		klog.Infof("[Redis & Batch]")
		logIfNotEmpty("  redis-addrs: %s", c.RedisAddrs)
		logIfNotEmpty("  redis-username: %s", c.RedisUsername)
		logIfNotEmpty("  redis-retry-times: %d", c.RedisRetryTimes)
		logIfNotEmpty("  redis-discovery-status-ttl: %dms", c.RedisDiscoveryStatusTTLMs)
		logIfNotEmpty("  redis-discovery-refresh-interval: %dms", c.RedisDiscoveryRefreshIntervalMs)
		logIfNotEmpty("  batch-oss-path: %s", c.BatchOSSPath)
		logIfNotEmpty("  batch-oss-endpoint: %s", c.BatchOSSEndpoint)
		logIfNotEmpty("  batch-parallel: %d", c.BatchParallel)
		logIfNotEmpty("  batch-lines-per-shard: %d", c.BatchLinesPerShard)
		logIfNotEmpty("  batch-request-timeout: %s", c.BatchRequestTimeout)
		logIfNotEmpty("  batch-request-retry-times: %d", c.BatchRequestRetryTimes)
	}

	// Route policy
	if c.RoutePolicy != "" {
		klog.Infof("[Routing]")
		logIfNotEmpty("  route-policy: %s", c.RoutePolicy)
		logIfNotEmpty("  route-config: %s", c.RouteConfigRaw)
	}

	// Llumnix full-mode scheduling
	if c.LlumnixConfig.EnableFullModeScheduling {
		klog.Infof("[Llumnix Full-Mode Scheduling]")
		klog.Infof("  enabled: %v", c.LlumnixConfig.EnableFullModeScheduling)

		// CMS settings
		klog.Infof("  [CMS]")
		logIfNotEmpty("    redis-host: %s", c.LlumnixConfig.CmsRedisHost)
		logIfNotEmpty("    redis-port: %s", c.LlumnixConfig.CmsRedisPort)
		logIfNotEmpty("    redis-username: %s", c.LlumnixConfig.CmsRedisUsername)
		logIfNotEmpty("    socket-timeout: %.1fs", c.LlumnixConfig.CmsRedisSocketTimeout)
		logIfNotEmpty("    retry-times: %d", c.LlumnixConfig.CmsRedisRetryTimes)
		logIfNotEmpty("    pull-status-interval: %dms", c.LlumnixConfig.CmsPullStatusIntervalMs)
		logIfNotEmpty("    pull-metadata-interval: %dms", c.LlumnixConfig.CmsPullMetadataIntervalMs)
		logIfNotEmpty("    record-metrics-interval: %dms", c.LlumnixConfig.CmsRecordMetricsInterval)

		// KVS MetaService
		if c.LlumnixConfig.EnableCacheAwareScheduling {
			klog.Infof("  [KVS MetaService]")
			klog.Infof("    cache-aware-scheduling: %v", c.LlumnixConfig.EnableCacheAwareScheduling)
			logIfNotEmpty("    config-path: %s", c.LlumnixConfig.KvsMetaServiceConfigPath)
			logIfNotEmpty("    chunk-size: %d", c.LlumnixConfig.KvsChunkSize)
			logIfNotEmpty("    enable-save-unfull-chunk: %v", c.LlumnixConfig.KvsEnableSaveUnfullChunk)
			logIfNotEmpty("    iris-meta-prefix: %s", c.LlumnixConfig.KvsIrisMetaPrefix)
			logIfNotEmpty("    vllm-block-prefix: %s", c.LlumnixConfig.KvsVLLMBlockPrefix)
			logIfNotEmpty("    retry-times: %d", c.LlumnixConfig.KvsRetryTimes)
			logIfNotEmpty("    retry-interval: %dms", c.LlumnixConfig.KvsRetryIntervalMs)
			logIfNotEmpty("    meta-service-down-duration: %ds", c.LlumnixConfig.KvsMetaServiceDownDurationS)
			logIfNotEmpty("    redis-cluster-hosts: %s", c.LlumnixConfig.KvsMetaServiceRedisClusterHosts)
		}

		// Dispatch settings
		klog.Infof("  [Dispatch]")
		logIfNotEmpty("    top-k: %d", c.LlumnixConfig.DispatchTopK)
		logIfNotEmpty("    neutral-load-metric: %s", c.LlumnixConfig.DispatchNeutralLoadMetric)
		logIfNotEmpty("    neutral-load-threshold: %.1f", c.LlumnixConfig.DispatchNeutralLoadThreshold)
		logIfNotEmpty("    prefill-load-metric: %s", c.LlumnixConfig.DispatchPrefillLoadMetric)
		logIfNotEmpty("    prefill-load-threshold: %.1f", c.LlumnixConfig.DispatchPrefillLoadThreshold)
		logIfNotEmpty("    decode-load-metric: %s", c.LlumnixConfig.DispatchDecodeLoadMetric)
		logIfNotEmpty("    decode-load-threshold: %.2f", c.LlumnixConfig.DispatchDecodeLoadThreshold)
		logIfNotEmpty("    prefill-cache-locality-metric: %s", c.LlumnixConfig.DispatchPrefillCacheLocalityMetric)
		logIfNotEmpty("    kv-cache-block-size: %d", c.LlumnixConfig.KvCacheBlockSize)
		logIfNotEmpty("    enable-instance-status-local-account: %v", c.LlumnixConfig.EnableInstanceStatusLocalAccount)
		logIfNotEmpty("    request-local-account-staleness: %ds", c.LlumnixConfig.RequestLocalAccountStalenessSeconds)
		logIfNotEmpty("    allow-concurrent-schedule: %v", c.LlumnixConfig.AllowConcurrentSchedule)
		logIfNotEmpty("    enable-predictor-enhanced-scheduling: %v", c.LlumnixConfig.EnablePredictorEnhancedScheduling)
		logIfNotEmpty("    max-num-batched-tokens: %d", c.LlumnixConfig.MaxNumBatchedTokens)
		logIfNotEmpty("    num-predictor-warmup-samples: %d", c.LlumnixConfig.NumPredictorWarmupSamples)

		// Adaptive PD
		if c.LlumnixConfig.EnableAdaptivePD {
			klog.Infof("  [Adaptive PD]")
			klog.Infof("    enabled: %v", c.LlumnixConfig.EnableAdaptivePD)
			logIfNotEmpty("    prefill-as-decode-load-metric: %s", c.LlumnixConfig.DispatchPrefillAsDecodeLoadMetric)
			logIfNotEmpty("    prefill-as-decode-load-threshold: %.1f", c.LlumnixConfig.DispatchPrefillAsDecodeLoadThreshold)
			logIfNotEmpty("    decode-as-prefill-load-metric: %s", c.LlumnixConfig.DispatchDecodeAsPrefillLoadMetric)
			logIfNotEmpty("    decode-as-prefill-load-threshold: %.2f", c.LlumnixConfig.DispatchDecodeAsPrefillLoadThreshold)
			logIfNotEmpty("    decode-compute-bound-batch-size: %d", c.LlumnixConfig.DecodeComputeBoundBatchSize)
		}

		// Failover settings
		klog.Infof("  [Failover]")
		logIfNotEmpty("    scope: %s", c.LlumnixConfig.FailoverScope)
		logIfNotEmpty("    instance-staleness-seconds: %d", c.LlumnixConfig.InstanceStalenessSeconds)

		// Rescheduling
		if c.LlumnixConfig.EnableRescheduling {
			klog.Infof("  [Rescheduling]")
			klog.Infof("    enabled: %v", c.LlumnixConfig.EnableRescheduling)
			logIfNotEmpty("    policies: %s", c.LlumnixConfig.ReschedulePolicies)
			logIfNotEmpty("    interval: %dms", c.LlumnixConfig.RescheduleIntervalMs)
			logIfNotEmpty("    decode-load-metric: %s", c.LlumnixConfig.RescheduleDecodeLoadMetric)
			logIfNotEmpty("    decode-load-threshold: %.2f", c.LlumnixConfig.RescheduleDecodeLoadThreshold)
			logIfNotEmpty("    prefill-load-metric: %s", c.LlumnixConfig.ReschedulePrefillLoadMetric)
			logIfNotEmpty("    neutral-load-metric: %s", c.LlumnixConfig.RescheduleNeutralLoadMetric)
			logIfNotEmpty("    neutral-load-threshold: %.2f", c.LlumnixConfig.RescheduleNeutralLoadThreshold)
			logIfNotEmpty("    req-select-order: %s", c.LlumnixConfig.RescheduleReqSelectOrder)
			logIfNotEmpty("    req-select-rule: %s", c.LlumnixConfig.RescheduleReqSelectRule)
			logIfNotEmpty("    req-select-value: %.2f", c.LlumnixConfig.RescheduleReqSelectValue)
			logIfNotEmpty("    load-balance-threshold: %.3f", c.LlumnixConfig.RescheduleLoadBalanceThreshold)
			logIfNotEmpty("    load-balance-scope: %s", c.LlumnixConfig.RescheduleLoadBalanceScope)
		}

		// Llumlet
		klog.Infof("  [Llumlet]")
		logIfNotEmpty("    grpc-connection-pool-size: %d", c.LlumnixConfig.LlumletGrpcConnectionPoolSize)
		logIfNotEmpty("    grpc-timeout-seconds: %d", c.LlumnixConfig.LlumletGrpcTimeoutSeconds)

		// Metrics
		logIfNotEmpty("  enable-metrics: %v", c.LlumnixConfig.EnableMetrics)
		logIfNotEmpty("  extra-args: %s", c.LlumnixConfig.ExtraArgs)
	}

	// Feature flags and operational modes
	klog.Infof("[Features & Modes]")
	logIfNotEmpty("  schedule-mode: %v", c.ScheduleMode)
	logIfNotEmpty("  colocated-reschedule-mode: %v", c.ColocatedRescheduleMode)
	logIfNotEmpty("  standalone-reschedule-mode: %v", c.StandaloneRescheduleMode)
	logIfNotEmpty("  serverless-mode: %v", c.ServerlessMode)
	logIfNotEmpty("  enable-access-log: %v", c.EnableAccessLog)
	logIfNotEmpty("  enable-log-input: %v", c.EnableLogInput)
	logIfNotEmpty("  enable-pprof: %v", c.EnablePprof)
	logIfNotEmpty("  requests-report-duration: %ds", c.RequestsReporterDuration)

	klog.Infof("===========================================")
}

type LlmSchedulerProperty struct {
	Name           *string `json:"name"`
	Group          *string `json:"group"`
	SchedulePolicy *string `json:"schedule_policy"`
}

type LlmGatewayProperty struct {
	Name        *string `json:"name"`
	Group       *string `json:"group"`
	ServiceName *string `json:"service_name"`

	WaitScheduleTimeout   *int  `json:"wait_schedule_timeout"`
	WaitScheduleTryPeriod *int  `json:"wait_schedule_try_period"`
	MaxQueueSize          *int  `json:"max_queue_size"`
	RetryCount            *int  `json:"retry_count"`
	ServerlessMode        *bool `json:"serverless_mode"`

	InferBackend *string `json:"infer_backend"`
}

type RedisServiceProperty struct {
	Name  *string `json:"name"`
	Group *string `json:"group"`
}

type RPCProperty struct {
	ServiceToken *string `json:"token"`
}

type Properties struct {
	LlmGateway   LlmGatewayProperty    `json:"llm_gateway"`
	LlmScheduler LlmSchedulerProperty  `json:"llm_scheduler"`
	RedisService *RedisServiceProperty `json:"redisservice"`
	RPC          RPCProperty           `json:"rpc"`
}

var prePropertyByte []byte

func (c *Config) loadCfgFromProperties() {
	var err error
	prePropertyByte, err = os.ReadFile(consts.PropertyFile)
	if err != nil {
		klog.Warningf("load properties(%s) failed: %v", consts.PropertyFile, err)
		return
	}
	var property Properties
	err = json.Unmarshal(prePropertyByte, &property)
	if err != nil {
		klog.Warningf("load properties, un marshal(%s) failed: %v", string(prePropertyByte), err)
		return
	}

	if property.LlmScheduler.Name != nil && property.LlmScheduler.Group != nil {
		c.LlmScheduler = fmt.Sprintf("%s.%s", *property.LlmScheduler.Group, *property.LlmScheduler.Name)
	}
	if property.LlmScheduler.SchedulePolicy != nil {
		c.SchedulePolicy = *property.LlmScheduler.SchedulePolicy
	}
	if property.LlmGateway.ServiceName != nil && property.LlmGateway.Group != nil {
		c.Service = fmt.Sprintf("%s.%s", *property.LlmGateway.Group, *property.LlmGateway.ServiceName)
	}
	if property.LlmGateway.Name != nil && property.LlmGateway.Group != nil {
		c.LlmGateway = fmt.Sprintf("%s.%s", *property.LlmGateway.Group, *property.LlmGateway.Name)
	}
	if property.LlmGateway.RetryCount != nil {
		c.RetryCount = *property.LlmGateway.RetryCount
	}
	if property.LlmGateway.MaxQueueSize != nil {
		c.MaxQueueSize = *property.LlmGateway.MaxQueueSize
	}
	if property.LlmGateway.WaitScheduleTimeout != nil {
		c.WaitScheduleTimeout = *property.LlmGateway.WaitScheduleTimeout
	}
	if property.LlmGateway.WaitScheduleTryPeriod != nil {
		c.WaitScheduleTryPeriod = *property.LlmGateway.WaitScheduleTryPeriod
	}
	if property.LlmGateway.InferBackend != nil {
		c.InferBackend = *property.LlmGateway.InferBackend
	}
	if property.LlmGateway.ServerlessMode != nil {
		c.ServerlessMode = *property.LlmGateway.ServerlessMode
	}
	if property.RedisService != nil && property.RedisService.Name != nil && property.RedisService.Group != nil {
		c.Redis = fmt.Sprintf("%s.%s", *property.RedisService.Group, *property.RedisService.Name)
	}
	if property.RPC.ServiceToken != nil {
		c.ServiceToken = *property.RPC.ServiceToken
	}
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
func (c *Config) ParseLlumnixExtraArgs(flags *pflag.FlagSet) error {
	if c.LlumnixConfig.ExtraArgs == "" {
		return nil
	}

	klog.Infof("Parsing llumnix-extra-args: %s", c.LlumnixConfig.ExtraArgs)

	// Use safeSplitArgs to safely split by comma, handling quotes, brackets and arrays
	args := safeSplitArgs(c.LlumnixConfig.ExtraArgs)

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

	return nil
}
