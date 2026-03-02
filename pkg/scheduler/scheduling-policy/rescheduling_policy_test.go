package scheduling_policy

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	"llumnix/cmd/config"
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/cms"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func NewReschedulingPolicyPartial(c *options.SchedulerConfig) *ReschedulingPolicy {
	rp := &ReschedulingPolicy{
		c:                    c,
		cmsClient:            nil,
		reschedulingIntervalMs: c.ReschedulingIntervalMs,
		grpcTimeoutSeconds:   5,
		stopChan:             make(chan bool),
	}

	if len(c.ReschedulingPolicies) > 0 {
		polices := strings.Split(c.ReschedulingPolicies, ",")
		for _, policy := range polices {
			rp.policies = append(rp.policies, newReschedulingPolicyInternal(c, policy))
		}
	}

	if c.EnableAdaptivePD {
		rp.policies = append(rp.policies, newReschedulingPolicyInternal(c,
			consts.ReschedulingPolicyCleanUpDecodeRequestsOnPrefill))
		rp.policies = append(rp.policies, newReschedulingPolicyInternal(c,
			consts.ReschedulingPolicyAggregateDecodeRequestsOnPrefill))
		rp.policies = append(rp.policies, newReschedulingPolicyInternal(c,
			consts.ReschedulingPolicyEaseBusyDecodeWithFreePrefill))
	}

	return rp
}

func TestReschedulingPolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			ReschedulingDecodeLoadMetric:  consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingPrefillLoadMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingNeutralLoadMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingPolicies:          "decode_load,prefill_failover,decode_failover,neutral_failover",
			ReschedulingLoadBalanceScope:  consts.ReschedulingLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulingPolicyPartial(config)

	assert.Equal(t, 4, len(rp.policies))

	for _, policy := range rp.policies {
		typeName := reflect.TypeOf(policy).Elem().Name()

		if typeName == "decodeLoadBalanceRescheduling" {
			continue
		}

		if typeName == "failoverRescheduling" {
			failoverPolicy, ok := policy.(*failoverRescheduling)
			assert.True(t, ok)
			assert.Contains(t,
				[]consts.InferType{
					consts.InferTypePrefill,
					consts.InferTypeDecode,
					consts.InferTypeNeutral,
				},
				failoverPolicy.inferType,
			)
			continue
		}

		t.Errorf("unexpected policy type: %s", typeName)
	}
}

func TestReschedulingLoop(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			ReschedulingIntervalMs: 100,
		},
	}

	rp := &ReschedulingPolicy{
		c:                    config,
		cmsClient:            nil,
		reschedulingIntervalMs: config.ReschedulingIntervalMs,
		stopChan:             make(chan bool),
	}

	executionCount := 0
	patches := gomonkey.ApplyFunc((*ReschedulingPolicy).reschedule,
		func(_ *ReschedulingPolicy) []*reschedulingPair {
			executionCount++
			return nil
		})
	defer patches.Reset()

	go rp.ReschedulingLoop()

	time.Sleep(350 * time.Millisecond)

	rp.stopChan <- true

	// wait ReschedulingLoop goroutine to finish
	time.Sleep(100 * time.Millisecond)

	assert.GreaterOrEqual(t, executionCount, 2)
	assert.LessOrEqual(t, executionCount, 4)
}

func generateReschedulerInstances() map[string]*instanceViewScheduling {
	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-1",
					InstanceType: "neutral",
					NodeId:       "node-1",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-2",
					InstanceType: "neutral",
					NodeId:       "node-2",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 2,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-3",
					InstanceType: "neutral",
					NodeId:       "node-neutral-3",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-4": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 1,
					Schedulable:                           true,
					TimestampMs:                           0,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-4",
					InstanceType: "neutral",
					NodeId:       "node-neutral-4",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-x": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral-x",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-x",
					InstanceType: "neutral",
					NodeId:       "node-neutral-4",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
					NodeId:       "node-3",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
					NodeId:       "node-4",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           0,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
					NodeId:       "node-9",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-4": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 20,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-4",
					InstanceType: "prefill",
					NodeId:       "node-10",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-1",
					InstanceType: "decode",
					NodeId:       "node-5",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-2",
					InstanceType: "decode",
					NodeId:       "node-6",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 2,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-3",
					InstanceType: "decode",
					NodeId:       "node-7",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-4": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 1,
					Schedulable:                           true,
					TimestampMs:                           0,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-4",
					InstanceType: "decode",
					NodeId:       "node-8",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-5": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-5",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-5",
					InstanceType: "decode",
					NodeId:       "node-100",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-6": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-6",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 60,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-6",
					InstanceType: "decode",
					NodeId:       "node-101",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-x": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-x",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 5,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-x",
					InstanceType: "decode",
					NodeId:       "node-8",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}
	return instanceViews
}

func TestDecodeLoadBalanceReschedulingPolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			FailoverScope:                    consts.FailoverScopeNode,
			ReschedulingDecodeLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingDecodeLoadThreshold:  1.0,
			ReschedulingPrefillLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingNeutralLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingPolicies:             consts.ReschedulingPolicyDecodeLoad,
			ReschedulingReqSelectRule:        consts.MigrationReqSelectRuleToken,
			ReschedulingLoadBalanceThreshold: 0.7,
			ReschedulingLoadBalanceScope:     consts.ReschedulingLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulingPolicyPartial(config)

	instanceViews := generateReschedulerInstances()

	migrationPairs := rp.getMigrationPairs(instanceViews)

	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-decode-2", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-decode-1", migrationPairs[0].dstView.GetInstanceId())
}

func TestNeutralLoadBalanceReschedulingPolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			FailoverScope:                    consts.FailoverScopeNode,
			ReschedulingDecodeLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingDecodeLoadThreshold:  1.0,
			ReschedulingPrefillLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingNeutralLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingNeutralLoadThreshold: 1.0,
			ReschedulingPolicies:             consts.ReschedulingPolicyNeutralLoad,
			ReschedulingLoadBalanceScope:     consts.ReschedulingLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulingPolicyPartial(config)

	instanceViews := generateReschedulerInstances()

	migrationPairs := rp.getMigrationPairs(instanceViews)

	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-neutral-2", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-neutral-1", migrationPairs[0].dstView.GetInstanceId())
}

func TestFailoverReschedulingPolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			FailoverScope:                   consts.FailoverScopeNode,
			InstanceStalenessSeconds:        100,
			ReschedulingDecodeLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingDecodeLoadThreshold: 1.0,
			ReschedulingPrefillLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingNeutralLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingPolicies:            "prefill_failover,decode_failover,neutral_failover",
			ReschedulingLoadBalanceScope:    consts.ReschedulingLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulingPolicyPartial(config)

	instanceViews := generateReschedulerInstances()
	migrationPairs := rp.getMigrationPairs(instanceViews)

	sort.SliceStable(migrationPairs, func(i, j int) bool {
		return migrationPairs[i].srcView.GetInstanceId() < migrationPairs[j].srcView.GetInstanceId()
	})

	assert.Equal(t, 8, len(migrationPairs))

	assert.Equal(t, "instance-decode-3", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-decode-1", migrationPairs[0].dstView.GetInstanceId())
	assert.Equal(t, "instance-decode-4", migrationPairs[1].srcView.GetInstanceId())
	assert.Equal(t, "instance-decode-2", migrationPairs[1].dstView.GetInstanceId())
	assert.Equal(t, "instance-decode-x", migrationPairs[2].srcView.GetInstanceId())
	assert.Equal(t, "instance-decode-5", migrationPairs[2].dstView.GetInstanceId())
	assert.Equal(t, "instance-neutral-3", migrationPairs[3].srcView.GetInstanceId())
	assert.Equal(t, "instance-neutral-1", migrationPairs[3].dstView.GetInstanceId())
	assert.Equal(t, "instance-neutral-4", migrationPairs[4].srcView.GetInstanceId())
	assert.Equal(t, "instance-neutral-2", migrationPairs[4].dstView.GetInstanceId())
	assert.Equal(t, "instance-neutral-x", migrationPairs[5].srcView.GetInstanceId())
	assert.Equal(t, "instance-neutral-1", migrationPairs[5].dstView.GetInstanceId())
	assert.Equal(t, "instance-prefill-3", migrationPairs[6].srcView.GetInstanceId())
	assert.Equal(t, "instance-prefill-1", migrationPairs[6].dstView.GetInstanceId())
	assert.Equal(t, "instance-prefill-4", migrationPairs[7].srcView.GetInstanceId())
	assert.Equal(t, "instance-prefill-2", migrationPairs[7].dstView.GetInstanceId())
}

