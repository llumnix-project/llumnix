package schedule_policy

import (
	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/scheduler/lrs"
	"llm-gateway/pkg/scheduler/schedule-policy/llumnix"
	"llm-gateway/pkg/types"
)

type SchedulePolicy interface {
	// Name schedule policy name
	Name() string

	// Schedule attempts to acquire a backend worker for processing a new request.
	Schedule(*types.ScheduleRequest) error
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
		return createNeutralDispatchPolicy(policy, config, lrsClient)
	case consts.SchedulePolicyPrefixCache:
		return NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)
	case consts.SchedulePolicyPDSplit:
		return NewPDSplitPolicy(config, lrsClient)
	default:
		klog.Errorf("unsupported schedule policy: %v", policy)
		return nil
	}
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

func createNeutralDispatchPolicy(policy string, config *options.Config, lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	config.SchedulePolicy = consts.SchedulePolicyLoadBalance
	config.LlumnixConfig.EnableFullModeScheduling = false
	config.LlumnixConfig.DispatchNeutralLoadMetric = transformPolicyToMetric(policy)
	return llumnix.NewDispatchPolicy(config, consts.SchedulePolicyLoadBalance, lrsClient)
}

func NewReschedulePolicy(config *options.Config) ReschedulePolicy {
	return llumnix.NewReschedulePolicy(config)
}

type ReschedulePolicy interface {
	RescheduleLoop()
}
