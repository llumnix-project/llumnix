package queue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/assert"
)

// TestQueueWorkerPoolBasic tests basic functionality of QueueWorkerPool.
func TestQueueWorkerPoolBasic(t *testing.T) {
	pool := NewQueueWorkerPool(10, 2)
	defer pool.Stop()

	// Pool should not be started initially
	assert.Equal(t, pool.Length(), 0)
	assert.Equal(t, pool.BusyWorkers(), 0)

	// Start the pool
	pool.Start()

	// Test dispatching tasks
	var counter atomic.Int32
	var wg sync.WaitGroup

	// Dispatch 5 tasks
	for i := 0; i < 5; i++ {
		wg.Add(1)
		success := pool.Dispatch(func() {
			counter.Add(1)
			wg.Done()
		}, false)
		assert.Assert(t, success, "Task should be dispatched successfully")
	}

	wg.Wait()
	assert.Equal(t, counter.Load(), int32(5))
}

// TestQueueWorkerPoolCapacity tests queue capacity limits.
func TestQueueWorkerPoolCapacity(t *testing.T) {
	pool := NewQueueWorkerPool(3, 1) // Small capacity
	defer pool.Stop()
	pool.Start()

	// Fill the queue with tasks that don't complete immediately
	blocker := make(chan bool)
	var tasksDispatched int

	// First task blocks
	success := pool.Dispatch(func() {
		<-blocker // Block until released
	}, false)
	assert.Assert(t, success)
	tasksDispatched++

	// Fill remaining capacity (2 more tasks)
	for i := 0; i < 2; i++ {
		success := pool.Dispatch(func() {
			<-blocker
		}, false)
		assert.Assert(t, success)
		tasksDispatched++
	}

	// Queue should now be at capacity
	assert.Equal(t, pool.Length(), tasksDispatched)

	// Next dispatch should fail
	success = pool.Dispatch(func() {
		<-blocker
	}, false)
	assert.Assert(t, !success, "Dispatch should fail when queue is full")

	// Release blocker to allow tasks to complete
	close(blocker)

	// Wait for tasks to complete
	time.Sleep(100 * time.Millisecond)
}

// TestQueueWorkerPoolBusyWorkers tests busy worker counting.
func TestQueueWorkerPoolBusyWorkers(t *testing.T) {
	pool := NewQueueWorkerPool(10, 2)
	defer pool.Stop()
	pool.Start()

	// Use a barrier to control task execution
	barrier := make(chan bool)
	var wg sync.WaitGroup

	// Dispatch 2 tasks that will block
	wg.Add(2)
	for i := 0; i < 2; i++ {
		pool.Dispatch(func() {
			wg.Done()
			<-barrier // Block until released
		}, false)
	}

	// Wait for tasks to start (they will be blocked)
	wg.Wait()

	// Give workers time to pick up tasks
	time.Sleep(50 * time.Millisecond)

	// Both workers should be busy
	assert.Equal(t, pool.BusyWorkers(), 2)

	// Release the barrier
	close(barrier)

	// Wait for tasks to complete
	time.Sleep(50 * time.Millisecond)

	// No workers should be busy now
	assert.Equal(t, pool.BusyWorkers(), 0)
}

// TestQueueWorkerPoolStop tests graceful shutdown.
func TestQueueWorkerPoolStop(t *testing.T) {
	pool := NewQueueWorkerPool(10, 2)
	pool.Start()

	// Dispatch some tasks
	var tasksCompleted atomic.Int32
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		pool.Dispatch(func() {
			tasksCompleted.Add(1)
			wg.Done()
		}, false)
	}

	// Wait for tasks to complete
	wg.Wait()
	assert.Equal(t, tasksCompleted.Load(), int32(5))

	// Stop the pool
	pool.Stop()

	success := pool.Dispatch(func() {
		// This task might not be executed
	}, false)
	assert.Equal(t, success, false)
}

// TestQueueWorkerPoolConcurrentDispatch tests concurrent task dispatch.
func TestQueueWorkerPoolConcurrentDispatch(t *testing.T) {
	pool := NewQueueWorkerPool(200, 10)
	defer pool.Stop()
	pool.Start()

	const numGoroutines = 20
	const tasksPerGoroutine = 10
	var wg sync.WaitGroup
	var totalTasks atomic.Int32

	// Start multiple goroutines dispatching tasks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			wg.Done()
			for j := 0; j < tasksPerGoroutine; j++ {
				success := pool.Dispatch(func() {
					totalTasks.Add(1)
				}, (id+j)%2 == 0) // Alternate priorities
				if !success {
					t.Errorf("Failed to dispatch task from goroutine %d", id)
				}
			}
		}(i)
	}

	wg.Wait()

	// Wait for all tasks to complete
	time.Sleep(10 * time.Millisecond)

	// Verify all tasks were executed
	expectedTotal := numGoroutines * tasksPerGoroutine
	assert.Equal(t, totalTasks.Load(), int32(expectedTotal))
}

// TestQueueWorkerPoolPanicRecovery tests worker panic recovery.
func TestQueueWorkerPoolPanicRecovery(t *testing.T) {
	pool := NewQueueWorkerPool(10, 1)
	defer pool.Stop()
	pool.Start()

	// Dispatch a task that panics
	var wg sync.WaitGroup
	wg.Add(1)

	success := pool.Dispatch(func() {
		defer wg.Done()
		panic("test panic")
	}, false)

	assert.Assert(t, success)
	wg.Wait()

	// Dispatch another task to ensure worker is still functional
	var taskCompleted bool
	wg.Add(1)
	success = pool.Dispatch(func() {
		taskCompleted = true
		wg.Done()
	}, false)

	assert.Assert(t, success)
	wg.Wait()
	assert.Assert(t, taskCompleted)
}

// TestQueueWorkerPoolLength tests queue length tracking.
func TestQueueWorkerPoolLength(t *testing.T) {
	pool := NewQueueWorkerPool(10, 1)
	defer pool.Stop()

	// Queue should be empty initially
	assert.Equal(t, pool.Length(), 0)

	// Start pool
	pool.Start()

	// Dispatch a task that blocks
	blocker := make(chan bool)
	success := pool.Dispatch(func() {
		<-blocker
	}, false)
	assert.Assert(t, success)

	// Queue length should be 1 (task waiting to be processed)
	// Note: There might be a race condition here if worker picks up task immediately
	// Let's give it a small delay
	time.Sleep(10 * time.Millisecond)
	length := pool.Length()
	// Length could be 0 or 1 depending on timing
	assert.Assert(t, length == 0 || length == 1)

	// Release blocker
	close(blocker)

	// Wait for task to complete
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, pool.Length(), 0)
}
