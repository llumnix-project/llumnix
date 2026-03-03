package ratelimiter

import (
	"container/list"
	"context"
	"fmt"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/scheduler/lrs"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ============================================================
//  E2E test infrastructure
// ============================================================

// e2eConfig implements RateLimiterConfigInterface with fully configurable limits.
// Int64 fields are accessed via atomic operations to support the dynamic-config test.
type e2eConfig struct {
	enabled     bool
	limitScope  LimitScope
	limitAction LimitAction

	maxRequestsPerInstance        int64
	maxTokensPerInstance          int64
	maxPrefillRequestsPerInstance int64
	maxPrefillTokensPerInstance   int64
	maxDecodeRequestsPerInstance  int64
	maxDecodeTokensPerInstance    int64
}

func (c *e2eConfig) GetConfigVersion() uint64 { return 0 }
func (c *e2eConfig) Enabled() bool            { return c.enabled }
func (c *e2eConfig) LimitScope() LimitScope   { return c.limitScope }
func (c *e2eConfig) LimitAction() LimitAction { return c.limitAction }
func (c *e2eConfig) MaxWaitTimeoutMs() int64  { return 5000 }
func (c *e2eConfig) RetryIntervalMs() int64   { return 100 }

func (c *e2eConfig) MaxRequestsPerInstance() int64 {
	return atomic.LoadInt64(&c.maxRequestsPerInstance)
}
func (c *e2eConfig) MaxTokensPerInstance() int64 { return atomic.LoadInt64(&c.maxTokensPerInstance) }
func (c *e2eConfig) MaxPrefillRequestsPerInstance() int64 {
	return atomic.LoadInt64(&c.maxPrefillRequestsPerInstance)
}
func (c *e2eConfig) MaxPrefillTokensPerInstance() int64 {
	return atomic.LoadInt64(&c.maxPrefillTokensPerInstance)
}
func (c *e2eConfig) MaxDecodeRequestsPerInstance() int64 {
	return atomic.LoadInt64(&c.maxDecodeRequestsPerInstance)
}
func (c *e2eConfig) MaxDecodeTokensPerInstance() int64 {
	return atomic.LoadInt64(&c.maxDecodeTokensPerInstance)
}

// defaultE2EConfig returns a config with all limits set to MaxInt64 (effectively no limit).
// Tests override only the specific limits they care about.
func defaultE2EConfig() *e2eConfig {
	return &e2eConfig{
		enabled:                       true,
		limitScope:                    LimitScopeInstance,
		limitAction:                   LimitActionReject,
		maxRequestsPerInstance:        math.MaxInt64,
		maxTokensPerInstance:          math.MaxInt64,
		maxPrefillRequestsPerInstance: math.MaxInt64,
		maxPrefillTokensPerInstance:   math.MaxInt64,
		maxDecodeRequestsPerInstance:  math.MaxInt64,
		maxDecodeTokensPerInstance:    math.MaxInt64,
	}
}

// buildRealLimiters constructs real limiter sets for all three infer modes.
func buildRealLimiters(cfg RateLimiterConfigInterface) map[string]LimiterSet {
	return map[string]LimiterSet{
		consts.NormalInferMode: {
			Instance: NewInstanceScopeRateLimiter(consts.NormalInferMode, cfg),
			Service:  NewServiceScopeRateLimiter(consts.NormalInferMode, cfg),
		},
		consts.PrefillInferMode: {
			Instance: NewInstanceScopeRateLimiter(consts.PrefillInferMode, cfg),
			Service:  NewServiceScopeRateLimiter(consts.PrefillInferMode, cfg),
		},
		consts.DecodeInferMode: {
			Instance: NewInstanceScopeRateLimiter(consts.DecodeInferMode, cfg),
			Service:  NewServiceScopeRateLimiter(consts.DecodeInferMode, cfg),
		},
	}
}

// newE2EWrapperSimple creates a wrapper with real limiters and NO worker (reject-mode tests).
func newE2EWrapperSimple(cfg RateLimiterConfigInterface) (*RateLimiterWrapper, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	queue := NewRateLimitQueue(ctx, func() time.Duration { return 500 * time.Millisecond })
	return &RateLimiterWrapper{
		cancel:   cancel,
		config:   cfg,
		limiters: buildRealLimiters(cfg),
		queue:    queue,
	}, cancel
}

// newE2EWrapperWithWorker creates a wrapper with real limiters and a running worker (queue-mode tests).
func newE2EWrapperWithWorker(cfg RateLimiterConfigInterface, maxQueueWait time.Duration) (*RateLimiterWrapper, context.CancelFunc) {
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

	return &RateLimiterWrapper{
		cancel:   cancel,
		config:   cfg,
		limiters: buildRealLimiters(cfg),
		queue:    queue,
	}, cancel
}

// ============================================================
//  Reject + Instance scope
// ============================================================

func TestE2E_Reject_Instance_RequestsLimit(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.maxRequestsPerInstance = 5
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 3, 100, 0), // 3+1=4 <= 5 → pass
		makeInstanceView("i2", 5, 100, 0), // 5+1=6 > 5 → fail
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

func TestE2E_Reject_Instance_TokensLimit(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.maxTokensPerInstance = 500
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 2, 100, 0), // tokens=200, 200+50=250 <= 500 → pass
		makeInstanceView("i2", 1, 460, 0), // tokens=460, 460+50=510 > 500 → fail
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 50), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

