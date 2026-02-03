package schedule_policy

import (
	"llumnix/pkg/cms"
	"testing"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func genInstanceViewInternals() map[string]*instanceViewScheduling {
	instances := map[string]*instanceViewScheduling{
		"instance-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Role: consts.NormalInferMode,
				},
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
						baseMetric{
							name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
							value: 0.3,
						},
					},
				},
				needsFailover: false,
			},
		},
		"instance-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Role: consts.NormalInferMode,
				},
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
						baseMetric{
							name:  consts.SchedulingMetricKVCacheUsageRatioProjected,
							value: 0.5,
						},
					},
				},
				needsFailover: false,
			},
		},
		"instance-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Role: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId: "instance-3",
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "instance-3",
				},
			},
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
		},
	}
	for _, instance := range instances {
		instance.InstanceViewInterface = instance.cmsView
	}
	return instances
}

func TestMetricBasedSelectorSelectMax(t *testing.T) {
	instances := genInstanceViewInternals()

	// Test selector with selectMax = true (select highest value)
	selectorMax := &metricBasedSelector{
		topK:        1,
		metricNames: []string{consts.SchedulingMetricKVCacheUsageRatioProjected},
	}

	selected := selectorMax.best(instances)
	assert.NotNil(t, selected)
	assert.Equal(t, "instance-1", selected.GetInstanceId())
}

func TestMetricBasedSelectorRandomChoiceFromTopK(t *testing.T) {
	instances := genInstanceViewInternals()

	// Test selector with selectMax = true (select highest value)
	selector := &metricBasedSelector{
		topK:        2,
		metricNames: []string{consts.SchedulingMetricKVCacheUsageRatioProjected},
	}

	// Test multiple times to ensure randomness works
	selectedInstances := make(map[string]int)
	for i := 0; i < 100; i++ {
		selected := selector.randomChoiceFromTopK(instances)
		assert.NotNil(t, selected)
		selectedInstances[selected.GetInstanceId()]++
	}

	assert.Equal(t, 0, selectedInstances["instance-3"])
	assert.True(t, selectedInstances["instance-2"] > 0)
	assert.True(t, selectedInstances["instance-1"] > 0)
}

func TestFixedPreferenceSelectorSelectInstance(t *testing.T) {
	tests := []struct {
		name                 string
		lastSelectedInstance string
		instances            map[string]*instanceViewScheduling
		expectedInstance     string
		unexpectedInstance   string
	}{
		{
			name:                 "Select last instance when available",
			lastSelectedInstance: "instance1",
			instances: map[string]*instanceViewScheduling{
				"instance1": {cmsView: &cms.InstanceView{
					Status:   &cms.InstanceStatus{InstanceId: "instance1"},
					Metadata: &cms.InstanceMetadata{InstanceId: "instance1"}}},
				"instance2": {cmsView: &cms.InstanceView{
					Status:   &cms.InstanceStatus{InstanceId: "instance2"},
					Metadata: &cms.InstanceMetadata{InstanceId: "instance2"}}},
			},
			expectedInstance: "instance1",
		},
		{
			name:                 "Select new instance when last is not available",
			lastSelectedInstance: "instance3",
			instances: map[string]*instanceViewScheduling{
				"instance1": {cmsView: &cms.InstanceView{
					Status:   &cms.InstanceStatus{InstanceId: "instance1"},
					Metadata: &cms.InstanceMetadata{InstanceId: "instance1"}}},
				"instance2": {cmsView: &cms.InstanceView{
					Status:   &cms.InstanceStatus{InstanceId: "instance2"},
					Metadata: &cms.InstanceMetadata{InstanceId: "instance2"}}},
			},
			expectedInstance:   "instance2_or_instance1",
			unexpectedInstance: "instance3",
		},
		{
			name:                 "Return nil when no instances available",
			lastSelectedInstance: "instance1",
			instances:            map[string]*instanceViewScheduling{},
			expectedInstance:     "",
		},
	}

	for _, tt := range tests {
		for _, instance := range tt.instances {
			instance.InstanceViewInterface = instance.cmsView
		}
		t.Run(tt.name, func(t *testing.T) {
			selector := &fixedPreferenceSelector{lastSelectedInstanceId: tt.lastSelectedInstance}
			result := selector.selectInstance(tt.instances)

			if tt.expectedInstance == "" {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				if tt.unexpectedInstance != "" {
					assert.NotEqual(t, tt.unexpectedInstance, result.GetInstanceId())
				} else {
					assert.Equal(t, tt.expectedInstance, result.GetInstanceId())
				}
			}

			// Check if lastSelectedInstanceId is updated correctly
			if len(tt.instances) > 0 {
				assert.Contains(t, tt.instances, selector.lastSelectedInstanceId)
			}
		})
	}
}
