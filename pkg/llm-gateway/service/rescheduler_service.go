package service

import (
	"llumnix/cmd/scheduler/app/options"
	policy "llumnix/pkg/llm-gateway/schedule-policy"
)

type ReschedulerService struct {
	config           *options.SchedulerConfig
	reschedulePolicy policy.ReschedulePolicy
}

func NewRescheduleService(c *options.SchedulerConfig) *ReschedulerService {
	return &ReschedulerService{
		config:           c,
		reschedulePolicy: policy.NewReschedulePolicy(c),
	}
}

func (r *ReschedulerService) Start() error {
	go r.reschedulePolicy.RescheduleLoop()
	return nil
}
