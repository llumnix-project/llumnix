package ratelimiter

import (
	"container/list"
	"context"
	"llm-gateway/pkg/consts"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// Expired checks whether the item has exceeded its deadline.
func Expired(deadline time.Time) bool {
	return !deadline.IsZero() && time.Now().After(deadline)
}

const (
	defaultDoneRemoveDuration = 5 * time.Second
	defaultCleanInterval      = 5 * time.Second
	defaultProcessTimeout     = 30 * time.Second // Default timeout for item processing
)

// Item represents a rate-limited request item in the queue.
type Item struct {
	id       string
	callback func() error
	deadline time.Time // Absolute expiration time; zero means no timeout

	err      error         // Error set when error occurs
	done     chan struct{} // Signal channel to notify when processing is complete
	doneOnce sync.Once     // Ensures NotifyDone closes done channel exactly once
	doneTime time.Time     // Timestamp when Done() was called

	// Results from rate limiter filter, stored here to avoid closure capture race.
	results interface{}
}

// newItem creates a new Item with the given deadline.
// The callback field is set by Push after item creation.
func newItem(id string, deadline time.Time) *Item {
	return &Item{
		id:       id,
		deadline: deadline,
		done:     make(chan struct{}, 1),
	}
}

// Error returns the timeout error if the item timed out.
func (i *Item) Error() error {
	return i.err
}

// Key returns the unique identifier of the token request.
func (i *Item) Key() string {
	return i.id
}

// NotifyDone marks the item as completed and signals any waiting goroutines.
// Safe to call from multiple goroutines; the done channel is closed exactly once.
func (i *Item) NotifyDone() {
	i.doneOnce.Do(func() {
		i.doneTime = time.Now()
		close(i.done)
	})
}

// WaitDone waits for the item to be done.
func (i *Item) WaitDone() {
	<-i.done
}

// Done returns a channel that is closed when the item is done.
func (i *Item) Done() <-chan struct{} {
	return i.done
}

// RateLimitQueue is a thread-safe FIFO queue with efficient removal capability.
// It uses a doubly-linked list for FIFO ordering and a map for O(1) lookup/removal.
type RateLimitQueue struct {
	mutex sync.Mutex
	cond  *sync.Cond // Condition variable for blocking WaitPeek operations

	context context.Context
	list    *list.List               // Doubly-linked list maintaining FIFO order
	dict    map[string]*list.Element // Map for O(1) lookup, Element.Value is *Item

	doneRemoveDuration time.Duration        // Duration after which completed items are removed
	cleanInterval      time.Duration        // Interval for running the cleaner goroutine
	maxWaitDuration    func() time.Duration // Timeout duration for item processing
}

// NewRateLimitQueue creates a new rate limit queue with automatic cleanup.
// The cleaner goroutine removes items after they've been done for 'duration'.
func NewRateLimitQueue(c context.Context, maxWaitDuration func() time.Duration) *RateLimitQueue {
	rq := &RateLimitQueue{
		context:            c,
		list:               list.New(),
		dict:               make(map[string]*list.Element),
		doneRemoveDuration: defaultDoneRemoveDuration,
		cleanInterval:      defaultCleanInterval,
		maxWaitDuration:    maxWaitDuration,
	}
	rq.cond = sync.NewCond(&rq.mutex)
	rq.StartCleaner()
	return rq
}

// StartCleaner starts a background goroutine that periodically removes done items from dict.
// Items are removed from list by WaitPeek on retrieval, but remain in dict for client-side lookup
// The cleaner removes dict entries after items have been done for doneRemoveDuration.
func (q *RateLimitQueue) StartCleaner() {
	go func() {
		ticker := time.NewTicker(q.cleanInterval)
		defer ticker.Stop()
		for {
			select {
			case <-q.context.Done():
				klog.Infof("ratelimit queue cleaner stopped")
				// Wake up all waiting goroutines to prevent deadlock
				q.cond.Broadcast()
				return
			case <-ticker.C:
				q.mutex.Lock()
				// Iterate through the dict to remove expired items
				for key, element := range q.dict {
					item := element.Value.(*Item)
					// Non-blocking check: only process done items.
					// Receiving from a closed channel establishes happens-before
					// with the close in NotifyDone, guaranteeing doneTime visibility.
					select {
					case <-item.Done():
						if time.Since(item.doneTime) >= q.doneRemoveDuration {
							klog.Infof("remove invalid request %s from ratelimit queue", key)
							q.list.Remove(element)
							delete(q.dict, key)
						}
					default:
						// Not done yet, skip.
					}
				}
				q.mutex.Unlock()
			}
		}
	}()
}

// Push adds an item to the end of the queue with a deadline derived from maxWaitDuration.
// The callback receives the item itself so callers can store results on it without
// capturing an outer variable (which would race with the Push return-value assignment).
// Returns the existing item if one with the same id is already queued.
func (q *RateLimitQueue) Push(id string, cb func(*Item) error) *Item {
	deadline := time.Now().Add(q.maxWaitDuration())
	item := newItem(id, deadline)
	// Wrap cb so it receives the local item reference — no closure capture race.
	item.callback = func() error { return cb(item) }
	q.mutex.Lock()
	defer q.mutex.Unlock()

	key := item.Key()
	if element, ok := q.dict[key]; ok {
		klog.Warningf("ratelimit input queue: request %s already exists", key)
		return element.Value.(*Item)
	}

	// Add item to the tail of the list
	element := q.list.PushBack(item)
	q.dict[key] = element

	// Signal one waiting goroutine that a new item is available
	q.cond.Signal()

	return item
}

// WaitPeek blocks until an item is available and returns it.
// The item is removed from the FIFO list on return, but remains in dict for lookup
// Dict entries are cleaned up by the caller via RemoveItem or by the Cleaner.
// Returns nil if context is canceled.
func (q *RateLimitQueue) WaitPeek() *Item {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for {
		// Check if context is canceled
		select {
		case <-q.context.Done():
			klog.Infof("ratelimit queue wait peek stopped")
			return nil
		default:
		}

		// If queue is not empty, return the front item
		if q.list.Len() > 0 {
			front := q.list.Front()
			item := front.Value.(*Item)

			// Skip expired items directly
			if Expired(item.deadline) {
				klog.V(3).Infof("ratelimit queue: skipping expired item %s in WaitPeek", item.Key())
				q.list.Remove(front)
				delete(q.dict, item.Key())
				item.err = consts.ErrorRateLimitQueueTimeOut
				item.NotifyDone()
				continue
			}

			// Only remove from list, keep in dict for lookup
			q.list.Remove(front)
			return item
		}

		// Queue is empty, wait for new items
		q.cond.Wait()
	}
}

// GetItem retrieves an item by key without removing it from the queue.
func (q *RateLimitQueue) GetItem(key string) *Item {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if element, ok := q.dict[key]; ok {
		return element.Value.(*Item)
	}
	return nil
}

// RemoveItem removes an item from both the list and dict by key.
func (q *RateLimitQueue) RemoveItem(key string) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if element, ok := q.dict[key]; ok {
		// Remove from list if still present, then remove from dict
		q.list.Remove(element)
		delete(q.dict, key)
	}
}

