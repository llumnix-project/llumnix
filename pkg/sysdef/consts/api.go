package consts

import (
	"time"
)

// Options:
// options starts with option identifier "?", for instance "?hrb".
// We have these options for now:
//   h: Hide the key when config exported. k: Keep the original key when it is not
//   primary. For example, user submitted
//      config with key "token" which has primary key "credential.token" defined below,
//      as the effect of "?k", "credential.token" will sustain and original "token"
//      will not be removed.

//go:generate go run ../../../cmd/service-spec-generator/generator.go

const (
	// The action keys can not be seen by users, only for internal usage
	ServiceKeyAction = "action,?h"
	// +type=int
	ServiceKeyActionVersion = "action.version,?h"
	// +type=string
	ServiceKeyActionUpdateType = "action.update_type,?h"
	// ServiceKeyActionUpdateMemberServiceType is used to define the sub service type to be updated in free-band service.
	// +type=string
	ServiceKeyActionTaskType = "action.task_type,?h"

	// Credential...
	// +type=string
	ServiceKeyDockerAuth = "credential.docker_auth,dockerAuth"
	// +type=string
	ServiceKeyToken = "credential.token,token,?h"
	// +type=string
	ServiceKeyPrivateToken = "credential.private_token,private_token,?h"
	// +type=bool
	ServiceKeyGenerateToken = "credential.generate_token,generate_token"
	// +type=string
	ServiceKeyOSSCred = "credential.oss,osscredentials"
	// +type=string
	ServiceKeyRoleArn = "credential.role_arn,role_arn"

	// Allspark rpc...
	// +type=bool
	ServiceKeyBatching = "rpc.batching"
	// +type=int
	ServiceKeyBatchSize = "rpc.batching.max_batch_size,metadata.batching.max_batch_size"
	// +type=int
	ServiceKeyBatchTimeout = "rpc.batching.max_batch_timeout,metadata.batching.max_batch_timeout"
	// +type=easgo/pkg/sysdef/models@ref.Mirror
	ServiceKeyMirror = "rpc.mirror,metadata.rpc.mirror"
	// +type=int
	ServiceKeyIoThread = "rpc.io_threads,metadata.io_threads"
	// +type=int
	ServiceKeyMaxQueueSize = "rpc.max_queue_size,metadata.max_queue_size"
	// +type=int
	ServiceKeyKeepalive = "rpc.keepalive,metadata.rpc.keepalive"
	// +type=int
	ServiceKeyRateLimit = "rpc.rate_limit,metadata.rpc.rate_limit"
	// +type=int
	ServiceKeyWorkers = "rpc.worker_threads,metadata.worker_threads,metadata.workers"
	// +type=bool
	ServiceKeyEnableHangDetect = "rpc.enable_service_hang_detect,metadata.rpc.enable_service_hang_detect"
	// +type=int
	ServiceKeyServiceHangDetectPeriod = "rpc.service_hang_detect_period,metadata.rpc.service_hang_detect_period"
	// +type=string
	ServiceKeyDecompressor = "rpc.decompressor,metadata.rpc.decompressor"
	// +type=bool
	ServiceKeyEnableSIGTERM = "rpc.enable_sigterm,metadata.rpc.enable_sigterm"
	// +type=bool
	ServiceKeyDisableAutoQueueWatch = "rpc.disable_auto_queue_watch"
	// +type=string
	ServiceKeyHealthCheckPath = "rpc.health_check_path,metadata.rpc.health_check_path"
	// +type=int
	ServiceKeyHealthCheckPort = "rpc.health_check_port,metadata.rpc.health_check_port"
	// +type=bool
	ServiceKeyRetries = "rpc.retries,metadata.rpc.retries"

	// eas ...
	// +type=bool
	ServiceKeyDisableFailureHandler = "eas.handlers.disable_failure_handler,metadata.eas.handlers.disable_failure_handler"
	// +type=bool
	ServiceKeyEnabledModelVerification = "eas.enabled_model_verification,metadata.eas.enabled_model_verification"

	// Scheduling...
	// +type=string
	ServiceKeySchedulerPolicy = "scheduling.policy,metadata.eas.scheduler.policy"
	// +type=string
	ServiceKeySchedulerKeys = "scheduling.topology_key,metadata.eas.scheduler.scatter_key"
	// +type=bool
	ServiceKeyEnableCpuset = "scheduling.enable_cpuset,metadata.eas.scheduler.enable_cpuset"
	// +type=string
	ServiceKeySpreadPolicy = "scheduling.spread.policy,metadata.scheduling.spread.policy"
	// +type=string
	ServiceKeySchedulingPriorityKeys = "scheduling.priority,metadata.scheduling.priority"
	// +type=gitlab.alibaba-inc.com/eas/client-go/apis/service/v1alpha1@ref.PackScheduling
	ServiceKeySchedulingPack = "scheduling.pack,metadata.scheduling.pack"
	// +type=gitlab.alibaba-inc.com/eas/client-go/apis/service/v1alpha1@ref.NodeAffinity
	ServiceKeySchedulingNodeAffinity = "scheduling.node_affinity,metadata.scheduling.node_affinity"

	// Metrics...
	// +type=easgo/pkg/sysdef/models@sliceOf.raw.MetricInfo
	ServiceKeyMetrics = "metrics"

	// +type=easgo/pkg/sysdef/models@ref.MetricsConfig
	ServiceKeyMetricsConfig = "metrics_config"
	// +type=sliceOf.raw.string
	ServiceKeyMetricsFetchUrls = "metrics_config.fetch_urls"
	// +type=int
	ServiceKeyMetricsFetchDuration = "metrics_config.fetch_duration_ms"
	// +type=sliceOf.raw.string
	ServiceKeyLLMMetricsUrls = "metrics_config.llm_metrics_urls"

	// Alerts...
	// +type=easgo/pkg/sysdef@sliceOf.raw.Alert
	ServiceKeyAlerts = "alerts"

	// Processors...
	// +type=string
	ServiceKeyProcessor = "processor"
	// +type=string
	ServiceKeyProcessorName = "processor_name"
	// +type=string
	ServiceKeyProcessorPath = "processor_path"
	// +type=string
	ServiceKeyProcessorType = "processor_type"
	// +type=string
	ServiceKeyProcessorEntry = "processor_entry"
	// +type=string
	ServiceKeyProcessorMainClass = "processor_mainclass"
	// +type=string
	ServiceKeyProcessorJVMOptions = "processor_jvm_options"
	// +type=easgo/pkg/sysdef/models@sliceOf.raw.ServiceProcessorEnv
	ServiceKeyProcessorEnvs = "processor_envs"
	// +type=string
	ServiceKeyProcessorImage = "processor_image"
	// +type=string
	ServiceKeyProcessorRequirements = "requirements"

	// +type=easgo/pkg/sysdef/models@sliceOf.raw.ServiceProcessorConfig
	ServiceKeyProcessors = "processors"

	// Models...
	// +type=string
	ServiceKeyModelPath = "model_path"
	// +type=string
	ServiceKeyModelEntry = "model_entry"
	// +type=easgo/pkg/util/stringormap@ref.StringOrMap
	ServiceKeyModelConfig = "model_config"
	// +type=string
	ServiceKeyModel = "model"
	// +type=easgo/pkg/sysdef/models@sliceOf.raw.ServiceModelConfig
	ServiceKeyModels = "models"
	// +type=easgo/pkg/sysdef/models@ref.MultiModelsConfig
	ServiceKeyMultiModels = "multi_models"

	// No category...
	// +type=gitlab.alibaba-inc.com/eas/client-go/apis/legacy/v1@sliceOf.raw.Container
	ServiceKeyContainers = "containers"
	// +type=bool
	ServiceKeyUncompress = "uncompress"
	// +type=string
	ServiceKeyWarmUpDataPath = "warm_up_data_path"
	// +type=string
	ServiceKeySource = "source"
	// +type=string
	ServiceKeyOSSEndpoint = "oss_endpoint"
	// +type=easgo/pkg/sysdef/storage@sliceOf.raw.StorageConfig
	ServiceKeyStorage = "storage"
	// +type=map[string]interface{}
	ServiceKeyFeatures = "features"

	// Networing...
	// +type=string
	ServiceKeyPathPrefix = "networking.path,metadata.path"
	// +type=string
	ServiceKeyPortExposure = "networking.port_exposure"
	// +type=string
	ServiceKeyTrafficState = "networking.traffic_state,metadata.traffic_state"
	// +type=string
	ServiceKeyGateway = "networking.gateway,metadata.gateway,?k"
	// +type=string
	ServiceKeyGatewayPolicy = "networking.gateway_policy"
	// +type=bool
	ServiceKeyHttpsSupport = "networking.enable_https,metadata.enable_https"
	// +type=bool
	ServiceKeyHttp2Support = "networking.enable_http2,metadata.enable_http2"
	// +type=bool
	ServiceKeyGrpcSupport = "networking.enable_grpc,metadata.enable_grpc"
	// +type=bool
	ServiceKeyEnableWebService = "metadata.enable_webservice"
	// +type=sliceOf.raw.string
	ServiceKeyDnsNameservers = "networking.dns.nameservers"
	// +type=string
	ServiceKeySecurityRestrictionModel = "networking.restriction_model"
	// +type=easgo/pkg/sysdef/network@sliceOf.raw.RestrictionRule
	ServiceKeySecurityRestrictionCIDR = "networking.restriction_rules"

	// +type=string
	ServiceKeyInternetEndpoint = "networking.disable_internet_endpoint,metadata.disable_internet_endpoint"

	// +type=string
	ServiceKeyNetworkingMountNlb   = "networking.nlb"
	ServiceKeyNetworkingMountNacos = "networking.nacos"

	// +type=bool
	ServiceKeyNetworkingHostNetwork = "networking.use_host_network"

	// Autoscaling...
	// +type=gitlab.alibaba-inc.com/eas/client-go/apis/legacy/v1@ref.AutoScaler
	ServiceKeyAutoscaler = "autoscaler,metadata.autoscaler"
	// +type=gitlab.alibaba-inc.com/eas/client-go/apis/legacy/v1@ref.CronScaler
	ServiceKeyCronscaler = "cronscaler,metadata.cronscaler"

	// Runtime...
	// +type=string
	ServiceKeyCuda = "runtime.cuda,metadata.cuda"
	// +type=string
	ServiceKeyBaseImage = "runtime.base_image,baseimage"
	// +type=string
	ServiceKeyWorkerImage = "runtime.worker_image,worker_image"
	// +type=string
	ServiceKeyCorePattern = "runtime.core_pattern"
	// +type=int
	ServiceKeyTerminationPeriod = "runtime.termination_grace_period,metadata.eas.termination_grace_period"
	// +type=bool
	ServiceKeyEnableCrashBlock = "runtime.enable_crash_block"
	// +type=bool
	ServiceKeyDisableImageAccelerate = "runtime.disable_image_accelerate"
	// +type=bool
	ServiceKeyDisableImageCache = "runtime.disable_image_cache"

	// Confidential computing
	ServiceKeyConfidential               = "confidential"
	ServiceKeyConfidentialTrusteeAddress = "confidential.trustee_endpoint"
	ServiceKeyConfidentialDecryptionKey  = "confidential.decryption_key"

	// Metadata...
	// +type=bool
	ServiceKeyDisableSidecar = "metadata.disable_sidecar"
	// +type=string
	ServiceKeyRegion = "metadata.region"
	// +type=string
	ServiceKeyResource = "metadata.resource"
	// +type=bool
	ServiceKeyResourceBurstable = "metadata.resource_burstable"
	// +type=bool
	ServiceKeyResourceRebalancing = "metadata.resource_rebalancing"
	// +type=int
	ServiceKeyInstance = "metadata.instance"
	// +type=string
	ServiceKeyWorkloadType = "metadata.workload_type"
	// +type=easgo/pkg/sysdef/models@ref.RollingUpdateStrategy
	ServiceKeyUpdateStrategy = "metadata.rolling_strategy"
	// +type=string
	ServiceKeyName = "metadata.name,name"
	// +type=string
	ServiceKeyServiceGroup = "metadata.group"
	// +type=string
	ServiceKeyUid = "metadata.uid,uid,?h"
	// +type=string
	ServiceKeyCallerUid = "metadata.caller_uid,caller_uid,?hr"
	// +type=int
	ServiceKeyCpu = "metadata.cpu"
	// +type=int
	ServiceKeyMemory = "metadata.memory"
	// +type=string
	ServiceKeyDisk = "metadata.disk"
	// +type=int
	ServiceKeyGpu = "metadata.gpu"
	// +type=int
	ServiceKeyRdma = "metadata.rdma"
	// +type=int
	ServiceKeyGpuMemory = "metadata.gpu_memory"
	// +type=int
	ServiceKeyGpuCorePercentage = "metadata.gpu_core_percentage"
	// +type=string
	ServiceKeyGpuModel = "metadata.gpu_model"
	// +type=int
	ServiceKeyNpu = "metadata.npu"
	// +type=int
	ServiceKeyFpga = "metadata.fpga"
	// +type=string
	ServiceKeySchema = "metadata.schema"
	// +type=string
	ServiceKeyRole = "metadata.role"
	// +type=string
	ServiceKeyType = "metadata.type"
	// +type=string
	ServiceKeyQoS = "metadata.qos"
	// +type=string
	ServiceKeyWorkspaceId = "metadata.workspace_id"
	// +type=string
	ServiceKeyQuotaId = "metadata.quota_id"
	// +type=string
	ServiceKeyQuotaType = "metadata.quota_type"
	// +type=int
	ServiceKeyShmSize = "metadata.shm_size"
	// +type=bool
	ServiceKeyEnableRamRole = "options.enable_ram_role"
	// +type=bool
	ServiceKeyEnableWritableCache = "options.enable_writable_cache"
	// +type=bool
	ServiceKeyEnableCache = "options.enable_cache"
	// +type=bool
	ServiceKeyEnableAIGC = "options.enable_aigc"
	// +type=bool
	ServiceKeyEnableVineyard = "options.enable_vineyard"
	// Vineyard is defined in easgo/pkg/sysdef/models/type.go
	// +type=easgo/pkg/sysdef/models@ref.Vineyard
	ServiceKeyVineyard = "vineyard"
	// +type=bool
	ServiceKeyIgnoreOssReaddir = "options.enable_ignore_oss_readdir" // deprecated
	// +type=string
	ServiceKeyQuotaOversold = "options.quota_oversold"
	// +type=string
	ServiceKeyPriority = "options.priority"
	// +type=easgo/pkg/sysdef/models@ref.ElasticJobParams
	ServiceKeyElasticJob            = "metadata.elastic_job"
	ServiceKeyElasticJobCompletions = "metadata.elastic_job.completions"

	// Sinker config...
	ServiceKeySinker = "sinker"
	// +type=string
	ServiceKeySinkerType = "sinker.type"
	// +type=easgo/pkg/sysdef/models@ref.OdpsSinkerInfo
	ServiceKeySinkerConfig = "sinker.config"

	// +type=map[string]interface{}
	ServiceKeyLabels = "labels"

	// Cloud...
	ServiceKeyCloud           = "cloud"
	ServiceKeyCloudNetworking = "cloud.networking"
	// +type=string
	ServiceKeyCloudSecurityGroupId = "cloud.networking.security_group_id"
	// +type=string
	ServiceKeyCloudVSwitchId = "cloud.networking.vswitch_id"
	// +type=string
	ServiceKeyCloudDestinationCidrs = "cloud.networking.destination_cidrs"
	// +type=string
	ServiceKeyCloudDefaultRoute = "cloud.networking.default_route"
	// +type=string
	ServiceKeyCloudVpcId = "cloud.networking.vpc_id"
	// +type=string
	ServiceKeyCloudVswitchOwner = "cloud.networking.vswitch_owner"
	// +type=string
	ServiceKeyEciInstanceType = "cloud.computing.instance_type"
	// +type=easgo/pkg/sysdef/models@sliceOf.raw.EciInstanceInfo
	ServiceKeyEciInstances = "cloud.computing.instances"
	// +type=sliceOf.raw.string
	ServiceKeyLoggingPaths = "cloud.logging.paths"
	// +type=string
	ServiceKeyLogtailUid = "cloud.logging.logtail_uid"
	// +type=bool
	ServiceKeyDisableSpotProtectionPeriod = "cloud.computing.disable_spot_protection_period"
	// +type=string
	ServiceKeyContainerRegistryInstanceId = "cloud.container_registry.instance_id"
	// +type=easgo/pkg/sysdef/models@sliceOf.raw.RoleChain
	ServiceKeyRamRoleChain = "cloud.role_chain"
	// +type=bool
	ServiceKeyCloudMonitoringEnableTracing = "cloud.monitoring.enable_tracing"
	// +type=string
	ServiceKeyDataImage = "data_image"

	// band members...
	ServiceKeyMembers = "members"
	//ServiceKeyIsMember = "is_member"

	// Exporter...
	ServiceKeyExporter = "exporter"

	// DataLoader...
	ServiceKeyDataLoader = "DataLoader,data_loader"

	// +type=easgo/pkg/sysdef/roles@sliceOf.raw.TaskMember
	ServiceKeyBandTasks = "tasks"

	// Unit is defined in easgo/pkg/sysdef/models/type.go
	// +type=easgo/pkg/sysdef/models@ref.Unit
	ServiceKeyUnit = "unit"
	// +type=bool
	ServiceKeyUnitEnableC4D = "unit.enable_c4d"
	// +type=bool
	ServiceKeyUnitEnableRebuild = "unit.enable_rebuild"

	// AIMaster
	// +type=easgo/pkg/sysdef/models@ref.AIMaster
	ServiceKeyAIMaster = "aimaster"
	// ServiceKeySanityCheck
	ServiceKeySanityCheck = "aimaster.sanity_check"
	// ServiceKeyRuntimeCheck
	ServiceKeyRuntimeCheck = "aimaster.runtime_check"

	// +type=easgo/pkg/sysdef/models@ref.RayCluster
	ServiceKeyRayCluster = "raycluster"

	// +type=string
	ServiceKeyRaiGuardrail = "rai"
)

