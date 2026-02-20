package ratelimiter

import (
	"context"
	"fmt"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/types"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	// NOTE: The 2s wait timeout is chosen because the gateway-side scheduler client has a 3s request timeout.
	// By returning earlier with an explicit error, the gateway can distinguish rate-limit rejection from
	// a scheduler timeout, and make informed retry decisions accordingly.
	DefaultRequestWaitTimeoutMs = 2 * time.Second
)

// LimiterSet groups instance-scope and service-scope limiters for a given infer mode.
type LimiterSet struct {
	Instance RateLimiter
	Service  RateLimiter
}

var (
	globalRateLimiter     *RateLimiterWrapper
	globalRateLimiterOnce sync.Once
)

// GetRateLimiter returns a process-wide singleton instance.
func GetRateLimiter() *RateLimiterWrapper {
	globalRateLimiterOnce.Do(func() {
		globalRateLimiter = NewRateLimiterWrapper()
	})
	return globalRateLimiter
}

type RateLimiterWrapper struct {
	cancel context.CancelFunc
	config RateLimiterConfigInterface

	// Limiter mapping: inferMode -> (instance / service limiters).
	limiters map[string]LimiterSet

	queue *RateLimitQueue
}

func (r *RateLimiterWrapper) FilterInstances(inferMode string, schReq *types.ScheduleRequest, instanceViews []*lrs.InstanceView) []*lrs.InstanceView {
	scope := r.config.LimitScope()
	set, ok := r.limiters[inferMode]
	if !ok {
		klog.Errorf("rate limiter: unsupported infer mode %q", inferMode)
		return nil
	}

	// Select limiter based on scope configuration
	var limiter RateLimiter
	switch scope {
	case LimitScopeService:
		limiter = set.Service
	case LimitScopeInstance:
		limiter = set.Instance
	default:
		klog.Errorf("rate limiter: unsupported scope %q", scope)
		return nil
	}

	return limiter.Filter(schReq, instanceViews)
}

func (r *RateLimiterWrapper) QueueFilter(inferMode string, schReq *types.ScheduleRequest, instanceViews []*lrs.InstanceView) ([]*lrs.InstanceView, error) {
	item := r.queue.GetItem(schReq.Id)
	if item == nil {
		klog.V(3).Infof("rate limit: new request %s pushed to ratelimit queue", schReq.Id)
		item = r.queue.Push(schReq.Id, func(it *Item) error {
			// Callback invoked by the queue worker; use `it` (not the outer `item`)
			// to avoid a data race with the Push return-value assignment.
			filtered := r.FilterInstances(inferMode, schReq, instanceViews)
			it.results = filtered
			if len(filtered) == 0 {
				return fmt.Errorf("no available instances after request filter")
			}
			return nil
		})
	}

	// Wait for the item to be done, or timeout on the request side.
	deadline := time.Now().Add(DefaultRequestWaitTimeoutMs)
	timeout := time.Until(deadline)
	if timeout <= 0 {
		timeout = 1
	}
	select {
	case <-item.Done():
		// Item processed by Queue Worker
	case <-time.After(timeout):
		// Request-side timeout: Worker didn't process in time, the client may need to retry
		klog.Warningf("rate limit: request %s timed out waiting for worker", schReq.Id)
		return nil, consts.ErrorRateLimitQueueTimeOut
	}

	r.queue.RemoveItem(schReq.Id)
	if item.err != nil {
		klog.Warningf("rate limit: request %s failed with error %v", schReq.Id, item.err)
		// Return an error to indicate rate limit exceeded, the client should not retry and should reject the request
		return nil, consts.ErrorRateLimitExceeded
	}

	// Retrieve results from item
	if item.results != nil {
		return item.results.([]*lrs.InstanceView), nil
	}
	klog.Errorf("unexpected error: request %s has no results", schReq.Id)
	return nil, consts.ErrorRateLimitExceeded
}

// Enabled returns whether the rate limiter is enabled
func (r *RateLimiterWrapper) Enabled() bool {
	return r.config.Enabled()
}

