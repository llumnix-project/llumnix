package llumnix

import (
	"fmt"
	"llumnix/cmd/llm-gateway/app/options"
	"llumnix/pkg/llm-gateway/consts"

	"k8s.io/klog/v2"
)

func newReschedulePolicyInternal(p *options.SchedulerConfig, policy string) reschedulePolicyInternal {
	switch policy {
	case consts.ReschedulePolicyNeutralLoad:
		return newLoadBalanceReschedule(p, consts.NormalInferMode)
	case consts.ReschedulePolicyDecodeLoad:
		return newLoadBalanceReschedule(p, consts.DecodeInferMode)
	case consts.ReschedulePolicyPrefillFailover:
		return newFailoverReschedule(p, consts.PrefillInferMode)
	case consts.ReschedulePolicyDecodeFailover:
		return newFailoverReschedule(p, consts.DecodeInferMode)
	case consts.ReschedulePolicyNeutralFailover:
		return newFailoverReschedule(p, consts.NormalInferMode)
	case consts.ReschedulePolicyCleanUpDecodeRequestsOnPrefill:
		return newCleanUpDecodeRequestsOnPrefillReschedule(p)
	case consts.ReschedulePolicyAggregateDecodeRequestsOnPrefill:
		return newAggregateDecodeRequestsOnPrefillReschedule(p)
	case consts.ReschedulePolicyEaseBusyDecodeWithFreePrefill:
		return newEaseBusyDecodeWithFreePrefillReschedule(p)
	default:
		panic(fmt.Sprintf("unsupported reschedule policy: %s", p.ReschedulePolicies))
	}
}

type decodeLoadBalanceReschedule struct {
	baseReschedulePolicy
	migrationReqSelectPolicy migrationReqSelectPolicy
	metric                   string
	kvCacheBlockSize         int32
	loadBalanceThreshold     float32
}

func (p *decodeLoadBalanceReschedule) selectPairs(
	srcInstanceViewInternal,
	dstInstanceViewInternal map[string]*instanceViewScheduling) []*reschedulePair {

	return p.acceptLoadImbalancePairs(
		p.selector.selectPairs(srcInstanceViewInternal, dstInstanceViewInternal))
}

func (p *decodeLoadBalanceReschedule) acceptLoadImbalancePairs(
	selectedPairs []*reschedulePair) (validatedPairs []*reschedulePair) {
	if p.loadBalanceThreshold <= 0 {
		return selectedPairs
	}
	for _, selectPair := range selectedPairs {
		srcLoad := selectPair.srcView.schedulingCtx.metrics[p.metric].GetValue()
		dstLoad := selectPair.dstView.schedulingCtx.metrics[p.metric].GetValue()
		if (srcLoad - dstLoad) < p.loadBalanceThreshold {
			klog.V(4).Infof("Migration pair (%s, %s) got rejected because "+
				"load diff is too small, metric: %v, values: (%v, %v), expected diff: %v, actual diff: %v",
				selectPair.srcView.GetInstanceId(), selectPair.dstView.GetInstanceId(),
				p.metric, srcLoad, dstLoad, p.loadBalanceThreshold, srcLoad-dstLoad)
			continue
		}
		validatedPairs = append(validatedPairs, selectPair)
	}
	return validatedPairs
}

func (p *decodeLoadBalanceReschedule) getMigrationReqSelectPolicy() migrationReqSelectPolicy {
	return p.migrationReqSelectPolicy
}

