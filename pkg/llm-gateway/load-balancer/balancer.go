package loadbalancer

import "easgo/pkg/llm-gateway/structs"

type LoadBalancer interface {
	GetNextTokens(req *structs.Request) (*structs.NextTokens, error)

	ReleaseToken(*structs.Request, *structs.Token)

	ExcludeService(string)
}
