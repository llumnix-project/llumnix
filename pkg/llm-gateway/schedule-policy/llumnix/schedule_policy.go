package llumnix

import (
	"time"

	"golang.org/x/exp/maps"
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/cms"
	"easgo/pkg/llm-gateway/consts"
	kvs "easgo/pkg/llm-gateway/kvs"
	"easgo/pkg/llm-gateway/lrs"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/types"
	"easgo/pkg/llm-gateway/utils"
)

type InstanceViewInterface interface {
	GetInstance() *types.LLMWorker
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
	prefixHitLen                      int
	prefixHitNumBlocks                int
	prefixHitRatio                    float32
	prefixMissLen                     int
	prefixMissNumBlocks               int
	numComputedPrefillBlocksPredicted int32
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

type DispatchPolicy struct {
	c                 *options.Config
	schedulePolicy    string
	cmsClient         *cms.CMSReadClient
	lrsClient         *lrs.LocalRealtimeStateClient
	clusterViewClient ClusterViewClientInterface
	kvsClient         kvs.KVSClientInterface
	policyInternal    dispatchPolicyInternal
	schedulePipelines map[string]*schedulePipeline
	clusterView       clusterView
}

func NewDispatchPolicy(
	c *options.Config, schedulePolicy string, lrsClient *lrs.LocalRealtimeStateClient) *DispatchPolicy {

	verifyConfig(c)

	var cmsClient *cms.CMSReadClient
	var clusterViewClient ClusterViewClientInterface = nil
	if c.LlumnixConfig.EnableFullModeScheduling {
		client, err := cms.CreateOrGetClient(
			c.LlumnixConfig.CmsRedisHost,
			c.LlumnixConfig.CmsRedisPort,
			c.LlumnixConfig.CmsRedisUsername,
			c.LlumnixConfig.CmsRedisPassword,
			c.LlumnixConfig.CmsRedisSocketTimeout,
			c.LlumnixConfig.CmsRedisRetryTimes,
			c.LlumnixConfig.CmsPullStatusIntervalMs,
			c.LlumnixConfig.CmsPullMetadataIntervalMs,
			c.LlumnixConfig.AllowConcurrentSchedule,
			c.LlumnixConfig.EnableInstanceStatusLocalAccount,
			c.LlumnixConfig.EnableCacheAwareScheduling,
			c.LlumnixConfig.RequestLocalAccountStalenessSeconds,
			c.LlumnixConfig.CmsRecordMetricsInterval,
			c.LlumnixConfig.EnablePredictorEnhancedScheduling,
			c.LlumnixConfig.KvCacheBlockSize,
			c.LlumnixConfig.NumPredictorWarmupSamples)
		if err != nil {
			panic(err)
		}
		cmsClient = client
		clusterViewClient = cmsClient
	} else {
		clusterViewClient = lrsClient
	}

	var kvsClient kvs.KVSClientInterface
	if c.LlumnixConfig.EnableCacheAwareScheduling {
		client, err := kvs.CreateOrGetClient(
			c.LlumnixConfig.KvsBackend,
			c.LlumnixConfig.KvsMetadataServiceConfigPath,
			c.LlumnixConfig.KvsChunkSize,
			c.LlumnixConfig.KvsEnableSaveUnfullChunk,
			c.LlumnixConfig.KvsIrisMetaPrefix,
			c.LlumnixConfig.KvsVLLMBlockPrefix,
			c.LlumnixConfig.KvsRetryTimes,
			c.LlumnixConfig.KvsRetryIntervalMs,
			c.LlumnixConfig.KvsMetadataServiceDownDurationS,
			c.LlumnixConfig.KvsMetadataServiceRedisClusterHosts,
			c.LlumnixConfig.KvsMetadataServiceRedisClusterPassword,
			c.LlumnixConfig.KvsMetadataServiceHttpServerHost,
			c.LlumnixConfig.KvsMetadataServiceHttpServerPort)
		if err != nil {
			panic(err)
		}
		kvsClient = client
	}

	return &DispatchPolicy{
		c:                 c,
		schedulePolicy:    schedulePolicy,
		cmsClient:         cmsClient,
		lrsClient:         lrsClient,
		clusterViewClient: clusterViewClient,
		kvsClient:         kvsClient,
		policyInternal:    newDispatchPolicyInternal(c),
		schedulePipelines: newSchedulerPipeline(&c.LlumnixConfig),
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

	if p.c.LlumnixConfig.EnableFullModeScheduling {
		if !p.cmsClient.IsAlive() {
			klog.V(2).Info("CMS client is not alive, return ErrorCmsNotAvailable")
			return consts.ErrorCmsNotAvailable
		}
	}

	klog.V(4).Infof("GetToken request received, promptTokenIds length: %d", len(request.PromptTokenIds))

	startTime := time.Now()

	if p.c.LlumnixConfig.AllowConcurrentSchedule {
		p.clusterViewClient.RLock()
	} else {
		p.clusterViewClient.Lock()
	}
	defer func() {
		if p.c.LlumnixConfig.AllowConcurrentSchedule {
			p.clusterViewClient.RUnlock()
		} else {
			p.clusterViewClient.Unlock()
		}
		metrics.AddLlumnixLatency(
			metrics.LlumnixMetricScheduleLatencyMicroseconds, metrics.Labels{}, time.Since(startTime).Microseconds())
	}()

	if p.c.LlumnixConfig.EnableFullModeScheduling {
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
		logSelectedInstance(instance, request.Id, consts.PrefillInferMode, p.c.LlumnixConfig.EnableFullModeScheduling)
	}
	if len(selectedInstances) > 1 {
		for _, instance := range selectedInstances[1] {
			if instance == nil {
				continue
			}

			schResults = append(schResults, *instance.GetInstance())
			klog.V(4).Infof("Added token2 from instance: %s", instance.GetInstanceId())
			logSelectedInstance(instance, request.Id, consts.DecodeInferMode, p.c.LlumnixConfig.EnableFullModeScheduling)
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
	if p.c.LlumnixConfig.EnableCacheAwareScheduling {
		// NOTE(sunbiao.sun): Calculating instance prompt cache hit len has ms-level latency, but it does not r/w
		// the raw instance view, so we unlock and re-lock here to improve schedule throughput
		// when not allowing concurrent schedule.
		if !p.c.LlumnixConfig.AllowConcurrentSchedule {
			p.cmsClient.Unlock()
		}
		// Write prefixHitLen in scheduling ctx of instance view.
		calcInstancesPrefixCacheHitLen(
			p.kvsClient, p.cmsClient, promptTokenIds, clusterView.instanceViews, p.c.LlumnixConfig.KvCacheBlockSize)
		if !p.c.LlumnixConfig.AllowConcurrentSchedule {
			p.cmsClient.Lock()
		}
	}

	numBlocks := int32(0)
	if p.c.LlumnixConfig.EnableInstanceStatusLocalAccount {
		numBlocks =
			(int32(len(promptTokenIds)) + p.c.LlumnixConfig.KvCacheBlockSize - 1) / p.c.LlumnixConfig.KvCacheBlockSize
	}

	if p.c.LlumnixConfig.EnablePredictorEnhancedScheduling {
		predictNumComputedPrefillBlocks(
			clusterView.groupedInstanceViews[consts.PrefillInferMode], p.cmsClient.TTFTPredictor,
			p.c.LlumnixConfig.KvCacheBlockSize, int32(p.c.LlumnixConfig.MaxNumBatchedTokens))
	}
	for inferMode, instanceViews := range clusterView.groupedInstanceViews {
		klog.V(3).Infof(
			"Calculating metrics for infer mode: %s, instance count: %d", inferMode, len(instanceViews))
		p.policyInternal.calculateMetrics(inferMode, instanceViews)
	}

	if request.ScheduleMode == types.ScheduleModeNormal {
		if _, exists := clusterView.groupedInstanceViews[consts.NormalInferMode]; exists {
			normal := p.executeSchedulePipeline(
				p.schedulePipelines[consts.NormalInferMode], clusterView.groupedInstanceViews)
			if normal != nil {
				klog.V(4).Infof("Normal instance selected: %s", normal.GetInstanceId())
				selectedInstances = append(selectedInstances, []*instanceViewScheduling{normal})
				if p.c.LlumnixConfig.EnableInstanceStatusLocalAccount {
					p.cmsClient.AddRequestLocalAccount(
						normal.cmsView, consts.NormalInferMode, numBlocks,
						int32(normal.schedulingCtx.prefixHitNumBlocks), requestId, true)
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
	needPrefill := request.ScheduleMode == types.ScheduleModePDBatch || (request.ScheduleMode == types.ScheduleModePDStaged && request.InferStage == types.InferStagePrefill)
	if needPrefill {
		prefill = p.executeSchedulePipeline(p.schedulePipelines[consts.PrefillInferMode], clusterView.groupedInstanceViews)
		if prefill == nil {
			klog.V(4).Info("No prefill instance selected, return")
		} else {
			if p.c.LlumnixConfig.EnableInstanceStatusLocalAccount {
				p.cmsClient.AddRequestLocalAccount(
					prefill.cmsView, consts.PrefillInferMode, numBlocks,
					int32(prefill.schedulingCtx.prefixHitNumBlocks), requestId, true)
			}
		}
	}

	var decode *instanceViewScheduling
	needDecode := request.ScheduleMode == types.ScheduleModePDBatch || (request.ScheduleMode == types.ScheduleModePDStaged && request.InferStage == types.InferStageDecode)
	if needDecode {
		decode = p.executeSchedulePipeline(p.schedulePipelines[consts.DecodeInferMode], clusterView.groupedInstanceViews)
		if decode == nil {
			klog.V(4).Info("No decode instance selected, return")
			if p.c.LlumnixConfig.EnableInstanceStatusLocalAccount && prefill != nil {
				p.cmsClient.RevertRequestPrefillLocalAccount(
					prefill.cmsView, numBlocks, int32(prefill.schedulingCtx.prefixHitNumBlocks), requestId)
			}
		} else {
			if p.c.LlumnixConfig.EnableInstanceStatusLocalAccount {
				p.cmsClient.AddRequestLocalAccount(
					decode.cmsView, consts.DecodeInferMode, numBlocks, int32(decode.schedulingCtx.prefixHitNumBlocks),
					requestId, decode != prefill)
			}
		}
	}

	selectedInstances = append(selectedInstances, []*instanceViewScheduling{prefill}, []*instanceViewScheduling{decode})
	return
}

// Execute the filters of the scheduler step in order. If no suitable instance
// is found, set fallback=true for all filters and execute them again. Then, apply
// the selector across all available instances to choose the most suitable instance.
func (p *DispatchPolicy) executeSchedulePipeline(
	pipeline *schedulePipeline,
	groupedInstanceViews map[string]map[string]*instanceViewScheduling,
) *instanceViewScheduling {
	klog.V(4).Infof("Executing schedule pipeline for infer mode: %s", pipeline.inferMode)

	var availableInstanceViews map[string]*instanceViewScheduling
	var availableInstanceType string

	availableInstanceType, availableInstanceViews = p.executeScheduleSteps(
		pipeline, groupedInstanceViews, false)

	if len(availableInstanceViews) == 0 {
		klog.V(4).Info("No instances found without fallback, executing schedule steps with fallback=true")
		availableInstanceType, availableInstanceViews = p.executeScheduleSteps(
			pipeline, groupedInstanceViews, true)
	}

	if len(availableInstanceViews) == 0 {
		klog.V(4).Info("No available instances after all steps, return nil")
		return nil
	}
	klog.V(4).Infof("Available instances count: %d, instance IDs: %v",
		len(availableInstanceViews), maps.Keys(availableInstanceViews))

	selected := p.policyInternal.selectInstance(
		pipeline.inferMode, availableInstanceType, availableInstanceViews)
	if selected == nil {
		klog.V(4).Info("No instance selected by policy internal, return nil")
		return nil
	}
	klog.V(4).Infof("Instance selected by policy internal: %s", selected.GetInstanceId())

	return selected
}

func (p *DispatchPolicy) executeScheduleSteps(
	pipeline *schedulePipeline,
	groupedInstanceViews map[string]map[string]*instanceViewScheduling,
	fallback bool,
) (string, map[string]*instanceViewScheduling) {

	var availableInstanceViews map[string]*instanceViewScheduling
	var availableInstanceType string

	for i, schedulerStep := range pipeline.scheduleSteps {
		klog.V(4).Infof("Processing schedule step %d, instance type: %s, skipWhenFallback: %v",
			i, schedulerStep.instanceType, schedulerStep.skipWhenFallback)
		if fallback && schedulerStep.skipWhenFallback {
			klog.V(4).Infof("Skipping step %d due to fallback and skipWhenFallback=true", i)
			continue
		}

		availableInstanceType = schedulerStep.instanceType
		groupedInstanceInferMode := utils.TransformInstanceType2InferMode(schedulerStep.instanceType)
		availableInstanceViews = p.policyInternal.filter(
			pipeline.inferMode,
			schedulerStep.instanceType,
			groupedInstanceViews[groupedInstanceInferMode],
			fallback)

		if len(availableInstanceViews) > 0 {
			klog.V(4).Infof("Step %d, filtered available instances count: %d, instance IDs: %v",
				i, len(availableInstanceViews), maps.Keys(availableInstanceViews))
			break
		}
	}

	klog.V(4).Infof(
		"Execute schedule steps completed, fallback: %v, available instance type: %s, available instances: %v",
		fallback, availableInstanceType, maps.Keys(availableInstanceViews))

	return availableInstanceType, availableInstanceViews
}

type dispatchPolicyInternal interface {
	calculateMetrics(
		inferMode string,
		instanceViews map[string]*instanceViewScheduling)
	filter(
		inferMode string,
		instanceType string,
		instanceViews map[string]*instanceViewScheduling,
		fallback bool) map[string]*instanceViewScheduling
	selectInstance(
		inferMode string,
		instanceType string,
		instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling
}

type baseDispatchPolicy map[string]*inferModeBaseDispatchPolicy

type inferModeBaseDispatchPolicy struct {
	metrics               map[string]func() instanceSchedulingMetric // key: metricsName
	globalFilters         []globalFilter
	singleInstanceFilters map[string][]singleInstanceFilter // key: instanceType
	selectors             map[string]dispatchSelector       // key: instanceType
}

func (p baseDispatchPolicy) calculateMetrics(
	inferMode string,
	instanceViews map[string]*instanceViewScheduling) {

	calculateMetrics(instanceViews, p[inferMode].metrics)
}

func (p baseDispatchPolicy) filter(
	inferMode string,
	instanceType string,
	instanceViews map[string]*instanceViewScheduling,
	fallback bool) map[string]*instanceViewScheduling {

	availableInstanceViews := filter(
		instanceViews,
		p[inferMode].singleInstanceFilters[instanceType],
		p[inferMode].globalFilters,
		fallback)

	klog.V(4).Infof("BaseDispatchPolicy filter completed, available instances count: %d, instance IDs: %v",
		len(availableInstanceViews), maps.Keys(availableInstanceViews))
	return availableInstanceViews
}

func (p baseDispatchPolicy) selectInstance(
	inferMode string,
	instanceType string,
	instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling {

	selected := p[inferMode].selectors[instanceType].selectInstance(instanceViews)
	if selected != nil {
		klog.V(4).Infof("Instance selected: %s", selected.GetInstanceId())
	} else {
		klog.V(4).Info("No instance selected")
	}
	return selected
}
