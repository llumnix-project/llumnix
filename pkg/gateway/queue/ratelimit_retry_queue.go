package queue

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
)

var ErrRateLimitRetryQueueFull = errors.New("rate limit retry queue is full")

var ErrRateLimitRetryExhausted = errors.New("rate limit retry exhausted: max retry count reached")

// RateLimitRetryQueue is a delay-retry queue for handling upstream 429 responses.
//
// Each retry task is dispatched into the worker pool immediately; the
// backoff delay is applied inside the TaskFunc via time.Sleep.
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

	workerPool *QueueWorkerPool
	started    atomic.Bool
}

func NewRateLimitRetryQueue(maxQueueSize, workerSize, maxRetries int, initialDelay, maxDelay time.Duration) *RateLimitRetryQueue {
	return &RateLimitRetryQueue{
		maxQueueSize: maxQueueSize,
		maxRetries:   maxRetries,
		initialDelay: initialDelay,
		maxDelay:     maxDelay,
		workerPool:   NewQueueWorkerPool(maxQueueSize, workerSize),
	}
}

func IsRateLimitError(err error) bool {
	var upstreamErr *consts.UpstreamError
	return errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusTooManyRequests
}

func (q *RateLimitRetryQueue) Len() int {
	return q.workerPool.Length()
}

func (q *RateLimitRetryQueue) Start() {
	q.workerPool.Start()
	q.started.Store(true)
}

func (q *RateLimitRetryQueue) Stop() {
	q.started.Store(false)
	q.workerPool.Stop()
}

type RetryResult struct {
	Err error
}

// Enqueue adds a retry task to the worker pool.
// Returns a channel to receive the final result, and a bool indicating success.
func (q *RateLimitRetryQueue) Enqueue(ctx context.Context, retryFn func() error) (<-chan RetryResult, bool) {
	if !q.started.Load() || q.maxRetries == 0 {
		return nil, false
	}

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

func (q *RateLimitRetryQueue) retryLoop(ctx context.Context, retryFn func() error, resultCh chan<- RetryResult) {
	for retryCount := 0; ; retryCount++ {
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

		if !IsRateLimitError(err) {
			resultCh <- RetryResult{Err: err}
			return
		}

		nextCount := retryCount + 1
		if q.maxRetries > 0 && nextCount >= q.maxRetries {
			klog.Warningf("rate limit retry exhausted after %d retries", retryCount)
			resultCh <- RetryResult{Err: ErrRateLimitRetryExhausted}
			return
		}

		klog.V(3).Infof("rate limit retry: will attempt %d after %s", nextCount, q.backoffDelay(nextCount))
	}
}

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
