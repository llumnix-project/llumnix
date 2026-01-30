package llumnix

import (
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/llm-gateway/consts"
)

type scheduleStep struct {
	instanceType     string
	skipWhenFallback bool
}

type schedulePipeline struct {
	inferMode     string
	scheduleSteps []scheduleStep
}

// schedulerPipeline is organized by inference mode. For a given inference mode,
// a sequence of scheduleSteps, which define the policies for executing on specific
// instance types, are carried out.
func newSchedulerPipeline(p *options.SchedulerConfig) map[string]*schedulePipeline {
	schedulerPipelines := map[string]*schedulePipeline{
		consts.NormalInferMode: {
			inferMode: consts.NormalInferMode,
			scheduleSteps: []scheduleStep{
				{
					instanceType:     consts.NeutralInstanceType,
					skipWhenFallback: false,
				},
			},
		},
		consts.PrefillInferMode: {
			inferMode: consts.PrefillInferMode,
			scheduleSteps: []scheduleStep{
				{
					instanceType:     consts.PrefillInstanceType,
					skipWhenFallback: false,
				},
			},
		},
		consts.DecodeInferMode: {
			inferMode: consts.DecodeInferMode,
			scheduleSteps: []scheduleStep{
				{
					instanceType:     consts.DecodeInstanceType,
					skipWhenFallback: false,
				},
			},
		},
	}

	if p.EnableAdaptivePD {
		schedulerPipelines[consts.PrefillInferMode].scheduleSteps = append(
			schedulerPipelines[consts.PrefillInferMode].scheduleSteps,
			scheduleStep{
				instanceType:     consts.DecodeInstanceType,
				skipWhenFallback: true,
			},
		)
		schedulerPipelines[consts.DecodeInferMode].scheduleSteps = append(
			schedulerPipelines[consts.DecodeInferMode].scheduleSteps,
			scheduleStep{
				instanceType:     consts.PrefillInstanceType,
				skipWhenFallback: true,
			},
		)
	}

	return schedulerPipelines
}