// The section of serverless config
const (
	ServerlessServiceKeyType          = "serverless.type"
	ServerlessServiceKeyNotActivation = "serverless.rpc.not_activation"
)

const (
	PropertyKeyRpcKeepAlive        = "rpc.keep_alive"
	PropertyKeyRpcDisableSubscribe = "rpc.disable_subscribe"
	PropertyKeyModelStatus         = "model_status"
	PropertyKeyModels              = "models"
	PropertyKeyMetricPint          = "rpc.metric._print_"
	PropertyKeyRpcProxyURL         = "rpc.proxy_url"
	PropertyKeyRpcEnableProxy      = "rpc.enable_proxy"
	PropertyKeyToken               = "rpc.token"
	PropertyKeyEndpoint            = "rpc.endpoint"
)

const (
	// ServiceUpdateTypeMerge merge is the default update type, it will merge the config with the existing one
	ServiceUpdateTypeMerge = "merge"
	// ServiceUpdateTypeReplace replace will replace the existing config with the new one
	ServiceUpdateTypeReplace = "replace"
	// ServiceUpdateTypeRollback rollback is similar to replace, but it will not update the version
	ServiceUpdateTypeRollback = "rollback"
)

const (
	QosBestEffort = "BestEffort"
	QosGuaranteed = "Guaranteed"
)

