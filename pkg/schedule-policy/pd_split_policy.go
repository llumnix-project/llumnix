package schedule_policy

import (
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/schedule-policy/llumnix"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

// PDSplitPolicy is a policy for pd split scene
type PDSplitPolicy struct {
	config *options.Config

	prefillPolicy SchedulePolicy
	decodePolicy  SchedulePolicy
}

func createPolicyByRole(role string, policyName string, config *options.Config, lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	if policyName == consts.SchedulePolicyLeastToken || policyName == consts.SchedulePolicyLlmMetricBased {
		newConfig := *config
		newConfig.SchedulePolicy = consts.SchedulePolicyLoadBalance
		newConfig.LlumnixConfig.EnableFullModeScheduling = false
		if role == "prefill" {
			newConfig.LlumnixConfig.DispatchPrefillLoadMetric = "num-tokens"
		} else if role == "decode" {
			newConfig.LlumnixConfig.DispatchDecodeLoadMetric = "num-tokens"
		} else {
			klog.Errorf("unsupported role: %v", role)
			return nil
		}
		return llumnix.NewDispatchPolicy(&newConfig, consts.SchedulePolicyLoadBalance, lrsClient)
	} else if policyName == consts.SchedulePolicyLeastRequest {
		newConfig := *config
		newConfig.SchedulePolicy = consts.SchedulePolicyLoadBalance
		newConfig.LlumnixConfig.EnableFullModeScheduling = false
		if role == "prefill" {
			newConfig.LlumnixConfig.DispatchPrefillLoadMetric = "num-requests"
		} else if role == "decode" {
			newConfig.LlumnixConfig.DispatchDecodeLoadMetric = "num-requests"
		} else {
			klog.Errorf("unsupported role: %v", role)
			return nil
		}
		return llumnix.NewDispatchPolicy(&newConfig, consts.SchedulePolicyLoadBalance, lrsClient)
	} else if policyName == consts.SchedulePolicyPrefixCache {
		return NewPrefixCachePolicy(config, role, lrsClient)
	} else {
		return nil
	}
}

func NewPDSplitPolicy(c *options.Config, lrsClient *lrs.LocalRealtimeStateClient) *PDSplitPolicy {
	if c.PrefillPolicy == consts.SchedulePolicyPDSplit || c.DecodePolicy == consts.SchedulePolicyPDSplit {
		klog.Errorf("pd node could not be pd-split policy.")
		return nil
	}
	prefillPolicy := createPolicyByRole(consts.PrefillInferMode, c.PrefillPolicy, c, lrsClient)
	if prefillPolicy == nil {
		klog.Errorf("failed to create prefill policy: %v", c.PrefillPolicy)
		return nil
	}
	decodePolicy := createPolicyByRole(consts.DecodeInferMode, c.DecodePolicy, c, lrsClient)
	if decodePolicy == nil {
		klog.Errorf("failed to create decode policy: %v", c.DecodePolicy)
		return nil
	}
	pdsp := &PDSplitPolicy{
		config:        c,
		prefillPolicy: prefillPolicy,
		decodePolicy:  decodePolicy,
	}
	return pdsp
}

func (pdsp *PDSplitPolicy) Name() string {
	return consts.SchedulePolicyPDSplit
}

func (pdsp *PDSplitPolicy) Schedule(schReq *types.ScheduleRequest) error {
	if schReq.ScheduleMode == types.ScheduleModePDBatch {
		err := pdsp.prefillPolicy.Schedule(schReq)
		if err != nil {
			klog.Errorf("failed to schedule prefill: %v", err)
			return err
		}
		pResults := schReq.ScheduleResult
		schReq.ScheduleResult = nil
		err = pdsp.decodePolicy.Schedule(schReq)
		if err != nil {
			klog.Errorf("failed to schedule decode: %v", err)
			return err
		}
		schReq.ScheduleResult = append(schReq.ScheduleResult, pResults...)
		return nil
	} else if schReq.ScheduleMode == types.ScheduleModePDStaged {
		switch schReq.InferStage {
		case types.InferStagePrefill:
			return pdsp.prefillPolicy.Schedule(schReq)
		case types.InferStageDecode:
			return pdsp.decodePolicy.Schedule(schReq)
		default:
			return nil
		}
	} else {
		klog.Errorf("unsupported schedule mode: %v", schReq.ScheduleMode)
		return nil
	}
}
