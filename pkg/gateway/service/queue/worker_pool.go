package queue

import (
	"runtime/debug"
	"sync"
	"sync/atomic"

	"k8s.io/klog/v2"
)

// TaskFunc represents a function that can be executed by the worker pool.
type TaskFunc func()

// QueueWorkerPool is a worker pool that manages a fixed number of goroutines (workers)
// to process tasks from a priority queue. Tasks can be dispatched with different
// priorities (high or normal). The pool provides thread-safe task dispatch and
// automatic worker management.
type QueueWorkerPool struct {
	priorityQueue *PriorityQueue // Underlying priority queue for task storage
	capacity      int            // Maximum number of tasks the queue can hold
	started       atomic.Bool    // Whether the worker pool has been started

	workerSize  int          // Number of worker goroutines
	busyWorkers atomic.Int32 // Count of currently busy workers
}

// NewQueueWorkerPool creates a new QueueWorkerPool with the specified capacity and worker count.
// Parameters:
//   - cap: maximum number of tasks the queue can hold
//   - workers: number of worker goroutines to start
//
// Returns a new QueueWorkerPool instance.
func NewQueueWorkerPool(cap int, workers int) *QueueWorkerPool {
	return &QueueWorkerPool{
		workerSize:    workers,
		capacity:      cap,
		priorityQueue: NewPriorityQueue(),
	}
}

// Dispatch adds a task to the worker pool queue with the specified priority.
// Parameters:
//   - TaskFunc: the function to execute
//   - isHighPriority: if true, the task gets higher priority in the queue
//
// Returns true if the task was successfully queued, false if the queue is full.
func (wp *QueueWorkerPool) Dispatch(TaskFunc func(), isHighPriority bool) bool {
	if !wp.started.Load() {
		klog.Warningf("worker pool not started")
		return false
	}
	if wp.priorityQueue.Len() >= int(wp.capacity) {
		klog.Warningf("queue is full, capacity: %d", wp.capacity)
		return false
	}
	wp.priorityQueue.Push(TaskFunc, isHighPriority)
	return true
}

// Length returns the current number of pending tasks in the queue.
func (wp *QueueWorkerPool) Length() int {
	return wp.priorityQueue.Len()
}

// BusyWorkers returns the number of workers currently executing tasks.
func (wp *QueueWorkerPool) BusyWorkers() int {
	return int(wp.busyWorkers.Load())
}

// waitDequeue is an internal method that blocks until a task is available from the queue.
// Returns the task function or nil if the queue is closed.
func (wp *QueueWorkerPool) waitDequeue() func() {
	item := wp.priorityQueue.WaitPop()
	if item == nil {
		return nil
	}
	return item.(func())
}

// waitDoTaskFunc is the main loop for each worker goroutine.
// It continuously dequeues tasks and executes them, with panic recovery.
func (wp *QueueWorkerPool) waitDoTaskFunc() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("worker crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go wp.waitDoTaskFunc()
		}
	}()

	for {
		TaskFunc := wp.waitDequeue()
		if TaskFunc == nil || !wp.started.Load() {
			klog.Warningf("wait queue is closed, exit worker")
			return
		}
		wp.busyWorkers.Add(1)
		TaskFunc()
		wp.busyWorkers.Add(-1)
	}
}

// Start begins processing tasks by launching the worker goroutines.
// This method should be called before dispatching any tasks.
func (wp *QueueWorkerPool) Start() {
	wp.started.Store(true)
	for i := 0; i < wp.workerSize; i++ {
		go wp.waitDoTaskFunc()
	}
}

// Stop gracefully shuts down the worker pool.
// It stops accepting new tasks and closes the underlying queue to wake up waiting workers.
func (wp *QueueWorkerPool) Stop() {
	wp.started.Store(false)
	wp.priorityQueue.Close()
}

type ModelQueueWorkerPool struct {
	enableMultiModel bool
	defaultSize      int
	currency         int
	mu               sync.Mutex
	workerPools      map[string]*QueueWorkerPool
}

func NewModelQueueWorkerPool(cap int, consumerCurrency int, enableMultiModel bool) *ModelQueueWorkerPool {
	return &ModelQueueWorkerPool{
		enableMultiModel: enableMultiModel,
		defaultSize:      cap,
		currency:         consumerCurrency,
		workerPools:      make(map[string]*QueueWorkerPool),
	}
}

func (mwp *ModelQueueWorkerPool) GetOrCreateModelQueueWorkerPool(model string) *QueueWorkerPool {
	if !mwp.enableMultiModel {
		model = ""
	}
	mwp.mu.Lock()
	defer mwp.mu.Unlock()
	q, ok := mwp.workerPools[model]
	if ok {
		return q
	}
	newQ := NewQueueWorkerPool(mwp.defaultSize, mwp.currency)
	newQ.Start()
	mwp.workerPools[model] = newQ
	return newQ
}

func (mwp *ModelQueueWorkerPool) Length() int {
	length := 0
	mwp.mu.Lock()
	defer mwp.mu.Unlock()
	for _, q := range mwp.workerPools {
		length += q.Length()
	}
	return length
}

func (mwp *ModelQueueWorkerPool) BusyWorkers() int {
	busy := 0
	mwp.mu.Lock()
	defer mwp.mu.Unlock()
	for _, q := range mwp.workerPools {
		busy += q.BusyWorkers()
	}
	return busy
}
