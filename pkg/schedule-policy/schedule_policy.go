package schedule_policy

import (
	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/schedule-policy/llumnix"
	"llm-gateway/pkg/types"
)

type SchedulePolicy interface {
	// Name schedule policy name
	Name() string

	// Schedule attempts to acquire a backend worker for processing a new request.
	Schedule(*types.ScheduleRequest) error
}

func transformPolicyToMetric(policy string) string {
	switch policy {
	case consts.SchedulePolicyLeastToken, consts.SchedulePolicyLlmMetricBased:
		return "num-tokens"
	case consts.SchedulePolicyLeastRequest:
		return "num-requests"
	default:
		klog.Errorf("unsupported to transform policy: %v", policy)
		return ""
	}
}

func transformPolicyToLlumnixPolicy(policy string, config *options.Config, lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	config.SchedulePolicy = consts.SchedulePolicyLoadBalance
	config.LlumnixConfig.EnableFullModeScheduling = false
	config.LlumnixConfig.DispatchNeutralLoadMetric = transformPolicyToMetric(policy)
	return llumnix.NewDispatchPolicy(config, consts.SchedulePolicyLoadBalance, lrsClient)
}

func createPDSplitPolicy(config *options.Config, lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	// Now least token and least request all use the llumnix policy.
	// Currently, the prefix cache strategy is not compatible with llumnix, so special handling is required.
	if config.PrefillPolicy == consts.SchedulePolicyPrefixCache ||
		config.DecodePolicy == consts.SchedulePolicyPrefixCache {
		return NewPDSplitPolicy(config, lrsClient)
	}

	policy := consts.SchedulePolicyLoadBalance
	config.LlumnixConfig.EnableFullModeScheduling = false
	config.LlumnixConfig.DispatchPrefillLoadMetric = transformPolicyToMetric(config.PrefillPolicy)
	config.LlumnixConfig.DispatchDecodeLoadMetric = transformPolicyToMetric(config.DecodePolicy)
	return llumnix.NewDispatchPolicy(config, policy, lrsClient)
}

func NewSchedulePolicy(
	policy string,
	config *options.Config,
	lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	if len(policy) == 0 {
		panic("create schedule policy exception, policy is empty.")
	}

	klog.Infof("create scheduler with policy: %v", policy)
	switch policy {
	// The llm-metric-based strategy has since been phased out, but to maintain
	// configuration compatibility, it is retained and replaced with the least-token strategy.
	case consts.SchedulePolicyLeastRequest, consts.SchedulePolicyLeastToken, consts.SchedulePolicyLlmMetricBased:
		return transformPolicyToLlumnixPolicy(policy, config, lrsClient)
	case consts.SchedulePolicyPrefixCache:
		return NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)
	case consts.SchedulePolicyPDSplit:
		return createPDSplitPolicy(config, lrsClient)
	default:
		klog.Errorf("unsupported schedule policy: %v", policy)
		return nil
	}
}

func NewReschedulePolicy(config *options.Config) ReschedulePolicy {
	return llumnix.NewReschedulePolicy(config)
}

type ReschedulePolicy interface {
	RescheduleLoop()
}
