package consts

import (
	"time"

	v1 "k8s.io/api/core/v1"
)

const (
	LegacyServiceWeightKey    = "eas.alibaba-inc.com/service-weight"
	LegacyServiceWeightMseKey = "mse.ingress.kubernetes.io/destination"

	LegacyServiceGroupKey = "app"

	ServicePubKey                     = "eas.aliyun.com"
	ServiceImageAccelerateMode        = ServicePubKey + "/image-accelerate-mode"
	ServiceEnableFaultInjectionKey    = ServicePubKey + "/enable-fault-injection"
	ServiceEnableGpuFaultInjectionKey = ServicePubKey + "/enable-gpu-fault-injection"

	ServiceKey                        = "service.eas.alibaba-inc.com"
	ServiceController                 = ServiceKey + "/controller"
	ServiceNameKey                    = ServiceKey + "/name" // ** critical **
	ServiceNamespaceKey               = ServiceKey + "/namespace"
	ServiceRoleKey                    = ServiceKey + "/role"
	ServiceUUIDKey                    = ServiceKey + "/uuid"
	ServiceSignature                  = ServiceKey + "/signature"
	ServiceTrafficState               = ServiceKey + "/traffic-state"
	ServiceTrafficWeight              = ServiceKey + "/traffic-weight"
	ServiceWorkloadType               = ServiceKey + "/workload"
	ServiceVesselType                 = ServiceKey + "/vessel"
	ServicePreStopCommand             = ServiceKey + "/pre-stop-command"
	ServiceRecycleFailedInstanceAt    = ServiceKey + "/failed-recycle-at"
	ServiceRecyclePendingInstanceAt   = ServiceKey + "/pending-recycle-at"
	ServiceRestartedAt                = ServiceKey + "/restarted-date"
	OriginalReplicasKey               = ServiceKey + "/replicas"
	ServiceOwnerUid                   = ServiceKey + "/owner-uid"
	ServiceParentUid                  = ServiceKey + "/parent-uid"
	ServiceParentBid                  = ServiceKey + "/parent-bid"
	ServiceUid                        = ServiceKey + "/uid"
	ServiceWorkspaceId                = ServiceKey + "/workspace-id"
	ServiceQuotaId                    = ServiceKey + "/quota-id"
	ServiceQuotaType                  = ServiceKey + "/quota-type"
	ServiceLastModified               = ServiceKey + "/last-modified"
	ServiceCRLastModified             = ServiceKey + "/cr-last-modified"
	ServiceIngressRecycledAt          = ServiceKey + "/ingress-recycled-at"
	ServiceCRInstanceId               = ServiceKey + "/cr-instance-id"
	ServiceCRInstanceName             = ServiceKey + "/cr-instance-name"
	ServiceLatestPersonalImageVersion = ServiceKey + "/latest-personal-image-version"
	ServiceComputeClusterId           = ServiceKey + "/compute-cluster-id"
	ServiceTags                       = ServiceKey + "/tags"
	ServiceSkipReconcileKey           = ServiceKey + "/skip-reconcile"
	ServicePortExposure               = ServiceKey + "/port-exposure"
	ServiceEASWorkerPort              = ServiceKey + "/easworker-port"
	ServiceAllowCpfsPathChange        = ServiceKey + "/allow-cpfs-path-change"
	ServiceDetached                   = ServiceKey + "/detached"
	ServiceEnableStreamingLog         = ServiceKey + "/enable-streaming-log"
	ServiceGrafanaVersion             = ServiceKey + "/grafana-version"
	ServiceShardCluster               = ServiceKey + "/shard-cluster"

	ServiceRamRoleInfo           = ServiceKey + "/ram-role-info"
	ServiceRamRoleName           = ServiceKey + "/ram-role-name"
	ServiceSecretType            = ServiceKey + "/secret-type"
	ServiceBurstInstances        = ServiceKey + "/burst-instances"
	ServiceCommitVersion         = ServiceKey + "/commit-version"
	ServiceCommitImage           = ServiceKey + "/commit-image"
	ServiceImageRecycled         = ServiceKey + "/image-recycled"
	ServiceCloneFrom             = ServiceKey + "/clone-from"
	ServiceSkipDisableHttpRouter = ServiceKey + "/skip-disable-hr"
	ServiceArchive               = ServiceKey + "/archive"

	ServiceStreamlogType     = ServiceKey + "/streamlog-type"
	ServiceSecretTypeRAMRole = "ram-role"

	ServiceBillingByKey = ServiceKey + "/billing-by"

	ServiceAppQuotaKey  = ServiceKey + "/app-service-quota"

	ServiceEnablePrivilegedForUserContainerKey = ServiceKey + "/enable-privileged-for-user-container"
	ServiceCachefsCacheRootConfigsKey          = ServiceKey + "/cachefs-cacheroot-configs"
	ServiceCachefsMetricsPortsKey              = ServiceKey + "/cachefs-metrics-ports"
	ServiceDisableScopeMutationKey             = ServiceKey + "/disable-scope-mutation"

	// deprecated
	ServiceObservedServiceSpecKey              = ServiceKey + "/observed-service-spec"
	ServiceObservedServiceGenerationKey        = ServiceKey + "/observed-service-generation"
	ServiceObservedRestartKey                  = ServiceKey + "/observed-restarted-date"
	ServiceSnapshotNameDigestKey               = ServiceKey + "/service-snapshot-digest"
	ServiceDesiredServiceGenerationKey         = ServiceKey + "/desired-service-generation"
	ServiceLastSucceedToReconcileGenerationKey = ServiceKey + "/last-succeed-to-reconcile-generation"
	//ServiceGroupOdpsEndpoint            = ServiceKey + "/maxcompute-endpoint"
	//ServiceGroupOdpsProject             = ServiceKey + "/maxcompute-project"
	//ServiceGroupOdpsTable               = ServiceKey + "/maxcompute-table"

	// Llm gateway
	ServiceLlmGatewaySchedulePolicyKey = ServiceKey + "/llm-gateway-schedule-policy"

	ServiceDomainKey = "domain.eas.alibaba-inc.com"

	ExternalServiceKey       = "externalservice.eas.alicloud.com"
	ExternalServiceFinalizer = ExternalServiceKey + "/finalizer"
	ExternalServiceTypeKey   = ExternalServiceKey + "/type"

	ServiceGroupKey                     = "servicegroup.eas.alibaba-inc.com"
	ServiceGroupNameKey                 = ServiceGroupKey + "/name" // ** critical **
	ServiceGroupNamespaceKey            = ServiceGroupKey + "/namespace"
	ServiceGroupFinalizer               = ServiceGroupKey + "/finalizer"
	ServiceGroupEnableNetworkUpdatesKey = ServiceGroupKey + "/enable-network-updates"

	ServiceUpdatingInitMessage  = "Service specification changed"
	ServiceHotUpdateInitMessage = "Performing hot updating"
	ServiceCreatingInitMessage  = "Service is now creating"
	ServiceCloningInitMessage   = "Service is now cloning"
	ServiceScaleInitMessage     = "Service is now scaling"
	ServiceDeployInitMessage    = "Service is now deploying"
	ServiceCommitInitMessage    = "Service is now committing"
	ServiceAutoScaleInitMessage = "Service is now auto scaling"
	ServiceWaitingMessage       = "Service is now waiting to all available"

	WorkflowKey            = "workflow.eas.alibaba-inc.com"
	WorkflowNameKey        = "workflows.eas.alibaba-inc.com/workflow"
	WorkflowStageNameKey   = WorkflowKey + "/stage"
	WorkflowControllerKey  = WorkflowKey + "/controller"
	WorkflowTerminatedBy   = WorkflowKey + "/terminated-by"
	WorkflowRetriedTimeKey = WorkflowKey + "/retried-time"
	WorkflowLauncherKey    = WorkflowKey + "/launcher"

	ControllerAgentName = "service-controller"
	ServiceFinalizer    = WorkflowKey + "/finalizer"

	BandKey                = "band.eas.alibaba-inc.com"
	BandNameKey            = BandKey + "/name" // ** critical **
	BandTypeKey            = BandKey + "/type" // ** critical **
	BandFinalizer          = BandKey + "/finalizer"
	BandJobRerunIndex      = BandKey + "/rerun-index"
	BandJobBackoffLimitKey = BandKey + "/backoff-limit"
	BandJobCompletionKey   = BandKey + "/completion"

	PodKey                        = "pods.eas.alibaba-inc.com"
	PodKeyReadinessGate           = PodKey + "/readiness-gate"
	PodKeyVesselReadinessGate     = PodKey + "/vessel-readiness-gate"
	PodKeyNetworkRestrictionModel = PodKey + "/network-restriction-model"
	PodKeyNetworkRestrictionRules = PodKey + "/network-restriction-rules"
	PodKeyHandleTrafficEnable     = "handle-traffic-enabled"
	PodKeyHibernated              = PodKey + "/hibernated"

	PodReadinessGateKey               = "readiness.pods.eas.alibaba-inc.com"
	PodAlbReadinessGateKey            = PodReadinessGateKey + "/alb-ready"
	PodNlbReadinessGateKey            = PodReadinessGateKey + "/nlb-ready"
	PodNacosReadinessGateKey          = PodReadinessGateKey + "/nacos-ready"
	PodBaseReadinessGateAnnotationKey = PodKey + "/base-readiness-gate"
	podVesselReadinessGateKey         = PodReadinessGateKey + "/vessel-ready"
	PodIpForLbGwLabelKey              = "lb.eas.aliyun-inc.com/pod-ip"

	SecretKeySkipUpdateValidation = "eas.alicloud.com/skip-check-secret-update-validation"

	EASKey = "eas.alicloud.com"

	FleetKey                    = "fleet.workload.eas.alicloud.com"
	FleetNameKey                = FleetKey + "/name"
	FleetVesselNameKey          = FleetKey + "/vessel-name"
	FleetVesselReplicasKey      = "vessel." + FleetKey + "/replicas"
	FleetRankIdKey              = FleetKey + "/rank-id"
	FleetPackUnitByNetwork      = FleetKey + "/pack-unit-by-network"
	FleetPackUnitByRack         = FleetKey + "/pack-unit-by-rack"   // for L20A rack scheduling
	FleetPackUnitByClique       = FleetKey + "/pack-unit-by-clique" // for L20A clique scheduling
	FleetVesselRebuildKey       = FleetKey + "/rebuild-vessel"
	FleetVesselTypeKey          = FleetKey + "/vessel-type"
	FleetVesselServingTargetKey = FleetKey + "/serving-target"

	SecretKey = "secret.eas.alibaba-inc.com"

	// this cert is associated with idst_pai
	SecretCertIdentifierKey = SecretKey + "/cert-identifier"

	// this cert is associated with resource account
	// Due to the financial cloud and ks02, the resource account is not idst_pai.
	// Therefore, in the financial cloud and ks02, the MSE gateway uses this key to obtain the certificate.
	SecretResourceCertIdentifierKey = SecretKey + "/resource-cert-identifier"

	EasTlsSecretName = "eas-tls-secret"

	// for node
	ResourceNodeInstanceIDKey = "eas.alibaba-inc.com/instance-id"
	ResourceNodeParentUid     = "eas.alibaba-inc.com/parent-uid"

	ServiceWaitToBeSynced = "service.eas.alibaba-inc.com/wait-to-be-synced"

	// for resource
	ResourceKey                       = "resource.eas.alibaba-inc.com"
	ResourceKeySelfManagedClusterType = ResourceKey + "/self-managed-cluster-type"
	ResourceKeySelfManagedNodeAction  = ResourceKey + "/self-managed-node-action"

	FeatureGateEnableResourceNode = "EnableResourceNode"
	FeatureGateEnableKubeSharer   = "EnableKubeSharer"

	// fault injection
	FaultInjectionKey     = "faultinjection.eas.alibaba-inc.com"
	FaultInjectionTypeKey = FaultInjectionKey + "/type"
	FaultInjectionListKey = FaultInjectionKey + "/list"

	// features
	FeatureEnableCachefsSafeCacheKey = ServicePubKey + "/enable-cachefs-safe-cache-key"
)