// Filter filters the given instance views based on the rate limiter configuration.
func (r *RateLimiterWrapper) Filter(inferMode string, schReq *types.ScheduleRequest, instanceViews []*lrs.InstanceView) ([]*lrs.InstanceView, error) {
	if !r.config.Enabled() {
		return instanceViews, nil
	}
	if len(instanceViews) == 0 {
		return nil, consts.ErrorNoAvailableEndpoint
	}
	action := r.config.LimitAction()
	switch action {
	case LimitActionReject:
		results := r.FilterInstances(inferMode, schReq, instanceViews)
		if len(results) == 0 {
			return nil, consts.ErrorRateLimitExceeded
		}
		return results, nil
	case LimitActionQueue:
		return r.QueueFilter(inferMode, schReq, instanceViews)
	default:
		klog.Warningf("rate limiter: unknown action %v, defaulting to do nothing", action)
		return instanceViews, nil
	}
}

// Stop stops the rate limiter wrapper, cancelling any ongoing operations.
func (r *RateLimiterWrapper) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

func NewRateLimiterWrapper() *RateLimiterWrapper {
	config := NewRateLimiterConfig()
	cancelCtx, cancel := context.WithCancel(context.Background())

	limiters := map[string]LimiterSet{
		consts.NormalInferMode: {
			Instance: NewInstanceScopeRateLimiter(consts.NormalInferMode, config),
			Service:  NewServiceScopeRateLimiter(consts.NormalInferMode, config),
		},
		consts.PrefillInferMode: {
			Instance: NewInstanceScopeRateLimiter(consts.PrefillInferMode, config),
			Service:  NewServiceScopeRateLimiter(consts.PrefillInferMode, config),
		},
		consts.DecodeInferMode: {
			Instance: NewInstanceScopeRateLimiter(consts.DecodeInferMode, config),
			Service:  NewServiceScopeRateLimiter(consts.DecodeInferMode, config),
		},
	}

	queue := NewRateLimitQueue(cancelCtx, func() time.Duration {
		return time.Duration(config.MaxWaitTimeoutMs()) * time.Millisecond
	})

	worker := NewRateLimitWorker(queue, func() time.Duration {
		return time.Duration(config.RetryIntervalMs()) * time.Millisecond
	})

	rlw := &RateLimiterWrapper{
		cancel:   cancel,
		config:   config,
		limiters: limiters,
		queue:    queue,
	}
	go worker.Run(cancelCtx)
	return rlw
}

// excludeInstancesByHost filters out instances whose hosts are in the excluded list.
// It extracts host information from excluded instance IDs and removes all instances
// that match those hosts from the instance views map.
func excludeInstancesByHost(instanceViews map[string]*lrs.InstanceView, excludedInstances []string) map[string]*lrs.InstanceView {
	if len(excludedInstances) == 0 {
		return instanceViews
	}

	// Build a set of excluded hosts for efficient lookup
	excludedHosts := make(map[string]struct{})
	for _, excludedId := range excludedInstances {
		// Find the worker by excluded instance ID to get its host
		for _, iv := range instanceViews {
			if iv.GetInstanceId() == excludedId {
				worker := iv.GetInstance()
				if worker != nil {
					excludedHosts[worker.Endpoint.Host] = struct{}{}
				}
				break
			}
		}
	}

	if len(excludedHosts) == 0 {
		return instanceViews
	}

	// Filter out instances with excluded hosts
	filteredViews := make(map[string]*lrs.InstanceView)
	for instanceId, iv := range instanceViews {
		worker := iv.GetInstance()
		if worker != nil {
			host := worker.Endpoint.Host
			if _, excluded := excludedHosts[host]; !excluded {
				filteredViews[instanceId] = iv
			}
		}
	}
	return filteredViews
}

// Filter is a helper function, converting the map of instance views to a slice and calling the RateLimiterWrapper's Filter method.
func Filter(inferMode string, schReq *types.ScheduleRequest, instanceViews map[string]*lrs.InstanceView) (map[string]*lrs.InstanceView, error) {
	// Exclude instances with hosts in schReq.ExcludedInstances
	instanceViews = excludeInstancesByHost(instanceViews, schReq.ExcludedInstances)

	// Try to filter instances using the rate limiter
	r := GetRateLimiter()
	if !r.Enabled() {
		return instanceViews, nil
	}
	instanceViewsSlice := make([]*lrs.InstanceView, 0, len(instanceViews))
	for _, iv := range instanceViews {
		instanceViewsSlice = append(instanceViewsSlice, iv)
	}
	results, err := r.Filter(inferMode, schReq, instanceViewsSlice)
	if err != nil {
		return nil, err
	}
	instanceViewsMap := make(map[string]*lrs.InstanceView)
	for _, iv := range results {
		instanceViewsMap[iv.GetInstanceId()] = iv
	}
	return instanceViewsMap, nil
}