func TestE2E_Reject_Instance_WaitingRequestsLimit(t *testing.T) {
	cfg := defaultE2EConfig()
	// In Normal mode, MaxPrefillRequestsPerInstance → InstanceWaitingRequestsLimitRule
	cfg.maxPrefillRequestsPerInstance = 3
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 4, 100, 2), // waiting=4-2=2, 2+1=3 <= 3 → pass
		makeInstanceView("i2", 4, 100, 0), // waiting=4,     4+1=5 > 3  → fail
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

func TestE2E_Reject_Instance_WaitingTokensLimit(t *testing.T) {
	cfg := defaultE2EConfig()
	// In Normal mode, MaxPrefillTokensPerInstance → InstanceWaitingTokensLimitRule
	cfg.maxPrefillTokensPerInstance = 400
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 3, 100, 1), // waitingTokens=(3-1)*100=200, 200+50=250 <= 400 → pass
		makeInstanceView("i2", 4, 100, 0), // waitingTokens=4*100=400,     400+50=450 > 400 → fail
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 50), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

func TestE2E_Reject_Instance_AllFiltered(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.maxRequestsPerInstance = 2
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 3, 100, 0), // 3+1=4 > 2 → fail
		makeInstanceView("i2", 5, 100, 0), // 5+1=6 > 2 → fail
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.ErrorIs(t, err, consts.ErrorRateLimitExceeded)
	assert.Nil(t, result)
}

func TestE2E_Reject_Instance_MultiRuleCrossFilter(t *testing.T) {
	// Each instance passes one rule but fails another — neither survives.
	cfg := defaultE2EConfig()
	cfg.maxRequestsPerInstance = 5
	cfg.maxTokensPerInstance = 500
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 2, 240, 0), // reqs: 2+1=3 <= 5 ✓, tokens: 480+50=530 > 500 ✗
		makeInstanceView("i2", 5, 50, 0),  // reqs: 5+1=6 > 5 ✗
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 50), instances)
	assert.ErrorIs(t, err, consts.ErrorRateLimitExceeded)
	assert.Nil(t, result)
}

func TestE2E_Reject_Instance_EmptyInstances(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.maxRequestsPerInstance = 10
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	// When no instances available, return ErrorNoAvailableEndpoint (not rate limit error)
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), nil)
	assert.ErrorIs(t, err, consts.ErrorNoAvailableEndpoint)
	assert.Nil(t, result)
}

func TestE2E_Reject_Instance_LimitZeroMeansUnlimited(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.maxRequestsPerInstance = 0 // 0 means unlimited — request should pass
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.NotNil(t, result)
}

// ============================================================
//  Reject + Service scope
// ============================================================

func TestE2E_Reject_Service_Pass(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitScope = LimitScopeService
	// NOTE: Service rules multiply max by instance count; avoid MaxInt64 overflow.
	cfg.maxRequestsPerInstance = 10
	cfg.maxTokensPerInstance = 1000000
	cfg.maxPrefillRequestsPerInstance = 1000000
	cfg.maxPrefillTokensPerInstance = 1000000
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 3, 100, 0),
		makeInstanceView("i2", 4, 100, 0),
	}
	// total reqs = 7, +1 = 8 <= 10*2 = 20 → pass
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestE2E_Reject_Service_Fail(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitScope = LimitScopeService
	cfg.maxRequestsPerInstance = 3
	cfg.maxTokensPerInstance = 1000000
	cfg.maxPrefillRequestsPerInstance = 1000000
	cfg.maxPrefillTokensPerInstance = 1000000
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 3, 100, 0),
		makeInstanceView("i2", 4, 100, 0),
	}
	// total reqs = 7, +1 = 8 > 3*2 = 6 → fail
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.ErrorIs(t, err, consts.ErrorRateLimitExceeded)
	assert.Nil(t, result)
}

// ============================================================
//  Queue mode
// ============================================================