/*
LoadBalanceReschedule enables load balancing among instances by redistributing
workload from overloaded instances to underutilized ones.

Instance Type:
  - Source: Any
  - Destination: Same to Source

Filters:
  - Source: A schedulable, healthy instance with excessive load and non-expired instance information.
  - Destination: A schedulable, healthy, less-loaded instance with non-expired instance information.

Selector:
  - Source instances with high load are preferentially paired with destination instances with low load.
*/
func newLoadBalanceReschedule(p *options.SchedulerConfig, inferMode string) *decodeLoadBalanceReschedule {
	var targetLoadMetric string
	var targetLoadThreshold float32

	switch inferMode {
	case consts.DecodeInferMode:
		targetLoadMetric = p.RescheduleDecodeLoadMetric
		targetLoadThreshold = p.RescheduleDecodeLoadThreshold
	case consts.NormalInferMode:
		targetLoadMetric = p.RescheduleNeutralLoadMetric
		targetLoadThreshold = p.RescheduleNeutralLoadThreshold
	default:
		panic(fmt.Sprintf("unsupported failover reschedule infer mode: %s", inferMode))
	}

	if p.RescheduleLoadBalanceScope != consts.RescheduleLoadBalanceScopeCluster &&
		p.RescheduleLoadBalanceScope != consts.RescheduleLoadBalanceScopeUnit {
		panic(fmt.Sprintf("unsupported reschedule load balance scope: %s", p.RescheduleLoadBalanceScope))
	}

	r := &decodeLoadBalanceReschedule{
		baseReschedulePolicy: baseReschedulePolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				targetLoadMetric: getSchedulingMetric(p, targetLoadMetric),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: inferMode},
				&schedulabilityFilter{},
				&stalenessFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
				},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: inferMode},
				&schedulabilityFilter{},
				&stalenessFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
				},
			},
			srcGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			dstGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			selector: &metricBalanceSelector{
				srcMetric:          targetLoadMetric,
				dstMetric:          targetLoadMetric,
				forceHigherToLower: true,
				balanceScope:       p.RescheduleLoadBalanceScope,
			},
		},
		metric:               targetLoadMetric,
		loadBalanceThreshold: p.RescheduleLoadBalanceThreshold,
		migrationReqSelectPolicy: migrationReqSelectPolicy{
			rule:  p.RescheduleReqSelectRule,
			order: p.RescheduleReqSelectOrder,
			value: p.RescheduleReqSelectValue,
		},
		kvCacheBlockSize: p.KvCacheBlockSize,
	}

	// If reschedule load balance scope is unit, instance load inside the same unit should be balanced under any load,
	// so there is no need to apply load threshold filter.
	if targetLoadThreshold > 0 && p.RescheduleLoadBalanceScope != consts.RescheduleLoadBalanceScopeUnit {
		r.srcSingleInstanceFilters = append(r.srcSingleInstanceFilters, &invertedSingleInstanceFilterWrapper{
			innerFilter: &metricBasedFilter{
				metricName: targetLoadMetric,
				threshold:  targetLoadThreshold,
			},
		})
		r.dstSingleInstanceFilters = append(r.dstSingleInstanceFilters, &metricBasedFilter{
			metricName: targetLoadMetric,
			threshold:  targetLoadThreshold,
		})
	}
	return r
}

type failoverReschedule struct {
	baseReschedulePolicy
	inferMode string
}

func (p *failoverReschedule) getMigrationReqSelectPolicy() migrationReqSelectPolicy {
	return migrationReqSelectPolicy{
		rule:  consts.MigrationReqSelectRuleNumReq,
		order: consts.MigrationReqSelectOrderLR,
		// migration value -1 means pre stop
		value: -1,
	}
}

/*
FailoverReschedule enables fault tolerance by migrating workloads from unhealthy or failing
Decode/Prefill/Normal instances to healthy, available ones within the same infer mode.

Instance Type:
  - Source: Prefill / Decode / Normal (depending on infer mode)
  - Destination: Prefill / Decode / Normal (same infer mode as source)

Filters:
  - Source: An instance that is in the target infer mode and identified as failing.
  - Destination: An instance that is in the same infer mode, schedulable, healthy,
    and has non-expired instance information.

Selector:
  - Source instances with high load are preferentially paired with destination instances with low load.
*/
func newFailoverReschedule(p *options.SchedulerConfig, inferMode string) *failoverReschedule {
	var reschedulerMetric string
	switch inferMode {
	case consts.PrefillInferMode:
		reschedulerMetric = p.ReschedulePrefillLoadMetric
	case consts.DecodeInferMode:
		reschedulerMetric = p.RescheduleDecodeLoadMetric
	case consts.NormalInferMode:
		reschedulerMetric = p.RescheduleNeutralLoadMetric
	default:
		panic(fmt.Sprintf("unsupported failover reschedule infer mode: %s", inferMode))
	}
	return &failoverReschedule{
		baseReschedulePolicy: baseReschedulePolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				reschedulerMetric: getSchedulingMetric(p, reschedulerMetric),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: inferMode},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: inferMode},
				&schedulabilityFilter{},
				&stalenessFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
				},
			},
			srcGlobalFilters: []globalFilter{
				&failoverMigrationSrcFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
					failoverScope:            p.FailoverScope,
				},
			},
			dstGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			selector: &roundRobinSelector{},
		},
		inferMode: inferMode,
	}
}

