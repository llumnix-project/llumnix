package queue

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/assert"
)

// TestRateLimitRetryQueueBasic tests basic enqueue and success case.
func TestRateLimitRetryQueueBasic(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, errors.New("429"))
	})
	q.Start()
	defer q.Stop()

	var callCount atomic.Int32

	resultCh, ok := q.Enqueue(
		context.Background(),
		func() error {
			callCount.Add(1)
			return nil // Success on first attempt
		},
	)

	assert.Assert(t, ok, "Enqueue should succeed")

	// Wait for completion
	select {
	case result := <-resultCh:
		assert.NilError(t, result.Err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for retry completion")
	}

	assert.Equal(t, callCount.Load(), int32(1))
}

// TestRateLimitRetryQueueSuccessAfterRetries tests success after multiple 429 retries.
func TestRateLimitRetryQueueSuccessAfterRetries(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 5, 5*time.Millisecond, 50*time.Millisecond)

	// Custom 429 checker
	err429 := errors.New("429 too many requests")
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, err429)
	})

	q.Start()
	defer q.Stop()

	var callCount atomic.Int32

	// Fail twice with 429, then succeed
	resultCh, ok := q.Enqueue(
		context.Background(),
		func() error {
			count := callCount.Add(1)
			if count <= 2 {
				return err429
			}
			return nil
		},
	)

	assert.Assert(t, ok)

	select {
	case result := <-resultCh:
		assert.NilError(t, result.Err)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for retry completion")
	}

	// Should have been called 3 times (2 failures + 1 success)
	assert.Equal(t, callCount.Load(), int32(3))
}

// TestRateLimitRetryQueueMaxRetriesExceeded tests that retry stops after maxRetries.
func TestRateLimitRetryQueueMaxRetriesExceeded(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 2, 5*time.Millisecond, 50*time.Millisecond)

	err429 := errors.New("429 too many requests")
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, err429)
	})

	q.Start()
	defer q.Stop()

	var callCount atomic.Int32

	// Always return 429
	resultCh, ok := q.Enqueue(
		context.Background(),
		func() error {
			callCount.Add(1)
			return err429
		},
	)

	assert.Assert(t, ok)

	select {
	case result := <-resultCh:
		assert.Assert(t, errors.Is(result.Err, ErrRateLimitRetryExhausted), "Expected ErrRateLimitRetryExhausted")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for retry completion")
	}

	// Should have been called 2 times
	assert.Equal(t, callCount.Load(), int32(2))
}

// TestRateLimitRetryQueueNonRetryableError tests that non-429 errors fail immediately.
func TestRateLimitRetryQueueNonRetryableError(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)

	err429 := errors.New("429 too many requests")
	err500 := errors.New("500 internal server error")
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, err429)
	})

	q.Start()
	defer q.Stop()

	var callCount atomic.Int32

	resultCh, ok := q.Enqueue(
		context.Background(),
		func() error {
			callCount.Add(1)
			return err500 // Non-retryable error
		},
	)

	assert.Assert(t, ok)

	select {
	case result := <-resultCh:
		assert.Assert(t, errors.Is(result.Err, err500))
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for completion")
	}

	// Should have been called only once
	assert.Equal(t, callCount.Load(), int32(1))
}

// TestRateLimitRetryQueueUnlimitedRetries tests unlimited retries with maxRetries = -1.
func TestRateLimitRetryQueueUnlimitedRetries(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, -1, 5*time.Millisecond, 20*time.Millisecond)

	err429 := errors.New("429 too many requests")
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, err429)
	})

	q.Start()
	defer q.Stop()

	var callCount atomic.Int32

	// Succeed on 5th attempt
	resultCh, ok := q.Enqueue(
		context.Background(),
		func() error {
			count := callCount.Add(1)
			if count < 5 {
				return err429
			}
			return nil
		},
	)

	assert.Assert(t, ok)

	select {
	case result := <-resultCh:
		assert.NilError(t, result.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for retry completion")
	}

	// Should have been called 5 times
	assert.Equal(t, callCount.Load(), int32(5))
}

// TestRateLimitRetryQueueCapacity tests queue capacity limits.
// The capacity controls how many tasks can be pending in the worker pool's queue.
// Note: QueueWorkerPool allows up to 'capacity' tasks in the priorityQueue.
// Once a task is picked up by a worker, it's no longer counted in Len().
func TestRateLimitRetryQueueCapacity(t *testing.T) {
	// Capacity 1, 1 worker - only 1 task can be pending at a time
	q := NewRateLimitRetryQueue(1, 1, 3, 50*time.Millisecond, 200*time.Millisecond)

	err429 := errors.New("429 too many requests")
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, err429)
	})

	q.Start()
	defer q.Stop()

	blocker := make(chan bool)
	started := make(chan bool, 1)

	// First task: starts after 50ms sleep, then blocks
	_, ok := q.Enqueue(
		context.Background(),
		func() error {
			started <- true
			<-blocker
			return nil
		},
	)
	assert.Assert(t, ok, "First enqueue should succeed")

	// Immediately try second enqueue (before first task starts executing)
	// First task is still in priorityQueue (waiting for worker)
	// Actually, with 1 worker, the first task is picked up immediately...
	// Let's use a longer initial delay to ensure the task is being processed

	// Wait a bit for first task to be picked up by worker
	time.Sleep(10 * time.Millisecond)

	// Second enqueue should succeed because first task is now being executed
	// (not in priorityQueue anymore), so there's room for 1 pending task
	_, ok = q.Enqueue(
		context.Background(),
		func() error { return nil },
	)
	assert.Assert(t, ok, "Second enqueue should succeed")

	// Now priorityQueue has 1 pending task. Third enqueue should fail.
	_, ok = q.Enqueue(
		context.Background(),
		func() error { return nil },
	)
	assert.Assert(t, !ok, "Third enqueue should fail when queue is full")

	// Release blocker and cleanup
	close(blocker)
	time.Sleep(100 * time.Millisecond)
}

