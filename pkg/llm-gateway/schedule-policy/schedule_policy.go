package schedule_policy

import (
	"llumnix/cmd/scheduler/app/options"

	"k8s.io/klog/v2"

	"llumnix/pkg/llm-gateway/lrs"
	"llumnix/pkg/llm-gateway/schedule-policy/llumnix"
	"llumnix/pkg/llm-gateway/types"
)

type SchedulePolicy interface {
	// Name schedule policy name
	Name() string

	// Schedule attempts to acquire an instance for processing a new request.
	Schedule(*types.ScheduleRequest) error
}

func NewSchedulePolicy(
	policy string,
	config *options.SchedulerConfig,
	lrsClient *lrs.LocalRealtimeStateClient) SchedulePolicy {
	if len(policy) == 0 {
		panic("create schedule policy exception, policy is empty.")
	}

	klog.Infof("create scheduler with policy: %v", policy)

	return llumnix.NewDispatchPolicy(config, policy, lrsClient)
}

func NewReschedulePolicy(config *options.SchedulerConfig) ReschedulePolicy {
	return llumnix.NewReschedulePolicy(config)
}

type ReschedulePolicy interface {
	RescheduleLoop()
}
