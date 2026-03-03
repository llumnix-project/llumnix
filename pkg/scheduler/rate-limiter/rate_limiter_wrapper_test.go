package ratelimiter

import (
	"container/list"
	"context"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/scheduler/lrs"
	"llm-gateway/pkg/types"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ============================================================
//  Mock types and test helpers
// ============================================================

// mockConfig implements RateLimiterConfigInterface for testing.
type mockConfig struct {
	enabled     bool
	limitScope  LimitScope
	limitAction LimitAction
}

func (m *mockConfig) GetConfigVersion() uint64             { return 0 }
func (m *mockConfig) Enabled() bool                        { return m.enabled }
func (m *mockConfig) LimitScope() LimitScope               { return m.limitScope }
func (m *mockConfig) LimitAction() LimitAction             { return m.limitAction }
func (m *mockConfig) MaxWaitTimeoutMs() int64              { return 5000 }
func (m *mockConfig) RetryIntervalMs() int64               { return 100 }
func (m *mockConfig) MaxRequestsPerInstance() int64        { return math.MaxInt64 }
func (m *mockConfig) MaxTokensPerInstance() int64          { return math.MaxInt64 }
func (m *mockConfig) MaxPrefillRequestsPerInstance() int64 { return math.MaxInt64 }
func (m *mockConfig) MaxPrefillTokensPerInstance() int64   { return math.MaxInt64 }
func (m *mockConfig) MaxDecodeRequestsPerInstance() int64  { return math.MaxInt64 }
func (m *mockConfig) MaxDecodeTokensPerInstance() int64    { return math.MaxInt64 }

// mockLimiter implements RateLimiter for wrapper testing.
type mockLimiter struct {
	filterFunc func(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView
}

func (m *mockLimiter) Filter(schReq *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
	return m.filterFunc(schReq, instances)
}

// passthroughLimiter returns a mock limiter that passes through all instances unchanged.
func passthroughLimiter() *mockLimiter {
	return &mockLimiter{
		filterFunc: func(_ *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
			return instances
		},
	}
}

// blockingLimiter returns a mock limiter that always filters out all instances.
func blockingLimiter() *mockLimiter {
	return &mockLimiter{
		filterFunc: func(_ *types.ScheduleRequest, _ []*lrs.InstanceView) []*lrs.InstanceView {
			return nil
		},
	}
}

// countingLimiter returns a mock limiter that passes through all instances and counts calls atomically.
func countingLimiter(counter *int32) *mockLimiter {
	return &mockLimiter{
		filterFunc: func(_ *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
			atomic.AddInt32(counter, 1)
			return instances
		},
	}
}

// newTestWrapperSimple creates a wrapper without a worker (for FilterInstances / Filter-reject tests).
func newTestWrapperSimple(cfg RateLimiterConfigInterface, instanceLimiter, serviceLimiter RateLimiter) (*RateLimiterWrapper, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := NewRateLimitQueue(ctx, func() time.Duration { return 500 * time.Millisecond })
	return &RateLimiterWrapper{
		cancel: cancel,
		config: cfg,
		limiters: map[string]LimiterSet{
			consts.NormalInferMode: {
				Instance: instanceLimiter,
				Service:  serviceLimiter,
			},
		},
		queue: queue,
	}, cancel
}

// newTestWrapperWithWorker creates a wrapper with a running queue + worker for QueueFilter tests.
func newTestWrapperWithWorker(
	cfg RateLimiterConfigInterface,
	instanceLimiter, serviceLimiter RateLimiter,
	maxQueueWait time.Duration,
) (*RateLimiterWrapper, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := &RateLimitQueue{
		context:            ctx,
		list:               list.New(),
		dict:               make(map[string]*list.Element),
		doneRemoveDuration: 20 * time.Millisecond,
		cleanInterval:      10 * time.Millisecond,
		maxWaitDuration:    func() time.Duration { return maxQueueWait },
	}
	queue.cond = sync.NewCond(&queue.mutex)
	queue.StartCleaner()

	worker := NewRateLimitWorker(queue, func() time.Duration { return time.Millisecond })
	go worker.Run(ctx)

	wrapper := &RateLimiterWrapper{
		cancel: cancel,
		config: cfg,
		limiters: map[string]LimiterSet{
			consts.NormalInferMode: {
				Instance: instanceLimiter,
				Service:  serviceLimiter,
			},
		},
		queue: queue,
	}
	return wrapper, cancel
}

// ============================================================
//  FilterInstances
// ============================================================

func TestFilterInstances_InstanceScope(t *testing.T) {
	var instanceCalls, serviceCalls int32
	cfg := &mockConfig{limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperSimple(cfg,
		countingLimiter(&instanceCalls),
		countingLimiter(&serviceCalls),
	)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)
	result := wrapper.FilterInstances(consts.NormalInferMode, schReq, instances)

	assert.Len(t, result, 1)
	assert.Equal(t, int32(1), atomic.LoadInt32(&instanceCalls))
	assert.Equal(t, int32(0), atomic.LoadInt32(&serviceCalls))
}

func TestFilterInstances_ServiceScope(t *testing.T) {
	var instanceCalls, serviceCalls int32
	cfg := &mockConfig{limitScope: LimitScopeService}
	wrapper, cancel := newTestWrapperSimple(cfg,
		countingLimiter(&instanceCalls),
		countingLimiter(&serviceCalls),
	)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)
	result := wrapper.FilterInstances(consts.NormalInferMode, schReq, instances)

	assert.Len(t, result, 1)
	assert.Equal(t, int32(0), atomic.LoadInt32(&instanceCalls))
	assert.Equal(t, int32(1), atomic.LoadInt32(&serviceCalls))
}

func TestFilterInstances_UnsupportedInferMode(t *testing.T) {
	cfg := &mockConfig{limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperSimple(cfg, passthroughLimiter(), passthroughLimiter())
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)
	result := wrapper.FilterInstances("unknown_mode", schReq, instances)

	assert.Nil(t, result)
}

func TestFilterInstances_UnsupportedScope(t *testing.T) {
	cfg := &mockConfig{limitScope: LimitScope("unknown")}
	wrapper, cancel := newTestWrapperSimple(cfg, passthroughLimiter(), passthroughLimiter())
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)
	result := wrapper.FilterInstances(consts.NormalInferMode, schReq, instances)

	assert.Nil(t, result)
}

