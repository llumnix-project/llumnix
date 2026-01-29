package llumnix

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/llm-gateway/consts"
)

func TestBaseMetric(t *testing.T) {
	metric := &baseMetric{
		name:  "test",
		value: 37.0,
	}
	assert.Equal(t, "test", metric.GetName())
	assert.Equal(t, float32(37.0), metric.GetValue())
}

func TestKVBlocksRatioWithAllPrefillsCalculate(t *testing.T) {
	metric := &kvBlocksRatioWithAllPrefills{
		baseMetric{
			name: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
		},
	}

	instances := genInstanceViewInternals()
	instance1 := instances["instance-1"]
	instance1.cmsView.Status.NumTotalGpuBlocks = 100
	instance1.cmsView.Status.NumUsedGpuBlocks = 30
	instance1.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills = 10

	// Test normal case
	metric.Calculate(instance1)
	assert.Equal(t, float32(0.4), metric.GetValue()) // (30 + 10) / 100 = 0.4

	// Test with zero total blocks
	instance2 := instances["instance-2"]
	instance2.cmsView.Status.NumTotalGpuBlocks = 0
	instance2.cmsView.Status.NumUsedGpuBlocks = 30
	metric.Calculate(instance2)
	assert.Equal(t, float32(3.4028235e+38), metric.GetValue()) // MaxFloat32

	// Test with zero used and waiting blocks
	instance3 := instances["instance-2"]
	instance3.cmsView.Status.NumTotalGpuBlocks = 100
	instance3.cmsView.Status.NumUsedGpuBlocks = 0
	instance3.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills = 0
	metric.Calculate(instance2)
	assert.Equal(t, float32(0.0), metric.GetValue()) // (0 + 0) / 100 = 0.0
}

func TestKVBlocksRatioWithAllPrefillsCalculateLess(t *testing.T) {
	metric1 := &kvBlocksRatioWithAllPrefills{
		baseMetric{
			name:  consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			value: 0.1,
		},
	}

	metric2 := &kvBlocksRatioWithAllPrefills{
		baseMetric{
			name:  consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			value: 0.2,
		},
	}

	assert.True(t, metric1.ValueLess(metric2.GetValue()))
	assert.False(t, metric2.ValueLess(metric1.GetValue()))
}

func TestDecodeBatchSizeCalculate(t *testing.T) {
	metric := &decodeBatchSize{
		baseMetric{
			name: consts.SchedulingMetricDecodeBatchSize,
		},
	}

	instances := genInstanceViewInternals()
	instance1 := instances["instance-1"]
	instance1.cmsView.Status.NumTotalGpuBlocks = 100
	instance1.cmsView.Status.NumUsedGpuBlocks = 30
	instance1.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills = 10
	instance1.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 10

	metric.Calculate(instance1)
	assert.Equal(t, float32(10.0), metric.GetValue()) // (0 + 0) / 100 = 0.0
}

func TestDecodeBatchSizeLess(t *testing.T) {
	metric1 := &decodeBatchSize{
		baseMetric{
			name:  consts.SchedulingMetricDecodeBatchSize,
			value: 1.0,
		},
	}

	metric2 := &decodeBatchSize{
		baseMetric{
			name:  consts.SchedulingMetricDecodeBatchSize,
			value: 2.0,
		},
	}

	assert.True(t, metric1.ValueLess(metric2.GetValue()))
	assert.False(t, metric2.ValueLess(metric1.GetValue()))
}

func TestNumWaitingRequestsCalculate(t *testing.T) {
	metric := &numWaitingRequests{
		baseMetric{
			name: consts.SchedulingMetricNumWaitingRequests,
		},
	}

	instances := genInstanceViewInternals()
	instance1 := instances["instance-1"]
	instance1.cmsView.Status.NumTotalGpuBlocks = 100
	instance1.cmsView.Status.NumUsedGpuBlocks = 30
	instance1.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills = 10
	instance1.cmsView.Status.NumWaitingRequests = 10

	metric.Calculate(instance1)
	assert.Equal(t, float32(10.0), metric.GetValue())
}

func TestNumWaitingRequestsLess(t *testing.T) {
	metric1 := &numWaitingRequests{
		baseMetric{
			name:  consts.SchedulingMetricNumWaitingRequests,
			value: 1.0,
		},
	}

	metric2 := &numWaitingRequests{
		baseMetric{
			name:  consts.SchedulingMetricNumWaitingRequests,
			value: 2.0,
		},
	}

	assert.True(t, metric1.ValueLess(metric2.GetValue()))
	assert.False(t, metric2.ValueLess(metric1.GetValue()))
}

