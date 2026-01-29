package llumnix

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/llm-gateway/cms"
	"llumnix/pkg/llm-gateway/consts"
)

func TestMetricBalanceSelector(t *testing.T) {
	srcCandidates := map[string]*instanceViewScheduling{
		"src1": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  60,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "src1",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
		"src2": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  80,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "src2",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
		"src3": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  40,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "src3",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
	}
	for _, instance := range srcCandidates {
		instance.InstanceViewInterface = instance.cmsView
	}

	dstCandidates := map[string]*instanceViewScheduling{
		"dst1": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  40,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "dst1",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
		"dst2": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  20,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "dst2",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
		"dst3": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  60,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "dst3",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
	}
	for _, instance := range dstCandidates {
		instance.InstanceViewInterface = instance.cmsView
	}

	selector := &metricBalanceSelector{
		srcMetric:          consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
		dstMetric:          consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
		forceHigherToLower: true,
		balanceScope:       consts.RescheduleLoadBalanceScopeCluster,
	}

	config := newConfig()
	calculateMetrics(srcCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(&config.SchedulerConfig, selector.srcMetric),
	})

	calculateMetrics(dstCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(&config.SchedulerConfig, selector.dstMetric),
	})

	pairs := selector.selectPairs(srcCandidates, dstCandidates)

	// src3(40%) - dst3(60%) will be rejected
	assert.Equal(t, 2, len(pairs))
	// src2(80%) - dst2(20%)
	assert.Equal(t, float32(0.8), pairs[0].srcView.metrics[selector.srcMetric].GetValue())
	assert.Equal(t, float32(0.2), pairs[0].dstView.metrics[selector.dstMetric].GetValue())
	// src1(60%) - dst1(40%)
	assert.Equal(t, float32(0.6), pairs[1].srcView.metrics[selector.srcMetric].GetValue())
	assert.Equal(t, float32(0.4), pairs[1].dstView.metrics[selector.dstMetric].GetValue())
}

func TestAggregateSelector(t *testing.T) {
	srcCandidates := map[string]*instanceViewScheduling{
		"i1": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					InstanceId:        "i1",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  120,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "i1",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: make(map[string]instanceSchedulingMetric),
			},
		},
	}
	for _, instance := range srcCandidates {
		instance.InstanceViewInterface = instance.cmsView
	}

	dstCandidates := srcCandidates
	selector := &aggregateSelector{
		srcMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
		dstMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
	}

	config := newConfig()
	calculateMetrics(srcCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(&config.SchedulerConfig, selector.srcMetric),
	})

	calculateMetrics(dstCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(&config.SchedulerConfig, selector.dstMetric),
	})

	pairs := selector.selectPairs(srcCandidates, dstCandidates)
	assert.Equal(t, 0, len(pairs))

	srcCandidates["i2"] = &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				InstanceId:        "i2",
				NumTotalGpuBlocks: 100,
				NumUsedGpuBlocks:  80,
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId: "i2",
			},
		},
		schedulingCtx: schedulingCtx{
			metrics: make(map[string]instanceSchedulingMetric),
		},
	}

	srcCandidates["i3"] = &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				InstanceId:        "i3",
				NumTotalGpuBlocks: 100,
				NumUsedGpuBlocks:  60,
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId: "i3",
			},
		},
		schedulingCtx: schedulingCtx{
			metrics: make(map[string]instanceSchedulingMetric),
		},
	}

	for _, instance := range srcCandidates {
		instance.InstanceViewInterface = instance.cmsView
	}

	calculateMetrics(srcCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(&config.SchedulerConfig, selector.srcMetric),
	})

	calculateMetrics(dstCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(&config.SchedulerConfig, selector.dstMetric),
	})

	pairs = selector.selectPairs(srcCandidates, dstCandidates)
	assert.Equal(t, 1, len(pairs))
	assert.Equal(t, "i3", pairs[0].srcView.GetInstanceId())
	assert.Equal(t, "i1", pairs[0].dstView.GetInstanceId())
}
