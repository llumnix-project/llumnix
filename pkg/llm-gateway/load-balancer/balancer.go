package balancer

import (
	"easgo/pkg/llm-gateway/types"
)

type Balancer interface {
	Get(*types.RequestContext) (types.ScheduledResult, error)

	Release(*types.RequestContext, *types.LLMWorker)
}
