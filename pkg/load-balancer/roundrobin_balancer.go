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
	excludeScope string
}

// NewRoundRobinBalancer creates a new RoundRobinBalancer object.
func NewRoundRobinBalancer(r resolver.LLMResolver, excludeScope string) *RoundRobinBalancer {
	lb := &RoundRobinBalancer{
		Resolver:     r,
		excludeScope: excludeScope,
	}

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

// filterExcludedWorkers filters out workers based on the exclude scope.
// When excludeScope is "host", it excludes all workers on the same host as the excluded instances.
// When excludeScope is "instance", it excludes only the specified worker instances.
func (rrb *RoundRobinBalancer) filterExcludedWorkers(reqCtx *types.RequestContext) types.LLMWorkerSlice {
	if reqCtx.ScheduleCtx == nil || len(reqCtx.ScheduleCtx.ExcludedInstances) == 0 {
		return rrb.workers
	}

	// exclude only the specified worker instances (instance scope)
	if rrb.excludeScope == consts.RetryExcludeScopeInstance {
		return rrb.filterExcludedWorkersByInstance(reqCtx)
	}
	// Default: exclude all workers on the same host (host scope)
	return rrb.filterExcludedWorkersByHost(reqCtx)
}

// filterExcludedWorkersByHost filters out workers whose hosts are in the excluded list.
func (rrb *RoundRobinBalancer) filterExcludedWorkersByHost(reqCtx *types.RequestContext) types.LLMWorkerSlice {
	// Build a set of excluded hosts for efficient lookup
	excludedHosts := make(map[string]struct{})
	for excludedId := range reqCtx.ScheduleCtx.ExcludedInstances {
		// Find the worker by excluded instance ID to get its host
		for _, worker := range rrb.workers {
			if worker.Id() == excludedId {
				excludedHosts[worker.Endpoint.Host] = struct{}{}
				break
			}
		}
	}

	if len(excludedHosts) == 0 {
		return rrb.workers
	}

	// Filter out workers with excluded hosts
	filtered := make(types.LLMWorkerSlice, 0, len(rrb.workers))
	for _, worker := range rrb.workers {
		if _, excluded := excludedHosts[worker.Endpoint.Host]; !excluded {
			filtered = append(filtered, worker)
		}
	}
	return filtered
}

// filterExcludedWorkersByInstance filters out only the specified worker instances.
func (rrb *RoundRobinBalancer) filterExcludedWorkersByInstance(reqCtx *types.RequestContext) types.LLMWorkerSlice {
	filtered := make(types.LLMWorkerSlice, 0, len(rrb.workers))
	for _, worker := range rrb.workers {
		if _, excluded := reqCtx.ScheduleCtx.ExcludedInstances[worker.Id()]; !excluded {
			filtered = append(filtered, worker)
		}
	}
	return filtered
}

func (rrb *RoundRobinBalancer) Release(*types.RequestContext, *types.LLMWorker) {}
