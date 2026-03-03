package ratelimiter

import (
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/scheduler/lrs"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

////////////////////////////////////////////////////////////////////////////
// Instance level rate limiter Configurations
////////////////////////////////////////////////////////////////////////////

// InstanceLimitRule defines the interface for instance level rate limit rules
type InstanceLimitRule interface {
	WithInLimit(schReq *types.ScheduleRequest, instance *lrs.InstanceView) bool
}

// InstanceRequestsLimitRule implements the instance level requests limit rule
type InstanceRequestsLimitRule struct {
	MaxRequestsPerInstance func() int64
}

func (c *InstanceRequestsLimitRule) WithInLimit(schReq *types.ScheduleRequest, instance *lrs.InstanceView) bool {
	maxReqs := c.MaxRequestsPerInstance()
	if maxReqs == 0 {
		return true
	}
	klog.V(3).Infof("InstanceRequestsLimitRule: current requests %d, max requests %d", instance.NumRequests()+1, maxReqs)
	return instance.NumRequests()+1 <= maxReqs
}

// InstanceTokensLimitRule implements the instance level tokens limit rule
type InstanceTokensLimitRule struct {
	MaxTokensPerInstance func() int64
}

func (c *InstanceTokensLimitRule) WithInLimit(schReq *types.ScheduleRequest, instance *lrs.InstanceView) bool {
	maxTokens := c.MaxTokensPerInstance()
	if maxTokens == 0 {
		return true
	}
	klog.V(3).Infof("InstanceTokensLimitRule: current tokens %d, max tokens %d", instance.NumTokens()+int64(schReq.GetPromptLen()), maxTokens)
	return instance.NumTokens()+int64(schReq.GetPromptLen()) <= maxTokens
}

// InstanceWaitingRequestsLimitRule implements the instance level waiting requests limit rule
type InstanceWaitingRequestsLimitRule struct {
	MaxWaitingRequestsPerInstance func() int64
}

func (c *InstanceWaitingRequestsLimitRule) WithInLimit(schReq *types.ScheduleRequest, instance *lrs.InstanceView) bool {
	maxWaitingReqs := c.MaxWaitingRequestsPerInstance()
	if maxWaitingReqs == 0 {
		return true
	}
	klog.V(3).Infof("InstanceWaitingRequestsLimitRule: current waiting requests %d, max waiting requests %d", instance.NumWaitingRequests()+1, maxWaitingReqs)
	return instance.NumWaitingRequests()+1 <= maxWaitingReqs
}

// InstanceWaitingTokensLimitRule implements the instance level waiting tokens limit rule
type InstanceWaitingTokensLimitRule struct {
	MaxWaitingTokensPerInstance func() int64
}

func (c *InstanceWaitingTokensLimitRule) WithInLimit(schReq *types.ScheduleRequest, instance *lrs.InstanceView) bool {
	maxWaitingTokens := c.MaxWaitingTokensPerInstance()
	if maxWaitingTokens == 0 {
		return true
	}
	klog.V(3).Infof("InstanceWaitingTokensLimitRule: current waiting tokens %d, max waiting tokens %d", instance.NumWaitingTokens()+int64(schReq.GetPromptLen()), maxWaitingTokens)
	return instance.NumWaitingTokens()+int64(schReq.GetPromptLen()) <= maxWaitingTokens
}

////////////////////////////////////////////////////////////////////////////
// Service level rate limiter Configurations
////////////////////////////////////////////////////////////////////////////

// ServiceLimitRule defines the interface for service level rate limit rules
type ServiceLimitRule interface {
	WithInLimit(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) bool
}

// ServiceRequestsLimitRule implements the service level requests limit rule
type ServiceRequestsLimitRule struct {
	MaxRequestsPerInstance func() int64
}

func (c *ServiceRequestsLimitRule) WithInLimit(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) bool {
	maxPerInstance := c.MaxRequestsPerInstance()
	if maxPerInstance == 0 {
		return true
	}

	var totalReqs int64 = 0
	for _, instance := range instances {
		totalReqs += instance.NumRequests()
	}

	maxTotal := maxPerInstance * int64(len(instances))
	klog.V(3).Infof("ServiceRequestsLimitRule: current total requests %d, max total requests %d", totalReqs+1, maxTotal)
	return totalReqs+1 <= maxTotal
}

// ServiceTokensLimitRule implements the service level tokens limit rule
type ServiceTokensLimitRule struct {
	MaxTokensPerInstance func() int64
}

func (c *ServiceTokensLimitRule) WithInLimit(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) bool {
	maxPerInstance := c.MaxTokensPerInstance()
	if maxPerInstance == 0 {
		return true
	}

	var totalTokens int64 = 0
	for _, instance := range instances {
		totalTokens += instance.NumTokens()
	}

	maxTotal := maxPerInstance * int64(len(instances))
	klog.V(3).Infof("ServiceTokensLimitRule: current total tokens %d, max total tokens %d", totalTokens+int64(schReq.GetPromptLen()), maxTotal)
	return totalTokens+int64(schReq.GetPromptLen()) <= maxTotal
}

// ServiceWaitingRequestsLimitRule implements the service level waiting requests limit rule
type ServiceWaitingRequestsLimitRule struct {
	MaxWaitingRequestsPerInstance func() int64
}

func (c *ServiceWaitingRequestsLimitRule) WithInLimit(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) bool {
	maxPerInstance := c.MaxWaitingRequestsPerInstance()
	if maxPerInstance == 0 {
		return true
	}

	var totalWaitingReqs int64 = 0
	for _, instance := range instances {
		totalWaitingReqs += instance.NumWaitingRequests()
	}

	maxTotal := maxPerInstance * int64(len(instances))
	klog.V(3).Infof("ServiceWaitingRequestsLimitRule: current total waiting requests %d, max total waiting requests %d", totalWaitingReqs+1, maxTotal)
	return totalWaitingReqs+1 <= maxTotal
}

// ServiceWaitingTokensLimitRule implements the service level waiting tokens limit rule
type ServiceWaitingTokensLimitRule struct {
	MaxWaitingTokensPerInstance func() int64
}

func (c *ServiceWaitingTokensLimitRule) WithInLimit(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) bool {
	maxPerInstance := c.MaxWaitingTokensPerInstance()
	if maxPerInstance == 0 {
		return true
	}

	var totalWaitingTokens int64 = 0
	for _, instance := range instances {
		totalWaitingTokens += instance.NumWaitingTokens()
	}

	maxTotal := maxPerInstance * int64(len(instances))
	klog.V(3).Infof("ServiceWaitingTokensLimitRule: current total waiting tokens %d, max total waiting tokens %d", totalWaitingTokens+int64(schReq.GetPromptLen()), maxTotal)
	return totalWaitingTokens+int64(schReq.GetPromptLen()) <= maxTotal
}

////////////////////////////////////////////////////////////////////////////
// 	Rate limiter
////////////////////////////////////////////////////////////////////////////

// RateLimiter defines the interface for rate limiters
type RateLimiter interface {
	Filter(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView
}

// InstanceRateLimiter implements the RateLimiter interface for instance level rate limiting
type InstanceRateLimiter struct {
	rules []InstanceLimitRule
}

func (r *InstanceRateLimiter) filterByRule(config InstanceLimitRule, schReq *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
	ret := make([]*lrs.InstanceView, 0, len(instances))
	for _, instance := range instances {
		if config.WithInLimit(schReq, instance) {
			ret = append(ret, instance)
		}
	}
	return ret
}

func (r *InstanceRateLimiter) Filter(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
	if klog.V(3).Enabled() {
		klog.Infof("instance filter request %s ", schReq.Id)
		for _, instance := range instances {
			klog.Infof("instance id: %s, tokens: %d, reqs: %d, waitingReqs: %d, waitingTokens: %d", instance.GetInstanceId(), instance.NumTokens(), instance.NumRequests(), instance.NumWaitingRequests(), instance.NumWaitingTokens())
		}
	}
	for _, c := range r.rules {
		instances = r.filterByRule(c, schReq, instances)
	}
	return instances
}

// ServiceRateLimiter implements the RateLimiter interface for service level rate limiting
type ServiceRateLimiter struct {
	config []ServiceLimitRule
}

func (r *ServiceRateLimiter) Filter(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
	if klog.V(3).Enabled() {
		klog.Infof("service filter request %s ", schReq.Id)
		for _, instance := range instances {
			klog.Infof("instance id: %s, tokens: %d, reqs: %d, waitingReqs: %d", instance.GetInstanceId(), instance.NumTokens(), instance.NumRequests(), instance.NumWaitingRequests(), instance.NumWaitingTokens())
		}
	}

	for _, c := range r.config {
		if ok := c.WithInLimit(schReq, instances); !ok {
			return nil
		}
	}
	return instances
}

func NewInstanceScopeRateLimiter(inferMode string, config RateLimiterConfigInterface) RateLimiter {
	var instanceRules []InstanceLimitRule

	switch inferMode {
	case consts.NormalInferMode:
		// max requests limit
		instanceRules = append(instanceRules, &InstanceRequestsLimitRule{
			MaxRequestsPerInstance: config.MaxRequestsPerInstance,
		})

		// max tokens limit
		instanceRules = append(instanceRules, &InstanceTokensLimitRule{
			MaxTokensPerInstance: config.MaxTokensPerInstance,
		})

		// max waiting requests limit
		instanceRules = append(instanceRules, &InstanceWaitingRequestsLimitRule{
			MaxWaitingRequestsPerInstance: config.MaxPrefillRequestsPerInstance,
		})

		// max waiting tokens limit
		instanceRules = append(instanceRules, &InstanceWaitingTokensLimitRule{
			MaxWaitingTokensPerInstance: config.MaxPrefillTokensPerInstance,
		})
	case consts.PrefillInferMode:
		instanceRules = append(instanceRules, &InstanceRequestsLimitRule{
			MaxRequestsPerInstance: config.MaxPrefillRequestsPerInstance,
		})
		instanceRules = append(instanceRules, &InstanceTokensLimitRule{
			MaxTokensPerInstance: config.MaxPrefillTokensPerInstance,
		})
	case consts.DecodeInferMode:
		instanceRules = append(instanceRules, &InstanceRequestsLimitRule{
			MaxRequestsPerInstance: config.MaxDecodeRequestsPerInstance,
		})
		instanceRules = append(instanceRules, &InstanceTokensLimitRule{
			MaxTokensPerInstance: config.MaxDecodeTokensPerInstance,
		})
	}

	return &InstanceRateLimiter{rules: instanceRules}
}

func NewServiceScopeRateLimiter(inferMode string, config RateLimiterConfigInterface) RateLimiter {
	var serviceRules []ServiceLimitRule

	switch inferMode {
	case consts.NormalInferMode:
		// max requests limit
		serviceRules = append(serviceRules, &ServiceRequestsLimitRule{
			MaxRequestsPerInstance: config.MaxRequestsPerInstance,
		})

		// max tokens limit
		serviceRules = append(serviceRules, &ServiceTokensLimitRule{
			MaxTokensPerInstance: config.MaxTokensPerInstance,
		})

		// max waiting requests limit
		serviceRules = append(serviceRules, &ServiceWaitingRequestsLimitRule{
			MaxWaitingRequestsPerInstance: config.MaxPrefillRequestsPerInstance,
		})

		// max waiting tokens limit
		serviceRules = append(serviceRules, &ServiceWaitingTokensLimitRule{
			MaxWaitingTokensPerInstance: config.MaxPrefillTokensPerInstance,
		})

	case consts.PrefillInferMode:
		serviceRules = append(serviceRules, &ServiceRequestsLimitRule{
			MaxRequestsPerInstance: config.MaxPrefillRequestsPerInstance,
		})

		serviceRules = append(serviceRules, &ServiceTokensLimitRule{
			MaxTokensPerInstance: config.MaxPrefillTokensPerInstance,
		})

	case consts.DecodeInferMode:
		serviceRules = append(serviceRules, &ServiceRequestsLimitRule{
			MaxRequestsPerInstance: config.MaxDecodeRequestsPerInstance,
		})

		serviceRules = append(serviceRules, &ServiceTokensLimitRule{
			MaxTokensPerInstance: config.MaxDecodeTokensPerInstance,
		})
	}

	return &ServiceRateLimiter{config: serviceRules}
}
