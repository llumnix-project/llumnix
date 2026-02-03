package schedule_policy

import (
	"llumnix/pkg/cms"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func TestSchedulabilityFilter(t *testing.T) {
	filter := &schedulabilityFilter{}

	assert.False(t, filter.skipWhenFallback())

	// Test with schedulable instance
	schedulableInstance := &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				Schedulable: true,
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId: "instance-1",
			},
		},
	}
	schedulableInstance.InstanceViewInterface = schedulableInstance.cmsView
	assert.False(t, filter.instanceFilteredOut(schedulableInstance), "Schedulable instance should not be filtered out")
	assert.False(t, schedulableInstance.needsFailover, "Schedulable instance does not need failover.")

	// Test with unschedulable instance
	unschedulableInstance := &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				Schedulable: false,
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId: "instance-2",
			},
		},
	}
	unschedulableInstance.InstanceViewInterface = unschedulableInstance.cmsView
	assert.True(t, filter.instanceFilteredOut(unschedulableInstance), "Unschedulable instance should be filtered out")
	assert.True(t, unschedulableInstance.needsFailover, "Unschedulable instance need failover.")
}

func TestMetricBasedFilter(t *testing.T) {
	filter1 := &metricBasedFilter{
		metricName: consts.SchedulingMetricKVCacheUsageRatioProjected,
		threshold:  0.6,
	}

	assert.True(t, filter1.skipWhenFallback())

	instance1 := &instanceViewScheduling{
		cmsView: &cms.InstanceView{Metadata: &cms.InstanceMetadata{InstanceId: "instance-1"}},
		schedulingCtx: schedulingCtx{
			metrics: map[string]instanceSchedulingMetric{
				consts.SchedulingMetricKVCacheUsageRatioProjected: &kvCacheUsageRatioProjected{
					baseMetric{
						name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
						value: 0.5,
					},
				},
			},
			needsFailover: false,
		},
	}
	instance1.InstanceViewInterface = instance1.cmsView
	instance2 := &instanceViewScheduling{
		cmsView: &cms.InstanceView{Metadata: &cms.InstanceMetadata{InstanceId: "instance-2"}},
		schedulingCtx: schedulingCtx{
			metrics: map[string]instanceSchedulingMetric{
				consts.SchedulingMetricKVCacheUsageRatioProjected: &kvCacheUsageRatioProjected{
					baseMetric{
						name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
						value: 0.8,
					},
				},
			},
			needsFailover: false,
		},
	}
	instance2.InstanceViewInterface = instance2.cmsView
	instance3 := &instanceViewScheduling{
		cmsView: &cms.InstanceView{Metadata: &cms.InstanceMetadata{InstanceId: "instance-3"}},
		schedulingCtx: schedulingCtx{
			metrics: map[string]instanceSchedulingMetric{
				consts.SchedulingMetricKVCacheUsageRatioProjected: &kvCacheUsageRatioProjected{
					baseMetric{
						name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
						value: 0.3,
					},
				},
			},
			needsFailover: false,
		},
	}
	instance3.InstanceViewInterface = instance3.cmsView

	assert.False(t, filter1.instanceFilteredOut(instance1), "Instance with value 0.5 should not be filtered out (threshold 0.6)")
	assert.True(t, filter1.instanceFilteredOut(instance2), "Instance with value 0.8 should be filtered out (threshold 0.6)")
	assert.False(t, filter1.instanceFilteredOut(instance3), "Instance with value 0.3 should not be filtered out (threshold 0.6)")
}

func TestStalenessFilter(t *testing.T) {
	const instanceStalenessSeconds = 5
	filter := &stalenessFilter{instanceStalenessSeconds: instanceStalenessSeconds}
	assert.False(t, filter.skipWhenFallback())

	now := time.Now().UnixMilli()

	// Helper function to create test instance
	createInstance := func(timestamp int64) *instanceViewScheduling {
		result := &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					TimestampMs: timestamp,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "instance-x",
				},
			},
		}
		result.InstanceViewInterface = result.cmsView
		return result
	}

	// Test stale and unstale instances
	staleInstance := createInstance(now - 2*instanceStalenessSeconds*1000)
	unstaleInstance := createInstance(now)

	assert.True(t, filter.instanceFilteredOut(staleInstance), "Stale instance should be filtered out")
	assert.True(t, staleInstance.needsFailover, "Stale instance needs failover")

	assert.False(t, filter.instanceFilteredOut(unstaleInstance), "Unstale instance should not be filtered out")
	assert.False(t, unstaleInstance.needsFailover, "Unstale instance does not need failover")
}