const (
	FleetVesselTargetTypeIndex0 = "Index0"
	FleetVesselTargetTypeAll    = "All"
)

const (
	PriorityKey = "_priority_"
)

const (
	PodReadinessGate       v1.PodConditionType = PodKeyReadinessGate
	PodVesselReadinessGate v1.PodConditionType = PodKeyVesselReadinessGate
	PodAlbReadinessGate    v1.PodConditionType = PodAlbReadinessGateKey
	PodNlbReadinessGate    v1.PodConditionType = PodNlbReadinessGateKey
	PodNacosReadinessGate  v1.PodConditionType = PodNacosReadinessGateKey
)

const (
	MaxServiceWeight = "100"
	MinServiceWeight = "0"
)

const (
	// INF indicating a resource version is infinite large
	INF  = "inf"
	ZERO = ""
)

const (
	Version2 = "version2"
	Version1 = "version1"
)

const (
	GrafanaVersion2 = 2
	GrafanaVersion1 = 1
)

const (
	ServiceLastModifiedTimeFormat = "20060102150405"
	ServiceRestartTimeFormat      = time.RFC3339
)

const (
	FeatureGateDisableUnterminatedPodReactor = "DisableUnterminatedPodReactor"
	FeatureGateEnableCrossClusterScheduling  = "EnableCrossClusterScheduling"
	FeatureGateDisableHttpRoute              = "DisableHttpRoute"
)

