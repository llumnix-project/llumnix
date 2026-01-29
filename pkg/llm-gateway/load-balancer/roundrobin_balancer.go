package balancer

import (
	"context"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/resolver"
	"llumnix/pkg/llm-gateway/types"
	"sync"

	"k8s.io/klog/v2"
)

// RoundRobinBalancer implements simple round-robin load balancing algorithm.
// It cycles through endpoints in order, returning the next endpoint on each request.
type RoundRobinBalancer struct {
	Resolver resolver.LLMResolver

	addChan <-chan types.LLMInstanceSlice
	delChan <-chan types.LLMInstanceSlice

	mu           sync.Mutex
	currentIndex uint64
	instances    types.LLMInstanceSlice
}

// NewRoundRobinBalancer creates a new RoundRobinBalancer object.
func NewRoundRobinBalancer(r resolver.LLMResolver) *RoundRobinBalancer {
	lb := &RoundRobinBalancer{Resolver: r}

	addChan, delChan, err := r.Watch(context.Background())
	if err != nil {
		klog.Errorf("failed to watch LLM instances: %v", err)
		return nil
	}
	lb.addChan = addChan
	lb.delChan = delChan

	go func() {
		for {
			select {
			case instances := <-lb.addChan:
				lb.mu.Lock()
				lb.instances = append(lb.instances, instances...)
				lb.mu.Unlock()
			case instances := <-lb.delChan:
				lb.mu.Lock()
				for _, w := range instances {
					for i := 0; i < len(lb.instances); i++ {
						if lb.instances[i].Id() == w.Id() {
							lb.instances = append(lb.instances[:i], lb.instances[i+1:]...)
							break
						}
					}
				}
				lb.mu.Unlock()
			}
		}
	}()

	return lb
}

func (rrb *RoundRobinBalancer) Get(*types.RequestContext) (types.ScheduledResult, error) {
	rrb.mu.Lock()
	defer rrb.mu.Unlock()

	if len(rrb.instances) == 0 {
		return nil, consts.ErrorNoAvailableEndpoint
	}

	rrb.currentIndex++
	index := rrb.currentIndex % uint64(len(rrb.instances))
	klog.V(4).Infof("round-robin balancer: selected instance %s", rrb.instances[index].Id())

	return types.ScheduledResult{rrb.instances[index]}, nil
}

func (rrb *RoundRobinBalancer) Release(*types.RequestContext, *types.LLMInstance) {}