func TestE2E_Queue_Instance_Success(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitAction = LimitActionQueue
	cfg.maxRequestsPerInstance = 10
	wrapper, cancel := newE2EWrapperWithWorker(cfg, 500*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 2, 100, 0), // 2+1=3 <= 10 → pass
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("q-ok", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

func TestE2E_Queue_Instance_WorkerTimeout(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitAction = LimitActionQueue
	cfg.maxRequestsPerInstance = 1
	wrapper, cancel := newE2EWrapperWithWorker(cfg, 50*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 5, 100, 0), // 5+1=6 > 1 → always fails
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("q-timeout", 100), instances)
	assert.ErrorIs(t, err, consts.ErrorRateLimitExceeded)
	assert.Nil(t, result)
}

func TestE2E_Queue_Service_Success(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitAction = LimitActionQueue
	cfg.limitScope = LimitScopeService
	cfg.maxRequestsPerInstance = 10
	cfg.maxTokensPerInstance = 1000000
	cfg.maxPrefillRequestsPerInstance = 1000000
	cfg.maxPrefillTokensPerInstance = 1000000
	wrapper, cancel := newE2EWrapperWithWorker(cfg, 500*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 3, 100, 0),
		makeInstanceView("i2", 4, 100, 0),
	}
	// total reqs = 7, +1 = 8 <= 10*2 = 20 → pass
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("q-svc", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestE2E_Queue_DynamicConfigRelax(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitAction = LimitActionQueue
	cfg.maxRequestsPerInstance = 1 // initially everything blocked

	wrapper, cancel := newE2EWrapperWithWorker(cfg, 500*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 5, 100, 0), // 5+1=6 > 1 → fail initially
	}

	// Relax config after a short delay so the worker's next retry succeeds.
	go func() {
		time.Sleep(20 * time.Millisecond)
		atomic.StoreInt64(&cfg.maxRequestsPerInstance, 100) // 5+1=6 <= 100 → pass
	}()

	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("q-dyn", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

// ============================================================
//  Infer modes
// ============================================================

func TestE2E_Prefill_Instance(t *testing.T) {
	cfg := defaultE2EConfig()
	// Prefill mode uses MaxPrefillRequestsPerInstance / MaxPrefillTokensPerInstance
	cfg.maxPrefillRequestsPerInstance = 3
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 2, 100, 0), // 2+1=3 <= 3 → pass
		makeInstanceView("i2", 3, 100, 0), // 3+1=4 > 3  → fail
	}
	result, err := wrapper.Filter(consts.PrefillInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

func TestE2E_Decode_Instance(t *testing.T) {
	cfg := defaultE2EConfig()
	// Decode mode uses MaxDecodeRequestsPerInstance / MaxDecodeTokensPerInstance
	cfg.maxDecodeRequestsPerInstance = 3
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 2, 100, 0), // 2+1=3 <= 3 → pass
		makeInstanceView("i2", 3, 100, 0), // 3+1=4 > 3  → fail
	}
	result, err := wrapper.Filter(consts.DecodeInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "i1", result[0].GetInstanceId())
}

// ============================================================
//  Disabled
// ============================================================

func TestE2E_Disabled(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.enabled = false
	cfg.maxRequestsPerInstance = 1 // would reject if enabled
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 5, 100, 0), // would be rejected if enabled
	}
	result, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("r1", 100), instances)
	assert.NoError(t, err)
	assert.Equal(t, instances, result)
}

// ============================================================
//  Concurrent
// ============================================================

func TestE2E_Concurrent_Reject(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.maxRequestsPerInstance = 5
	wrapper, cancel := newE2EWrapperSimple(cfg)
	defer cancel()

	instances := []*lrs.InstanceView{
		makeInstanceView("i1", 2, 100, 0), // passes
		makeInstanceView("i2", 5, 100, 0), // fails
	}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			schReq := makeScheduleRequest(fmt.Sprintf("c-rej-%d", idx), 100)
			result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
			assert.NoError(t, err)
			assert.Len(t, result, 1)
			assert.Equal(t, "i1", result[0].GetInstanceId())
		}(i)
	}
	wg.Wait()
}

func TestE2E_Concurrent_Queue(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitAction = LimitActionQueue
	cfg.maxRequestsPerInstance = 100
	wrapper, cancel := newE2EWrapperWithWorker(cfg, 500*time.Millisecond)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 2, 100, 0)}

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			schReq := makeScheduleRequest(fmt.Sprintf("c-q-%d", idx), 100)
			result, err := wrapper.Filter(consts.NormalInferMode, schReq, instances)
			assert.NoError(t, err)
			assert.Len(t, result, 1)
		}(i)
	}
	wg.Wait()
}

// ============================================================
//  Stop
// ============================================================

func TestE2E_StopInterruptsQueue(t *testing.T) {
	cfg := defaultE2EConfig()
	cfg.limitAction = LimitActionQueue
	cfg.maxRequestsPerInstance = 1 // instance has 5 reqs → 5+1 > 1 → always blocked

	// Long queue wait so item stays retrying; we interrupt with Stop().
	wrapper, cancel := newE2EWrapperWithWorker(cfg, 5*time.Second)
	defer cancel()

	instances := []*lrs.InstanceView{makeInstanceView("i1", 5, 100, 0)}

	errCh := make(chan error, 1)
	go func() {
		_, err := wrapper.Filter(consts.NormalInferMode, makeScheduleRequest("stop-e2e", 100), instances)
		errCh <- err
	}()

	// Let item enter queue and worker start retrying.
	time.Sleep(30 * time.Millisecond)
	wrapper.Stop()

	// QueueFilter must return — verify no deadlock.
	// After Stop, worker exits; request-side timeout (2s) fires.
	select {
	case err := <-errCh:
		assert.Error(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("QueueFilter did not return after Stop — possible deadlock")
	}
}