const (
	StableProtectionTimeWindow = time.Minute * 10
	ForceUpdateValidTimeWindow = time.Hour * 6
)

const (
	// BizDomain represents eas user domain
	BizDomain = "biz"
)

const (
	ServiceLabelCountLimit       = 20
	ServiceLabelKeyLengthLimit   = 128
	ServiceLabelValueLengthLimit = 256
)

const (
	GatewayKey  = "gateway.eas.alibaba-inc.com"
	GatewayTags = GatewayKey + "/tags"
)

const (
	EnvTenantIDKey          = "TENANT_ID"
	EnvDiscoveryEndpointKey = "DISCOVERY_ENDPOINT"
)

const (
	RayRedisPortLabelKey = "ray.io/redis-port"
	RayNodeTypeLabelKey  = "ray.io/node-type"
)

const (
	ResourceCpu            = "cpu"
	ResourceMemory         = "memory"
	ResourceGpu            = "nvidia.com/gpu"
	ResourceGpuMemory      = "aliyun.com/gpu-mem"
	ResourceGpuCoreSize    = "resource.eas.alibaba-inc.com/gpu-core-size"
	ResourceGpuRatioSize   = "resource.eas.alibaba-inc.com/gpu-memory-ratio"
	ResourceTotalGpu       = "resource.eas.alibaba-inc.com/gpu"
	ResourceTotalGpuMemory = "resource.eas.alibaba-inc.com/gpu-memory"
	ResourceGpuUsed        = "resource.eas.alibaba-inc.com/gpu-used"
	ResourceGpuUsage       = "resource.eas.alibaba-inc.com/gpu-usage"
	ResourceCpuUsed        = "resource.eas.alibaba-inc.com/cpu-used"
	ResourceMemoryUsed     = "resource.eas.alibaba-inc.com/memory-used"
)

