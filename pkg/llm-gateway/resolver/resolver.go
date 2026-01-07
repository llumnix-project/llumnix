package resolver

import (
	"context"
	"easgo/pkg/llm-gateway/structs"
)

// Old interface to get weight endpoints
// TODO(hengfei.zf) to be removed
type Resolver interface {
	GetWeightEndpoints() []structs.WeightEndpoint
}

// New interface to get llm instances
type LLMResolver interface {
	Resolver

	// get all llm instances
	GetLLMInstances(ctx context.Context) []structs.LLMInstance
	// get all workers of each dp/tp
	GetWorkers(ctx context.Context) []structs.LLMWorker
	// get all dp workers, only return the tp_rank=0 workers
	// same as GetWeightEndpoints
	GetDPWorkers(ctx context.Context) []structs.LLMWorker
}
