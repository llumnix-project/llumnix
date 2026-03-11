package policy

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
						failoverDomain: p.FailoverDomain,
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
						failoverDomain: p.FailoverDomain,
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
						failoverDomain: p.FailoverDomain,
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

	// TODO(sunbiao.sun): Extract this part into a separate function
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

	// TODO(KuilongCui): add a configurable argument to allow users to choose whether to reject
	// requests that fail to meet SLO targets for SloDispatchPolicy and Adaptive PD.
	policy := &sloDispatchPolicy{
		baseDispatchPolicy: baseDispatchPolicy{
			consts.InferTypePrefill: {
				metrics: map[string]func() instanceSchedulingMetric{
					consts.SchedulingMetricPredictedTtft: getSchedulingMetric(p, consts.SchedulingMetricPredictedTtft),
				},
				globalFilters: []globalFilter{
					&failoverFilter{
						failoverDomain: p.FailoverDomain,
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
						failoverDomain: p.FailoverDomain,
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

	if p.EnableAdaptivePD {
		configureAdaptivePDForSlo(policy, p)
	}

	return policy
}

func configureAdaptivePDForSlo(policy *sloDispatchPolicy, p *options.SchedulerConfig) {
	// On instances without decode requests, the instance with the lowest predicted TTFT will be selected for Prefill.
	policy.baseDispatchPolicy[consts.InferTypePrefill].metrics[consts.SchedulingMetricDecodeBatchSize] =
		getSchedulingMetric(p, consts.SchedulingMetricDecodeBatchSize)
	policy.baseDispatchPolicy[consts.InferTypePrefill].metrics[consts.SchedulingMetricPredictedTpot] =
		getSchedulingMetric(p, consts.SchedulingMetricPredictedTpot)
	policy.baseDispatchPolicy[consts.InferTypePrefill].singleInstanceFilters = []singleInstanceFilter{
		&schedulabilityFilter{},
		&stalenessFilter{
			instanceStalenessSeconds: p.InstanceStalenessSeconds,
		},
		&instanceAttributeFilter{
			attrKey:       consts.AttrKeyReservedInferType,
			rejectedValue: consts.InferTypeDecode,
		},
		&metricBasedFilter{
			metricName: consts.SchedulingMetricPredictedTtft,
			threshold:  p.TtftSlo * p.TtftSloDispatchThreshold,
		},
		&metricBasedFilter{
			metricName:          consts.SchedulingMetricDecodeBatchSize,
			threshold:           0.1,
			notSkipWhenFallback: true,
		}}
	policy.baseDispatchPolicy[consts.InferTypePrefill].selectors = &sloPrefillApdSelector{}

	// Select the instance with the highest Predicted TPOT that does not exceed the TPOT SLO as decode
	// (bin-packing for decode). If no available instance is found, attempt to convert the P instance with
	// the lowest Predicted TTFT to D. Finally, if no available instance that meets the TPOT SLO can be
	// found, perform load balancing across all D instances.
	policy.baseDispatchPolicy[consts.InferTypeDecode].metrics[consts.SchedulingMetricPredictedTtft] =
		getSchedulingMetric(p, consts.SchedulingMetricPredictedTtft)
	policy.baseDispatchPolicy[consts.InferTypeDecode].metrics[consts.SchedulingMetricDecodeBatchSize] =
		getSchedulingMetric(p, consts.SchedulingMetricDecodeBatchSize)
	policy.baseDispatchPolicy[consts.InferTypeDecode].singleInstanceFilters = []singleInstanceFilter{
		&schedulabilityFilter{},
		&stalenessFilter{
			instanceStalenessSeconds: p.InstanceStalenessSeconds,
		},
		&instanceAttributeFilter{
			attrKey:       consts.AttrKeyReservedInferType,
			rejectedValue: consts.InferTypePrefill,
		},
		&metricBasedFilter{
			metricName: consts.SchedulingMetricPredictedTtft,
			threshold:  p.TpotSlo * p.TpotSloDispatchThreshold,
		},
		&metricBasedFilter{
			metricName: consts.SchedulingMetricPredictedTpot,
			threshold:  p.TpotSlo * p.TpotSloDispatchThreshold,
		},
	}
	policy.baseDispatchPolicy[consts.InferTypeDecode].selectors = &sloDecodeApdSelector{}
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
						failoverDomain: p.FailoverDomain,
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
						failoverDomain: p.FailoverDomain,
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
						failoverDomain: p.FailoverDomain,
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
