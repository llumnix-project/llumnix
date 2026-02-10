package schedule_policy

import (
	"fmt"
	"llumnix/pkg/types"
	"math"
	"time"

	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
)

func calculateMetrics(
	request *types.ScheduleRequest,
	instances map[string]*instanceViewScheduling,
	metrics map[string]func() instanceSchedulingMetric) {
	for _, instanceView := range instances {
		for _, metricCtor := range metrics {
			metric := metricCtor()
			metricName := metric.GetName()
			// assume that the calculation results of all metrics are deterministic
			if _, ok := instanceView.schedulingCtx.metrics[metricName]; !ok {
				metric.Calculate(request, instanceView)
				instanceView.schedulingCtx.metrics[metricName] = metric
			}
		}
	}
}

type instanceSchedulingMetric interface {
	GetName() string
	GetValue() float32
	Calculate(request *types.ScheduleRequest, instanceView *instanceViewScheduling)
	Less(metric instanceSchedulingMetric) bool
	ValueLess(value float32) bool
}

func getSchedulingMetric(p *options.SchedulerConfig, metricName string) func() instanceSchedulingMetric {
	klog.V(3).Infof("Getting scheduling metric factory for metric: %s", metricName)
	switch metricName {
	case consts.SchedulingMetricKVCacheUsageRatioProjected:
		klog.V(3).Infof("Creating KVCacheUsageRatioProjected metric factory")
		return func() instanceSchedulingMetric {
			return &kvCacheUsageRatioProjected{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricKVCacheUsageRatioProjected,
				},
			}
		}
	case consts.SchedulingMetricDecodeBatchSize:
		klog.V(3).Infof("Creating DecodeBatchSize metric factory")
		return func() instanceSchedulingMetric {
			return &decodeBatchSize{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricDecodeBatchSize,
				},
			}
		}
	case consts.SchedulingMetricNumWaitingRequests:
		klog.V(3).Infof("Creating NumWaitingRequests metric factory")
		return func() instanceSchedulingMetric {
			return &numWaitingRequests{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricNumWaitingRequests,
				},
			}
		}
	case consts.SchedulingMetricAllPrefillsTokensNum:
		klog.V(3).Infof("Creating allPrefillsTokensNum metric factory")
		return func() instanceSchedulingMetric {
			return &allPrefillsTokensNum{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricAllPrefillsTokensNum,
				},
			}
		}
	case consts.SchedulingMetricKVCacheHitLen:
		klog.V(3).Infof("Creating KVCacheHitLen metric factory")
		return func() instanceSchedulingMetric {
			return &kvCacheHitLen{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricKVCacheHitLen,
				},
			}
		}
	case consts.SchedulingMetricCacheAwareAllPrefillsTokensNum:
		klog.V(3).Infof("Creating CacheAwareAllPrefillsTokensNum metric factory")
		return func() instanceSchedulingMetric {
			return &CacheAwareAllPrefillsTokensNum{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricCacheAwareAllPrefillsTokensNum,
				},
				allPrefillsTokensNumMetric: allPrefillsTokensNum{
					baseMetric: baseMetric{
						name: consts.SchedulingMetricAllPrefillsTokensNum,
					},
				},
			}
		}
	case consts.SchedulingMetricNumRequests:
		klog.V(3).Infof("Creating NumRequests metric factory")
		return func() instanceSchedulingMetric {
			return &numRequests{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricNumRequests,
				},
				enableFullModeScheduling: p.EnableFullModeScheduling,
			}
		}
	case consts.SchedulingMetricAllDecodesTokensNum:
		klog.V(3).Infof("Creating AllDecodesTokensNum metric factory")
		return func() instanceSchedulingMetric {
			return &allDecodesTokensNum{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricAllDecodesTokensNum,
				},
			}
		}
	case consts.SchedulingMetricPredictedTtft:
		klog.V(3).Infof("Creating TtftLatency metric factory")
		return func() instanceSchedulingMetric {
			return &PredictedTtft{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricPredictedTtft,
				},
				latencyPredictor: GetLatencyPredictor(p.TtftProfilingDataPath, p.TpotProfilingDataPath),
			}
		}
	case consts.SchedulingMetricPredictedTpot:
		klog.V(3).Infof("Creating Itlatency metric factory")
		return func() instanceSchedulingMetric {
			return &PredictedTpot{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricPredictedTpot,
				},
				latencyPredictor: GetLatencyPredictor(p.TtftProfilingDataPath, p.TpotProfilingDataPath),
			}
		}
	case consts.SchedulingMetricNumTokens:
		klog.V(3).Infof("Creating NumTokens metric factory")
		return func() instanceSchedulingMetric {
			return &numTokens{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricNumTokens,
				},
			}
		}
	default:
		errMsg := fmt.Sprintf("Unknown scheduling metric: %s", metricName)
		klog.Errorf("Error creating scheduling metric: %s", errMsg)
		panic(errMsg)
	}
}

