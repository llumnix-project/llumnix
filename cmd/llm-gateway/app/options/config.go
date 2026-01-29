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
	// request state report (report to llm scheduler) interval (seconds)
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
	SchedulerConfig

	BatchServiceConfig
}

type DiscoveryConfig struct {
	// instead of relying on the active registration of inference instances, the
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

type SchedulerConfig struct {
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
	flags.StringVar(&c.SchedulePolicy, "schedule-policy", "load-balance", "schedule policy, now support round-robin, load-balance, flood")

	flags.StringVar(&c.PDSplitMode, "pdsplit-mode", "", "pd split mode, this configuration only takes effect under the pd-split policy, now support vllm-vineyard, vllm-kvt, sglang-mooncake")

	flags.BoolVar(&c.EnableAccessLog, "enable-access-log", true, "enable access log or not")
	flags.BoolVar(&c.EnableLogInput, "enable-log-input", false, "enable log input or not")
	flags.BoolVar(&c.ServerlessMode, "serverless-mode", false, "run on serverless")

	flags.IntVar(&c.WaitQueueThreads, "wait-queue-threads", 5, "number of coroutines which read the requests from queue")
	flags.IntVar(&c.WaitScheduleTimeout, "wait-schedule-timeout", 10000, "waiting timeout if no free endpoint, unit(milliseconds)")
	flags.IntVar(&c.WaitScheduleRetryInterval, "wait-schedule-try-period", 1000, "retry period while waiting free tokens, unit(milliseconds)")

	flags.IntVar(&c.MaxQueueSize, "max-queue-size", 512, "max buffer queue size")

	flags.StringVar(&c.UseDiscovery, "use-discovery", "", "the scheduler use redis to discovery backend services.")

	flags.StringVar(&c.TokenizerName, "tokenizer-name", "", "builtin tokenizer name")
	flags.StringVar(&c.TokenizerPath, "tokenizer-path", "", "builtin tokenizer path")
	flags.StringVar(&c.ChatTemplatePath, "chat-template", "", "chat template path")
	flags.StringVar(&c.ToolCallParser, "tool-call-parser", "", "tool call parser type")
	flags.StringVar(&c.ReasoningParser, "reasoning-parser", "", "reasoning parser type")

	flags.IntVar(&c.RequestStateReportInterval, "request-state-report-interval", 0, "Specify request state report (to llm-scheduler) interval (seconds)")

	flags.BoolVar(&c.SeparatePDSchedule, "separate-pd-schedule", false, "Specify whether to separate pd schedule")

	flags.BoolVar(&c.SchedulerConfig.EnableFullModeScheduling, "enable-full-mode-scheduling", consts.DefaultEnableFullModeScheduling, "Enable full mode scheduling")

	flags.StringVar(&c.SchedulerConfig.CmsRedisHost, "cms-redis-host", consts.DefaultCmsRedisHost, "CMS redis host")
	flags.StringVar(&c.SchedulerConfig.CmsRedisPort, "cms-redis-port", consts.DefaultCmsRedisPort, "CMS redis port")
	flags.StringVar(&c.SchedulerConfig.CmsRedisUsername, "cms-redis-username", consts.DefaultCmsRedisUsername, "CMS redis username")
	flags.StringVar(&c.SchedulerConfig.CmsRedisPassword, "cms-redis-password", consts.DefaultCmsRedisPassword, "CMS redis password")
	flags.Float64Var(&c.SchedulerConfig.CmsRedisSocketTimeout, "cms-redis-timeout", consts.DefaultCmsRedisSocketTimeout, "CMS redis socket timeout")
	flags.IntVar(&c.SchedulerConfig.CmsRedisRetryTimes, "cms-redis-retry-times", consts.DefaultCmsRedisRetryTimes, "CMS redis retry times")
	flags.Int32Var(&c.SchedulerConfig.CmsPullStatusIntervalMs, "cms-pull-status-interval-ms", consts.DefaultCmsPullStatusIntervalMs, "CMS pull status interval in milliseconds")
	flags.Int32Var(&c.SchedulerConfig.CmsPullMetadataIntervalMs, "cms-pull-metadata-interval-ms", consts.DefaultCmsPullMetadataIntervalMs, "CMS pull metadata interval in milliseconds")
	flags.Int32Var(&c.SchedulerConfig.CmsRecordMetricsInterval, "cms-record-metrics-interval", consts.DefaultCmsRecordMetricsInterval, "CMS record metrics interval")

	flags.BoolVar(&c.SchedulerConfig.EnableCacheAwareScheduling, "enable-cache-aware-scheduling", consts.DefaultEnableCacheAwareScheduling, "enable cache aware scheduling")
	flags.StringVar(&c.SchedulerConfig.KvsBackend, "kvs-backend", consts.DefaultKvsBackend, "KVS backend")
	flags.StringVar(&c.SchedulerConfig.KvsMetadataServiceConfigPath, "kvs-metadata-service-config-path", consts.DefaultKvsMetadataServiceConfigPath, "KVS MetadataService config path")
	flags.IntVar(&c.SchedulerConfig.KvsChunkSize, "kvs-chunk-size", consts.DefaultKvsChunkSize, "KVS chunk size")
	flags.BoolVar(&c.SchedulerConfig.KvsEnableSaveUnfullChunk, "kvs-enable-save-unfull-chunk", consts.DefaultKvsEnableSaveUnfullChunk, "KVS enable save unfull chunk")
	flags.StringVar(&c.SchedulerConfig.KvsIrisMetaPrefix, "kvs-iris-meta-prefix", consts.DefaultKvsIrisMetaPrefix, "KVS iris meta prefix")
	flags.StringVar(&c.SchedulerConfig.KvsVLLMBlockPrefix, "kvs-vllm-block-prefix", consts.DefaultKvsVLLMBlockPrefix, "KVS vllm block prefix")
	flags.IntVar(&c.SchedulerConfig.KvsRetryIntervalMs, "kvs-retry-interval-ms", consts.DefaultKvsRetryIntervalMs, "KVS retry interval in milliseconds")
	flags.IntVar(&c.SchedulerConfig.KvsRetryTimes, "kvs-retry-times", consts.DefaultKvsRetryTimes, "KVS retry times")
	flags.IntVar(&c.SchedulerConfig.KvsMetadataServiceDownDurationS, "kvs-metadata-service-down-duration-s", consts.DefaultKvsMetadataServiceDownDurationS, "KVS metadata service down duration in seconds")
	flags.StringVar(&c.SchedulerConfig.KvsMetadataServiceRedisClusterHosts, "kvs-metadata-service-redis-cluster-hosts", consts.DefaultKvsMetadataServiceRedisClusterHosts, "KVS metadata service redis cluster hosts")
	flags.StringVar(&c.SchedulerConfig.KvsMetadataServiceRedisClusterPassword, "kvs-metadata-service-redis-cluster-password", consts.DefaultKvsMetadataServiceRedisClusterPassword, "KVS metadata service redis cluster password")
	flags.StringVar(&c.SchedulerConfig.KvsMetadataServiceHttpServerHost, "kvs-metadata-service-http-server-host", consts.DefaultKvsMetadataServiceHttpServerHost, "KVS metadata service http server host")
	flags.StringVar(&c.SchedulerConfig.KvsMetadataServiceHttpServerPort, "kvs-metadata-service-http-server-port", consts.DefaultKvsMetadataServiceHttpServerPort, "KVS metadata service http server port")

	flags.IntVar(&c.SchedulerConfig.DispatchTopK, "dispatch-top-k", consts.DefaultDispatchTopK, "dispatch top K")
	flags.StringVar(&c.SchedulerConfig.DispatchNeutralLoadMetric, "dispatch-neutral-load-metric", consts.DefaultDispatchNeutralLoadMetric, "dispatch neutral load metric")
	flags.Float32Var(&c.SchedulerConfig.DispatchNeutralLoadThreshold, "dispatch-neutral-load-threshold", consts.DefaultDispatchNeutralLoadThreshold, "dispatch neutral load threshold")
	flags.StringVar(&c.SchedulerConfig.DispatchPrefillLoadMetric, "dispatch-prefill-load-metric", consts.DefaultDispatchPrefillLoadMetric, "dispatch prefill load metric")
	flags.Float32Var(&c.SchedulerConfig.DispatchPrefillLoadThreshold, "dispatch-prefill-load-threshold", consts.DefaultDispatchPrefillLoadThreshold, "dispatch prefill load threshold")
	flags.StringVar(&c.SchedulerConfig.DispatchDecodeLoadMetric, "dispatch-decode-load-metric", consts.DefaultDispatchDecodeLoadMetric, "dispatch decode load metric")
	flags.Float32Var(&c.SchedulerConfig.DispatchDecodeLoadThreshold, "dispatch-decode-load-threshold", consts.DefaultDispatchDecodeLoadThreshold, "dispatch decode load threshold")
	flags.StringVar(&c.SchedulerConfig.DispatchPrefillCacheLocalityMetric, "dispatch-prefill-cache-locality-metric", consts.DefaultDispatchPrefillCacheLocalityMetric, "dispatch prefill cache locality metric")
	flags.BoolVar(&c.SchedulerConfig.EnableInstanceStatusLocalAccount, "enable-instance-status-local-account", consts.DefaultEnableInstanceStatusLocalAccount, "enable instance status local account")
	flags.Int32Var(&c.SchedulerConfig.KvCacheBlockSize, "kv-cache-block-size", consts.DefaultKvCacheBlockSize, "KV cache block size")
	flags.Int32Var(&c.SchedulerConfig.RequestLocalAccountStalenessSeconds, "request-local-account-staleness-seconds", consts.DefaultRequestLocalAccountStalenessSeconds, "request local account staleness seconds")
	flags.BoolVar(&c.SchedulerConfig.AllowConcurrentSchedule, "allow-concurrent-schedule", consts.DefaultAllowConcurrentSchedule, "allow concurrent schedule")
	flags.BoolVar(&c.SchedulerConfig.EnablePredictorEnhancedScheduling, "enable-predictor-enhanced-scheduling", consts.DefaultEnablePredictorEnhancedScheduling, "enable predictor enhanced scheduling")
	flags.IntVar(&c.SchedulerConfig.MaxNumBatchedTokens, "max-num-batched-tokens", consts.DefaultMaxNumBatchedTokens, "max num batched tokens")
	flags.IntVar(&c.SchedulerConfig.NumPredictorWarmupSamples, "num-predictor-warmup-samples", consts.DefaultNumPredictorWarmupSamples, "num predictor warmup samples")

	flags.BoolVar(&c.SchedulerConfig.EnableAdaptivePD, "enable-adaptive-pd", consts.DefaultEnableAdaptivePD, "enable adaptive pd")
	flags.StringVar(&c.SchedulerConfig.DispatchPrefillAsDecodeLoadMetric, "dispatch-prefill-as-decode-load-metric", consts.DefaultDispatchPrefillAsDecodeLoadMetric, "dispatch prefill as decode load metric")
	flags.Float32Var(&c.SchedulerConfig.DispatchPrefillAsDecodeLoadThreshold, "dispatch-prefill-as-decode-load-threshold", consts.DefaultDispatchPrefillAsDecodeLoadThreshold, "dispatch prefill as decode load threshold")
	flags.StringVar(&c.SchedulerConfig.DispatchDecodeAsPrefillLoadMetric, "dispatch-decode-as-prefill-load-metric", consts.DefaultDispatchDecodeAsPrefillLoadMetric, "dispatch decode as prefill load metric")
	flags.Float32Var(&c.SchedulerConfig.DispatchDecodeAsPrefillLoadThreshold, "dispatch-decode-as-prefill-load-threshold", consts.DefaultDispatchDecodeAsPrefillLoadThreshold, "dispatch decode as prefill load threshold")
	flags.Int32Var(&c.SchedulerConfig.DecodeComputeBoundBatchSize, "decode-compute-bound-batch-size", consts.DefaultDecodeComputeBoundBatchSize, "decode compute bound batch size for adaptive decode batch size metric")

	flags.StringVar(&c.SchedulerConfig.FailoverScope, "failover-scope", consts.DefaultFailoverScope, "failover scope")
	flags.Int64Var(&c.SchedulerConfig.InstanceStalenessSeconds, "instance-staleness-seconds", consts.DefaultInstanceStalenessSeconds, "instance staleness seconds")

	flags.BoolVar(&c.SchedulerConfig.EnableRescheduling, "enable-rescheduling", consts.DefaultEnableRescheduling, "enable rescheduling")
	flags.StringVar(&c.SchedulerConfig.ReschedulePolicies, "reschedule-policies", consts.DefaultReschedulePolicies, "reschedule policies, comma separated")
	flags.Int32Var(&c.SchedulerConfig.RescheduleIntervalMs, "reschedule-interval-ms", consts.DefaultRescheduleIntervalMs, "reschedule interval milliseconds")
	flags.StringVar(&c.SchedulerConfig.RescheduleDecodeLoadMetric, "reschedule-decode-load-metric", consts.DefaultRescheduleDecodeLoadMetric, "reschedule decode load metric")
	flags.Float32Var(&c.SchedulerConfig.RescheduleDecodeLoadThreshold, "reschedule-decode-load-threshold", consts.DefaultRescheduleDecodeLoadThreshold, "reschedule decode load threshold")
	flags.StringVar(&c.SchedulerConfig.ReschedulePrefillLoadMetric, "reschedule-prefill-load-metric", consts.DefaultReschedulePrefillLoadMetric, "reschedule prefill load metric")
	flags.StringVar(&c.SchedulerConfig.RescheduleNeutralLoadMetric, "reschedule-neutral-load-metric", consts.DefaultRescheduleNeutralLoadMetric, "reschedule neutral load metric")
	flags.Float32Var(&c.SchedulerConfig.RescheduleNeutralLoadThreshold, "reschedule-neutral-load-threshold", consts.DefaultRescheduleNeutralLoadThreshold, "reschedule neutral load threshold")
	flags.IntVar(&c.SchedulerConfig.LlumletGrpcConnectionPoolSize, "llumlet-grpc-connection-pool-size", consts.DefaultLlumletGrpcConnectionPoolSize, "llumlet grpc connection pool size")
	flags.StringVar(&c.SchedulerConfig.RescheduleReqSelectOrder, "reschedule-req-select-order", consts.DefaultRescheduleReqSelectOrder, "reschedule req selection order")
	flags.StringVar(&c.SchedulerConfig.RescheduleReqSelectRule, "reschedule-req-select-rule", consts.DefaultRescheduleReqSelectRule, "reschedule req selection rule")
	flags.Float32Var(&c.SchedulerConfig.RescheduleReqSelectValue, "reschedule-req-select-value", consts.DefaultRescheduleReqSelectValue, "reschedule req selection value")
	flags.Float32Var(&c.SchedulerConfig.RescheduleLoadBalanceThreshold, "reschedule-load-balance-threshold", consts.DefaultRescheduleLoadBalanceThreshold, "reschedule load balance threshold")
	flags.StringVar(&c.SchedulerConfig.RescheduleLoadBalanceScope, "reschedule-load-balance-scope", consts.DefaultRescheduleLoadBalanceScope, "reschedule load balance scope")
	flags.IntVar(&c.SchedulerConfig.LlumletGrpcTimeoutSeconds, "llumlet-grpc-timeout-seconds", consts.DefaultLlumletGrpcTimeoutSeconds, "llumlet grpc timeout seconds")

	flags.BoolVar(&c.SchedulerConfig.EnableMetrics, "enable-metrics", consts.DefaultSchedulerEnableMetrics, "scheduler enable recording metrics")
	flags.StringVar(&c.SchedulerConfig.ExtraArgs, "extra-args", consts.DefaultSchedulerExtraArgs, "scheduler extra args")

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
	return !c.SchedulerConfig.EnableFullModeScheduling && c.RequestStateReportInterval > 0 && len(c.LocalTestIPs) == 0
}

func (c *Config) ScheduleNeedTokens() bool {
	return c.SchedulerConfig.EnableFullModeScheduling &&
		(c.SchedulerConfig.EnableCacheAwareScheduling || c.SchedulerConfig.EnableInstanceStatusLocalAccount)
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
	ServerlessMode        *bool `json:"serverless_mode"`
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

// ParseLlumnixExtraArgs parses extra-args and overrides corresponding flag values.
// Supports two formats for array values:
//   - Quoted strings: key='value1,value2' -> parsed as: key=value1,value2 (quotes removed)
//   - Bracket arrays: key=[value1,value2] -> parsed as: key=[value1,value2] (brackets kept)
//
// Example: "dispatch-top-k=5,policies=[p1,p2],timeout='10,20'" ->
//
//	"dispatch-top-k=5", "policies=[p1,p2]", "timeout=10,20"
func (c *Config) ParseLlumnixExtraArgs(flags *pflag.FlagSet) error {
	if c.SchedulerConfig.ExtraArgs == "" {
		return nil
	}

	klog.Infof("Parsing extra-args: %s", c.SchedulerConfig.ExtraArgs)

	// Use safeSplitArgs to safely split by comma, handling quotes, brackets and arrays
	args := safeSplitArgs(c.SchedulerConfig.ExtraArgs)

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