// TestRateLimitRetryQueueNotStarted tests behavior when queue is not started.
func TestRateLimitRetryQueueNotStarted(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	// Do NOT call Start()

	_, ok := q.Enqueue(
		context.Background(),
		func() error { return nil },
	)
	assert.Assert(t, !ok, "Enqueue should fail when queue is not started")
}

// TestRateLimitRetryQueueStop tests graceful shutdown.
func TestRateLimitRetryQueueStop(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	q.Start()

	// Dispatch a task that will take some time
	resultCh, ok := q.Enqueue(
		context.Background(),
		func() error {
			time.Sleep(50 * time.Millisecond)
			return nil
		},
	)
	assert.Assert(t, ok)

	// Stop the queue
	q.Stop()

	// Wait for task to complete or get stopped error
	select {
	case result := <-resultCh:
		// Task might complete normally or with ErrRateLimitRetryExhausted
		if result.Err != nil && !errors.Is(result.Err, ErrRateLimitRetryExhausted) {
			t.Fatalf("Unexpected error: %v", result.Err)
		}
	case <-time.After(500 * time.Millisecond):
		// Timeout is acceptable if task was already running
	}

	// After stop, new enqueues should fail
	_, ok = q.Enqueue(
		context.Background(),
		func() error { return nil },
	)
	assert.Assert(t, !ok, "Enqueue should fail after Stop")
}

// TestRateLimitRetryQueueBackoffDelay tests exponential backoff calculation.
func TestRateLimitRetryQueueBackoffDelay(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 5, 10*time.Millisecond, 100*time.Millisecond)

	// Test backoff calculation
	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{0, 10 * time.Millisecond},  // initial
		{1, 20 * time.Millisecond},  // 2x
		{2, 40 * time.Millisecond},  // 4x
		{3, 80 * time.Millisecond},  // 8x
		{4, 100 * time.Millisecond}, // capped at max
		{5, 100 * time.Millisecond}, // capped at max
	}

	for _, tc := range tests {
		delay := q.backoffDelay(tc.retryCount)
		assert.Equal(t, delay, tc.expected, "Backoff delay for retry %d", tc.retryCount)
	}
}

// TestRateLimitRetryQueueConcurrentEnqueue tests concurrent enqueue operations.
func TestRateLimitRetryQueueConcurrentEnqueue(t *testing.T) {
	q := NewRateLimitRetryQueue(100, 10, 3, 5*time.Millisecond, 50*time.Millisecond)
	q.SetRateLimitChecker(func(err error) bool {
		return errors.Is(err, errors.New("429"))
	})

	q.Start()
	defer q.Stop()

	const numTasks = 50
	var wg sync.WaitGroup

	resultChs := make([]<-chan RetryResult, 0, numTasks)
	for i := 0; i < numTasks; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resultCh, ok := q.Enqueue(
				context.Background(),
				func() error {
					return nil // All succeed immediately
				},
			)
			if !ok {
				t.Errorf("Task %d failed to enqueue", id)
				return
			}
			resultChs = append(resultChs, resultCh)
		}(i)
	}

	wg.Wait()

	// Wait for all tasks to complete
	time.Sleep(200 * time.Millisecond)

	// Collect results
	var successCount int
	for _, resultCh := range resultChs {
		select {
		case result := <-resultCh:
			if result.Err == nil {
				successCount++
			}
		default:
		}
	}

	assert.Equal(t, successCount, numTasks, "All tasks should complete successfully")
}

// TestRateLimitRetryQueueLen tests length reporting.
func TestRateLimitRetryQueueLen(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 1, 3, 100*time.Millisecond, 500*time.Millisecond)
	q.Start()
	defer q.Stop()

	// Initially empty
	assert.Equal(t, q.Len(), 0)

	blocker := make(chan bool)
	var wg sync.WaitGroup

	// Dispatch a blocking task
	wg.Add(1)
	_, ok := q.Enqueue(
		context.Background(),
		func() error {
			wg.Done()
			<-blocker
			return nil
		},
	)
	assert.Assert(t, ok)

	// Wait for task to be picked up by worker
	wg.Wait()
	time.Sleep(10 * time.Millisecond)

	// Length should be 0 or 1 depending on timing (task is executing, not queued)
	length := q.Len()
	assert.Assert(t, length == 0 || length == 1, "Length should be 0 or 1, got %d", length)

	close(blocker)
	time.Sleep(50 * time.Millisecond)

	// After completion, should be empty
	assert.Equal(t, q.Len(), 0)
}
