package scheduling_policy

import (
	"time"

	"golang.org/x/exp/maps"
	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/cms"
	"llumnix/pkg/consts"
	"llumnix/pkg/lrs"
	"llumnix/pkg/metrics"
	"llumnix/pkg/scheduler/hasher"
	"llumnix/pkg/scheduler/kvs"
	"llumnix/pkg/types"
)

type InstanceViewInterface interface {
	GetInstance() *types.LLMInstance
	GetInstanceId() string
	GetInferType() consts.InferType
}

type clusterView struct {
	groupedInstanceViews map[consts.InferType]map[string]InstanceViewInterface
}

type instanceViewScheduling struct {
	cmsView *cms.InstanceView
	lrsView *lrs.InstanceView
	InstanceViewInterface
	schedulingCtx
}

type schedulingCtx struct {
	metrics map[string]instanceSchedulingMetric
	// needsFailover indicates whether the instance needs failover by failover filter.
	needsFailover                     bool
	prefixHitTokens                   int
	prefixHitRatio                    float32
	prefixMissTokens                  int
	numComputedPrefillTokensPredicted int32
}

type clusterViewScheduling struct {
	groupedInstanceViews map[consts.InferType]map[string]*instanceViewScheduling
	instanceViews        map[string]*instanceViewScheduling
	clusterSchedulingCtx clusterSchedulingCtx
}

type clusterSchedulingCtx struct {
}

type ClusterViewClientInterface interface {
	RLock()
	RUnlock()
	Lock()
	Unlock()
}

type SchedulingPolicy interface {
	// Name scheduling policy name
	Name() string

	// Schedule attempts to acquire an instance for processing a new request.
	Schedule(*types.SchedulingRequest) error
}

func NewSchedulingPolicy(
	policy string,
	config *options.SchedulerConfig,
	lrsClient *lrs.LocalRealtimeStateClient) SchedulingPolicy {
	if len(policy) == 0 {
		panic("create scheduling policy exception, policy is empty.")
	}

	klog.Infof("create scheduler with policy: %v", policy)

	return NewDispatchPolicy(config, policy, lrsClient)
}

type DispatchPolicy struct {
	c                 *options.SchedulerConfig
	schedulingPolicy  string
	cmsClient         *cms.CMSReadClient
	lrsClient         *lrs.LocalRealtimeStateClient
	clusterViewClient ClusterViewClientInterface
	kvsClient         kvs.KVSClientInterface
	tokenHasher       *hasher.TokenHasher
	policyInternal    dispatchPolicyInternal
	clusterView       clusterView
}

func NewDispatchPolicy(
	c *options.SchedulerConfig, schedulingPolicy string, lrsClient *lrs.LocalRealtimeStateClient) *DispatchPolicy {

	verifyConfig(c)

	var cmsClient *cms.CMSReadClient
	var clusterViewClient ClusterViewClientInterface = nil
	if c.EnableFullModeScheduling {
		client, err := cms.CreateOrGetClient(
			c.CmsRedisHost,
			c.CmsRedisPort,
			c.CmsRedisUsername,
			c.CmsRedisPassword,
			c.CmsRedisSocketTimeout,
			c.CmsRedisRetryTimes,
			c.CmsPullStatusIntervalMs,
			c.CmsPullMetadataIntervalMs,
			c.AllowConcurrentScheduling,
			c.EnableInstanceStatusLocalAccount,
			c.EnableCacheAwareScheduling,
			c.RequestLocalAccountStalenessSeconds,
			c.CmsRecordMetricsInterval,
			c.EnablePredictorEnhancedScheduling,
			c.NumPredictorWarmupSamples)
		if err != nil {
			panic(err)
		}
		cmsClient = client
		clusterViewClient = cmsClient
	} else {
		clusterViewClient = lrsClient
	}

	var kvsClient kvs.KVSClientInterface
	var tokenHasher *hasher.TokenHasher
	if c.EnableCacheAwareScheduling {
		client, err := kvs.CreateOrGetClient(
			c.KvsBackend,
			c.KvsMetadataServiceConfigPath,
			c.KvsRetryTimes,
			c.KvsRetryIntervalMs,
			c.KvsMetadataServiceDownDurationS,
			c.KvsMetadataServiceRedisClusterHosts,
			c.KvsMetadataServiceRedisClusterPassword,
			c.KvsMetadataServiceHttpServerHost,
			c.KvsMetadataServiceHttpServerPort)
		if err != nil {
			panic(err)
		}
		kvsClient = client

		// Override hash algo for v6d backend: v6d always uses xxhash.
		hashAlgo := c.KvsHashAlgo
		if c.KvsBackend == consts.KvsBackendV6d {
			klog.Infof("Overriding KvsHashAlgo from %q to %q for v6d backend", c.KvsHashAlgo, consts.KvsHashAlgoXxhash)
			hashAlgo = consts.KvsHashAlgoXxhash
		}
		th, err := hasher.NewTokenHasher(hashAlgo)
		if err != nil {
			panic(err)
		}
		tokenHasher = th
	}

	return &DispatchPolicy{
		c:                 c,
		schedulingPolicy:  schedulingPolicy,
		cmsClient:         cmsClient,
		lrsClient:         lrsClient,
		clusterViewClient: clusterViewClient,
		kvsClient:         kvsClient,
		tokenHasher:       tokenHasher,
		policyInternal:    newDispatchPolicyInternal(c),
		clusterView: clusterView{
			groupedInstanceViews: nil,
		},
	}
}

