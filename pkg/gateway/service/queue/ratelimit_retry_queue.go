package queue

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
)

// ErrRateLimitRetryQueueFull is returned when the retry queue is at capacity.
var ErrRateLimitRetryQueueFull = errors.New("rate limit retry queue is full")

// ErrRateLimitRetryExhausted is returned when all retry attempts have been exhausted.
var ErrRateLimitRetryExhausted = errors.New("rate limit retry exhausted: max retry count reached")

// RateLimitChecker is a function that checks whether an error is a 429 rate limit error.
// Callers should inject a concrete implementation via SetRateLimitChecker to avoid import cycles.
type RateLimitChecker func(err error) bool

// defaultRateLimitChecker is a no-op default: never matches (caller must override).
func defaultRateLimitChecker(_ error) bool { return false }

// RateLimitRetryQueue is a delay-retry queue for handling upstream 429 responses.
//
// It is built directly on top of QueueWorkerPool — there is no separate pending
// list. Each retry task is dispatched into the worker pool immediately; the
// backoff delay is applied inside the TaskFunc via time.Sleep, so the worker
// "holds" the slot during the wait period. This keeps the implementation simple
// and avoids a redundant scheduler goroutine.
//
// Capacity: maxQueueSize controls how many tasks (waiting + executing) the
// underlying worker pool may hold at once. Enqueue returns false when full.
//
// Retry policy:
//   - maxRetries == -1: retry indefinitely until the caller's context is cancelled.
//   - maxRetries >= 0: retry at most maxRetries additional times after the first failure.
//
// Backoff: delay = min(initialDelay * 2^retryCount, maxDelay)
type RateLimitRetryQueue struct {
	maxQueueSize int
	maxRetries   int
	initialDelay time.Duration
	maxDelay     time.Duration
	isRateLimit  RateLimitChecker

	workerPool *QueueWorkerPool
	started    atomic.Bool
}

// NewRateLimitRetryQueue creates a new RateLimitRetryQueue.
//
// Parameters:
//   - maxQueueSize: maximum number of tasks the worker pool may hold (waiting + executing).
//   - workerSize: number of concurrent worker goroutines.
//   - maxRetries: maximum number of retry attempts (-1 for unlimited).
//   - initialDelay: base backoff delay for the first retry.
//   - maxDelay: upper bound for exponential backoff delay.
func NewRateLimitRetryQueue(maxQueueSize, workerSize, maxRetries int, initialDelay, maxDelay time.Duration) *RateLimitRetryQueue {
	return &RateLimitRetryQueue{
		maxQueueSize: maxQueueSize,
		maxRetries:   maxRetries,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
		isRateLimit:  defaultRateLimitChecker,
		workerPool:   NewQueueWorkerPool(maxQueueSize, workerSize),
	}
}

// SetRateLimitChecker overrides the default 429 checker with a custom implementation.
// Must be called before Start().
func (q *RateLimitRetryQueue) SetRateLimitChecker(fn RateLimitChecker) {
	q.isRateLimit = fn
}

// Len returns the current number of tasks in the worker pool queue.
func (q *RateLimitRetryQueue) Len() int {
	return q.workerPool.Length()
}

// Start launches the underlying worker pool. Must be called before Enqueue.
func (q *RateLimitRetryQueue) Start() {
	q.workerPool.Start()
	q.started.Store(true)
}

// Stop shuts down the underlying worker pool.
func (q *RateLimitRetryQueue) Stop() {
	q.started.Store(false)
	q.workerPool.Stop()
}

// Enqueue adds a retry task to the worker pool with the initial backoff delay.
// retryFn is the function to retry.
// The ctx is used to cancel the retry loop when the request is cancelled.
// Returns a channel to receive the final result, and a bool indicating if the task was successfully dispatched.
// The result channel is buffered (capacity 1) and will be closed after sending the result.
// Returns (nil, false) if the queue is full or not started.
func (q *RateLimitRetryQueue) Enqueue(ctx context.Context, retryFn func() error) (<-chan RetryResult, bool) {
	if !q.started.Load() || q.maxRetries == 0 {
		return nil, false
	}

	// Use a channel to coordinate between the loop and worker pool callbacks.
	// Buffer 1 so the worker can send without blocking even if the loop is sleeping.
	resultCh := make(chan RetryResult, 1)

	dispatched := q.workerPool.Dispatch(func() {
		q.retryLoop(ctx, retryFn, resultCh)
	}, false)

	if !dispatched {
		klog.Warningf("rate limit retry queue is full (capacity: %d), dropping retry", q.maxQueueSize)
		return nil, false
	}

	return resultCh, true
}

// RetryResult is sent back from the retry loop to the waiting goroutine.
type RetryResult struct {
	Err error
}

// retryLoop runs the retry logic in a loop until completion.
// It executes on a worker pool goroutine and sends the final result via resultCh.
// The ctx is used to cancel the retry loop when the request is cancelled.
func (q *RateLimitRetryQueue) retryLoop(ctx context.Context, retryFn func() error, resultCh chan<- RetryResult) {
	for retryCount := 0; ; retryCount++ {
		// Apply backoff delay before executing.
		// Note: backoffDelay(0) returns initialDelay (not 0)
		delay := q.backoffDelay(retryCount)
		if delay > 0 {
			select {
			case <-ctx.Done():
				resultCh <- RetryResult{Err: ctx.Err()}
				return
			case <-time.After(delay):
			}
		}

		if !q.started.Load() {
			resultCh <- RetryResult{Err: ErrRateLimitRetryExhausted}
			return
		}

		// Check context cancellation before executing retry.
		select {
		case <-ctx.Done():
			resultCh <- RetryResult{Err: ctx.Err()}
			return
		default:
		}

		err := retryFn()
		if err == nil {
			resultCh <- RetryResult{Err: nil}
			klog.Infof("rate limit retry successful")
			return
		}

		// Non-429 error: propagate immediately.
		if !q.isRateLimit(err) {
			resultCh <- RetryResult{Err: err}
			return
		}

		// It is a 429. Check retry budget.
		// nextCount is the number of retries completed (0-indexed attempt becomes 1-indexed retry count).
		// If maxRetries=2, we allow retryCount 0,1,2 (3 attempts), exit when nextCount=3 > 2.
		nextCount := retryCount + 1
		if q.maxRetries > 0 && nextCount >= q.maxRetries {
			klog.Warningf("rate limit retry exhausted after %d retries", retryCount)
			resultCh <- RetryResult{Err: ErrRateLimitRetryExhausted}
			return
		}

		klog.V(3).Infof("rate limit retry: will attempt %d after %s", nextCount, q.backoffDelay(nextCount))
		// Loop continues for next retry attempt.
	}
}

// backoffDelay calculates the exponential backoff delay for a given retry count.
func (q *RateLimitRetryQueue) backoffDelay(retryCount int) time.Duration {
	delay := q.initialDelay
	for i := 0; i < retryCount; i++ {
		delay *= 2
		if delay > q.maxDelay {
			return q.maxDelay
		}
	}
	if delay > q.maxDelay {
		return q.maxDelay
	}
	return delay
}
