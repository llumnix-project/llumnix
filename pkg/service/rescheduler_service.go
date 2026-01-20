package service

import (
	"llm-gateway/cmd/llm-gateway/app/options"
	schedule_policy "llm-gateway/pkg/schedule-policy"
)

type ReschedulerService struct {
	config           *options.Config
	reschedulePolicy schedule_policy.ReschedulePolicy
}

func NewRescheduleService(c *options.Config) *ReschedulerService {
	return &ReschedulerService{
		config:           c,
		reschedulePolicy: schedule_policy.NewReschedulePolicy(c),
	}
}

func (r *ReschedulerService) Start() error {
	go r.reschedulePolicy.RescheduleLoop()
	return nil
}
