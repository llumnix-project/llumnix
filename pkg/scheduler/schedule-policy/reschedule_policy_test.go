package schedule_policy

import (
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"llumnix/cmd/config"
	"llumnix/pkg/cms"
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func NewReschedulePolicyPartial(c *options.SchedulerConfig) *ReschedulePolicy {
	rp := &ReschedulePolicy{
		c:                    c,
		cmsClient:            nil,
		rescheduleIntervalMs: c.RescheduleIntervalMs,
		grpcTimeoutSeconds:   5,
		stopChan:             make(chan bool),
	}

	if len(c.ReschedulePolicies) > 0 {
		polices := strings.Split(c.ReschedulePolicies, ",")
		for _, policy := range polices {
			rp.policies = append(rp.policies, newReschedulePolicyInternal(c, policy))
		}
	}

	if c.EnableAdaptivePD {
		rp.policies = append(rp.policies, newReschedulePolicyInternal(c,
			consts.ReschedulePolicyCleanUpDecodeRequestsOnPrefill))
		rp.policies = append(rp.policies, newReschedulePolicyInternal(c,
			consts.ReschedulePolicyAggregateDecodeRequestsOnPrefill))
		rp.policies = append(rp.policies, newReschedulePolicyInternal(c,
			consts.ReschedulePolicyEaseBusyDecodeWithFreePrefill))
	}

	return rp
}

func TestReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			RescheduleDecodeLoadMetric:  consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePrefillLoadMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:          "decode_load,prefill_failover,decode_failover,neutral_failover",
			RescheduleLoadBalanceScope:  consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	assert.Equal(t, 4, len(rp.policies))

	for _, policy := range rp.policies {
		typeName := reflect.TypeOf(policy).Elem().Name()

		if typeName == "decodeLoadBalanceReschedule" {
			continue
		}

		if typeName == "failoverReschedule" {
			failoverPolicy, ok := policy.(*failoverReschedule)
			assert.True(t, ok)
			assert.Contains(t,
				[]string{
					consts.PrefillInferMode,
					consts.DecodeInferMode,
					consts.NormalInferMode,
				},
				failoverPolicy.inferMode,
			)
			continue
		}

		t.Errorf("unexpected policy type: %s", typeName)
	}
}

func TestAdaptivePDReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			EnableAdaptivePD:            true,
			DispatchPrefillLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchDecodeLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadMetric:  consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePrefillLoadMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric: consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleLoadBalanceScope:  consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	assert.Equal(t, 3, len(rp.policies))

	allReschedulePolicies := sets.String{}
	for _, policy := range rp.policies {
		typeName := reflect.TypeOf(policy).Elem().Name()
		allReschedulePolicies.Insert(typeName)
	}

	assert.True(t, allReschedulePolicies.HasAll(
		"cleanUpDecodeRequestsOnPrefillReschedule",
		"aggregateDecodeRequestsOnPrefillReschedule",
		"easeBusyDecodeWithFreePrefillReschedule"))
}

func TestRescheduleLoop(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			RescheduleIntervalMs: 100,
		},
	}

	rp := &ReschedulePolicy{
		c:                    config,
		cmsClient:            nil,
		rescheduleIntervalMs: config.RescheduleIntervalMs,
		stopChan:             make(chan bool),
	}

	executionCount := 0
	patches := gomonkey.ApplyFunc((*ReschedulePolicy).reschedule,
		func(_ *ReschedulePolicy) []*reschedulePair {
			executionCount++
			return nil
		})
	defer patches.Reset()

	go rp.RescheduleLoop()

	time.Sleep(350 * time.Millisecond)

	rp.stopChan <- true

	// wait RescheduleLoop goroutine to finish
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
					Role:     consts.NormalInferMode,
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
					Role:     consts.NormalInferMode,
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
					Role:     consts.NormalInferMode,
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
					Role:     consts.NormalInferMode,
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
					Role:     consts.NormalInferMode,
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
					Role:     consts.PrefillInferMode,
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
					Role:     consts.PrefillInferMode,
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
					Role:     consts.PrefillInferMode,
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
					Role:     consts.PrefillInferMode,
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
					Role:     consts.DecodeInferMode,
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
					Role:     consts.DecodeInferMode,
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
					Role:     consts.DecodeInferMode,
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
					Role:     consts.DecodeInferMode,
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
					Role:     consts.DecodeInferMode,
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
					Role:     consts.DecodeInferMode,
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
					Role:     consts.DecodeInferMode,
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

func TestDecodeLoadBalanceReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			FailoverScope:                  consts.FailoverScopeNode,
			RescheduleDecodeLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold:  1.0,
			ReschedulePrefillLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:             consts.ReschedulePolicyDecodeLoad,
			RescheduleReqSelectRule:        consts.MigrationReqSelectRuleToken,
			RescheduleLoadBalanceThreshold: 0.7,
			RescheduleLoadBalanceScope:     consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	instanceViews := generateReschedulerInstances()

	migrationPairs := rp.getMigrationPairs(instanceViews)

	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-decode-2", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-decode-1", migrationPairs[0].dstView.GetInstanceId())
}

func TestNeutralLoadBalanceReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			FailoverScope:                  consts.FailoverScopeNode,
			RescheduleDecodeLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold:  1.0,
			ReschedulePrefillLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadThreshold: 1.0,
			ReschedulePolicies:             consts.ReschedulePolicyNeutralLoad,
			RescheduleLoadBalanceScope:     consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	instanceViews := generateReschedulerInstances()

	migrationPairs := rp.getMigrationPairs(instanceViews)

	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-neutral-2", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-neutral-1", migrationPairs[0].dstView.GetInstanceId())
}

func TestFailoverReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			FailoverScope:                 consts.FailoverScopeNode,
			InstanceStalenessSeconds:      100,
			RescheduleDecodeLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold: 1.0,
			ReschedulePrefillLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:            "prefill_failover,decode_failover,neutral_failover",
			RescheduleLoadBalanceScope:    consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

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

func TestCleanUpDecodeRequestsOnPrefillReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			DecodeComputeBoundBatchSize:   10,
			FailoverScope:                 consts.FailoverScopeNode,
			InstanceStalenessSeconds:      100,
			DispatchPrefillLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchPrefillLoadThreshold:  1.0,
			DispatchDecodeLoadMetric:      consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchDecodeLoadThreshold:   1.0,
			RescheduleDecodeLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold: 1.0,
			ReschedulePrefillLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:            consts.ReschedulePolicyCleanUpDecodeRequestsOnPrefill,
			RescheduleLoadBalanceScope:    consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
					NodeId:       "node-1",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   0,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
					NodeId:       "node-2",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
					NodeId:       "node-3",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-4",
					InstanceType: "prefill",
					NodeId:       "node-4",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-x1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-x1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   4,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-x1",
					InstanceType: "prefill",
					NodeId:       "node-3",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					SchedulerRunningToDecodeRequestsNum:   3,
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
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
		"instance-decode-x1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-x1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-x1",
					InstanceType: "decode",
					NodeId:       "node-7",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instanceView := range instanceViews {
		instanceView.InstanceViewInterface = instanceView.cmsView
	}

	migrationPairs := rp.getMigrationPairs(instanceViews)
	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-prefill-1", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-decode-1", migrationPairs[0].dstView.GetInstanceId())
}

func TestAggregateDecodeRequestsOnPrefillReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			DecodeComputeBoundBatchSize:   10,
			FailoverScope:                 consts.FailoverScopeNodeUnit,
			InstanceStalenessSeconds:      100,
			DispatchDecodeLoadMetric:      consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchDecodeLoadThreshold:   1.0,
			RescheduleDecodeLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold: 1.0,
			ReschedulePrefillLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:            consts.ReschedulePolicyAggregateDecodeRequestsOnPrefill,
			RescheduleLoadBalanceScope:    consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
					NodeId:       "node-1",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   2,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
					NodeId:       "node-2",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
					NodeId:       "node-3",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-4",
					InstanceType: "prefill",
					NodeId:       "node-4",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-5": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-5",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   12,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-5",
					InstanceType: "prefill",
					NodeId:       "node-5",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-6": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-6",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   0,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-6",
					InstanceType: "prefill",
					NodeId:       "node-6",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-x1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-x1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   4,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-x1",
					InstanceType: "prefill",
					NodeId:       "node-3",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-x2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill-x2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   1,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-x2",
					InstanceType: "prefill",
					NodeId:       "node-3",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-1",
					InstanceType: "decode",
					NodeId:       "node-7",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instanceView := range instanceViews {
		instanceView.InstanceViewInterface = instanceView.cmsView
	}

	migrationPairs := rp.getMigrationPairs(instanceViews)
	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-prefill-2", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-prefill-1", migrationPairs[0].dstView.GetInstanceId())
}

