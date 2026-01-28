package options

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/pflag"
	"k8s.io/klog/v2"

	"llumnix/pkg/llm-gateway/consts"
	prop "llumnix/pkg/llm-gateway/property"
)

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

	ServiceToken string

	// max queue size
	MaxQueueSize int
	// number of coroutines which read the requests from queue
	WaitQueueThreads int

	ScheduleMode bool
	// ColocatedRescheduleMode means whether to start a rescheduler inside a scheduler process
	// (so that they can share the cms client).
	ColocatedRescheduleMode bool
	// StandaloneRescheduleMode means whether to start a rescheduler as a separate process.
	StandaloneRescheduleMode bool

	// schedule policy
	SchedulePolicy string

	// waiting schedule timeout if no schedule result, unit is milliseconds, 0 means that drop request
	WaitScheduleTimeout int
	// retry interval of waiting schedule results, unit is milliseconds
	WaitScheduleRetryInterval int
	// request token state report (report to llm scheduler) interval (seconds)
	RequestStateReportInterval int

	// enable access log
	EnableAccessLog bool
	// enable input log
	EnableLogInput bool

	// run on serverless
	ServerlessMode bool

	DiscoveryConfig
	ProcessorConfig
	RouteConfig

	PDSplitConfig
	LlumnixConfig

	BatchServiceConfig
}

