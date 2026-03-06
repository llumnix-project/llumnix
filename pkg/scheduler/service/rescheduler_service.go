package service

import (
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/scheduler/policy"
)

type ReschedulerService struct {
	config             *options.SchedulerConfig
	reschedulingPolicy policy.ReschedulingInterface
}

func NewReschedulerService(c *options.SchedulerConfig) *ReschedulerService {
	return &ReschedulerService{
		config:             c,
		reschedulingPolicy: policy.NewReschedulingPolicy(c),
	}
}

func (r *ReschedulerService) Start() error {
	go r.reschedulingPolicy.ReschedulingLoop()
	return nil
}