func (p *DispatchPolicy) Name() string {
	return p.schedulingPolicy
}

func Uint32ToInt64(arr []uint32) []int64 {
	result := make([]int64, len(arr))
	for i, v := range arr {
		result[i] = int64(v)
	}
	return result
}

func (p *DispatchPolicy) Schedule(request *types.SchedulingRequest) error {
	tStart := time.Now()
	defer func() {
		elapsed := time.Since(tStart).Milliseconds()
		klog.V(3).Infof("Llumnix GetToken took %dms, request id: %v, promptTokenIds len: %v",
			elapsed, request.Id, len(request.PromptTokenIds))
	}()

	if p.c.EnableFullModeScheduling {
		if !p.cmsClient.IsAlive() {
			klog.V(2).Info("CMS client is not alive, return ErrorCmsNotAvailable")
			return consts.ErrorCmsNotAvailable
		}
	}

	klog.V(4).Infof("GetToken request received, promptTokenIds length: %d", len(request.PromptTokenIds))

	startTime := time.Now()

	if p.c.AllowConcurrentScheduling {
		p.clusterViewClient.RLock()
	} else {
		p.clusterViewClient.Lock()
	}
	defer func() {
		if p.c.AllowConcurrentScheduling {
			p.clusterViewClient.RUnlock()
		} else {
			p.clusterViewClient.Unlock()
		}
		metrics.AddLlumnixLatency(
			metrics.LlumnixMetricSchedulingLatencyMicroseconds, metrics.Labels{}, time.Since(startTime).Microseconds())
	}()

	if p.c.EnableFullModeScheduling {
		p.clusterView.groupedInstanceViews = toInstanceViewInterfaceMap(p.cmsClient.GetGroupedInstanceViews())
	} else {
		p.clusterView.groupedInstanceViews = toInstanceViewInterfaceMap(p.lrsClient.GetGroupedInstanceViews())
	}
	clusterViewScheduling := toClusterViewScheduling(p.clusterView)
	klog.V(4).Infof("Retrieved cluster instances, count: %d", len(clusterViewScheduling.instanceViews))

	selectedInstances := p.schedule(request, clusterViewScheduling)
	if len(selectedInstances) == 0 {
		klog.Warningf("No instances selected, return ErrorNoAvailableEndpoint")
		return consts.ErrorNoAvailableEndpoint
	}

	var schResults types.SchedulingResult
	for _, instance := range selectedInstances[0] {
		if instance == nil {
			continue
		}

		schResults = append(schResults, *instance.GetInstance())
		klog.V(4).Infof("Added token from instance: %s", instance.GetInstanceId())
		logSelectedInstance(instance, request.Id, consts.InferTypePrefill, p.c.EnableFullModeScheduling)
	}
	if len(selectedInstances) > 1 {
		for _, instance := range selectedInstances[1] {
			if instance == nil {
				continue
			}

			schResults = append(schResults, *instance.GetInstance())
			klog.V(4).Infof("Added token2 from instance: %s", instance.GetInstanceId())
			logSelectedInstance(instance, request.Id, consts.InferTypeDecode, p.c.EnableFullModeScheduling)
		}
	}
	request.SchedulingResult = schResults
	return nil
}