const (
	NodeInstanceAnnotationName              = "instance.eas.alibabacloud.com/name"
	NodeInstanceAnnotationIp                = "instance.eas.alibabacloud.com/ip"
	NodeInstanceAnnotationType              = "instance.eas.alibabacloud.com/type"
	NodeInstanceAnnotationProviderId        = "instance.eas.alibabacloud.com/provider-id"
	NodeInstanceAnnotationInstanceId        = "instance.eas.alibabacloud.com/instance-id"
	NodeInstanceAnnotationKubeletVersion    = "instance.eas.alibabacloud.com/kubelet-version"
	NodeInstanceAnnotationVswitchId         = "instance.eas.alibabacloud.com/vswitch-id"
	NodeInstanceAnnotationResourceName      = "instance.eas.alibabacloud.com/resource-name"
	NodeInstanceAnnotationSecondaryIp       = "instance.eas.alibabacloud.com/secondary-ip"
	NodeInstanceAnnotationRole              = "instance.eas.alibabacloud.com/role"
	NodeInstanceAnnotationExternalClusterId = "instance.eas.alibabacloud.com/external-cluster-id"
	NodeInstanceAnnotationProjectedNodeName = "instance.eas.alibabacloud.com/projected-node-name"
	//NodeInstanceAnnotationExtraData         = "instance.eas.alibabacloud.com/extra-data"
	NodeInstanceAnnotationCreateTime = "instance.eas.alibabacloud.com/create-time"
	NodeInstanceAnnotationArch       = "instance.eas.alibabacloud.com/arch"
	NodeInstanceAnnotationZone       = "instance.eas.alibabacloud.com/zone"
)

const (
	HybridResourceNodeAnnotationCordonBy         = "resource.pai.alicloud.com/cordon-by"
	HybridResourceNodeAnnotationEvictionContext  = "resource.pai.alicloud.com/eviction-context"
	HybridResourceNodeAnnotationEvictedTimestamp = "resource.pai.alicloud.com/evicted-timestamp"
	HybridResourceNodeLabelEvictedByDrainNode    = "resource.pai.alicloud.com/evicted-by-drain-node"
	HybridResourceNodeLabelEviction              = "resource.pai.alicloud.com/eviction"

	PaiLrnName = "pai.alibaba.com/lrn-name"

	LabelClusterId         = "pai.alicloud.com/cluster-id"
	LabelResourceClusterId = "qm.alibabacloud.com/resource-cluster-id"
	LabelResourceQuotaId   = "qm.alibabacloud.com/resource-quota-id"
)

const (
	NodeAffinityStrategyRequired string = "required"
)