type DiscoveryConfig struct {
	// instead of relying on the active registration of inference workers, the
	// backend services are actively discovered through the scheduler, which can
	// only be used in the scenario where BackendService are set
	UseDiscovery                    string
	RedisDiscoveryRefreshIntervalMs int
	RedisDiscoveryStatusTTLMs       int
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

type RouteConfig struct {
	RoutePolicy    string
	RouteConfigRaw string
}

type PDSplitConfig struct {
	// The configuration of pd split
	// LLM Gateway currently supports a variety of separate implementations of the prefill
	// and decode phases, which can be distinguished by this configuration
	PDSplitMode string
	// separate scheduling for p and d or not
	SeparatePDSchedule bool
}

type BatchServiceConfig struct {
	BatchOSSPath           string
	BatchOSSEndpoint       string
	BatchParallel          int
	BatchLinesPerShard     int
	BatchRequestTimeout    time.Duration
	BatchRequestRetryTimes int

	RedisAddrs      string
	RedisUsername   string
	RedisPassword   string
	RedisRetryTimes int
}

type LlumnixConfig struct {
	// lite-mode scheduling: When not enabling full mode scheduling (lite-mode scheduling), llumnix does not
	// intrusively modify inference engine, and only support basic load balance scheduling.
	// In lite mode scheduling, LLM gateway collect update realtime request token states and report these data to
	// LLM scheduler periodically. LLM scheduler perform load balance scheduling based on these local realtime states,
	// supporting num-requests and num-tokens scheduling metric.
	// full-mode scheduling: When enable full mode scheduling, llumnix intrusively modifies inference engine to
	// collect accurate load information from inference engine and support kv cache migration, and therefore can
	// support advanced scheduling feature like rescheduling, adaptive pd, etc.
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
}

func (c *Config) AddServerFlags(flags *pflag.FlagSet) {
	flags.IntVar(&c.Port, "port", 8001, "http service listen port")
	flags.StringVar(&c.Host, "host", "0.0.0.0", "http service listen host")
}

func (c *Config) AddConfigFlags(flags *pflag.FlagSet) {
	flags.BoolVar(&c.EnablePprof, "enable-pprof", false, "enable pprof")

	flags.StringVar(&c.ServiceToken, "token", "", "service token")
	flags.StringVar(&c.LlmScheduler, "llm-scheduler", "", "llm scheduler service")

	flags.StringVar(&c.LocalTestIPs, "local-test-ip", "", "local test")
	flags.StringVar(&c.LocalTestSchedulerIP, "local-test-scheduler-ip", "", "local test scheduler ip")

	flags.BoolVar(&c.ScheduleMode, "schedule-mode", false, "work as a scheduler")
	flags.BoolVar(&c.ColocatedRescheduleMode, "colocated-reschedule-mode", false, "work as a scheduler with a colocated rescheduler")
	flags.BoolVar(&c.StandaloneRescheduleMode, "standalone-reschedule-mode", false, "work as a standalone rescheduler")
	flags.StringVar(&c.SchedulePolicy, "schedule-policy", "least-token", "schedule policy, now support round-robin, load-balance, flood")

	flags.StringVar(&c.PDSplitMode, "pdsplit-mode", "", "pd split mode, this configuration only takes effect under the pd-split policy, now support vllm-vineyard, vllm-kvt, sglang-mooncake")

	flags.BoolVar(&c.EnableAccessLog, "enable-access-log", true, "enable access log or not")
	flags.BoolVar(&c.EnableLogInput, "enable-log-input", false, "enable log input or not")
	flags.BoolVar(&c.ServerlessMode, "serverless-mode", false, "run on serverless")

	flags.IntVar(&c.WaitQueueThreads, "wait-queue-threads", 5, "number of coroutines which read the requests from queue")
	flags.IntVar(&c.WaitScheduleTimeout, "wait-schedule-timeout", 10000, "waiting timeout if no free token, unit(milliseconds)")
	flags.IntVar(&c.WaitScheduleRetryInterval, "wait-schedule-try-period", 1000, "retry period while waiting free tokens, unit(milliseconds)")

	flags.IntVar(&c.MaxQueueSize, "max-queue-size", 512, "max buffer queue size")

	flags.StringVar(&c.UseDiscovery, "use-discovery", "", "the scheduler use cache-server/message-bus to discovery backend services.")

	flags.StringVar(&c.TokenizerName, "tokenizer-name", "", "builtin tokenizer name")
	flags.StringVar(&c.TokenizerPath, "tokenizer-path", "", "builtin tokenizer path")
	flags.StringVar(&c.ChatTemplatePath, "chat-template", "", "chat template path")
	flags.StringVar(&c.ToolCallParser, "tool-call-parser", "", "tool call parser type")
	flags.StringVar(&c.ReasoningParser, "reasoning-parser", "", "reasoning parser type")

	flags.IntVar(&c.RequestStateReportInterval, "request-state-report-interval", 0, "Specify request state report (to llm-scheduler) interval (seconds)")

	flags.BoolVar(&c.SeparatePDSchedule, "separate-pd-schedule", false, "Specify whether to separate pd schedule")

	flags.BoolVar(&c.LlumnixConfig.EnableFullModeScheduling, "enable-full-mode-scheduling", consts.DefaultLlumnixEnableFullModeScheduling, "Enable full mode scheduling")

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
	flags.StringVar(&c.LlumnixConfig.KvsBackend, "llumnix-kvs-backend", consts.DefaultLlumnixKvsBackend, "Llumnix KVS backend")
	flags.StringVar(&c.LlumnixConfig.KvsMetadataServiceConfigPath, "llumnix-kvs-metadata-service-config-path", consts.DefaultLlumnixKvsMetadataServiceConfigPath, "Llumnix KVS MetadataService config path")
	flags.IntVar(&c.LlumnixConfig.KvsChunkSize, "llumnix-kvs-chunk-size", consts.DefaultLlumnixKvsChunkSize, "Llumnix KVS chunk size")
	flags.BoolVar(&c.LlumnixConfig.KvsEnableSaveUnfullChunk, "llumnix-kvs-enable-save-unfull-chunk", consts.DefaultLlumnixKvsEnableSaveUnfullChunk, "Llumnix KVS enable save unfull chunk")
	flags.StringVar(&c.LlumnixConfig.KvsIrisMetaPrefix, "llumnix-kvs-iris-meta-prefix", consts.DefaultLlumnixKvsIrisMetaPrefix, "Llumnix KVS iris meta prefix")
	flags.StringVar(&c.LlumnixConfig.KvsVLLMBlockPrefix, "llumnix-kvs-vllm-block-prefix", consts.DefaultLlumnixKvsVLLMBlockPrefix, "Llumnix KVS vllm block prefix")
	flags.IntVar(&c.LlumnixConfig.KvsRetryIntervalMs, "llumnix-kvs-retry-interval-ms", consts.DefaultLlumnixKvsRetryIntervalMs, "Llumnix KVS retry interval in milliseconds")
	flags.IntVar(&c.LlumnixConfig.KvsRetryTimes, "llumnix-kvs-retry-times", consts.DefaultLlumnixKvsRetryTimes, "Llumnix KVS retry times")
	flags.IntVar(&c.LlumnixConfig.KvsMetadataServiceDownDurationS, "llumnix-kvs-metadata-service-down-duration-s", consts.DefaultLlumnixKvsMetadataServiceDownDurationS, "Llumnix KVS metadata service down duration in seconds")
	flags.StringVar(&c.LlumnixConfig.KvsMetadataServiceRedisClusterHosts, "llumnix-kvs-metadata-service-redis-cluster-hosts", consts.DefaultLlumnixKvsMetadataServiceRedisClusterHosts, "Llumnix KVS metadata service redis cluster hosts")
	flags.StringVar(&c.LlumnixConfig.KvsMetadataServiceRedisClusterPassword, "llumnix-kvs-metadata-service-redis-cluster-password", consts.DefaultLlumnixKvsMetadataServiceRedisClusterPassword, "Llumnix KVS metadata service redis cluster password")
	flags.StringVar(&c.LlumnixConfig.KvsMetadataServiceHttpServerHost, "llumnix-kvs-metadata-service-http-server-host", consts.DefaultLlumnixKvsMetadataServiceHttpServerHost, "Llumnix KVS metadata service http server host")
	flags.StringVar(&c.LlumnixConfig.KvsMetadataServiceHttpServerPort, "llumnix-kvs-metadata-service-http-server-port", consts.DefaultLlumnixKvsMetadataServiceHttpServerPort, "Llumnix KVS metadata service http server port")

	flags.IntVar(&c.LlumnixConfig.DispatchTopK, "llumnix-dispatch-top-k", consts.DefaultLlumnixDispatchTopK, "Llumnix dispatch top K")
	flags.StringVar(&c.LlumnixConfig.DispatchNeutralLoadMetric, "llumnix-dispatch-neutral-load-metric", consts.DefaultLlumnixDispatchNeutralLoadMetric, "Llumnix dispatch neutral load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchNeutralLoadThreshold, "llumnix-dispatch-neutral-load-threshold", consts.DefaultLlumnixDispatchNeutralLoadThreshold, "Llumnix dispatch neutral load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchPrefillLoadMetric, "llumnix-dispatch-prefill-load-metric", consts.DefaultLlumnixDispatchPrefillLoadMetric, "Llumnix dispatch prefill load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchPrefillLoadThreshold, "llumnix-dispatch-prefill-load-threshold", consts.DefaultLlumnixDispatchPrefillLoadThreshold, "Llumnix dispatch prefill load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchDecodeLoadMetric, "llumnix-dispatch-decode-load-metric", consts.DefaultLlumnixDispatchDecodeLoadMetric, "Llumnix dispatch decode load metric")
	flags.Float32Var(&c.LlumnixConfig.DispatchDecodeLoadThreshold, "llumnix-dispatch-decode-load-threshold", consts.DefaultLlumnixDispatchDecodeLoadThreshold, "Llumnix dispatch decode load threshold")
	flags.StringVar(&c.LlumnixConfig.DispatchPrefillCacheLocalityMetric, "llumnix-dispatch-prefill-cache-locality-metric", consts.DefaultLlumnixDispatchPrefillCacheLocalityMetric, "Llumnix dispatch prefill cache locality metric")
	flags.BoolVar(&c.LlumnixConfig.EnableInstanceStatusLocalAccount, "llumnix-enable-instance-status-local-account", consts.DefaultLlumnixEnableInstanceStatusLocalAccount, "Llumnix enable instance status local account")
	flags.Int32Var(&c.LlumnixConfig.KvCacheBlockSize, "llumnix-kv-cache-block-size", consts.DefaultLlumnixKvCacheBlockSize, "Llumnix KV cache block size")
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
	flags.IntVar(&c.RedisDiscoveryRefreshIntervalMs, "redis-discovery-refresh-interval-ms", 1000, "Redis discovery refresh interval milliseconds")

	flags.IntVar(&c.BatchParallel, "batch-parallel", 8, "The parallel of shard process")
	flags.IntVar(&c.BatchLinesPerShard, "batch-lines-per-shard", 1000, "The number of lines per shard file")
	flags.DurationVar(&c.BatchRequestTimeout, "batch-request-timeout", 3*time.Minute, "HTTP request timeout duration")
	flags.IntVar(&c.BatchRequestRetryTimes, "batch-request-retry-times", 3, "HTTP retry times")
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

func (c *Config) GetModelName(origName string) string {
	if c.ServerlessMode {
		return origName
	} else {
		return ""
	}
}

func (c *Config) EnableRequestStateTracking() bool {
	return !c.LlumnixConfig.EnableFullModeScheduling && c.RequestStateReportInterval > 0 && len(c.LocalTestIPs) == 0
}

func (c *Config) ScheduleNeedTokens() bool {
	return c.LlumnixConfig.EnableFullModeScheduling &&
		(c.LlumnixConfig.EnableCacheAwareScheduling || c.LlumnixConfig.EnableInstanceStatusLocalAccount)
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

func (c *Config) LoadCfgFromProperties() {
	const propertyFile = "/etc/override.properties"
	var err error
	prePropertyByte, err = os.ReadFile(propertyFile)
	if err != nil {
		klog.Warningf("load properties(%s) failed: %v", propertyFile, err)
		return
	}
	var property Properties
	err = json.Unmarshal(prePropertyByte, &property)
	if err != nil {
		klog.Warningf("load properties, un marshal(%s) failed: %v", string(prePropertyByte), err)
		return
	}

	prefetchKeys := []prop.PrefetchKey{
		{Key: "llm_gateway.traffic_mirror.enable", Type: prop.BoolType},
		{Key: "llm_gateway.traffic_mirror.target", Type: prop.StringType},
		{Key: "llm_gateway.traffic_mirror.ratio", Type: prop.FloatType},
		{Key: "llm_gateway.traffic_mirror.token", Type: prop.StringType},
		{Key: "llm_gateway.traffic_mirror.timeout", Type: prop.FloatType},
		{Key: "llm_gateway.traffic_mirror.enable_log", Type: prop.BoolType},
	}
	c.configManager = prop.NewConfigManager([]string{propertyFile}, prefetchKeys)

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
	if property.LlmGateway.MaxQueueSize != nil {
		c.MaxQueueSize = *property.LlmGateway.MaxQueueSize
	}
	if property.LlmGateway.WaitScheduleTimeout != nil {
		c.WaitScheduleTimeout = *property.LlmGateway.WaitScheduleTimeout
	}
	if property.LlmGateway.WaitScheduleTryPeriod != nil {
		c.WaitScheduleRetryInterval = *property.LlmGateway.WaitScheduleTryPeriod
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

	// Basic service settings
	klog.Infof("service listen host and port: %s:%d", c.Host, c.Port)
	logIfNotEmpty("service: %s", c.Service)
	logIfNotEmpty("llm gateway: %s", c.LlmGateway)
	logIfNotEmpty("llm scheduler: %s", c.LlmScheduler)
	logIfNotEmpty("use discovery: %s", c.UseDiscovery)
	logIfNotEmpty("local test ips: %s", c.LocalTestIPs)
	logIfNotEmpty("builtin tokenizer: %s", c.TokenizerName)
	logIfNotEmpty("tokenizer path: %s", c.TokenizerPath)

	// Network/connection settings
	logIfNotEmpty("max queue size: %d", c.MaxQueueSize)
	logIfNotEmpty("wait queue threads: %d", c.WaitQueueThreads)
	logIfNotEmpty("wait schedule timeout: %dms", c.WaitScheduleTimeout)
	logIfNotEmpty("wait schedule try period: %dms", c.WaitScheduleRetryInterval)

	// Scheduling policies
	logIfNotEmpty("schedule policy: %s", c.SchedulePolicy)
	logIfNotEmpty("pd split mode: %+v", c.PDSplitMode)
	logIfNotEmpty("request report interval: %d", c.RequestStateReportInterval)

	// Feature flags
	logIfNotEmpty("llm serverless mode: %v", c.ServerlessMode)
	logIfNotEmpty("schedule mode: %v", c.ScheduleMode)
	logIfNotEmpty("enable access log: %v", c.EnableAccessLog)
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
