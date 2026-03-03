package balancer

import (
	"llm-gateway/pkg/types"
)

type Balancer interface {
	Get(*types.RequestContext) (types.ScheduledResult, error)

	Release(*types.RequestContext, *types.LLMWorker)
}