// Len returns the number of items pending in the FIFO list
func (q *RateLimitQueue) Len() int {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	return q.list.Len()
}

// TrackedCount returns the total number of tracked items (pending + in-flight + done-awaiting-cleanup).
func (q *RateLimitQueue) TrackedCount() int {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	return len(q.dict)
}

// Empty returns true if no items are tracked (both list and dict are empty).
func (q *RateLimitQueue) Empty() bool {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	return q.list.Len() == 0 && len(q.dict) == 0
}

// RateLimitWorker executes queued items using a background worker loop.
// It is responsible for pulling items from the queue and invoking their callbacks until
// they either succeed or the worker context is cancelled.
type RateLimitWorker struct {
	queue         *RateLimitQueue
	retryInterval func() time.Duration
}

// NewRateLimitWorker creates a new worker bound to the given queue and retry interval provider.
func NewRateLimitWorker(queue *RateLimitQueue, retryInterval func() time.Duration) *RateLimitWorker {
	return &RateLimitWorker{
		queue:         queue,
		retryInterval: retryInterval,
	}
}

// Run starts the main worker loop that continuously processes items from the queue.
// For each item, the worker retries the callback until success or deadline expiration.
// The loop stops when the queue returns nil from WaitPeek (context cancelled).
func (w *RateLimitWorker) Run(ctx context.Context) {
	for {
		item := w.queue.WaitPeek()
		if item == nil {
			// Queue context cancelled, exit worker loop.
			return
		}

		// Retry callback until success or deadline
		for {
			if Expired(item.deadline) {
				klog.V(3).Infof("rate limit worker: request %s expired", item.id)
				item.err = consts.ErrorRateLimitQueueTimeOut
				item.NotifyDone()
				break
			}

			err := item.callback()
			if err == nil {
				item.NotifyDone()
				// Short delay to let scheduling/resource stats propagate
				// TODO: May need a better solution
				time.Sleep(2 * time.Millisecond)
				break
			}

			// Check if worker context has been cancelled
			select {
			case <-ctx.Done():
				klog.Infof("rate limit worker stopped")
				return
			default:
			}

			time.Sleep(w.retryInterval())
		}
	}
}
