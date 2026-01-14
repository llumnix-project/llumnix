package queue

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gotest.tools/assert"
)

// TestPriorityQueueBasic tests basic functionality of the priority queue.
func TestPriorityQueueBasic(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Test empty queue
	assert.Equal(t, pq.Len(), 0)
	assert.Assert(t, pq.TryPop() == nil)

	// Push normal priority task
	pq.Push("task1", false)
	assert.Equal(t, pq.Len(), 1)

	// Push high priority task
	pq.Push("task2", true)
	assert.Equal(t, pq.Len(), 2)

	// TryPop should return high priority task first
	task := pq.TryPop()
	assert.Equal(t, task, "task2")
	assert.Equal(t, pq.Len(), 1)

	// Next should be normal priority task
	task = pq.TryPop()
	assert.Equal(t, task, "task1")
	assert.Equal(t, pq.Len(), 0)

	// Queue should be empty again
	assert.Assert(t, pq.TryPop() == nil)
}

// TestPriorityQueueFIFOOrder tests FIFO ordering for tasks with same priority.
func TestPriorityQueueFIFOOrder(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Push multiple tasks with same priority (normal)
	pq.Push("task1", false)
	pq.Push("task2", false)
	pq.Push("task3", false)

	// Tasks should be popped in FIFO order
	assert.Equal(t, pq.TryPop(), "task1")
	assert.Equal(t, pq.TryPop(), "task2")
	assert.Equal(t, pq.TryPop(), "task3")
}

// TestPriorityQueuePriorityOrder tests that higher priority tasks are popped first.
func TestPriorityQueuePriorityOrder(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Push tasks in mixed priority order
	pq.Push("normal1", false) // priority 1
	pq.Push("high1", true)    // priority 0
	pq.Push("normal2", false) // priority 1
	pq.Push("high2", true)    // priority 0
	pq.Push("normal3", false) // priority 1

	// High priority tasks should come first, in FIFO order
	assert.Equal(t, pq.TryPop(), "high1")
	assert.Equal(t, pq.TryPop(), "high2")

	// Then normal priority tasks, in FIFO order
	assert.Equal(t, pq.TryPop(), "normal1")
	assert.Equal(t, pq.TryPop(), "normal2")
	assert.Equal(t, pq.TryPop(), "normal3")
}

// TestPriorityQueueWaitPop tests blocking pop functionality.
func TestPriorityQueueWaitPop(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Start a goroutine that will push a task after a short delay
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond)
		pq.Push("delayed task", false)
	}()

	// WaitPop should block until task is available
	start := time.Now()
	task := pq.WaitPop()
	elapsed := time.Since(start)

	assert.Equal(t, task, "delayed task")
	assert.Assert(t, elapsed >= 50*time.Millisecond, "WaitPop should have waited at least 50ms")

	wg.Wait()
}

// TestPriorityQueueWaitPopWithExisting tests WaitPop when tasks already exist.
func TestPriorityQueueWaitPopWithExisting(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Push a task before calling WaitPop
	pq.Push("existing task", true)

	// WaitPop should return immediately
	start := time.Now()
	task := pq.WaitPop()
	elapsed := time.Since(start)

	assert.Equal(t, task, "existing task")
	assert.Assert(t, elapsed < 10*time.Millisecond, "WaitPop should return immediately when tasks exist")
}

// TestPriorityQueueConcurrentPush tests concurrent push operations.
func TestPriorityQueueConcurrentPush(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	const numGoroutines = 100
	const tasksPerGoroutine = 10
	var wg sync.WaitGroup

	// Start multiple goroutines pushing tasks
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < tasksPerGoroutine; j++ {
				// Alternate between high and normal priority
				isHighPriority := (id+j)%2 == 0
				task := taskID(id, j)
				pq.Push(task, isHighPriority)
			}
		}(i)
	}

	wg.Wait()

	// Verify total number of tasks
	expectedTotal := numGoroutines * tasksPerGoroutine
	assert.Equal(t, pq.Len(), expectedTotal)

	// Verify we can pop all tasks
	poppedCount := 0
	for pq.Len() > 0 {
		task := pq.TryPop()
		assert.Assert(t, task != nil)
		poppedCount++
	}

	assert.Equal(t, poppedCount, expectedTotal)
}

// TestPriorityQueueConcurrentPop tests concurrent pop operations.
func TestPriorityQueueConcurrentPop(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	const totalTasks = 1000
	const numConsumers = 10

	// Push all tasks first
	for i := 0; i < totalTasks; i++ {
		isHighPriority := i%3 == 0 // Every 3rd task is high priority
		pq.Push(i, isHighPriority)
	}

	assert.Equal(t, pq.Len(), totalTasks)

	// Channel to collect popped tasks
	results := make(chan interface{}, totalTasks)
	var wg sync.WaitGroup

	// Start consumer goroutines
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				task := pq.TryPop()
				if task == nil {
					return
				}
				results <- task
			}
		}()
	}

	wg.Wait()
	close(results)

	// Count and verify all tasks were popped
	poppedCount := 0
	for range results {
		poppedCount++
	}

	assert.Equal(t, poppedCount, totalTasks)
	assert.Equal(t, pq.Len(), 0)
}