// ============================================================
//  Filter
// ============================================================

func TestFilter_Disabled(t *testing.T) {
	cfg := &mockConfig{enabled: false}
	// Use blocking limiter to prove Filter never delegates when disabled.
	wrapper, cancel := newTestWrapperSimple(cfg, blockingLimiter(), blockingLimiter())
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)

	result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err)
	assert.Equal(t, instances, result)
}

func TestFilter_RejectAction_Pass(t *testing.T) {
	cfg := &mockConfig{enabled: true, limitAction: LimitActionReject, limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperSimple(cfg, passthroughLimiter(), passthroughLimiter())
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)

	result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestFilter_RejectAction_AllFiltered(t *testing.T) {
	cfg := &mockConfig{enabled: true, limitAction: LimitActionReject, limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperSimple(cfg, blockingLimiter(), blockingLimiter())
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)

	result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
	assert.ErrorIs(t, err, consts.ErrorRateLimitExceeded)
	assert.Nil(t, result)
}

func TestFilter_QueueAction_Success(t *testing.T) {
	cfg := &mockConfig{enabled: true, limitAction: LimitActionQueue, limitScope: LimitScopeInstance}
	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	wrapper, cancel := newTestWrapperWithWorker(cfg, passthroughLimiter(), passthroughLimiter(), 500*time.Millisecond)
	defer cancel()

	schReq := makeScheduleRequest("q-ok", 100)
	result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestFilter_UnknownAction(t *testing.T) {
	cfg := &mockConfig{enabled: true, limitAction: LimitAction("unknown"), limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperSimple(cfg, blockingLimiter(), blockingLimiter())
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("r1", 100)

	// Unknown action defaults to passthrough.
	result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err)
	assert.Equal(t, instances, result)
}

// ============================================================
//  QueueFilter
// ============================================================

func TestQueueFilter_CallbackSuccess(t *testing.T) {
	cfg := &mockConfig{limitScope: LimitScopeInstance}
	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	wrapper, cancel := newTestWrapperWithWorker(cfg, passthroughLimiter(), passthroughLimiter(), 500*time.Millisecond)
	defer cancel()

	schReq := makeScheduleRequest("q1", 100)
	result, err := wrapper.QueueFilter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestQueueFilter_RetryThenSuccess(t *testing.T) {
	var callCount int32
	flipLimiter := &mockLimiter{
		filterFunc: func(_ *types.ScheduleRequest, instances []*lrs.InstanceView) []*lrs.InstanceView {
			if atomic.AddInt32(&callCount, 1) <= 2 {
				return nil // Fail first 2 attempts
			}
			return instances // Succeed on 3rd
		},
	}

	cfg := &mockConfig{limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperWithWorker(cfg, flipLimiter, flipLimiter, 500*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("q-retry", 100)

	result, err := wrapper.QueueFilter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.True(t, atomic.LoadInt32(&callCount) >= 3, "expected at least 3 callback invocations")
}

func TestQueueFilter_WorkerDeadlineExpires(t *testing.T) {
	// Callback always fails → worker retries until deadline (50ms) expires → sets item.err
	cfg := &mockConfig{limitScope: LimitScopeInstance}
	wrapper, cancel := newTestWrapperWithWorker(cfg, blockingLimiter(), blockingLimiter(), 50*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("q-expire", 100)

	result, err := wrapper.QueueFilter(consts.NormalInferMode, schReq, instances)
	// Worker expires item with ErrorRateLimitQueueTimeOut, QueueFilter maps item.err to ErrorRateLimitExceeded.
	assert.ErrorIs(t, err, consts.ErrorRateLimitExceeded)
	assert.Nil(t, result)
}

func TestQueueFilter_DuplicateRequest(t *testing.T) {
	var callCount int32
	cfg := &mockConfig{limitScope: LimitScopeInstance}
	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	wrapper, cancel := newTestWrapperWithWorker(cfg, countingLimiter(&callCount), passthroughLimiter(), 500*time.Millisecond)
	defer cancel()

	schReq := makeScheduleRequest("q-dup", 100)

	// First call creates and processes the item.
	result1, err1 := wrapper.QueueFilter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err1)
	assert.Len(t, result1, 1)

	// Second call with same ID: item already removed, creates a new one.
	result2, err2 := wrapper.QueueFilter(consts.NormalInferMode, schReq, instances)
	assert.NoError(t, err2)
	assert.Len(t, result2, 1)

	// Limiter callback was invoked at least twice (once per successful queue processing).
	assert.True(t, atomic.LoadInt32(&callCount) >= 2)
}

func TestQueueFilter_RequestSideTimeout(t *testing.T) {
	// No worker started — items are never processed, triggering the 2s request-side timeout.
	cfg := &mockConfig{limitScope: LimitScopeInstance}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	queue := NewRateLimitQueue(ctx, func() time.Duration { return 10 * time.Second })
	wrapper := &RateLimiterWrapper{
		cancel: cancel,
		config: cfg,
		limiters: map[string]LimiterSet{
			consts.NormalInferMode: {
				Instance: passthroughLimiter(),
				Service:  passthroughLimiter(),
			},
		},
		queue: queue,
	}

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("q-rto", 100)

	start := time.Now()
	result, err := wrapper.QueueFilter(consts.NormalInferMode, schReq, instances)
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, consts.ErrorRateLimitQueueTimeOut)
	assert.Nil(t, result)
	assert.True(t, elapsed >= DefaultRequestWaitTimeoutMs-100*time.Millisecond,
		"expected timeout around %v, got %v", DefaultRequestWaitTimeoutMs, elapsed)
}

// ============================================================
//  Stop
// ============================================================

func TestStop(t *testing.T) {
	cfg := &mockConfig{}
	wrapper, _ := newTestWrapperSimple(cfg, passthroughLimiter(), passthroughLimiter())
	// Verify Stop does not panic.
	wrapper.Stop()
}

func TestStop_NilCancel(t *testing.T) {
	wrapper := &RateLimiterWrapper{}
	// Verify Stop with nil cancel does not panic.
	wrapper.Stop()
}

// ============================================================
//  GetRateLimiter singleton
// ============================================================

func TestGlobalRateLimiter_ReturnsSingletonInstance(t *testing.T) {
	rl1 := GetRateLimiter()
	rl2 := GetRateLimiter()
	assert.Same(t, rl1, rl2)
}

func TestGlobalRateLimiter_ImplementsInterfaceAndIsUsable(t *testing.T) {
	rl := GetRateLimiter()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	schReq := makeScheduleRequest("global-rl", 10)

	// We only verify that Filter can be called without panicking and Stop is safe to invoke.
	_, _ = rl.Filter(consts.NormalInferMode, schReq, instances)
	rl.Stop()
}