type cleanUpDecodeRequestsOnPrefillReschedule struct {
	baseReschedulePolicy
}

/*
cleanUpDecodeRequestsOnPrefillReschedule is a rescheduling policy designed to migrate Decode requests
from Prefill instances to healthy, low-load Decode instances.

When EnableAdaptivePD is enabled, Decode workloads may run on Prefill instances. To preserve the intended
role semantics of each instance type, this policy actively relocates such Decode requests to Decode
instances that are both healthy and under low load.

Condition:
  - Enable PDD
  - Enable AdaptivePD

Instance Type:
  - Source: Prefill
  - Destination: Decode

Filters:
  - Source: Instances must be in Prefill infer mode, schedulable, have non-expired instance information,
    and currently hold one or more Decode requests.
  - Destination: Instances must be in Decode infer mode, schedulable, have non-expired instance information,
    and have a current Decode load below the configured threshold.

Selector:
  - Prefer pairing source instances with high Decode batch sizes with destination instances low Decode load.
*/
func newCleanUpDecodeRequestsOnPrefillReschedule(
	p *options.SchedulerConfig) *cleanUpDecodeRequestsOnPrefillReschedule {
	return &cleanUpDecodeRequestsOnPrefillReschedule{
		baseReschedulePolicy: baseReschedulePolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				p.DispatchDecodeLoadMetric:             getSchedulingMetric(p, p.DispatchDecodeLoadMetric),
				consts.SchedulingMetricDecodeBatchSize: getSchedulingMetric(p, consts.SchedulingMetricDecodeBatchSize),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: consts.PrefillInferMode},
				&schedulabilityFilter{},
				&stalenessFilter{instanceStalenessSeconds: p.InstanceStalenessSeconds},
				&invertedSingleInstanceFilterWrapper{
					innerFilter: &metricBasedFilter{
						metricName: consts.SchedulingMetricDecodeBatchSize,
						threshold:  0.5,
					},
				},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: consts.DecodeInferMode},
				&schedulabilityFilter{},
				&stalenessFilter{instanceStalenessSeconds: p.InstanceStalenessSeconds},
				&metricBasedFilter{
					metricName: p.DispatchDecodeLoadMetric,
					threshold:  p.DispatchDecodeLoadThreshold,
				},
			},
			srcGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			dstGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			selector: &metricBalanceSelector{
				srcMetric:    consts.SchedulingMetricDecodeBatchSize,
				dstMetric:    p.DispatchDecodeLoadMetric,
				balanceScope: consts.RescheduleLoadBalanceScopeCluster,
			},
		},
	}
}

type aggregateDecodeRequestsOnPrefillReschedule struct {
	baseReschedulePolicy
}