// TestPriorityQueueConcurrentPushPop tests concurrent push and pop operations.
func TestPriorityQueueConcurrentPushPop(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	const numProducers = 5
	const numConsumers = 5
	const tasksPerProducer = 100

	var wg sync.WaitGroup
	var lenMutex sync.Mutex
	var cnt atomic.Int32
	results := make(chan interface{}, numProducers*tasksPerProducer)

	// Start producer goroutines
	for i := 0; i < numProducers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < tasksPerProducer; j++ {
				isHighPriority := (id+j)%2 == 0
				task := taskID(id, j)
				pq.Push(task, isHighPriority)
				// Small delay to allow interleaving
				time.Sleep(time.Microsecond)
			}
		}(i)
	}

	// Start consumer goroutines
	for i := 0; i < numConsumers; i++ {
		wg.Add(1)
		cnt.Add(1)
		go func() {
			defer wg.Done()
			defer cnt.Add(-1)
			for {
				// Use WaitPop to block until tasks are available
				task := pq.WaitPop()
				if task != nil {
					results <- task
				}
				// Check if we've processed enough tasks
				// fmt.Printf("results len: %d\n", len(results))
				lenMutex.Lock()
				if len(results) >= numProducers*tasksPerProducer {
					lenMutex.Unlock()
					return
				}
				lenMutex.Unlock()
			}
		}()
	}

	time.Sleep(100 * time.Millisecond)
	pq.Close() // Close to wake up consumers when done

	wg.Wait()
	close(results)

	// Verify we got all tasks
	poppedCount := 0
	for range results {
		poppedCount++
	}

	assert.Equal(t, poppedCount, numProducers*tasksPerProducer)
}

// TestPriorityQueueClose tests that Close wakes up waiting goroutines.
func TestPriorityQueueClose(t *testing.T) {
	pq := NewPriorityQueue()

	// Start a goroutine that will block on WaitPop
	done := make(chan bool)
	go func() {
		task := pq.WaitPop()
		// After Close, WaitPop should return nil
		assert.Assert(t, task == nil)
		done <- true
	}()

	// Give goroutine time to start blocking
	time.Sleep(50 * time.Millisecond)

	// Close should wake up the waiting goroutine
	pq.Close()

	// Wait for goroutine to finish
	select {
	case <-done:
		// Success
	case <-time.After(100 * time.Millisecond):
		t.Fatal("WaitPop did not return after Close")
	}
}

// TestPriorityQueueLenConcurrent tests Len() with concurrent modifications.
func TestPriorityQueueLenConcurrent(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	const numOperations = 1000
	var wg sync.WaitGroup

	// Start goroutines that push and pop concurrently
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < numOperations/10; j++ {
				if (id+j)%2 == 0 {
					pq.Push(taskID(id, j), false)
				} else {
					pq.TryPop()
				}
			}
		}(i)
	}

	wg.Wait()

	// Len should still work correctly
	length := pq.Len()
	assert.Assert(t, length >= 0, "Queue length should not be negative")

	// Pop remaining items
	for pq.Len() > 0 {
		assert.Assert(t, pq.TryPop() != nil)
	}

	assert.Equal(t, pq.Len(), 0)
}

// TestPriorityQueueMixedOperations tests mixed operations in sequence.
func TestPriorityQueueMixedOperations(t *testing.T) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Sequence of operations
	pq.Push("task1", true)
	pq.Push("task2", false)
	assert.Equal(t, pq.Len(), 2)

	assert.Equal(t, pq.TryPop(), "task1")
	assert.Equal(t, pq.Len(), 1)

	pq.Push("task3", true)
	assert.Equal(t, pq.Len(), 2)

	assert.Equal(t, pq.TryPop(), "task3") // High priority
	assert.Equal(t, pq.TryPop(), "task2") // Normal priority

	assert.Equal(t, pq.Len(), 0)
	assert.Assert(t, pq.TryPop() == nil)
}

// taskID generates a unique task identifier for testing.
func taskID(goroutineID, taskNum int) string {
	return string(rune('A'+goroutineID)) + string(rune('a'+taskNum))
}

// BenchmarkPriorityQueueSingleProducerSingleConsumer benchmarks single producer, single consumer scenario
func BenchmarkPriorityQueueSingleProducerSingleConsumer(b *testing.B) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Start producer goroutine
	done := make(chan bool)
	go func() {
		for i := 0; i < b.N; i++ {
			pq.Push(i, i%2 == 0)
		}
		done <- true
	}()

	b.ResetTimer()
	// Consumer loop
	for i := 0; i < b.N; i++ {
		pq.WaitPop()
	}
	<-done
}

// BenchmarkPriorityQueueMultiProducerMultiConsumer benchmarks multiple producers and consumers
func BenchmarkPriorityQueueMultiProducerMultiConsumer(b *testing.B) {
	pq := NewPriorityQueue()
	defer pq.Close()

	const numProducers = 10
	const numConsumers = 10

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		// Each parallel goroutine acts as both producer and consumer
		for pb.Next() {
			// Simulate one producer-consumer cycle
			var wg sync.WaitGroup

			// Start producers
			for i := 0; i < numProducers; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					// Each producer pushes one item
					pq.Push(id, id%2 == 0)
				}(i)
			}

			// Start consumers
			for i := 0; i < numConsumers; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// Each consumer pops one item
					pq.WaitPop()
				}()
			}

			wg.Wait()
		}
	})
}

// BenchmarkPriorityQueuePush benchmarks push operations only
func BenchmarkPriorityQueuePush(b *testing.B) {
	pq := NewPriorityQueue()
	defer pq.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.Push(i, i%2 == 0)
	}
}

// BenchmarkPriorityQueuePop benchmarks pop operations only (with pre-filled queue)
func BenchmarkPriorityQueuePop(b *testing.B) {
	pq := NewPriorityQueue()
	defer pq.Close()

	// Pre-fill the queue
	for i := 0; i < b.N; i++ {
		pq.Push(i, i%2 == 0)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		pq.TryPop()
	}
}
