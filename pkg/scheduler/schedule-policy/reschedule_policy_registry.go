package schedule_policy

import (
	"fmt"

	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
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
	default:
		panic(fmt.Sprintf("unsupported reschedule policy: %s", p.ReschedulePolicies))
	}
}

type decodeLoadBalanceReschedule struct {
	baseReschedulePolicy
	migrationReqSelectPolicy migrationReqSelectPolicy
	metric                   string
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
