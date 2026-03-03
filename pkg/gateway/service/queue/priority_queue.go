package queue

import (
	"container/heap"
	"sync"
)

// Task represents any type that can be stored in the priority queue.
type Task any

// Item is an element in the priority queue.
// It contains a task with its priority and index for FIFO ordering.
type Item struct {
	priority int   // lower value means higher priority
	index    int64 // keep FIFO order among same priority
	task     Task
}

// LessCompare returns true if x should be ordered before y in the heap.
// Items are compared first by priority (lower value = higher priority),
// then by index (lower index = earlier insertion) for FIFO ordering.
func (x *Item) LessCompare(y *Item) bool {
	if x.priority != y.priority {
		return x.priority < y.priority
	}
	return x.index < y.index
}

// MinHeap implements a min-heap based on container/heap.Interface.
// The heap is ordered by priority (lower value = higher priority) and
// insertion index for FIFO ordering among equal priorities.
type MinHeap []*Item

// Len returns the number of elements in the heap.
func (h MinHeap) Len() int { return len(h) }

// Less returns true if element at index i should be ordered before element at index j.
func (h MinHeap) Less(i, j int) bool {
	return h[i].LessCompare(h[j])
}

// Swap swaps the elements at indices i and j.
func (h MinHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

// Push adds an item to the heap.
func (h *MinHeap) Push(item any) {
	*h = append(*h, item.(*Item))
}

// Pop removes and returns the smallest element from the heap.
func (h *MinHeap) Pop() any {
	old := *h
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	*h = old[0 : n-1]
	return item
}

// PriorityQueue is a thread-safe priority queue with FIFO ordering for equal priorities.
// It supports both blocking and non-blocking pop operations.
type PriorityQueue struct {
	h         MinHeap
	ready     bool
	nextIndex int64
	mutex     sync.Mutex
	cond      *sync.Cond
}

// NewPriorityQueue creates and initializes a new PriorityQueue.
func NewPriorityQueue() *PriorityQueue {
	pq := &PriorityQueue{
		ready: true,
	}
	pq.cond = sync.NewCond(&pq.mutex)
	heap.Init(&pq.h)
	return pq
}

// WaitPop blocks until an item is available, then removes and returns the highest priority task.
// If the queue is empty, it waits for a task to be pushed.
func (pq *PriorityQueue) WaitPop() Task {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()

	for pq.ready && pq.h.Len() == 0 {
		pq.cond.Wait()
	}
	if !pq.ready {
		return nil
	}
	item := heap.Pop(&pq.h).(*Item)
	return item.task
}

// TryPop removes and returns the highest priority task if available.
// Returns nil if the queue is empty (non-blocking).
func (pq *PriorityQueue) TryPop() Task {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()

	if !pq.ready || pq.h.Len() == 0 {
		return nil
	}

	item := heap.Pop(&pq.h).(*Item)
	return item.task
}

// Push adds a task to the queue with the specified priority.
// If isHigherPriority is true, the task gets priority 0 (highest),
// otherwise priority 1 (normal).
func (pq *PriorityQueue) Push(task Task, isHigherPriority bool) {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()

	priority := 1
	if isHigherPriority {
		priority = 0
	}

	item := &Item{
		task:     task,
		priority: priority,
		index:    pq.nextIndex,
	}
	pq.nextIndex++

	heap.Push(&pq.h, item)

	pq.cond.Broadcast()
}

// Len returns the current number of tasks in the queue.
func (pq *PriorityQueue) Len() int {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()
	return pq.h.Len()
}

// Close signals all waiting goroutines to wake up.
// This is useful for shutting down the queue gracefully.
func (pq *PriorityQueue) Close() {
	pq.mutex.Lock()
	defer pq.mutex.Unlock()
	pq.ready = false
	pq.cond.Broadcast()
}