/*
aggregateDecodeRequestsOnPrefillReschedule is a rescheduling policy designed to consolidate decode requests
running on Prefill instances.

Under AdaptivePD, decode workloads may be scheduled on prefill instances for flexibility. To preserve the
original role of instances as much as possible, and considering that decode is memory-bound, this policy
aggressively coalesces scattered decode requests from multiple prefill instances into a smaller set of
target instances capable of handling higher aggregate loads.

Condition:
  - Enable PDD
  - Enable AdaptivePD

Instance Type:
  - Source: Prefill
  - Destination: Prefill

Filters:
  - Source: Instances must be in Prefill infer mode, schedulable, healthy, have non-expired instance
    information, and currently serve one or more Decode requests. Additionally, they should be
    memory-bound under decode workload (based on configured compute-bound batch size threshold).
  - Destination: Same as source

Selector:
  - Prefer pairing sources with low decode batch size with destinations that have high decode batch size.
*/
func newAggregateDecodeRequestsOnPrefillReschedule(p *options.SchedulerConfig) *aggregateDecodeRequestsOnPrefillReschedule {
	return &aggregateDecodeRequestsOnPrefillReschedule{
		baseReschedulePolicy: baseReschedulePolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				consts.SchedulingMetricDecodeBatchSize: getSchedulingMetric(p, consts.SchedulingMetricDecodeBatchSize),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: consts.PrefillInferMode},
				&schedulabilityFilter{},
				&stalenessFilter{instanceStalenessSeconds: p.InstanceStalenessSeconds},
				&invertedSingleInstanceFilterWrapper{
					innerFilter: &metricBasedFilter{
						metricName: consts.SchedulingMetricDecodeBatchSize,
						threshold:  0.5,
					},
				},
				&metricBasedFilter{
					metricName: consts.SchedulingMetricDecodeBatchSize,
					threshold:  float32(p.DecodeComputeBoundBatchSize),
				},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: consts.PrefillInferMode},
				&schedulabilityFilter{},
				&stalenessFilter{instanceStalenessSeconds: p.InstanceStalenessSeconds},
				&invertedSingleInstanceFilterWrapper{
					innerFilter: &metricBasedFilter{
						metricName: consts.SchedulingMetricDecodeBatchSize,
						threshold:  0.5,
					},
				},
				&metricBasedFilter{
					metricName: consts.SchedulingMetricDecodeBatchSize,
					threshold:  float32(p.DecodeComputeBoundBatchSize),
				},
			},
			srcGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			dstGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			selector: &aggregateSelector{
				srcMetric: consts.SchedulingMetricDecodeBatchSize,
				dstMetric: consts.SchedulingMetricDecodeBatchSize,
			},
		},
	}
}

type easeBusyDecodeWithFreePrefillReschedule struct {
	baseReschedulePolicy
}

/*
easeBusyDecodeWithFreePrefillReschedule is a rescheduling policy designed to ease overloaded decode
instances by migrating decode requests to free prefill instances.

Condition:
  - Enable PDD
  - Enable AdaptivePD

Instance Type:
  - Source: Decode
  - Destination: Prefill

Filters:
  - Source: Instances must be in Decode infer mode, schedulable, healthy, have non-expired instance
    information, and exceed the configured decode load threshold.
  - Destination: Instances must be in Prefill infer mode, schedulable, healthy, have non-expired instance
    information, and have no requests.

Selector:
  - Prioritize pairing high decode load with free prefill.
*/
func newEaseBusyDecodeWithFreePrefillReschedule(p *options.SchedulerConfig) *easeBusyDecodeWithFreePrefillReschedule {
	return &easeBusyDecodeWithFreePrefillReschedule{
		baseReschedulePolicy: baseReschedulePolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				p.DispatchPrefillLoadMetric:        getSchedulingMetric(p, p.DispatchPrefillLoadMetric),
				p.DispatchDecodeLoadMetric:         getSchedulingMetric(p, p.DispatchDecodeLoadMetric),
				consts.SchedulingMetricNumRequests: getSchedulingMetric(p, consts.SchedulingMetricNumRequests),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: consts.DecodeInferMode},
				&schedulabilityFilter{},
				&stalenessFilter{instanceStalenessSeconds: p.InstanceStalenessSeconds},
				&invertedSingleInstanceFilterWrapper{
					innerFilter: &metricBasedFilter{
						metricName: p.DispatchDecodeLoadMetric,
						threshold:  p.DispatchDecodeLoadThreshold,
					},
				},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferModeFilter{targetInferMode: consts.PrefillInferMode},
				&schedulabilityFilter{},
				&stalenessFilter{instanceStalenessSeconds: p.InstanceStalenessSeconds},
				&metricBasedFilter{
					metricName: consts.SchedulingMetricNumRequests,
					threshold:  0.5,
				},
			},
			srcGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			dstGlobalFilters: []globalFilter{
				&failoverFilter{
					failoverScope: p.FailoverScope,
				},
			},
			selector: &metricBalanceSelector{
				srcMetric:    p.DispatchDecodeLoadMetric,
				dstMetric:    p.DispatchPrefillLoadMetric,
				balanceScope: consts.RescheduleLoadBalanceScopeCluster,
			},
		},
	}
}
