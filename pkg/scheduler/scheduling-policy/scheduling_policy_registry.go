package scheduling_policy

import (
	"fmt"

	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
)

func newDispatchPolicyInternal(c *options.SchedulerConfig) dispatchPolicyInternal {
	switch c.SchedulingPolicy {
	case consts.SchedulingPolicyLoadBalance:
		if c.EnableFullModeScheduling {
			return newLoadBalanceDispatchFullMode(c)
		} else {
			return newLoadBalanceDispatchLiteMode(c)
		}
	case consts.SchedulingPolicyFlood:
		return newFloodDispatchPolicyFullMode(c)
	case consts.SchedulingPolicySlo:
		return newSloDispatchFullMode(c)
	default:
		panic(fmt.Sprintf("unsupported scheduling policy: %s", c.SchedulingPolicy))
	}
}

type loadBalanceDispatchPolicy struct {
	baseDispatchPolicy
}

func newLoadBalanceDispatchFullMode(p *options.SchedulerConfig) *loadBalanceDispatchPolicy {
	policy := &loadBalanceDispatchPolicy{
		baseDispatchPolicy: baseDispatchPolicy{
			consts.InferTypePrefill: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchPrefillLoadMetric: getSchedulingMetric(p, p.DispatchPrefillLoadMetric),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
					&metricBasedFilter{
						metricName: p.DispatchPrefillLoadMetric,
						threshold:  p.DispatchPrefillLoadThreshold,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{p.DispatchPrefillLoadMetric},
				},
			},
			consts.InferTypeDecode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchDecodeLoadMetric: getSchedulingMetric(p, p.DispatchDecodeLoadMetric),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
					&metricBasedFilter{
						metricName: p.DispatchDecodeLoadMetric,
						threshold:  p.DispatchDecodeLoadThreshold,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{p.DispatchDecodeLoadMetric},
				},
			},
			consts.InferTypeNeutral: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchNeutralLoadMetric: getSchedulingMetric(p, p.DispatchNeutralLoadMetric),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
					&metricBasedFilter{
						metricName: p.DispatchNeutralLoadMetric,
						threshold:  p.DispatchNeutralLoadThreshold,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{p.DispatchNeutralLoadMetric},
				},
			},
		},
	}

	// Placed the cache locality metric as the first metric to be used in the metric-based selector
	if p.EnableCacheAwareScheduling {
		prefillInferTypeMetrics := policy.baseDispatchPolicy[consts.InferTypePrefill].metrics
		prefillInferTypeMetrics[p.DispatchPrefillCacheLocalityMetric] = getSchedulingMetric(p, p.DispatchPrefillCacheLocalityMetric)
		prefillInstanceSelector := policy.baseDispatchPolicy[consts.InferTypePrefill].selectors.(*metricBasedSelector)
		prefillInstanceSelector.metricNames = append([]string{p.DispatchPrefillCacheLocalityMetric}, prefillInstanceSelector.metricNames...)

		normalInferTypeMetrics := policy.baseDispatchPolicy[consts.InferTypeNeutral].metrics
		normalInferTypeMetrics[p.DispatchPrefillCacheLocalityMetric] = getSchedulingMetric(p, p.DispatchPrefillCacheLocalityMetric)
		neutralInstanceSelector := policy.baseDispatchPolicy[consts.InferTypeNeutral].selectors.(*metricBasedSelector)
		neutralInstanceSelector.metricNames = append([]string{p.DispatchPrefillCacheLocalityMetric}, neutralInstanceSelector.metricNames...)
	}

	return policy
}

// The SLO policy attempts to route requests to the instance with the lowest latency.
type sloDispatchPolicy struct {
	baseDispatchPolicy
}

func newSloDispatchFullMode(p *options.SchedulerConfig) *sloDispatchPolicy {
	// init latency predictor, fast fail
	GetLatencyPredictor(p.TtftProfilingDataPath, p.TpotProfilingDataPath)

	policy := &sloDispatchPolicy{
		baseDispatchPolicy: baseDispatchPolicy{
			consts.InferTypePrefill: {
				metrics: map[string]func() instanceSchedulingMetric{
					consts.SchedulingMetricPredictedTtft: getSchedulingMetric(p, consts.SchedulingMetricPredictedTtft),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
					&metricBasedFilter{
						metricName:          consts.SchedulingMetricPredictedTtft,
						threshold:           p.TtftSlo * p.TtftSloDispatchThreshold,
						notSkipWhenFallback: true,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{consts.SchedulingMetricPredictedTtft},
				},
			},
			consts.InferTypeDecode: {
				metrics: map[string]func() instanceSchedulingMetric{
					consts.SchedulingMetricPredictedTpot: getSchedulingMetric(p, consts.SchedulingMetricPredictedTpot),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
					&metricBasedFilter{
						metricName:          consts.SchedulingMetricPredictedTpot,
						threshold:           p.TpotSlo * p.TpotSloDispatchThreshold,
						notSkipWhenFallback: true,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{consts.SchedulingMetricPredictedTpot},
				},
			},
		},
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
			consts.InferTypePrefill: {
				metrics: map[string]func() instanceSchedulingMetric{},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
				},
				selectors: &fixedPreferenceSelector{},
			},
			consts.InferTypeDecode: {
				metrics: map[string]func() instanceSchedulingMetric{},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
				},
				selectors: &fixedPreferenceSelector{},
			},
			consts.InferTypeNeutral: {
				metrics: map[string]func() instanceSchedulingMetric{},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverScope: p.FailoverScope,
					},
				},
				singleInstanceFilters: []singleInstanceFilter{
					&schedulabilityFilter{},
					&stalenessFilter{
						instanceStalenessSeconds: p.InstanceStalenessSeconds,
					},
				},
				selectors: &fixedPreferenceSelector{},
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
			consts.InferTypePrefill: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchPrefillLoadMetric: getSchedulingMetric(p, p.DispatchPrefillLoadMetric),
				},
				globalFilters: []globalFilter{},
				singleInstanceFilters: []singleInstanceFilter{
					&metricBasedFilter{
						metricName: p.DispatchPrefillLoadMetric,
						threshold:  p.DispatchPrefillLoadThreshold,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{p.DispatchPrefillLoadMetric},
				},
			},
			consts.InferTypeDecode: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchDecodeLoadMetric: getSchedulingMetric(p, p.DispatchDecodeLoadMetric),
				},
				globalFilters: []globalFilter{},
				singleInstanceFilters: []singleInstanceFilter{
					&metricBasedFilter{
						metricName: p.DispatchDecodeLoadMetric,
						threshold:  p.DispatchDecodeLoadThreshold,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{p.DispatchDecodeLoadMetric},
				},
			},
			consts.InferTypeNeutral: {
				metrics: map[string]func() instanceSchedulingMetric{
					p.DispatchNeutralLoadMetric: getSchedulingMetric(p, p.DispatchNeutralLoadMetric),
				},
				globalFilters: []globalFilter{},
				singleInstanceFilters: []singleInstanceFilter{
					&metricBasedFilter{
						metricName: p.DispatchNeutralLoadMetric,
						threshold:  p.DispatchNeutralLoadThreshold,
					},
				},
				selectors: &metricBasedSelector{
					topK:        p.DispatchTopK,
					metricNames: []string{p.DispatchNeutralLoadMetric},
				},
			},
		},
	}

	return policy
}
