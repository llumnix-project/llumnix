package resolver

import (
	"easgo/pkg/llm-gateway/structs"
	"sort"
	"strings"

	"k8s.io/klog/v2"
)

type IPListResolver struct {
	endpoints []structs.WeightEndpoint
}

func NewIPListResolver(addr string) *IPListResolver {
	r := &IPListResolver{}
	for _, s := range strings.Split(addr, ",") {
		ep, err := structs.NewWeightEndpoint(strings.TrimSpace(s))
		if err != nil {
			klog.Errorf("new IP List resolver error: %v", err)
			return nil
		}
		r.endpoints = append(r.endpoints, ep)
	}

	if len(r.endpoints) == 0 {
		klog.Errorf("empty ip list.")
		return nil
	}
	sort.Slice(r.endpoints, func(i, j int) bool {
		return strings.Compare(r.endpoints[i].Ep.String(), r.endpoints[i].Ep.String()) < 0
	})
	klog.Infof("create ip list resolver: %v", addr)
	return r
}

func (r *IPListResolver) GetWeightEndpoints() []structs.WeightEndpoint {
	return r.endpoints
}