func TestNeedsFailoverInstances(t *testing.T) {
	// 1. Define test configuration
	const instanceStalenessSeconds = 60
	filters := []singleInstanceFilter{
		&schedulabilityFilter{},
		&stalenessFilter{instanceStalenessSeconds: instanceStalenessSeconds},
	}

	// 2. Helper function to create test instances
	now := time.Now().UnixMilli()
	staleTimestamp := now - 2*instanceStalenessSeconds*1000

	createInstance := func(id string, schedulable bool, timestamp int64) *instanceViewScheduling {
		result := &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					InstanceId:  id,
					Schedulable: schedulable,
					TimestampMs: timestamp,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: id,
				},
			},
		}
		result.InstanceViewInterface = result.cmsView
		return result
	}

	// 3. Create test instances with different combinations of schedulability and staleness
	instances := []*instanceViewScheduling{
		createInstance("0", true, staleTimestamp),  // schedulableStale
		createInstance("1", true, now),             // schedulableUnstale
		createInstance("2", false, staleTimestamp), // unschedulableStale
		createInstance("3", false, now),            // unschedulableUnstale
	}

	// 4. Apply filters and collect filtered out instances
	filteredOutInstances := sets.NewString()
	for _, instance := range instances {
		for _, filter := range filters {
			if filter.instanceFilteredOut(instance) {
				filteredOutInstances.Insert(instance.GetInstanceId())
			}
		}
	}

	// 5. Verify results
	assert.Equal(t, sets.NewString("0", "2", "3"), filteredOutInstances)
}
func TestFailoverFilter(t *testing.T) {
	// Helper function to create test instances
	createInstance := func(instanceId, nodeId, unitId string, needsFailover bool, dataParallelSize int32) *instanceViewScheduling {
		return &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{InstanceId: instanceId},
				Metadata: &cms.InstanceMetadata{
					InstanceId:       instanceId,
					NodeId:           nodeId,
					UnitId:           unitId,
					DataParallelSize: dataParallelSize,
				},
			},
			schedulingCtx: schedulingCtx{needsFailover: needsFailover},
		}
	}

	// Test cases
	tests := []struct {
		name          string
		failoverScope string
		parallelSize  int32
		expected      []string
	}{
		{
			name:          "instance failover scope",
			failoverScope: consts.FailoverScopeInstance,
			parallelSize:  1,
			expected:      []string{"0"},
		},
		{
			name:          "node failover scope",
			failoverScope: consts.FailoverScopeNode,
			parallelSize:  1,
			expected:      []string{"0", "1", "2"},
		},
		{
			name:          "instance unit failover scope without data parallel",
			failoverScope: consts.FailoverScopeInstanceUnit,
			parallelSize:  1,
			expected:      []string{"0"},
		},
		{
			name:          "instance unit failover scope with data parallel",
			failoverScope: consts.FailoverScopeInstanceUnit,
			parallelSize:  2,
			expected:      []string{"0", "1", "3"},
		},
		{
			name:          "node unit failover scope without data parallel",
			failoverScope: consts.FailoverScopeNodeUnit,
			parallelSize:  1,
			expected:      []string{"0", "1", "2"},
		},
		{
			name:          "node unit failover scope with data parallel",
			failoverScope: consts.FailoverScopeNodeUnit,
			parallelSize:  2,
			expected:      []string{"0", "1", "2", "3", "4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instanceView0 := createInstance("0", "0", "0", true, tt.parallelSize)
			instanceView1 := createInstance("1", "0", "0", false, tt.parallelSize)
			instanceView2 := createInstance("2", "0", "1", false, tt.parallelSize)
			instanceView3 := createInstance("3", "1", "0", false, tt.parallelSize)
			instanceView4 := createInstance("4", "1", "1", false, tt.parallelSize)

			instanceViews := map[string]*instanceViewScheduling{
				"0": instanceView0,
				"1": instanceView1,
				"2": instanceView2,
				"3": instanceView3,
				"4": instanceView4,
			}

			// Run test
			filter := &failoverFilter{failoverScope: tt.failoverScope}
			result := filter.filterOutInstances(instanceViews)

			// Verify result
			assert.Equal(t, sets.NewString(tt.expected...), result)
		})
	}
}

func TestInferModeFilter(t *testing.T) {
	// Helper function to create test instance
	createInstance := func(instanceId string, inferMode string) *instanceViewScheduling {
		result := &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					InstanceId: instanceId,
				},
				Instance: &types.LLMInstance{
					Role: types.InferRole(inferMode),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: instanceId,
				},
			},
		}
		result.InstanceViewInterface = result.cmsView
		return result
	}

	tests := []struct {
		testName                     string
		targetInferMode              string
		expectedFilteredOutInstances []string
	}{
		{
			testName:                     "prefill infer mode filter",
			targetInferMode:              consts.PrefillInferMode,
			expectedFilteredOutInstances: []string{"1", "2"}, // decode and normal instances
		},
		{
			testName:                     "decode infer mode filter",
			targetInferMode:              consts.DecodeInferMode,
			expectedFilteredOutInstances: []string{"0", "2"}, // prefill and normal instances
		},
		{
			testName:                     "normal infer mode filter",
			targetInferMode:              consts.NormalInferMode,
			expectedFilteredOutInstances: []string{"0", "1"}, // prefill and decode instances
		},
	}

	// Create test instances
	instances := []*instanceViewScheduling{
		createInstance("0", consts.PrefillInferMode), // prefill instance
		createInstance("1", consts.DecodeInferMode),  // decode instance
		createInstance("2", consts.NormalInferMode),  // normal instance
	}

	for _, test := range tests {
		t.Run(test.testName, func(t *testing.T) {
			inferModeFilter := &inferModeFilter{targetInferMode: test.targetInferMode}
			assert.False(t, inferModeFilter.skipWhenFallback())
			filteredOutInstances := sets.NewString()
			for _, instance := range instances {
				if inferModeFilter.instanceFilteredOut(instance) {
					filteredOutInstances.Insert(instance.GetInstanceId())
				}
			}

			assert.Equal(t, sets.NewString(test.expectedFilteredOutInstances...), filteredOutInstances)
		})
	}
}

