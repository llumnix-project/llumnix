package schedule_policy

import (
	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/schedule-policy/llumnix"
	"llm-gateway/pkg/types"
)

type SchedulePolicy interface {
	// Name schedule policy name
	Name() string

	// GetToken attempts to acquire a token for processing a new request.
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

	return llumnix.NewDispatchPolicy(config, policy, lrsClient)
}

func NewReschedulePolicy(config *options.Config) ReschedulePolicy {
	return llumnix.NewReschedulePolicy(config)
}

type ReschedulePolicy interface {
	RescheduleLoop()
}