func TestNumRequestsCalculate(t *testing.T) {
	metric := &numRequests{
		baseMetric{
			name: consts.SchedulingMetricNumRequests,
		},
		true,
	}

	instances := genInstanceViewInternals()
	instance1 := instances["instance-1"]
	instance1.cmsView.Status.NumTotalGpuBlocks = 100
	instance1.cmsView.Status.NumUsedGpuBlocks = 30
	instance1.cmsView.Status.NumUncomputedBlocksAllWaitingPrefills = 10
	instance1.cmsView.Status.NumWaitingRequests = 10
	instance1.cmsView.Status.NumRunningRequests = 10

	metric.Calculate(instance1)
	assert.Equal(t, float32(20.0), metric.GetValue())
}

func TestNumRequestsLess(t *testing.T) {
	metric1 := &numRequests{
		baseMetric{
			name:  consts.SchedulingMetricNumRequests,
			value: 1.0,
		},
		true,
	}

	metric2 := &numWaitingRequests{
		baseMetric{
			name:  consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			value: 2.0,
		},
	}

	assert.True(t, metric1.ValueLess(metric2.GetValue()))
	assert.False(t, metric2.ValueLess(metric1.GetValue()))
}

func TestKVCacheHitLen(t *testing.T) {
	metric := &kvCacheHitLen{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricKVCacheHitLen,
		},
	}

	instances := genInstanceViewInternals()

	// Test normal case
	instance1 := instances["instance-1"]
	instance1.schedulingCtx.prefixHitLen = 100
	metric.Calculate(instance1)
	assert.Equal(t, float32(100), metric.GetValue())

	// Test another case with different prefixHitLen
	instance2 := instances["instance-2"]
	instance2.schedulingCtx.prefixHitLen = 50
	metric.Calculate(instance2)
	assert.Equal(t, float32(50), metric.GetValue())

	// Test Less function
	t.Run("test Less function", func(t *testing.T) {
		metric1 := &kvCacheHitLen{
			baseMetric: baseMetric{
				name:  consts.SchedulingMetricKVCacheHitLen,
				value: 100,
			},
		}

		metric2 := &kvCacheHitLen{
			baseMetric: baseMetric{
				name:  consts.SchedulingMetricKVCacheHitLen,
				value: 50,
			},
		}

		// metric1(100) > metric2(50), so metric1 should be preferred
		assert.True(t, metric1.Less(metric2))
		// metric2(50) < metric1(100), so metric2 should not be preferred
		assert.False(t, metric2.Less(metric1))
	})

	// Test ValueLess function
	t.Run("test ValueLess function", func(t *testing.T) {
		metric := &kvCacheHitLen{
			baseMetric: baseMetric{
				value: 100,
			},
		}
		// 100 > 50, so 100 should be preferred over 50
		assert.True(t, metric.ValueLess(50))
		// 100 < 150, so 100 should not be preferred over 150
		assert.False(t, metric.ValueLess(150))
	})
}

func TestAdaptiveDecodeBatchSizeCalculate(t *testing.T) {
	metric := &adaptiveDecodeBatchSize{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
		},
		decodeComputeBoundBatchSize: 10,
	}

	instances := genInstanceViewInternals()
	instance1 := instances["instance-1"]
	instance1.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 0

	metric.Calculate(instance1)
	assert.Equal(t, float32(10), metric.GetValue())

	instance1.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 2
	metric.Calculate(instance1)
	assert.Equal(t, float32(8), metric.GetValue())

	instance1.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 10
	metric.Calculate(instance1)
	assert.Equal(t, float32(10), metric.GetValue())

	instance1.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 12
	metric.Calculate(instance1)
	assert.Equal(t, float32(12), metric.GetValue())
}

func TestAdaptiveDecodeBatchSizeLess(t *testing.T) {
	metric1 := &adaptiveDecodeBatchSize{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
		},
		decodeComputeBoundBatchSize: 10,
	}
	instances := genInstanceViewInternals()
	instance := instances["instance-1"]
	instance.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 0
	metric1.Calculate(instance)

	metric2 := &adaptiveDecodeBatchSize{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
		},
		decodeComputeBoundBatchSize: 10,
	}
	instance.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 2
	metric2.Calculate(instance)

	metric3 := &adaptiveDecodeBatchSize{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
		},
		decodeComputeBoundBatchSize: 10,
	}
	instance.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 8
	metric3.Calculate(instance)

	metric4 := &adaptiveDecodeBatchSize{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
		},
		decodeComputeBoundBatchSize: 10,
	}
	instance.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 10
	metric4.Calculate(instance)

	metric5 := &adaptiveDecodeBatchSize{
		baseMetric: baseMetric{
			name: consts.SchedulingMetricAdaptiveDecodeBatchSize,
		},
		decodeComputeBoundBatchSize: 10,
	}
	instance.cmsView.Status.SchedulerRunningToDecodeRequestsNum = 12
	metric5.Calculate(instance)

	assert.True(t, metric4.Less(metric5))
	assert.True(t, metric1.Less(metric5))
	assert.True(t, !metric1.Less(metric4))
	assert.True(t, !metric4.Less(metric1))
	assert.True(t, metric2.Less(metric1))
	assert.True(t, metric2.Less(metric4))
	assert.True(t, metric3.Less(metric2))
}
