package llumnix

import (
	"fmt"
	"math"

	"k8s.io/klog/v2"

	"llumnix/cmd/llm-gateway/app/options"
	"llumnix/pkg/llm-gateway/consts"
)

func calculateMetrics(
	instances map[string]*instanceViewScheduling,
	metrics map[string]func() instanceSchedulingMetric) {
	for _, instanceView := range instances {
		for _, metricCtor := range metrics {
			metric := metricCtor()
			metricName := metric.GetName()
			// assume that the calculation results of all metrics are deterministic
			if _, ok := instanceView.schedulingCtx.metrics[metricName]; !ok {
				metric.Calculate(instanceView)
				instanceView.schedulingCtx.metrics[metricName] = metric
			}
		}
	}
}

type instanceSchedulingMetric interface {
	GetName() string
	GetValue() float32
	Calculate(instanceView *instanceViewScheduling)
	Less(metric instanceSchedulingMetric) bool
	ValueLess(value float32) bool
}

func getSchedulingMetric(p *options.SchedulerConfig, metricName string) func() instanceSchedulingMetric {
	klog.V(3).Infof("Getting scheduling metric factory for metric: %s", metricName)
	switch metricName {
	case consts.SchedulingMetricKVBlocksRatioWithAllPrefills:
		klog.V(3).Infof("Creating KVBlocksRatioWithAllPrefills metric factory")
		return func() instanceSchedulingMetric {
			return &kvBlocksRatioWithAllPrefills{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
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
	case consts.SchedulingMetricAllPrefillsKVBlocksNum:
		klog.V(3).Infof("Creating AllPrefillsKVBlocksNum metric factory")
		return func() instanceSchedulingMetric {
			return &AllPrefillsKVBlocksNum{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricAllPrefillsKVBlocksNum,
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
	case consts.SchedulingMetricCacheAwareAllPrefillsKVBlocksNum:
		klog.V(3).Infof("Creating CacheAwareAllPrefillsKVBlocksNum metric factory")
		return func() instanceSchedulingMetric {
			return &CacheAwareAllPrefillsKVBlocksNum{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricCacheAwareAllPrefillsKVBlocksNum,
				},
				allPrefillsKVBlocksNumMetric: AllPrefillsKVBlocksNum{
					baseMetric: baseMetric{
						name: consts.SchedulingMetricAllPrefillsKVBlocksNum,
					},
				},
			}
		}
	case consts.SchedulingMetricAdaptiveDecodeBatchSize:
		klog.V(3).Infof(
			"Creating AdaptiveDecodeBatchSize metric factory with decodeComputeBoundBatchSize: %d",
			p.DecodeComputeBoundBatchSize)
		return func() instanceSchedulingMetric {
			return &adaptiveDecodeBatchSize{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
				},
				decodeBatchSizeMetric: decodeBatchSize{
					baseMetric: baseMetric{
						name: consts.SchedulingMetricDecodeBatchSize,
					},
				},
				decodeComputeBoundBatchSize: p.DecodeComputeBoundBatchSize,
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
	case consts.SchedulingMetricAllDecodesKVBlocksNumWithAllPrefills:
		klog.V(3).Infof("Creating AllDecodesKVBlocksNumWithAllPrefills metric factory")
		return func() instanceSchedulingMetric {
			return &allDecodesKVBlocksNumWithAllPrefills{
				baseMetric: baseMetric{
					name: consts.SchedulingMetricAllDecodesKVBlocksNumWithAllPrefills,
				},
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

type kvBlocksRatioWithAllPrefills struct {
	baseMetric
}

func (br *kvBlocksRatioWithAllPrefills) Calculate(instanceView *instanceViewScheduling) {
	if instanceView.cmsView.Status.NumTotalGpuBlocks == 0 {
		br.value = float32(math.MaxFloat32)
		klog.V(3).Infof(
			"Instance %s has zero total GPU blocks, setting KVBlocksRatioWithAllPrefills to MaxFloat32: %f",
			instanceView.GetInstanceId(), br.value)
	} else {
		numUnallocatedBlocks := instanceView.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills +
			instanceView.cmsView.Status.NumUnallocatedBlocksSchedulerRunningPrefills +
			instanceView.cmsView.Status.NumUnallocatedBlocksHybridSchedulerWaitingDecodes
		// NOTE(sunbiao.sun): This metric is still not completely correct, because the prefill blocks statuses
		// are computation amount, but this metric requires allocation amount. But the error is small, thus acceptable.
		br.value = float32(instanceView.cmsView.Status.NumUsedGpuBlocks+
			numUnallocatedBlocks+
			instanceView.cmsView.NumBlocksInflightDispatchDecodeRequests) /
			float32(instanceView.cmsView.Status.NumTotalGpuBlocks)
		klog.V(3).Infof(
			"Instance %s KVBlocksRatioWithAllPrefills calculated: "+
				"(usedBlocks:%d + allWaitingPrefillsBlocks:%d + "+
				"schedulerRunningPrefillsBlocks:%d + hybridSchedulerWaitingDecodesBlocks:%d + "+
				"inflightDecodeBlocks:%d) / totalBlocks:%d = %f",
			instanceView.GetInstanceId(),
			instanceView.cmsView.Status.NumUsedGpuBlocks,
			instanceView.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills,
			instanceView.cmsView.Status.NumUnallocatedBlocksSchedulerRunningPrefills,
			instanceView.cmsView.Status.NumUnallocatedBlocksHybridSchedulerWaitingDecodes,
			instanceView.cmsView.NumBlocksInflightDispatchDecodeRequests,
			instanceView.cmsView.Status.NumTotalGpuBlocks,
			br.value)
	}
}

func (br *kvBlocksRatioWithAllPrefills) ValueLess(value float32) bool {
	return br.value < value
}

func (br *kvBlocksRatioWithAllPrefills) Less(metric instanceSchedulingMetric) bool {
	return br.value < metric.GetValue()
}

type decodeBatchSize struct {
	baseMetric
}

func (dbs *decodeBatchSize) Calculate(instanceView *instanceViewScheduling) {
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

func (nr *numWaitingRequests) Calculate(instanceView *instanceViewScheduling) {
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

type AllPrefillsKVBlocksNum struct {
	baseMetric
}

func (pb *AllPrefillsKVBlocksNum) Calculate(instanceView *instanceViewScheduling) {
	pb.value = float32(
		instanceView.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills +
			instanceView.cmsView.Status.NumUncomputedBlocksSchedulerRunningPrefills +
			instanceView.cmsView.NumUncomputedBlocksInflightDispatchPrefillRequests -
			instanceView.schedulingCtx.numComputedPrefillBlocksPredicted)
	klog.V(3).Infof(
		"Instance %s AllPrefillsKVBlocksNum calculated: "+
			"(allWaitingPrefillsBlocks:%d + "+
			"schedulerRunningPrefillsBlocks:%d + inflightDispatchPrefillBlocks:%d - "+
			"predictedComputedPrefillBlocks:%d) = %f",
		instanceView.GetInstanceId(),
		instanceView.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills,
		instanceView.cmsView.Status.NumUncomputedBlocksSchedulerRunningPrefills,
		instanceView.cmsView.NumUncomputedBlocksInflightDispatchPrefillRequests,
		instanceView.schedulingCtx.numComputedPrefillBlocksPredicted,
		pb.value)
}

func (pb *AllPrefillsKVBlocksNum) ValueLess(value float32) bool {
	return pb.value < value
}

func (pb *AllPrefillsKVBlocksNum) Less(metric instanceSchedulingMetric) bool {
	return pb.value < metric.GetValue()
}

type numRequests struct {
	baseMetric
	enableFullModeScheduling bool
}

func (nr *numRequests) Calculate(instanceView *instanceViewScheduling) {
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

func (hl *kvCacheHitLen) Calculate(instanceView *instanceViewScheduling) {
	// prefixHitLen is written when calculating the prompt cache locality for each instances before.
	hl.value = float32(instanceView.schedulingCtx.prefixHitLen)
	klog.V(3).Infof(
		"Instance %s KVCacheHitLen calculated: "+
			"(prefixHitLen:%d) = %f",
		instanceView.GetInstanceId(),
		instanceView.schedulingCtx.prefixHitLen,
		hl.value)
}

func (hl *kvCacheHitLen) ValueLess(value float32) bool {
	return hl.value > value
}

func (hl *kvCacheHitLen) Less(metric instanceSchedulingMetric) bool {
	return hl.value > metric.GetValue()
}

type CacheAwareAllPrefillsKVBlocksNum struct {
	baseMetric
	allPrefillsKVBlocksNumMetric AllPrefillsKVBlocksNum
}

func (cpb *CacheAwareAllPrefillsKVBlocksNum) Calculate(instanceView *instanceViewScheduling) {
	cpb.allPrefillsKVBlocksNumMetric.Calculate(instanceView)
	allPrefillsKVBlocksNum := cpb.allPrefillsKVBlocksNumMetric.GetValue()
	cpb.value = float32(instanceView.schedulingCtx.prefixMissNumBlocks) + allPrefillsKVBlocksNum
	klog.V(3).Infof(
		"Instance %s CacheAwareNumKVBlocksAllPrefills calculated: "+
			"(prefixMissBlocks:%d + allPrefillsBlocks:%f) = %f",
		instanceView.GetInstanceId(),
		instanceView.schedulingCtx.prefixMissNumBlocks,
		allPrefillsKVBlocksNum,
		cpb.value)
}

func (cpb *CacheAwareAllPrefillsKVBlocksNum) ValueLess(value float32) bool {
	return cpb.value < value
}

func (cpb *CacheAwareAllPrefillsKVBlocksNum) Less(metric instanceSchedulingMetric) bool {
	return cpb.value < metric.GetValue()
}

type adaptiveDecodeBatchSize struct {
	baseMetric
	decodeBatchSizeMetric       decodeBatchSize
	decodeComputeBoundBatchSize int32
}

func (adbs *adaptiveDecodeBatchSize) Calculate(instanceView *instanceViewScheduling) {
	adbs.decodeBatchSizeMetric.Calculate(instanceView)
	decodeBatchSize := int32(adbs.decodeBatchSizeMetric.GetValue())
	if decodeBatchSize >= adbs.decodeComputeBoundBatchSize {
		adbs.value = float32(decodeBatchSize)
		klog.V(3).Infof("Instance %s AdaptiveDecodeBatchSize: "+
			"decodeBatchSize:%d >= computeBoundBatchSize:%d, using actual decodeBatchSize: %f",
			instanceView.GetInstanceId(),
			decodeBatchSize,
			adbs.decodeComputeBoundBatchSize,
			adbs.value)
	} else {
		adbs.value = float32(
			adbs.decodeComputeBoundBatchSize - decodeBatchSize)
		klog.V(3).Infof("Instance %s AdaptiveDecodeBatchSize: "+
			"decodeBatchSize:%d < computeBoundBatchSize:%d, using difference between them: %f",
			instanceView.GetInstanceId(),
			decodeBatchSize,
			adbs.decodeComputeBoundBatchSize,
			adbs.value)
	}
}

func (adbs *adaptiveDecodeBatchSize) Less(metric instanceSchedulingMetric) bool {
	return adbs.value < metric.GetValue()
}

func (adbs *adaptiveDecodeBatchSize) ValueLess(value float32) bool {
	return adbs.value < value
}

type allDecodesKVBlocksNumWithAllPrefills struct {
	baseMetric
}

func (adb *allDecodesKVBlocksNumWithAllPrefills) Calculate(instanceView *instanceViewScheduling) {
	allDecodeBlocks := instanceView.cmsView.Status.HybridSchedulerWaitingToDecodeBlocksNum +
		instanceView.cmsView.Status.SchedulerWaitingToDecodeBlocksNum +
		instanceView.cmsView.Status.SchedulerRunningToDecodeBlocksNum +
		instanceView.cmsView.Status.NumBlocksLoadingRequests
	adb.value = float32(allDecodeBlocks +
		instanceView.cmsView.NumBlocksInflightDispatchDecodeRequests)
	klog.V(3).Infof(
		"Instance %s allDecodesKVBlocksNumWithAllPrefills calculated: "+
			"(hybridSchedulerWaitingToDecodesBlocks:%d + schedulerWaitingToDecodeBlocks:%d + "+
			"schedulerRunningToDecodesBlocks:%d + loadingBlocks:%d + inflightDecodesBlocks:%d = %f",
		instanceView.GetInstanceId(),
		instanceView.cmsView.Status.HybridSchedulerWaitingToDecodeBlocksNum,
		instanceView.cmsView.Status.SchedulerWaitingToDecodeBlocksNum,
		instanceView.cmsView.Status.SchedulerRunningToDecodeBlocksNum,
		instanceView.cmsView.Status.NumBlocksLoadingRequests,
		instanceView.cmsView.NumBlocksInflightDispatchDecodeRequests,
		adb.value)
}

func (br *allDecodesKVBlocksNumWithAllPrefills) ValueLess(value float32) bool {
	return br.value < value
}

func (br *allDecodesKVBlocksNumWithAllPrefills) Less(metric instanceSchedulingMetric) bool {
	return br.value < metric.GetValue()
}

type numTokens struct {
	baseMetric
}

func (nt *numTokens) Calculate(instanceView *instanceViewScheduling) {
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