func TestEaseBusyDecodeWithFreePrefillReschedulePolicy(t *testing.T) {
	config := &options.SchedulerConfig{
		ScheduleBaseConfig: config.ScheduleBaseConfig{
			EnableFullModeScheduling: true,
		},
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			DecodeComputeBoundBatchSize:   10,
			FailoverScope:                 consts.FailoverScopeNodeUnit,
			InstanceStalenessSeconds:      100,
			DispatchPrefillLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchPrefillLoadThreshold:  1.0,
			DispatchDecodeLoadMetric:      consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchDecodeLoadThreshold:   1.0,
			RescheduleDecodeLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold: 1.0,
			ReschedulePrefillLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:   consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:            consts.ReschedulePolicyEaseBusyDecodeWithFreePrefill,
			RescheduleLoadBalanceScope:    consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)

	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:         "instance-prefill-1",
					NumWaitingRequests: 0,
					NumRunningRequests: 0,
					Schedulable:        true,
					TimestampMs:        time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
					NodeId:       "node-1",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:         "instance-prefill-2",
					NumWaitingRequests: 1,
					NumRunningRequests: 0,
					Schedulable:        true,
					TimestampMs:        time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
					NodeId:       "node-2",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:         "instance-prefill-3",
					NumWaitingRequests: 0,
					NumRunningRequests: 0,
					Schedulable:        false,
					TimestampMs:        time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
					NodeId:       "node-3",
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
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:         "instance-prefill-4",
					NumWaitingRequests: 0,
					NumRunningRequests: 0,
					Schedulable:        true,
					TimestampMs:        0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-4",
					InstanceType: "prefill",
					NodeId:       "node-4",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-x1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:         "instance-prefill-x1",
					NumWaitingRequests: 0,
					NumRunningRequests: 0,
					Schedulable:        true,
					TimestampMs:        time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-x1",
					InstanceType: "prefill",
					NodeId:       "node-3",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-1",
					InstanceType: "decode",
					NodeId:       "node-7",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-2",
					InstanceType: "decode",
					NodeId:       "node-8",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 100,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-3",
					InstanceType: "decode",
					NodeId:       "node-9",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 10,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-4",
					InstanceType: "decode",
					NodeId:       "node-10",
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
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-5",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 110,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-5",
					InstanceType: "decode",
					NodeId:       "node-11",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode-x1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
					Role:     consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode-x1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      50,
					NumUncomputedTokensAllWaitingPrefills: 200,
					SchedulerRunningToDecodeRequestsNum:   3,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode-x1",
					InstanceType: "decode",
					NodeId:       "node-9",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instanceView := range instanceViews {
		instanceView.InstanceViewInterface = instanceView.cmsView
	}

	migrationPairs := rp.getMigrationPairs(instanceViews)
	assert.Equal(t, 1, len(migrationPairs))
	assert.Equal(t, "instance-decode-5", migrationPairs[0].srcView.GetInstanceId())
	assert.Equal(t, "instance-prefill-1", migrationPairs[0].dstView.GetInstanceId())
}

// TestDecodeLoadBalanceReschedulePolicyUnitLoadBalance tests the decode load balancing reschedule policy
// when the scope is set to 'unit'. It verifies that pairs are only created within the same unit,
// and also accounts for the effects of pre-filtering like schedulabilityFilter and stalenessFilter.
func TestDecodeLoadBalanceReschedulePolicyUnitLoadBalance(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			// Key configuration: set the load balancing scope to 'unit'
			RescheduleLoadBalanceScope:     consts.RescheduleLoadBalanceScopeUnit,
			RescheduleDecodeLoadMetric:     consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleDecodeLoadThreshold:  1.0,
			ReschedulePrefillLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			RescheduleNeutralLoadMetric:    consts.SchedulingMetricKVCacheUsageRatioProjected,
			ReschedulePolicies:             consts.ReschedulePolicyDecodeLoad,
			RescheduleReqSelectRule:        consts.MigrationReqSelectRuleToken,
			RescheduleLoadBalanceThreshold: 0.0,
			InstanceStalenessSeconds:       100,
			FailoverScope:                  consts.FailoverScopeNode,
		},
	}
	rp := NewReschedulePolicyPartial(config)

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
