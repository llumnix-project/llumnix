package balancer

import (
	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/llm-gateway/types"
)

type Balancer interface {
	Get(*types.RequestContext) (types.ScheduledResult, error)

	Release(*types.RequestContext, *types.LLMInstance)
}

func NewBalancer(c *options.GatewayConfig) Balancer {
	return NewCompositeBalancer(c)
}
