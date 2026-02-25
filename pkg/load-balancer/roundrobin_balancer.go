package balancer

import (
	"context"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/types"
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
				for _, w := range workers {
					klog.Infof("add backend service endpoint: %s/%s", w.Role, w.String())
				}
			case workers := <-lb.delChan:
				lb.mu.Lock()
				for _, w := range workers {
					for i := 0; i < len(lb.workers); i++ {
						if lb.workers[i].Id() == w.Id() {
							klog.Infof("remove backend service endpoint: %s/%s", w.Role, w.String())
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

func (rrb *RoundRobinBalancer) Get(reqCtx *types.RequestContext) (types.ScheduledResult, error) {
	rrb.mu.Lock()
	defer rrb.mu.Unlock()

	if len(rrb.workers) == 0 {
		return nil, consts.ErrorNoAvailableEndpoint
	}

	// Filter out excluded instances
	availableWorkers := rrb.filterExcludedWorkers(reqCtx)
	if len(availableWorkers) == 0 {
		return nil, consts.ErrorNoAvailableEndpoint
	}

	rrb.currentIndex++
	index := rrb.currentIndex % uint64(len(availableWorkers))
	return types.ScheduledResult{availableWorkers[index]}, nil
}

// filterExcludedWorkers filters out workers that are in the excluded instances list.
func (rrb *RoundRobinBalancer) filterExcludedWorkers(reqCtx *types.RequestContext) types.LLMWorkerSlice {
	if reqCtx.ScheduleCtx == nil || len(reqCtx.ScheduleCtx.ExcludedInstances) == 0 {
		return rrb.workers
	}

	filtered := make(types.LLMWorkerSlice, 0, len(rrb.workers))
	for _, worker := range rrb.workers {
		if _, excluded := reqCtx.ScheduleCtx.ExcludedInstances[worker.Id()]; !excluded {
			filtered = append(filtered, worker)
		}
	}
	return filtered
}

func (rrb *RoundRobinBalancer) Release(*types.RequestContext, *types.LLMWorker) {}
