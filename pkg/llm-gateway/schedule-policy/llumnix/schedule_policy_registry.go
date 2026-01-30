package llumnix

import (
	"fmt"

	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/llm-gateway/consts"
)

func newDispatchPolicyInternal(c *options.SchedulerConfig) dispatchPolicyInternal {
	switch c.SchedulePolicy {
	case consts.SchedulePolicyLoadBalance:
		if c.EnableFullModeScheduling {
			return newLoadBalanceDispatchFullMode(c)
		} else {
			return newLoadBalanceDispatchLiteMode(c)
		}
	case consts.SchedulePolicyFlood:
		return newFloodDispatchPolicyFullMode(c)
	default:
		panic(fmt.Sprintf("unsupported schedule policy: %s", c.SchedulePolicy))
	}
}

type loadBalanceDispatchPolicy struct {
	baseDispatchPolicy
}

func newLoadBalanceDispatchFullMode(p *options.SchedulerConfig) *loadBalanceDispatchPolicy {
	policy := &loadBalanceDispatchPolicy{
		baseDispatchPolicy: baseDispatchPolicy{
			consts.PrefillInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchPrefillLoadMetric: getSchedulingMetric(p, p.DispatchPrefillLoadMetric),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.PrefillInstanceType: {
						&schedulabilityFilter{},
						&stalenessFilter{
							instanceStalenessSeconds: p.InstanceStalenessSeconds,
						},
						&metricBasedFilter{
							metricName: p.DispatchPrefillLoadMetric,
							threshold:  p.DispatchPrefillLoadThreshold,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.PrefillInstanceType: &metricBasedSelector{
						topK:        p.DispatchTopK,
						metricNames: []string{p.DispatchPrefillLoadMetric},
					},
				},
			},
			consts.DecodeInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchDecodeLoadMetric: getSchedulingMetric(p, p.DispatchDecodeLoadMetric),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.DecodeInstanceType: {
						&schedulabilityFilter{},
						&stalenessFilter{
							instanceStalenessSeconds: p.InstanceStalenessSeconds,
						},
						&metricBasedFilter{
							metricName: p.DispatchDecodeLoadMetric,
							threshold:  p.DispatchDecodeLoadThreshold,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.DecodeInstanceType: &metricBasedSelector{
						topK:        p.DispatchTopK,
						metricNames: []string{p.DispatchDecodeLoadMetric},
					},
				},
			},
			consts.NormalInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchNeutralLoadMetric: getSchedulingMetric(p, p.DispatchNeutralLoadMetric),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.NeutralInstanceType: {
						&schedulabilityFilter{},
						&stalenessFilter{
							instanceStalenessSeconds: p.InstanceStalenessSeconds,
						},
						&metricBasedFilter{
							metricName: p.DispatchNeutralLoadMetric,
							threshold:  p.DispatchNeutralLoadThreshold,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.NeutralInstanceType: &metricBasedSelector{
						topK:        p.DispatchTopK,
						metricNames: []string{p.DispatchNeutralLoadMetric},
					},
				},
			},
		},
	}

	if p.EnableAdaptivePD {
		prefillInferModeMetrics := policy.baseDispatchPolicy[consts.PrefillInferMode].metrics
		if _, ok := prefillInferModeMetrics[p.DispatchDecodeAsPrefillLoadMetric]; !ok {
			prefillInferModeMetrics[p.DispatchDecodeAsPrefillLoadMetric] =
				getSchedulingMetric(p, p.DispatchDecodeAsPrefillLoadMetric)
		}
		policy.baseDispatchPolicy[consts.PrefillInferMode].singleInstanceFilters[consts.DecodeInstanceType] =
			[]singleInstanceFilter{
				&schedulabilityFilter{},
				&stalenessFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
				},
				&metricBasedFilter{
					metricName: p.DispatchDecodeAsPrefillLoadMetric,
					threshold:  p.DispatchDecodeAsPrefillLoadThreshold,
				},
			}
		policy.baseDispatchPolicy[consts.PrefillInferMode].selectors[consts.DecodeInstanceType] =
			&metricBasedSelector{
				topK:        p.DispatchTopK,
				metricNames: []string{p.DispatchDecodeAsPrefillLoadMetric},
			}

		decodeInferModeMetrics := policy.baseDispatchPolicy[consts.DecodeInferMode].metrics
		if _, ok := decodeInferModeMetrics[p.DispatchPrefillAsDecodeLoadMetric]; !ok {
			decodeInferModeMetrics[p.DispatchPrefillAsDecodeLoadMetric] =
				getSchedulingMetric(p, p.DispatchPrefillAsDecodeLoadMetric)
		}
		policy.baseDispatchPolicy[consts.DecodeInferMode].singleInstanceFilters[consts.PrefillInstanceType] =
			[]singleInstanceFilter{
				&schedulabilityFilter{},
				&stalenessFilter{
					instanceStalenessSeconds: p.InstanceStalenessSeconds,
				},
				&metricBasedFilter{
					metricName: p.DispatchPrefillAsDecodeLoadMetric,
					threshold:  p.DispatchPrefillAsDecodeLoadThreshold,
				},
			}
		policy.baseDispatchPolicy[consts.DecodeInferMode].selectors[consts.PrefillInstanceType] =
			&metricBasedSelector{
				topK:        p.DispatchTopK,
				metricNames: []string{p.DispatchPrefillAsDecodeLoadMetric},
			}
	}

	// Placed the cache locality metric as the first metric to be used in the metric-based selector
	if p.EnableCacheAwareScheduling {
		prefillInferModeMetrics := policy.baseDispatchPolicy[consts.PrefillInferMode].metrics
		prefillInferModeMetrics[p.DispatchPrefillCacheLocalityMetric] = getSchedulingMetric(p, p.DispatchPrefillCacheLocalityMetric)
		prefillInstanceSelector := policy.baseDispatchPolicy[consts.PrefillInferMode].selectors[consts.PrefillInstanceType].(*metricBasedSelector)
		prefillInstanceSelector.metricNames = append([]string{p.DispatchPrefillCacheLocalityMetric}, prefillInstanceSelector.metricNames...)

		normalInferModeMetrics := policy.baseDispatchPolicy[consts.NormalInferMode].metrics
		normalInferModeMetrics[p.DispatchPrefillCacheLocalityMetric] = getSchedulingMetric(p, p.DispatchPrefillCacheLocalityMetric)
		neutralInstanceSelector := policy.baseDispatchPolicy[consts.NormalInferMode].selectors[consts.NeutralInstanceType].(*metricBasedSelector)
		neutralInstanceSelector.metricNames = append([]string{p.DispatchPrefillCacheLocalityMetric}, neutralInstanceSelector.metricNames...)
	}

	return policy
}