type baseMetric struct {
	name  string
	value float32
}

func (m *baseMetric) GetName() string {
	return m.name
}

func (m *baseMetric) GetValue() float32 {
	return m.value
}

func (m *baseMetric) String() string {
	return m.name + ":" + fmt.Sprintf("%.3f", m.value)
}

type kvCacheUsageRatioProjected struct {
	baseMetric
}

func (br *kvCacheUsageRatioProjected) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	if instanceView.cmsView.Status.NumTotalGpuTokens == 0 {
		br.value = float32(math.MaxFloat32)
		klog.V(3).Infof(
			"Instance %s has zero total GPU tokens, setting KVCacheUsageRatioProjected to MaxFloat32: %f",
			instanceView.GetInstanceId(), br.value)
	} else {
		numUnallocatedTokens := instanceView.cmsView.Status.NumUncomputedTokensAllWaitingPrefills +
			instanceView.cmsView.Status.NumUnallocatedTokensSchedulerRunningPrefills +
			instanceView.cmsView.Status.NumUnallocatedTokensHybridSchedulerWaitingDecodes
		// NOTE(sunbiao.sun): This metric is still not completely correct, because the prefill tokens statuses
		// are computation amount, but this metric requires allocation amount. But the error is small, thus acceptable.
		br.value = float32(instanceView.cmsView.Status.NumUsedGpuTokens+
			numUnallocatedTokens+
			instanceView.cmsView.NumTokensInflightDispatchDecodeRequests) /
			float32(instanceView.cmsView.Status.NumTotalGpuTokens)
		klog.V(3).Infof(
			"Instance %s KVCacheUsageRatioProjected calculated: "+
				"(usedTokens:%d + allWaitingPrefillsTokens:%d + "+
				"schedulerRunningPrefillsTokens:%d + hybridSchedulerWaitingDecodesTokens:%d + "+
				"inflightDecodeTokens:%d) / totalTokens:%d = %f",
			instanceView.GetInstanceId(),
			instanceView.cmsView.Status.NumUsedGpuTokens,
			instanceView.cmsView.Status.NumUncomputedTokensAllWaitingPrefills,
			instanceView.cmsView.Status.NumUnallocatedTokensSchedulerRunningPrefills,
			instanceView.cmsView.Status.NumUnallocatedTokensHybridSchedulerWaitingDecodes,
			instanceView.cmsView.NumTokensInflightDispatchDecodeRequests,
			instanceView.cmsView.Status.NumTotalGpuTokens,
			br.value)
	}
}

func (br *kvCacheUsageRatioProjected) ValueLess(value float32) bool {
	return br.value < value
}

func (br *kvCacheUsageRatioProjected) Less(metric instanceSchedulingMetric) bool {
	return br.value < metric.GetValue()
}

type decodeBatchSize struct {
	baseMetric
}

func (dbs *decodeBatchSize) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	dbs.value = float32(instanceView.cmsView.Status.HybridSchedulerWaitingToDecodeRequestsNum +
		instanceView.cmsView.Status.NumLoadingRequests +
		instanceView.cmsView.Status.SchedulerWaitingToDecodeRequestsNum +
		instanceView.cmsView.Status.SchedulerRunningToDecodeRequestsNum +
		instanceView.cmsView.NumInflightDispatchDecodeRequests)
	klog.V(3).Infof(
		"Instance %s DecodeBatchSize calculated: "+
			"(hybridSchedulerWaitingToDecodes:%d + loadings:%d + schedulerWaitingToDecodes:%d + "+
			"schedulerRunningToDecodes:%d + inflightDecodes:%d) = %f",
		instanceView.GetInstanceId(),
		instanceView.cmsView.Status.HybridSchedulerWaitingToDecodeRequestsNum,
		instanceView.cmsView.Status.NumLoadingRequests,
		instanceView.cmsView.Status.SchedulerWaitingToDecodeRequestsNum,
		instanceView.cmsView.Status.SchedulerRunningToDecodeRequestsNum,
		instanceView.cmsView.NumInflightDispatchDecodeRequests,
		dbs.value)
}

