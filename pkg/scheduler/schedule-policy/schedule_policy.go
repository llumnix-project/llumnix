package schedule_policy

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
	GetInferMode() string
}

type clusterView struct {
	groupedInstanceViews map[string]map[string]InstanceViewInterface
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
	groupedInstanceViews map[string]map[string]*instanceViewScheduling
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

type SchedulePolicy interface {
	// Name schedule policy name
	Name() string

	// Schedule attempts to acquire an instance for processing a new request.
	Schedule(*types.ScheduleRequest) error
}

func NewSchedulePolicy(
	policy string,
	config *options.SchedulerConfig,
	lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	if len(policy) == 0 {
		panic("create schedule policy exception, policy is empty.")
	}

	klog.Infof("create scheduler with policy: %v", policy)

	return NewDispatchPolicy(config, policy, lrsClient)
}

type DispatchPolicy struct {
	c                 *options.SchedulerConfig
	schedulePolicy    string
	cmsClient         *cms.CMSReadClient
	lrsClient         *lrs.LocalRealtimeStateClient
	clusterViewClient ClusterViewClientInterface
	kvsClient         kvs.KVSClientInterface
	tokenHasher       *hasher.TokenHasher
	policyInternal    dispatchPolicyInternal
	clusterView       clusterView
}

func NewDispatchPolicy(
	c *options.SchedulerConfig, schedulePolicy string, lrsClient *lrs.LocalRealtimeStateClient) *DispatchPolicy {

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
			c.AllowConcurrentSchedule,
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
		schedulePolicy:    schedulePolicy,
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
	return p.schedulePolicy
}

func Uint32ToInt64(arr []uint32) []int64 {
	result := make([]int64, len(arr))
	for i, v := range arr {
		result[i] = int64(v)
	}
	return result
}

func (p *DispatchPolicy) Schedule(request *types.ScheduleRequest) error {
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

	if p.c.AllowConcurrentSchedule {
		p.clusterViewClient.RLock()
	} else {
		p.clusterViewClient.Lock()
	}
	defer func() {
		if p.c.AllowConcurrentSchedule {
			p.clusterViewClient.RUnlock()
		} else {
			p.clusterViewClient.Unlock()
		}
		metrics.AddLlumnixLatency(
			metrics.LlumnixMetricScheduleLatencyMicroseconds, metrics.Labels{}, time.Since(startTime).Microseconds())
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

	var schResults types.ScheduledResult
	for _, instance := range selectedInstances[0] {
		if instance == nil {
			continue
		}

		schResults = append(schResults, *instance.GetInstance())
		klog.V(4).Infof("Added token from instance: %s", instance.GetInstanceId())
		logSelectedInstance(instance, request.Id, consts.PrefillInferMode, p.c.EnableFullModeScheduling)
	}
	if len(selectedInstances) > 1 {
		for _, instance := range selectedInstances[1] {
			if instance == nil {
				continue
			}

			schResults = append(schResults, *instance.GetInstance())
			klog.V(4).Infof("Added token2 from instance: %s", instance.GetInstanceId())
			logSelectedInstance(instance, request.Id, consts.DecodeInferMode, p.c.EnableFullModeScheduling)
		}
	}
	request.ScheduleResult = schResults
	return nil
}

func (p *DispatchPolicy) schedule(
	request *types.ScheduleRequest,
	clusterView clusterViewScheduling) (selectedInstances [][]*instanceViewScheduling) {
	requestId := request.Id
	promptTokenIds := Uint32ToInt64(request.PromptTokenIds)
	selectedInstances = [][]*instanceViewScheduling{}
	if p.c.EnableCacheAwareScheduling && p.tokenHasher != nil && len(promptTokenIds) >= p.c.CacheAwareSchedulingMinTokens {
		// NOTE(sunbiao.sun): Calculating instance prompt cache hit len has ms-level latency, but it does not r/w
		// the raw instance view, so we unlock and re-lock here to improve schedule throughput
		// when not allowing concurrent schedule.
		if !p.c.AllowConcurrentSchedule {
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
		if !p.c.AllowConcurrentSchedule {
			p.cmsClient.Lock()
		}
	}

	numTokens := int32(0)
	if p.c.EnableInstanceStatusLocalAccount {
		numTokens = int32(len(promptTokenIds))
	}

	if p.c.EnablePredictorEnhancedScheduling {
		predictNumComputedPrefillTokens(
			clusterView.groupedInstanceViews[consts.PrefillInferMode], p.cmsClient.TTFTPredictor, int32(p.c.MaxNumBatchedTokens))
	}
	for inferMode, instanceViews := range clusterView.groupedInstanceViews {
		klog.V(3).Infof(
			"Calculating metrics for infer mode: %s, instance count: %d", inferMode, len(instanceViews))
		p.policyInternal.calculateMetrics(inferMode, request, instanceViews)
	}

	if request.ScheduleMode == types.ScheduleModeNormal {
		if _, exists := clusterView.groupedInstanceViews[consts.NormalInferMode]; exists {
			normal := p.executeSchedule(consts.NormalInferMode, clusterView)
			if normal != nil {
				klog.V(4).Infof("Normal instance selected: %s", normal.GetInstanceId())
				selectedInstances = append(selectedInstances, []*instanceViewScheduling{normal})
				if p.c.EnableInstanceStatusLocalAccount {
					p.cmsClient.AddRequestLocalAccount(
						normal.cmsView, consts.NormalInferMode, numTokens,
						int32(normal.schedulingCtx.prefixHitTokens), requestId, true)
				}
			} else {
				klog.V(4).Info("No normal instance selected")
			}
		}
		return
	}

	if len(clusterView.groupedInstanceViews) == 1 {
		klog.V(4).Info(
			"Only one group of instance views but not Normal mode, return empty selected instances")
		return
	}

	var prefill *instanceViewScheduling
	needPrefill := request.ScheduleMode == types.ScheduleModePDBatch ||
		(request.ScheduleMode == types.ScheduleModePDStaged && request.ScheduleStage == types.ScheduleStagePrefill)
	if needPrefill {
		prefill = p.executeSchedule(consts.PrefillInferMode, clusterView)
		if prefill == nil {
			klog.V(4).Info("No prefill instance selected, return")
		} else {
			if p.c.EnableInstanceStatusLocalAccount {
				p.cmsClient.AddRequestLocalAccount(
					prefill.cmsView, consts.PrefillInferMode, numTokens,
					int32(prefill.schedulingCtx.prefixHitTokens), requestId, true)
			}
		}
	}

	var decode *instanceViewScheduling
	needDecode := request.ScheduleMode == types.ScheduleModePDBatch ||
		(request.ScheduleMode == types.ScheduleModePDStaged && request.ScheduleStage == types.ScheduleStageDecode)
	if needDecode {
		decode = p.executeSchedule(consts.DecodeInferMode, clusterView)
		if decode == nil {
			if p.c.EnableInstanceStatusLocalAccount && prefill != nil {
				p.cmsClient.RevertRequestPrefillLocalAccount(
					prefill.cmsView, numTokens, int32(prefill.schedulingCtx.prefixHitTokens), requestId)
			}
			klog.V(4).Info("No decode instance selected, return")
		} else {
			if p.c.EnableInstanceStatusLocalAccount {
				p.cmsClient.AddRequestLocalAccount(
					decode.cmsView, consts.DecodeInferMode, numTokens, int32(decode.schedulingCtx.prefixHitTokens),
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
	inferMode string,
	clusterView clusterViewScheduling,
) *instanceViewScheduling {
	var availableInstanceViews map[string]*instanceViewScheduling

	fallback := false
	availableInstanceViews = p.policyInternal.filter(
		inferMode,
		clusterView.groupedInstanceViews[inferMode],
		fallback)

	if len(availableInstanceViews) == 0 {
		klog.V(4).Info("No instances found without fallback, executing schedule steps with fallback=true")
		fallback = true
		availableInstanceViews = p.policyInternal.filter(
			inferMode,
			clusterView.groupedInstanceViews[inferMode],
			fallback)
	}

	if len(availableInstanceViews) == 0 {
		klog.V(4).Info("No available instances after all steps, return nil")
		return nil
	}

	klog.V(4).Infof("Available instances count: %d, fallback: %v, instance IDs: %v",
		len(availableInstanceViews), fallback, maps.Keys(availableInstanceViews))

	selected := p.policyInternal.selectInstance(inferMode, availableInstanceViews)
	if selected == nil {
		klog.V(4).Info("No instance selected by policy internal, return nil")
		return nil
	}
	klog.V(4).Infof("Instance selected by policy internal: %s", selected.GetInstanceId())

	return selected
}

type dispatchPolicyInternal interface {
	calculateMetrics(
		inferMode string,
		request *types.ScheduleRequest,
		instanceViews map[string]*instanceViewScheduling)
	filter(
		inferMode string,
		instanceViews map[string]*instanceViewScheduling,
		fallback bool) map[string]*instanceViewScheduling
	selectInstance(
		inferMode string,
		instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling
}

type baseDispatchPolicy map[string]*inferModeBaseDispatchPolicy

type inferModeBaseDispatchPolicy struct {
	metrics               map[string]func() instanceSchedulingMetric // key: metricsName
	globalFilters         []globalFilter
	singleInstanceFilters []singleInstanceFilter
	selectors             dispatchSelector
}

func (p baseDispatchPolicy) calculateMetrics(
	inferMode string,
	request *types.ScheduleRequest,
	instanceViews map[string]*instanceViewScheduling) {

	calculateMetrics(request, instanceViews, p[inferMode].metrics)
}

func (p baseDispatchPolicy) filter(
	inferMode string,
	instanceViews map[string]*instanceViewScheduling,
	fallback bool) map[string]*instanceViewScheduling {

	availableInstanceViews := filter(
		instanceViews,
		p[inferMode].singleInstanceFilters,
		p[inferMode].globalFilters,
		fallback)

	klog.V(4).Infof("BaseDispatchPolicy filter completed, available instances count: %d, instance IDs: %v",
		len(availableInstanceViews), maps.Keys(availableInstanceViews))

	return availableInstanceViews
}

func (p baseDispatchPolicy) selectInstance(
	inferMode string,
	instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling {

	selected := p[inferMode].selectors.selectInstance(instanceViews)
	if selected != nil {
		klog.V(4).Infof("Instance selected: %s", selected.GetInstanceId())
	} else {
		klog.V(4).Info("No instance selected")
	}
	return selected
}