const (
	RecycleHours    = time.Hour * 24 * 180
	RecycleVersions = 10
)

// The old serviceType and bandType are mixed together, they all exposed as ServiceType in OpenAPI.
// Generally, the old serviceType 'simple' and 'graph' has been deprecated.
// But after prioritizing bandType, please also pay attention to the 'serverless' type.
const (
	ServiceTypeStandard   string = "Standard"
	ServiceTypeAsync      string = "Async"
	ServiceTypeSDCluster  string = "SDCluster"
	ServiceTypeLlmGateway string = "LLMGatewayService"
	ServiceTypeQueue      string = "Queue"
)

const (
	ServiceTypeServerless string = "serverless"
	ServiceTypeGraph      string = "graph"
	ServiceTypeSimple     string = "simple"
	ServiceTypeLLM        string = "LLM"
	ServiceTypeRAG        string = "RAG"
)

const (
	OversoldTypeAccept      = "AcceptQuotaOverSold"
	OversoldTypeReject      = "ForbiddenQuotaOverSold"
	OversoldTypeForceAccept = "ForceQuotaOverSold"
)

const (
	ServiceInternalDomainAnno string = "service.eas.alibaba-inc.com/internal-domain"
	ServiceExternalDomainAnno string = "service.eas.alibaba-inc.com/external-domain"
)