func (p *DispatchPolicy) schedule(
	request *types.SchedulingRequest,
	clusterView clusterViewScheduling) (selectedInstances [][]*instanceViewScheduling) {
	requestId := request.Id
	promptTokenIds := Uint32ToInt64(request.PromptTokenIds)
	selectedInstances = [][]*instanceViewScheduling{}
	if p.c.EnableCacheAwareScheduling && p.tokenHasher != nil && len(promptTokenIds) >= p.c.CacheAwareSchedulingMinTokens {
		// NOTE(sunbiao.sun): Calculating instance prompt cache hit len has ms-level latency, but it does not r/w
		// the raw instance view, so we unlock and re-lock here to improve scheduling throughput
		// when not allowing concurrent scheduling.
		if !p.c.AllowConcurrentScheduling {
			p.cmsClient.Unlock()
		}
		prefixHashes, err := p.tokenHasher.HashTokens(
			promptTokenIds, p.c.KvsChunkSize, p.c.KvsEnableSaveUnfullChunk,
			p.c.KvsIrisMetaPrefix, p.c.KvsVLLMBlockPrefix)
		if err != nil {
			klog.Warningf("HashTokens failed: %v", err)
		} else {
			// Write prefixHitTokens in scheduling ctx of instance view.
			getInstancesPrefixCacheHitLen(
				p.kvsClient, p.cmsClient, prefixHashes, p.c.KvsChunkSize,
				len(promptTokenIds), clusterView.instanceViews)
		}
		if !p.c.AllowConcurrentScheduling {
			p.cmsClient.Lock()
		}
	}

	numTokens := int32(0)
	if p.c.EnableInstanceStatusLocalAccount {
		numTokens = int32(len(promptTokenIds))
	}

	if p.c.EnablePredictorEnhancedScheduling {
		predictNumComputedPrefillTokens(
			clusterView.groupedInstanceViews[consts.InferTypePrefill], p.cmsClient.TTFTPredictor, int32(p.c.MaxNumBatchedTokens))
	}
	for inferType, instanceViews := range clusterView.groupedInstanceViews {
		klog.V(3).Infof(
			"Calculating metrics for infer type: %s, instance count: %d", inferType, len(instanceViews))
		p.policyInternal.calculateMetrics(inferType, request, instanceViews)
	}

	if request.SchedulingMode == types.SchedulingModeNormal {
		if _, exists := clusterView.groupedInstanceViews[consts.InferTypeNeutral]; exists {
			neutral := p.executeSchedule(consts.InferTypeNeutral, clusterView)
			if neutral != nil {
				klog.V(4).Infof("Neutral instance selected: %s", neutral.GetInstanceId())
				selectedInstances = append(selectedInstances, []*instanceViewScheduling{neutral})
				if p.c.EnableInstanceStatusLocalAccount {
					p.cmsClient.AddRequestLocalAccount(
						neutral.cmsView, consts.InferTypeNeutral, numTokens,
						int32(neutral.schedulingCtx.prefixHitTokens), requestId, true)
				}
			} else {
				klog.V(4).Info("No neutral instance selected")
			}
		}
		return
	}

	if len(clusterView.groupedInstanceViews) == 1 {
		klog.V(4).Info(
			"Only one group of instance views but not Neutral type, return empty selected instances")
		return
	}

	var prefill *instanceViewScheduling
	needPrefill := request.SchedulingMode == types.SchedulingModePDBatch ||
		(request.SchedulingMode == types.SchedulingModePDStaged && request.SchedulingStage == consts.SchedulingStagePrefill)
	if needPrefill {
		prefill = p.executeSchedule(consts.InferTypePrefill, clusterView)
		if prefill == nil {
			klog.V(4).Info("No prefill instance selected, return")
		} else {
			if p.c.EnableInstanceStatusLocalAccount {
				p.cmsClient.AddRequestLocalAccount(
					prefill.cmsView, consts.InferTypePrefill, numTokens,
					int32(prefill.schedulingCtx.prefixHitTokens), requestId, true)
			}
		}
	}

	var decode *instanceViewScheduling
	needDecode := request.SchedulingMode == types.SchedulingModePDBatch ||
		(request.SchedulingMode == types.SchedulingModePDStaged && request.SchedulingStage == consts.SchedulingStageDecode)
	if needDecode {
		decode = p.executeSchedule(consts.InferTypeDecode, clusterView)
		if decode == nil {
			if p.c.EnableInstanceStatusLocalAccount && prefill != nil {
				p.cmsClient.RevertRequestPrefillLocalAccount(
					prefill.cmsView, numTokens, int32(prefill.schedulingCtx.prefixHitTokens), requestId)
			}
			klog.V(4).Info("No decode instance selected, return")
		} else {
			if p.c.EnableInstanceStatusLocalAccount {
				p.cmsClient.AddRequestLocalAccount(
					decode.cmsView, consts.InferTypeDecode, numTokens, int32(decode.schedulingCtx.prefixHitTokens),
					requestId, decode != prefill)
			}
		}
	}

	selectedInstances = append(selectedInstances,
		[]*instanceViewScheduling{prefill}, []*instanceViewScheduling{decode})
	return
}

