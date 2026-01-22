package balancer

import (
	"context"
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/types"
	"sync"

	"k8s.io/klog/v2"
)

// RoundRobinBalancer implements simple round-robin load balancing algorithm.
// It cycles through endpoints in order, returning the next endpoint on each request.
type RoundRobinBalancer struct {
	Resolver resolver.LLMResolver

	addChan <-chan types.LLMWorkerSlice
	delChan <-chan types.LLMWorkerSlice

	mu           sync.Mutex
	currentIndex uint64
	workers      types.LLMWorkerSlice
}

// NewRoundRobinBalancer creates a new RoundRobinBalancer object.
func NewRoundRobinBalancer(r resolver.LLMResolver) *RoundRobinBalancer {
	lb := &RoundRobinBalancer{Resolver: r}

	addChan, delChan, err := r.Watch(context.Background())
	if err != nil {
		klog.Errorf("failed to watch LLM workers: %v", err)
		return nil
	}
	lb.addChan = addChan
	lb.delChan = delChan

	go func() {
		for {
			select {
			case workers := <-lb.addChan:
				lb.mu.Lock()
				lb.workers = append(lb.workers, workers...)
				lb.mu.Unlock()
			case workers := <-lb.delChan:
				lb.mu.Lock()
				for _, w := range workers {
					for i := 0; i < len(lb.workers); i++ {
						if lb.workers[i].Id() == w.Id() {
							lb.workers = append(lb.workers[:i], lb.workers[i+1:]...)
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

	if len(rrb.workers) == 0 {
		return nil, consts.ErrorNoAvailableEndpoint
	}

	rrb.currentIndex++
	index := rrb.currentIndex % uint64(len(rrb.workers))
	return types.ScheduledResult{rrb.workers[index]}, nil
}

func (rrb *RoundRobinBalancer) Release(*types.RequestContext, *types.LLMWorker) {}
