package scheduling_policy

import (
	"fmt"

	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
)

func newReschedulingPolicyInternal(p *options.SchedulerConfig, policy string) reschedulingPolicyInternal {
	switch policy {
	case consts.ReschedulingPolicyNeutralLoad:
		return newLoadBalanceRescheduling(p, consts.InferTypeNeutral)
	case consts.ReschedulingPolicyDecodeLoad:
		return newLoadBalanceRescheduling(p, consts.InferTypeDecode)
	case consts.ReschedulingPolicyPrefillFailover:
		return newFailoverRescheduling(p, consts.InferTypePrefill)
	case consts.ReschedulingPolicyDecodeFailover:
		return newFailoverRescheduling(p, consts.InferTypeDecode)
	case consts.ReschedulingPolicyNeutralFailover:
		return newFailoverRescheduling(p, consts.InferTypeNeutral)
	default:
		panic(fmt.Sprintf("unsupported rescheduling policy: %s", p.ReschedulingPolicies))
	}
}

type decodeLoadBalanceRescheduling struct {
	baseReschedulingPolicy
	migrationReqSelectPolicy migrationReqSelectPolicy
	metric                   string
	loadBalanceThreshold     float32
}

func (p *decodeLoadBalanceRescheduling) selectPairs(
	srcInstanceViewInternal,
	dstInstanceViewInternal map[string]*instanceViewScheduling) []*reschedulingPair {

	return p.acceptLoadImbalancePairs(
		p.selector.selectPairs(srcInstanceViewInternal, dstInstanceViewInternal))
}

func (p *decodeLoadBalanceRescheduling) acceptLoadImbalancePairs(
	selectedPairs []*reschedulingPair) (validatedPairs []*reschedulingPair) {
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

func (p *decodeLoadBalanceRescheduling) getMigrationReqSelectPolicy() migrationReqSelectPolicy {
	return p.migrationReqSelectPolicy
}

/*
LoadBalanceRescheduling enables load balancing among instances by redistributing
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
func newLoadBalanceRescheduling(p *options.SchedulerConfig, inferType consts.InferType) *decodeLoadBalanceRescheduling {
	var targetLoadMetric string
	var targetLoadThreshold float32

	switch inferType {
	case consts.InferTypeDecode:
		targetLoadMetric = p.ReschedulingDecodeLoadMetric
		targetLoadThreshold = p.ReschedulingDecodeLoadThreshold
	case consts.InferTypeNeutral:
		targetLoadMetric = p.ReschedulingNeutralLoadMetric
		targetLoadThreshold = p.ReschedulingNeutralLoadThreshold
	default:
		panic(fmt.Sprintf("unsupported failover rescheduling infer type: %s", inferType))
	}

	if p.ReschedulingLoadBalanceScope != consts.ReschedulingLoadBalanceScopeCluster &&
		p.ReschedulingLoadBalanceScope != consts.ReschedulingLoadBalanceScopeUnit {
		panic(fmt.Sprintf("unsupported rescheduling load balance scope: %s", p.ReschedulingLoadBalanceScope))
	}

	r := &decodeLoadBalanceRescheduling{
		baseReschedulingPolicy: baseReschedulingPolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				targetLoadMetric: getSchedulingMetric(p, targetLoadMetric),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferTypeFilter{targetInferType: inferType},
				&schedulabilityFilter{},
				&stalenessFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
				},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferTypeFilter{targetInferType: inferType},
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
				balanceScope:       p.ReschedulingLoadBalanceScope,
			},
		},
		metric:               targetLoadMetric,
		loadBalanceThreshold: p.ReschedulingLoadBalanceThreshold,
		migrationReqSelectPolicy: migrationReqSelectPolicy{
			rule:  p.ReschedulingReqSelectRule,
			order: p.ReschedulingReqSelectOrder,
			value: p.ReschedulingReqSelectValue,
		},
	}

	// If rescheduling load balance scope is unit, instance load inside the same unit should be balanced under any load,
	// so there is no need to apply load threshold filter.
	if targetLoadThreshold > 0 && p.ReschedulingLoadBalanceScope != consts.ReschedulingLoadBalanceScopeUnit {
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

type failoverRescheduling struct {
	baseReschedulingPolicy
	inferType consts.InferType
}

func (p *failoverRescheduling) getMigrationReqSelectPolicy() migrationReqSelectPolicy {
	return migrationReqSelectPolicy{
		rule:  consts.MigrationReqSelectRuleNumReq,
		order: consts.MigrationReqSelectOrderLR,
		// migration value -1 means pre stop
		value: -1,
	}
}

/*
FailoverRescheduling enables fault tolerance by migrating workloads from unhealthy or failing
Decode/Prefill/Neutral instances to healthy, available ones within the same infer type.

Instance Type:
  - Source: Prefill / Decode / Neutral (depending on infer type)
  - Destination: Prefill / Decode / Neutral (same infer type as source)

Filters:
  - Source: An instance that is in the target infer type and identified as failing.
  - Destination: An instance that is in the same infer type, schedulable, healthy,
    and has non-expired instance information.

Selector:
  - Source instances with high load are preferentially paired with destination instances with low load.
*/
func newFailoverRescheduling(p *options.SchedulerConfig, inferType consts.InferType) *failoverRescheduling {
	var reschedulerMetric string
	switch inferType {
	case consts.InferTypePrefill:
		reschedulerMetric = p.ReschedulingPrefillLoadMetric
	case consts.InferTypeDecode:
		reschedulerMetric = p.ReschedulingDecodeLoadMetric
	case consts.InferTypeNeutral:
		reschedulerMetric = p.ReschedulingNeutralLoadMetric
	default:
		panic(fmt.Sprintf("unsupported failover rescheduling infer type: %s", inferType))
	}
	return &failoverRescheduling{
		baseReschedulingPolicy: baseReschedulingPolicy{
			metrics: map[string]func() instanceSchedulingMetric{
				reschedulerMetric: getSchedulingMetric(p, reschedulerMetric),
			},
			srcSingleInstanceFilters: []singleInstanceFilter{
				&inferTypeFilter{targetInferType: inferType},
			},
			dstSingleInstanceFilters: []singleInstanceFilter{
				&inferTypeFilter{targetInferType: inferType},
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
		inferType: inferType,
	}
}