// The flood policy attempts to always route requests to the same instance whenever possible.
type floodDispatchPolicy struct {
	baseDispatchPolicy
}

func newFloodDispatchPolicyFullMode(p *options.SchedulerConfig) *floodDispatchPolicy {
	policy := &floodDispatchPolicy{
		baseDispatchPolicy: baseDispatchPolicy{
			consts.PrefillInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.PrefillInstanceType: {
						&schedulabilityFilter{},
						&stalenessFilter{
							instanceStalenessSeconds: p.InstanceStalenessSeconds,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.PrefillInstanceType: &fixedPreferenceSelector{},
				},
			},
			consts.DecodeInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.DecodeInstanceType: {
						&schedulabilityFilter{},
						&stalenessFilter{
							instanceStalenessSeconds: p.InstanceStalenessSeconds,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.DecodeInstanceType: &fixedPreferenceSelector{},
				},
			},
			consts.NormalInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.NeutralInstanceType: {
						&schedulabilityFilter{},
						&stalenessFilter{
							instanceStalenessSeconds: p.InstanceStalenessSeconds,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.NeutralInstanceType: &fixedPreferenceSelector{},
				},
			},
		},
	}

	if p.EnableAdaptivePD {
		klog.Warning("AdaptivePD is ignored for flood dispatch policy.")
	}

	if p.EnableCacheAwareScheduling {
		klog.Warning("CacheAwareScheduling is ignored for flood dispatch policy.")
	}

	return policy
}

func newLoadBalanceDispatchLiteMode(p *options.SchedulerConfig) *loadBalanceDispatchPolicy {
	policy := &loadBalanceDispatchPolicy{
		baseDispatchPolicy: baseDispatchPolicy{
			consts.PrefillInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchPrefillLoadMetric: getSchedulingMetric(p, p.DispatchPrefillLoadMetric),
				},
				globalFilters: []globalFilter{},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.PrefillInstanceType: {
						&metricBasedFilter{
							metricName: p.DispatchPrefillLoadMetric,
							threshold:  p.DispatchPrefillLoadThreshold,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.PrefillInstanceType: &metricBasedSelector{
						topK:        p.DispatchTopK,
						metricNames: []string{p.DispatchPrefillLoadMetric},
					},
				},
			},
			consts.DecodeInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchDecodeLoadMetric: getSchedulingMetric(p, p.DispatchDecodeLoadMetric),
				},
				globalFilters: []globalFilter{},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.DecodeInstanceType: {
						&metricBasedFilter{
							metricName: p.DispatchDecodeLoadMetric,
							threshold:  p.DispatchDecodeLoadThreshold,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.DecodeInstanceType: &metricBasedSelector{
						topK:        p.DispatchTopK,
						metricNames: []string{p.DispatchDecodeLoadMetric},
					},
				},
			},
			consts.NormalInferMode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchNeutralLoadMetric: getSchedulingMetric(p, p.DispatchNeutralLoadMetric),
				},
				globalFilters: []globalFilter{},
				singleInstanceFilters: map[string][]singleInstanceFilter{
					consts.NeutralInstanceType: {
						&metricBasedFilter{
							metricName: p.DispatchNeutralLoadMetric,
							threshold:  p.DispatchNeutralLoadThreshold,
						},
					},
				},
				selectors: map[string]dispatchSelector{
					consts.NeutralInstanceType: &metricBasedSelector{
						topK:        p.DispatchTopK,
						metricNames: []string{p.DispatchNeutralLoadMetric},
					},
				},
			},
		},
	}

	return policy
}