func (dbs *decodeBatchSize) ValueLess(value float32) bool {
	return dbs.value < value
}

func (dbs *decodeBatchSize) Less(metric instanceSchedulingMetric) bool {
	return dbs.value < metric.GetValue()
}

type numWaitingRequests struct {
	baseMetric
}

func (nr *numWaitingRequests) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	nr.value = float32(instanceView.cmsView.Status.NumWaitingRequests +
		instanceView.cmsView.NumInflightDispatchRequests)
	klog.V(3).Infof(
		"Instance %s NumWaitingRequests calculated: "+
			"(waitings:%d + allInflights:%d) = %f",
		instanceView.GetInstanceId(),
		instanceView.cmsView.Status.NumWaitingRequests,
		instanceView.cmsView.NumInflightDispatchRequests,
		nr.value)
}

func (nr *numWaitingRequests) ValueLess(value float32) bool {
	return nr.value < value
}

func (nr *numWaitingRequests) Less(metric instanceSchedulingMetric) bool {
	return nr.value < metric.GetValue()
}

type allPrefillsTokensNum struct {
	baseMetric
}

func (pb *allPrefillsTokensNum) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	pb.value = float32(
		instanceView.cmsView.Status.NumUncomputedTokensAllWaitingPrefills +
			instanceView.cmsView.Status.NumUncomputedTokensSchedulerRunningPrefills +
			instanceView.cmsView.NumUncomputedTokensInflightDispatchPrefillRequests -
			instanceView.schedulingCtx.numComputedPrefillTokensPredicted)
	klog.V(3).Infof(
		"Instance %s allPrefillsTokensNum calculated: "+
			"(allWaitingPrefillsTokens:%d + "+
			"schedulerRunningPrefillsTokens:%d + inflightDispatchPrefillTokens:%d - "+
			"predictedComputedPrefillTokens:%d) = %f",
		instanceView.GetInstanceId(),
		instanceView.cmsView.Status.NumUncomputedTokensAllWaitingPrefills,
		instanceView.cmsView.Status.NumUncomputedTokensSchedulerRunningPrefills,
		instanceView.cmsView.NumUncomputedTokensInflightDispatchPrefillRequests,
		instanceView.schedulingCtx.numComputedPrefillTokensPredicted,
		pb.value)
}

func (pb *allPrefillsTokensNum) ValueLess(value float32) bool {
	return pb.value < value
}

func (pb *allPrefillsTokensNum) Less(metric instanceSchedulingMetric) bool {
	return pb.value < metric.GetValue()
}

type numRequests struct {
	baseMetric
	enableFullModeScheduling bool
}

func (nr *numRequests) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	if nr.enableFullModeScheduling {
		nr.value = float32(
			instanceView.cmsView.Status.NumWaitingRequests +
				instanceView.cmsView.Status.NumLoadingRequests +
				instanceView.cmsView.Status.NumRunningRequests +
				instanceView.cmsView.NumInflightDispatchPrefillRequests +
				instanceView.cmsView.NumInflightDispatchDecodeRequests)
		klog.V(3).Infof(
			"Instance %s NumRequests calculated: "+
				"(waitings:%d + loadings:%d + runnings:%d + inflightPrefills:%d + inflightDecodes:%d) = %f",
			instanceView.GetInstanceId(),
			instanceView.cmsView.Status.NumWaitingRequests,
			instanceView.cmsView.Status.NumLoadingRequests,
			instanceView.cmsView.Status.NumRunningRequests,
			instanceView.cmsView.NumInflightDispatchPrefillRequests,
			instanceView.cmsView.NumInflightDispatchDecodeRequests,
			nr.value)
	} else {
		nr.value = float32(instanceView.lrsView.NumRequests())
		klog.V(3).Infof("Instance %s NumRequests calculated: (numRequests:%d) = %f",
			instanceView.GetInstanceId(), instanceView.lrsView.NumRequests(), nr.value)
	}
}

func (nr *numRequests) ValueLess(value float32) bool {
	return nr.value < value
}

func (nr *numRequests) Less(metric instanceSchedulingMetric) bool {
	return nr.value < metric.GetValue()
}

type kvCacheHitLen struct {
	baseMetric
}