func TestFailoverMigrationSrcFilter(t *testing.T) {
	const instanceStalenessSeconds = 60

	// Helper function to create test instances
	createInstance := func(instanceId, nodeId, unitId string,
		needsFailover bool, dataParallelSize int32) *instanceViewScheduling {
		result := &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					InstanceId:  instanceId,
					Schedulable: !needsFailover,
					TimestampMs: time.Now().UnixMilli() - map[bool]int64{true: 2 * instanceStalenessSeconds * 1000, false: 0}[needsFailover],
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:       instanceId,
					NodeId:           nodeId,
					UnitId:           unitId,
					DataParallelSize: dataParallelSize,
				},
			},
			schedulingCtx: schedulingCtx{needsFailover: false},
		}
		result.InstanceViewInterface = result.cmsView
		return result
	}

	// Test cases
	tests := []struct {
		name          string
		failoverScope string
		parallelSize  int32
		expected      []string
	}{
		{
			name:          "instance failover scope",
			failoverScope: consts.FailoverScopeInstance,
			parallelSize:  1,
			expected:      []string{"1", "2", "3", "4"},
		},
		{
			name:          "node failover scope",
			failoverScope: consts.FailoverScopeNode,
			parallelSize:  1,
			expected:      []string{"3", "4"},
		},
		{
			name:          "unit failover scope without data parallel",
			failoverScope: consts.FailoverScopeNodeUnit,
			parallelSize:  1,
			expected:      []string{"3", "4"},
		},
		{
			name:          "unit failover scope with data parallel",
			failoverScope: consts.FailoverScopeNodeUnit,
			parallelSize:  2,
			expected:      []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instanceView0 := createInstance("0", "0", "0", true, tt.parallelSize)
			instanceView1 := createInstance("1", "0", "0", false, tt.parallelSize)
			instanceView2 := createInstance("2", "0", "1", false, tt.parallelSize)
			instanceView3 := createInstance("3", "1", "0", false, tt.parallelSize)
			instanceView4 := createInstance("4", "1", "1", false, tt.parallelSize)

			instanceViews := map[string]*instanceViewScheduling{
				"0": instanceView0,
				"1": instanceView1,
				"2": instanceView2,
				"3": instanceView3,
				"4": instanceView4,
			}

			// Run test
			filter := &failoverMigrationSrcFilter{failoverScope: tt.failoverScope, instanceStalenessSeconds: instanceStalenessSeconds}
			result := filter.filterOutInstances(instanceViews)

			// Verify result
			assert.Equal(t, sets.NewString(tt.expected...), result)
		})
	}
}

func TestInvertedSingleInstanceFilterWrapper(t *testing.T) {
	filter1 := &invertedSingleInstanceFilterWrapper{
		innerFilter: &metricBasedFilter{
			metricName: consts.SchedulingMetricKVCacheUsageRatioProjected,
			threshold:  1.0,
		},
	}
	assert.True(t, filter1.skipWhenFallback())

	instance1 := &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				InstanceId: "instance-1",
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId: "instance-1",
			},
		},
		schedulingCtx: schedulingCtx{
			metrics: map[string]instanceSchedulingMetric{
				consts.SchedulingMetricKVCacheUsageRatioProjected: &kvCacheUsageRatioProjected{
					baseMetric: baseMetric{
						name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
						value: 2.0,
					},
				},
			},
		},
	}
	instance1.InstanceViewInterface = instance1.cmsView

	instance2 := &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				InstanceId: "instance-2",
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId: "instance-2",
			},
		},
		schedulingCtx: schedulingCtx{
			metrics: map[string]instanceSchedulingMetric{
				consts.SchedulingMetricKVCacheUsageRatioProjected: &kvCacheUsageRatioProjected{
					baseMetric: baseMetric{
						name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
						value: 0.5,
					},
				},
			},
		},
	}
	instance2.InstanceViewInterface = instance2.cmsView

	assert.False(t, filter1.instanceFilteredOut(instance1))
	assert.True(t, filter1.instanceFilteredOut(instance2))
}
