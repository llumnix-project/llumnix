package schedule_policy

import (
	"llumnix/pkg/cms"
	"testing"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/consts"
)

func TestMetricBalanceSelector(t *testing.T) {
	srcCandidates := map[string]*instanceViewScheduling{
		"src1": {
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  60,
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
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  80,
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
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  40,
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
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  40,
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
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  20,
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
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  60,
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
		srcMetric:          consts.SchedulingMetricKVCacheUsageRatioProjected,
		dstMetric:          consts.SchedulingMetricKVCacheUsageRatioProjected,
		forceHigherToLower: true,
		balanceScope:       consts.RescheduleLoadBalanceScopeCluster,
	}

	config := newConfig()
	calculateMetrics(srcCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(config, selector.srcMetric),
	})

	calculateMetrics(dstCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(config, selector.dstMetric),
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
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  120,
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
		srcMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
		dstMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
	}

	config := newConfig()
	calculateMetrics(srcCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(config, selector.srcMetric),
	})

	calculateMetrics(dstCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(config, selector.dstMetric),
	})

	pairs := selector.selectPairs(srcCandidates, dstCandidates)
	assert.Equal(t, 0, len(pairs))

	srcCandidates["i2"] = &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				InstanceId:        "i2",
				NumTotalGpuTokens: 100,
				NumUsedGpuTokens:  80,
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
				NumTotalGpuTokens: 100,
				NumUsedGpuTokens:  60,
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
		selector.srcMetric: getSchedulingMetric(config, selector.srcMetric),
	})

	calculateMetrics(dstCandidates, map[string]func() instanceSchedulingMetric{
		selector.srcMetric: getSchedulingMetric(config, selector.dstMetric),
	})

	pairs = selector.selectPairs(srcCandidates, dstCandidates)
	assert.Equal(t, 1, len(pairs))
	assert.Equal(t, "i3", pairs[0].srcView.GetInstanceId())
	assert.Equal(t, "i1", pairs[0].dstView.GetInstanceId())
}