func (hl *kvCacheHitLen) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	// prefixHitTokens is written when calculating the prompt cache locality for each instances before.
	hl.value = float32(instanceView.schedulingCtx.prefixHitTokens)
	klog.V(3).Infof(
		"Instance %s KVCacheHitLen calculated: "+
			"(prefixHitTokens:%d) = %f",
		instanceView.GetInstanceId(),
		instanceView.schedulingCtx.prefixHitTokens,
		hl.value)
}

func (hl *kvCacheHitLen) ValueLess(value float32) bool {
	return hl.value > value
}

func (hl *kvCacheHitLen) Less(metric instanceSchedulingMetric) bool {
	return hl.value > metric.GetValue()
}

type CacheAwareAllPrefillsTokensNum struct {
	baseMetric
	allPrefillsTokensNumMetric allPrefillsTokensNum
}

func (cpb *CacheAwareAllPrefillsTokensNum) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	cpb.allPrefillsTokensNumMetric.Calculate(request, instanceView)
	allPrefillsTokensNum := cpb.allPrefillsTokensNumMetric.GetValue()
	cpb.value = float32(instanceView.schedulingCtx.prefixMissTokens) + allPrefillsTokensNum
	klog.V(3).Infof(
		"Instance %s CacheAwareAllPrefillsTokensNum calculated: "+
			"(prefixMissTokens:%d + allPrefillsTokens:%f) = %f",
		instanceView.GetInstanceId(),
		instanceView.schedulingCtx.prefixMissTokens,
		allPrefillsTokensNum,
		cpb.value)
}

func (cpb *CacheAwareAllPrefillsTokensNum) ValueLess(value float32) bool {
	return cpb.value < value
}

func (cpb *CacheAwareAllPrefillsTokensNum) Less(metric instanceSchedulingMetric) bool {
	return cpb.value < metric.GetValue()
}

type allDecodesTokensNum struct {
	baseMetric
}

func (adb *allDecodesTokensNum) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	allDecodeTokens := instanceView.cmsView.Status.HybridSchedulerWaitingToDecodeTokensNum +
		instanceView.cmsView.Status.SchedulerWaitingToDecodeTokensNum +
		instanceView.cmsView.Status.SchedulerRunningToDecodeTokensNum +
		instanceView.cmsView.Status.NumTokensLoadingRequests
	adb.value = float32(allDecodeTokens +
		instanceView.cmsView.NumTokensInflightDispatchDecodeRequests)
	klog.V(3).Infof(
		"Instance %s allDecodesTokensNum calculated: "+
			"(hybridSchedulerWaitingToDecodesTokens:%d + schedulerWaitingToDecodeTokens:%d + "+
			"schedulerRunningToDecodesTokens:%d + loadingTokens:%d + inflightDecodesTokens:%d = %f",
		instanceView.GetInstanceId(),
		instanceView.cmsView.Status.HybridSchedulerWaitingToDecodeTokensNum,
		instanceView.cmsView.Status.SchedulerWaitingToDecodeTokensNum,
		instanceView.cmsView.Status.SchedulerRunningToDecodeTokensNum,
		instanceView.cmsView.Status.NumTokensLoadingRequests,
		instanceView.cmsView.NumTokensInflightDispatchDecodeRequests,
		adb.value)
}

func (br *allDecodesTokensNum) ValueLess(value float32) bool {
	return br.value < value
}

func (br *allDecodesTokensNum) Less(metric instanceSchedulingMetric) bool {
	return br.value < metric.GetValue()
}

type PredictedTtft struct {
	baseMetric
	allPrefillsTokensNum
	decodeBatchSize
	allDecodesTokensNum

	latencyPredictor *LatencyPredictor
	latency          float64
}