// TestDecodeLoadBalanceReschedulingPolicyUnitLoadBalance tests the decode load balancing rescheduling policy
// when the scope is set to 'unit'. It verifies that pairs are only created within the same unit,
// and also accounts for the effects of pre-filtering like schedulabilityFilter and stalenessFilter.
func TestDecodeLoadBalanceReschedulingPolicyUnitLoadBalance(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			// Key configuration: set the load balancing scope to 'unit'
			ReschedulingLoadBalanceScope:     consts.ReschedulingLoadBalanceScopeUnit,
			ReschedulingDecodeLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingDecodeLoadThreshold:  1.0,
			ReschedulingPrefillLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingNeutralLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulingPolicies:             consts.ReschedulingPolicyDecodeLoad,
			ReschedulingReqSelectRule:        consts.MigrationReqSelectRuleToken,
			ReschedulingLoadBalanceThreshold: 0.0,
			InstanceStalenessSeconds:         100,
			FailoverScope:                    consts.FailoverScopeNode,
		},
	}
	rp := NewReschedulingPolicyPartial(config)

	// 1. Get the basic instance data
	instanceViews := generateReschedulerInstances()

	// 2. Assign UnitIds to instances to create unit isolation scenarios.
	// We specifically choose instances that can pass the pre-filters (schedulability and staleness).
	// For example, instance-decode-4 (TimestampMs=0) and instance-decode-3 (Schedulable=false)
	// will be removed by the filters, so they will not participate in the subsequent unit pairing logic.
	//
	// - unit-1: instance-decode-2 (high load, NumUncomputedBlocks=100) and instance-decode-1 (low load, NumUncomputedBlocks=10)
	// - unit-2: instance-decode-6 (high load, NumUncomputedBlocks=60) and instance-decode-5 (low load, NumUncomputedBlocks=10)
	instanceViews["instance-decode-1"].cmsView.Metadata.UnitId = "unit-1"
	instanceViews["instance-decode-2"].cmsView.Metadata.UnitId = "unit-1"
	instanceViews["instance-decode-5"].cmsView.Metadata.UnitId = "unit-2"
	instanceViews["instance-decode-6"].cmsView.Metadata.UnitId = "unit-2"

	// 3. Execute the pairing logic
	migrationPairs := rp.getMigrationPairs(instanceViews)

	// 4. Sort the results for stable assertions
	sort.SliceStable(migrationPairs, func(i, j int) bool {
		return migrationPairs[i].srcView.GetInstanceId() < migrationPairs[j].srcView.GetInstanceId()
	})

	// 5. Perform assertions
	// Expect a total of 2 pairs, one for each unit.
	assert.Equal(t, 2, len(migrationPairs), "Should have 2 pairs, one for each unit")

	// Assert the pair for unit-1
	assert.Equal(t, "instance-decode-2", migrationPairs[0].srcView.GetInstanceId(),
		"Source of the first pair should be instance-decode-2 from unit-1")
	assert.Equal(t, "instance-decode-1", migrationPairs[0].dstView.GetInstanceId(),
		"Destination of the first pair should be instance-decode-1 from unit-1")

	// Assert the pair for unit-2
	assert.Equal(t, "instance-decode-6", migrationPairs[1].srcView.GetInstanceId(),
		"Source of the second pair should be instance-decode-6 from unit-2")
	assert.Equal(t, "instance-decode-5", migrationPairs[1].dstView.GetInstanceId(),
		"Destination of the second pair should be instance-decode-5 from unit-2")

	// 6. Negative assertion: ensure that instances removed by the pre-filters are indeed not in any pair.
	for _, pair := range migrationPairs {
		// instance-decode-3 is unschedulable (Schedulable: false)
		assert.NotEqual(t, "instance-decode-3", pair.srcView.GetInstanceId(),
			"Unschedulable instance-decode-3 should not be in any pair")
		assert.NotEqual(t, "instance-decode-3", pair.dstView.GetInstanceId(),
			"Unschedulable instance-decode-3 should not be in any pair")
		// instance-decode-4 is stale (TimestampMs: 0)
		assert.NotEqual(t, "instance-decode-4", pair.srcView.GetInstanceId(),
			"Stale instance-decode-4 should not be in any pair")
		assert.NotEqual(t, "instance-decode-4", pair.dstView.GetInstanceId(),
			"Stale instance-decode-4 should not be in any pair")
	}
}