const (
	GatewaySslRedirectAnno string = "gateway.eas.alibaba-inc.com/ssl-redirect"
)

const (
	IngressSslRedirectAnno      string = "ingress.kubernetes.io/ssl-redirect"
	NginxIngressSslRedirectAnno string = "nginx.ingress.kubernetes.io/ssl-redirect"
)

const (
	EphemeralStorageKey   = "eas.aliyun.com/extra-ephemeral-storage"
	CapacityReservation   = "eas.aliyun.com/capacity-reservation"
	GpuDriverVersionOnECI = "eas.aliyun.com/gpu-driver-version"
	GpuDriverVersionOnASI = "alibabacloud.com/gpu-card-driver"
	GpuCudaVersionOnASI   = "alibabacloud.com/gpu-card-cuda"
	// aliyundunEnabledKey   = "eas.aliyun.com/aliyundun-enabled" // features key memo: k8s.aliyun.com/eci-aliyundun-enabled on eci sofar
	NvidiaIBGDACap                  = "eas.aliyun.com/enable-nvidia-ibgda"
	NvidiaGDRCopyCap                = "eas.aliyun.com/enable-nvidia-gdrcopy"
	AsiInternalTreSpotEnabled       = "eas.aliyun.com/enable-tre-spot-instance"
	FaultInjection                  = "eas.aliyun.com/enable-fault-injection"
	GpuFaultInjection               = "eas.aliyun.com/enable-gpu-fault-injection"
	FuseCap                         = "eas.aliyun.com/enable-fuse"
	AutoBindingVipOnASI             = "eas.aliyun.com/auto-binding-vip"
	VMMaxMapCount                   = "eas.aliyun.com/vm-max-map-count"
	RestartPodWithGPUErrorsKey      = "eas.aliyun.com/restart-pod-with-gpu-errors"
	GatewaySchedulerUCHValueFeature = "eas.aliyun.com/gateway-scheduler-uch-value"
	AppTemplate                     = "eas.aliyun.com/enable-application-template"
	PreferToScaleDownOlderReplicas  = "eas.aliyun.com/prefer-to-scale-down-older-replicas"
	ManagedService                  = "eas.aliyun.com/enable-managed-service"
	ServiceSharding                 = "eas.aliyun.com/enable-service-sharding"
)

var (
	FeaturesKeys = []string{
		EphemeralStorageKey,
		CapacityReservation,
		GpuDriverVersionOnECI,
		GpuDriverVersionOnASI,
		GpuCudaVersionOnASI,
		NvidiaIBGDACap,
		NvidiaGDRCopyCap,
		AsiInternalTreSpotEnabled,
		FaultInjection,
		GpuFaultInjection,
		FuseCap,
		AutoBindingVipOnASI,
		VMMaxMapCount,
		RestartPodWithGPUErrorsKey,
		GatewaySchedulerUCHValueFeature,
		AppTemplate,
		PreferToScaleDownOlderReplicas,
		ServiceSharding,
	}
)