func (tl *PredictedTtft) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	tl.allPrefillsTokensNum.Calculate(request, instanceView)
	tl.allDecodesTokensNum.Calculate(request, instanceView)
	tl.decodeBatchSize.Calculate(request, instanceView)

	tokensOnInstance := instanceView.cmsView.Status.NumUncomputedTokensAllWaitingPrefills +
		instanceView.cmsView.Status.NumUncomputedTokensSchedulerRunningPrefills

	existingTokensTtft, err := tl.latencyPredictor.predictTtftLatencyByChunkPrefill(
		tokensOnInstance,
		int32(tl.decodeBatchSize.GetValue()),
		int32(tl.allDecodesTokensNum.GetValue()),
		float64(instanceView.cmsView.Metadata.MaxNumBatchedTokens))

	if err != nil {
		klog.Warningf("Failed to predict TTFT latency: %v", err)
		tl.latency = math.Inf(1)
		return
	}

	now := time.Now().UnixMilli()
	elapsedTimeMs := now - instanceView.cmsView.Status.TimestampMs

	requestTokens := 0
	if request != nil {
		requestTokens = request.PromptNumTokens
	}

	if elapsedTimeMs > int64(existingTokensTtft) {
		// All prefill tokens that already exist in the engine may have been exhausted since Status.Ts.
		// In this case, use inflight + current req's tokens to estimate ttft.
		inflightPrefillTokens := instanceView.cmsView.NumUncomputedTokensInflightDispatchPrefillRequests
		tl.latency, err = tl.latencyPredictor.predictTtftLatencyByChunkPrefill(
			inflightPrefillTokens+int32(requestTokens),
			int32(tl.decodeBatchSize.GetValue()),
			int32(tl.allDecodesTokensNum.GetValue()),
			float64(instanceView.cmsView.Metadata.MaxNumBatchedTokens))
	} else {
		tl.latency, err = tl.latencyPredictor.predictTtftLatencyByChunkPrefill(
			int32(tl.allPrefillsTokensNum.GetValue())+int32(requestTokens),
			int32(tl.decodeBatchSize.GetValue()),
			int32(tl.allDecodesTokensNum.GetValue()),
			float64(instanceView.cmsView.Metadata.MaxNumBatchedTokens))

		tl.latency -= float64(elapsedTimeMs)
	}

	if err != nil {
		klog.Warningf("Failed to predict TTFT latency: %v", err)
		tl.latency = math.Inf(1)
	}

	klog.V(3).Infof(
		"Instance %s PredictedTtft calculated: "+
			"(allPrefillsTokens:%f + decodeBatchSize:%f + allDecodesTokens:%f) = %f",
		instanceView.GetInstanceId(),
		tl.allPrefillsTokensNum.GetValue(),
		tl.decodeBatchSize.GetValue(),
		tl.allDecodesTokensNum.GetValue(),
		tl.latency)
}

func (tl *PredictedTtft) ValueLess(value float32) bool {
	return tl.GetValue() < value
}

func (tl *PredictedTtft) Less(metric instanceSchedulingMetric) bool {
	return tl.GetValue() < metric.GetValue()
}

func (tl *PredictedTtft) GetValue() float32 {
	return float32(tl.latency)
}

type PredictedTpot struct {
	baseMetric
	allDecodesTokensNum
	decodeBatchSize

	latencyPredictor *LatencyPredictor
	latency          float64
}

func (il *PredictedTpot) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	il.allDecodesTokensNum.Calculate(request, instanceView)
	il.decodeBatchSize.Calculate(request, instanceView)

	var err error
	il.latency, err = il.latencyPredictor.predictTpotLatency(
		int32(il.decodeBatchSize.GetValue()+1),
		int32(il.allDecodesTokensNum.GetValue())+int32(request.PromptNumTokens))

	if err != nil {
		klog.Warningf("Failed to predict ITL latency: %v", err)
		il.latency = math.Inf(1)
	}

	klog.V(3).Infof(
		"Instance %s PredictedTpot calculated: (decodeBatchSize:%f + allDecodesTokens:%f) = %f",
		instanceView.GetInstanceId(),
		il.decodeBatchSize.GetValue(),
		il.allDecodesTokensNum.GetValue(),
		il.latency)
}

func (il *PredictedTpot) ValueLess(value float32) bool {
	return il.GetValue() < value
}

func (il *PredictedTpot) Less(metric instanceSchedulingMetric) bool {
	return il.GetValue() < metric.GetValue()
}

func (il *PredictedTpot) GetValue() float32 {
	return float32(il.latency)
}

type numTokens struct {
	baseMetric
}

func (nt *numTokens) Calculate(
	request *types.ScheduleRequest, instanceView *instanceViewScheduling) {
	nt.value = float32(instanceView.lrsView.NumTokens())
	klog.V(3).Infof("Instance %s NumTokens calculated: (numTokens:%d) = %f",
		instanceView.GetInstanceId(), instanceView.lrsView.NumTokens(), nt.value)
}

func (nt *numTokens) ValueLess(value float32) bool {
	return nt.value < value
}

func (nt *numTokens) Less(metric instanceSchedulingMetric) bool {
	return nt.value < metric.GetValue()
}
