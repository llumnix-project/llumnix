package balancer

import (
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRoundRobinBalancer_BasicRotation tests the basic round-robin behavior
func TestRoundRobinBalancer_BasicRotation(t *testing.T) {
	// Create resolver with 3 endpoints
	uri := "llm+endpoints://192.168.1.1:8080,192.168.1.2:8080,192.168.1.3:8080"
	r, err := resolver.BuildLlmResolver(uri, resolver.BuildArgs{"role": types.InferRoleNormal.String()})
	assert.NoError(t, err)

	lb := NewRoundRobinBalancer(r, consts.RetryExcludeScopeInstance)
	assert.NotNil(t, lb)

	// Wait for workers to be loaded
	workers, err := r.GetLLMWorkers()
	assert.NoError(t, err)
	assert.Len(t, workers, 3)

	// Simulate workers being added
	lb.workers = workers

	// Create request context without exclusions
	reqCtx := &types.RequestContext{
		Id: "test-request",
		ScheduleCtx: &types.ScheduleContext{
			NeedSchedule:      true,
			ExcludedInstances: make(map[string]struct{}),
		},
	}

	// Test round-robin: should cycle through all 3 workers
	results := make(map[string]int)
	for i := 0; i < 9; i++ {
		result, err := lb.Get(reqCtx)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		results[result[0].Endpoint.String()]++
	}

	// Each worker should be selected 3 times (9 requests / 3 workers)
	assert.Equal(t, 3, results["192.168.1.1:8080"])
	assert.Equal(t, 3, results["192.168.1.2:8080"])
	assert.Equal(t, 3, results["192.168.1.3:8080"])
}

// TestRoundRobinBalancer_ExcludeInstance tests instance-level exclusion
func TestRoundRobinBalancer_ExcludeInstance(t *testing.T) {
	// Create resolver with 3 endpoints
	uri := "llm+endpoints://192.168.1.1:8080,192.168.1.2:8080,192.168.1.3:8080"
	r, err := resolver.BuildLlmResolver(uri, resolver.BuildArgs{"role": types.InferRoleNormal.String()})
	assert.NoError(t, err)

	lb := NewRoundRobinBalancer(r, consts.RetryExcludeScopeInstance)
	workers, err := r.GetLLMWorkers()
	assert.NoError(t, err)
	lb.workers = workers

	// Exclude one specific worker instance
	reqCtx := &types.RequestContext{
		Id: "test-request-exclude",
		ScheduleCtx: &types.ScheduleContext{
			NeedSchedule: true,
			ExcludedInstances: map[string]struct{}{
				workers[0].Id(): {}, // Exclude first worker
			},
		},
	}

	// Test that excluded worker is not selected
	results := make(map[string]int)
	for i := 0; i < 10; i++ {
		result, err := lb.Get(reqCtx)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		results[result[0].Endpoint.String()]++
	}

	// First worker should not be selected
	assert.Equal(t, 0, results["192.168.1.1:8080"])
	// Other two workers should be selected
	assert.Greater(t, results["192.168.1.2:8080"], 0)
	assert.Greater(t, results["192.168.1.3:8080"], 0)
}

// TestRoundRobinBalancer_ExcludeHost tests host-level exclusion
func TestRoundRobinBalancer_ExcludeHost(t *testing.T) {
	// Create resolver with endpoints on different hosts
	// Host1: 192.168.1.1 with two ports
	// Host2: 192.168.1.2 with one port
	uri := "llm+endpoints://192.168.1.1:8080,192.168.1.1:8081,192.168.1.2:8080"
	r, err := resolver.BuildLlmResolver(uri, resolver.BuildArgs{"role": types.InferRoleNormal.String()})
	assert.NoError(t, err)

	lb := NewRoundRobinBalancer(r, consts.RetryExcludeScopeHost)
	workers, err := r.GetLLMWorkers()
	assert.NoError(t, err)
	lb.workers = workers

	// Exclude one worker on host 192.168.1.1
	// This should exclude ALL workers on that host (both :8080 and :8081)
	reqCtx := &types.RequestContext{
		Id: "test-request-exclude-host",
		ScheduleCtx: &types.ScheduleContext{
			NeedSchedule: true,
			ExcludedInstances: map[string]struct{}{
				workers[0].Id(): {}, // Exclude first worker on 192.168.1.1:8080
			},
		},
	}

	// Test that all workers on host 192.168.1.1 are excluded
	results := make(map[string]int)
	for i := 0; i < 10; i++ {
		result, err := lb.Get(reqCtx)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		results[result[0].Endpoint.String()]++
	}

	// Both workers on host 192.168.1.1 should not be selected
	assert.Equal(t, 0, results["192.168.1.1:8080"])
	assert.Equal(t, 0, results["192.168.1.1:8081"])
	// Only worker on host 192.168.1.2 should be selected
	assert.Equal(t, 10, results["192.168.1.2:8080"])
}

// TestRoundRobinBalancer_ExcludeAll tests when all workers are excluded
func TestRoundRobinBalancer_ExcludeAll(t *testing.T) {
	uri := "llm+endpoints://192.168.1.1:8080,192.168.1.2:8080"
	r, err := resolver.BuildLlmResolver(uri, resolver.BuildArgs{"role": types.InferRoleNormal.String()})
	assert.NoError(t, err)

	lb := NewRoundRobinBalancer(r, consts.RetryExcludeScopeInstance)
	workers, err := r.GetLLMWorkers()
	assert.NoError(t, err)
	lb.workers = workers

	// Exclude all workers
	reqCtx := &types.RequestContext{
		Id: "test-request-exclude-all",
		ScheduleCtx: &types.ScheduleContext{
			NeedSchedule: true,
			ExcludedInstances: map[string]struct{}{
				workers[0].Id(): {},
				workers[1].Id(): {},
			},
		},
	}

	// Should return error when no workers are available
	result, err := lb.Get(reqCtx)
	assert.Error(t, err)
	assert.Equal(t, consts.ErrorNoAvailableEndpoint, err)
	assert.Nil(t, result)
}

// TestRoundRobinBalancer_EmptyExclusions tests behavior with no exclusions
func TestRoundRobinBalancer_EmptyExclusions(t *testing.T) {
	uri := "llm+endpoints://192.168.1.1:8080,192.168.1.2:8080"
	r, err := resolver.BuildLlmResolver(uri, resolver.BuildArgs{"role": types.InferRoleNormal.String()})
	assert.NoError(t, err)

	lb := NewRoundRobinBalancer(r, consts.RetryExcludeScopeInstance)
	workers, err := r.GetLLMWorkers()
	assert.NoError(t, err)
	lb.workers = workers

	// Empty exclusion list
	reqCtx := &types.RequestContext{
		Id: "test-request-no-exclude",
		ScheduleCtx: &types.ScheduleContext{
			NeedSchedule:      true,
			ExcludedInstances: map[string]struct{}{},
		},
	}

	// All workers should be available
	results := make(map[string]int)
	for i := 0; i < 10; i++ {
		result, err := lb.Get(reqCtx)
		assert.NoError(t, err)
		assert.Len(t, result, 1)
		results[result[0].Endpoint.String()]++
	}

	// Both workers should be selected
	assert.Greater(t, results["192.168.1.1:8080"], 0)
	assert.Greater(t, results["192.168.1.2:8080"], 0)
}