// Execute the filters in order. If no suitable instance is found, set fallback=true
// for all filters and execute them again. Then, apply the selector across all
// available instances to choose the most suitable instance.
func (p *DispatchPolicy) executeSchedule(
	inferType consts.InferType,
	clusterView clusterViewScheduling,
) *instanceViewScheduling {
	var availableInstanceViews map[string]*instanceViewScheduling

	fallback := false
	availableInstanceViews = p.policyInternal.filter(
		inferType,
		clusterView.groupedInstanceViews[inferType],
		fallback)

	if len(availableInstanceViews) == 0 {
		klog.V(4).Info("No instances found without fallback, executing scheduling steps with fallback=true")
		fallback = true
		availableInstanceViews = p.policyInternal.filter(
			inferType,
			clusterView.groupedInstanceViews[inferType],
			fallback)
	}

	if len(availableInstanceViews) == 0 {
		klog.V(4).Info("No available instances after all steps, return nil")
		return nil
	}

	klog.V(4).Infof("Available instances count: %d, fallback: %v, instance IDs: %v",
		len(availableInstanceViews), fallback, maps.Keys(availableInstanceViews))

	selected := p.policyInternal.selectInstance(inferType, availableInstanceViews)
	if selected == nil {
		klog.V(4).Info("No instance selected by policy internal, return nil")
		return nil
	}
	klog.V(4).Infof("Instance selected by policy internal: %s", selected.GetInstanceId())

	return selected
}

type dispatchPolicyInternal interface {
	calculateMetrics(
		inferType consts.InferType,
		request *types.SchedulingRequest,
		instanceViews map[string]*instanceViewScheduling)
	filter(
		inferType consts.InferType,
		instanceViews map[string]*instanceViewScheduling,
		fallback bool) map[string]*instanceViewScheduling
	selectInstance(
		inferType consts.InferType,
		instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling
}

type baseDispatchPolicy map[consts.InferType]*inferTypeBaseDispatchPolicy

type inferTypeBaseDispatchPolicy struct {
	metrics               map[string]func() instanceSchedulingMetric // key: metricsName
	globalFilters         []globalFilter
	singleInstanceFilters []singleInstanceFilter
	selectors             dispatchSelector
}

func (p baseDispatchPolicy) calculateMetrics(
	inferType consts.InferType,
	request *types.SchedulingRequest,
	instanceViews map[string]*instanceViewScheduling) {

	calculateMetrics(request, instanceViews, p[inferType].metrics)
}

func (p baseDispatchPolicy) filter(
	inferType consts.InferType,
	instanceViews map[string]*instanceViewScheduling,
	fallback bool) map[string]*instanceViewScheduling {

	availableInstanceViews := filter(
		instanceViews,
		p[inferType].singleInstanceFilters,
		p[inferType].globalFilters,
		fallback)

	klog.V(4).Infof("BaseDispatchPolicy filter completed, available instances count: %d, instance IDs: %v",
		len(availableInstanceViews), maps.Keys(availableInstanceViews))

	return availableInstanceViews
}

func (p baseDispatchPolicy) selectInstance(
	inferType consts.InferType,
	instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling {

	selected := p[inferType].selectors.selectInstance(instanceViews)
	if selected != nil {
		klog.V(4).Infof("Instance selected: %s", selected.GetInstanceId())
	} else {
		klog.V(4).Info("No instance selected")
	}
	return selected
}
