package ratelimiter

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"llm-gateway/pkg/consts"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test helpers ---

// newTestQueue creates a queue with a default 500ms wait timeout and fast cleaner for testing.
func newTestQueue(ctx context.Context) *RateLimitQueue {
	return newTestQueueFull(ctx, 500*time.Millisecond, 10*time.Millisecond)
}

// newTestQueueWithTimeout creates a queue with the given maxWait and fast cleaner.
func newTestQueueWithTimeout(ctx context.Context, maxWait time.Duration) *RateLimitQueue {
	return newTestQueueFull(ctx, maxWait, 10*time.Millisecond)
}

// newTestQueueFull creates a queue with full control over all timing parameters.
func newTestQueueFull(ctx context.Context, maxWait, cleanInterval time.Duration) *RateLimitQueue {
	q := &RateLimitQueue{
		context:            ctx,
		list:               list.New(),
		dict:               make(map[string]*list.Element),
		doneRemoveDuration: 20 * time.Millisecond,
		cleanInterval:      cleanInterval,
		maxWaitDuration:    func() time.Duration { return maxWait },
	}
	q.cond = sync.NewCond(&q.mutex)
	q.StartCleaner()
	return q
}

// ============================================================
//  Expired()
// ============================================================

func TestExpired(t *testing.T) {
	tests := []struct {
		name     string
		deadline time.Time
		want     bool
	}{
		{"zero deadline never expires", time.Time{}, false},
		{"future deadline not expired", time.Now().Add(time.Hour), false},
		{"past deadline expired", time.Now().Add(-time.Hour), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, Expired(tt.deadline))
		})
	}
}

// ============================================================
//  Item
// ============================================================

func TestItem_Key(t *testing.T) {
	item := newItem("req-123", time.Time{})
	assert.Equal(t, "req-123", item.Key())
}

func TestItem_Error(t *testing.T) {
	item := newItem("r1", time.Time{})
	assert.Nil(t, item.Error())

	item.err = errors.New("some error")
	assert.EqualError(t, item.Error(), "some error")
}

func TestItem_NotifyDone_Idempotent(t *testing.T) {
	item := newItem("r1", time.Time{})

	// Multiple calls must not panic (sync.Once protects close)
	assert.NotPanics(t, func() {
		item.NotifyDone()
		item.NotifyDone()
		item.NotifyDone()
	})

	// Channel should be closed after NotifyDone
	select {
	case <-item.Done():
	default:
		t.Fatal("done channel should be closed after NotifyDone")
	}

	// doneTime should be set
	assert.False(t, item.doneTime.IsZero())
}

func TestItem_WaitDone(t *testing.T) {
	item := newItem("r1", time.Time{})

	go func() {
		time.Sleep(10 * time.Millisecond)
		item.NotifyDone()
	}()

	done := make(chan struct{})
	go func() {
		item.WaitDone()
		close(done)
	}()

	select {
	case <-done:
		// OK: WaitDone returned
	case <-time.After(time.Second):
		t.Fatal("WaitDone did not return in time")
	}
}

func TestItem_Done_Channel(t *testing.T) {
	item := newItem("r1", time.Time{})
	ch := item.Done()

	// Before NotifyDone, channel should block
	select {
	case <-ch:
		t.Fatal("channel should block before NotifyDone")
	default:
	}

	item.NotifyDone()

	// After NotifyDone, channel should be readable
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel should be closed after NotifyDone")
	}
}

// ============================================================
//  RateLimitQueue — basic operations
// ============================================================

func TestRateLimitQueue_New(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	assert.NotNil(t, q)
	assert.Equal(t, 0, q.Len())
	assert.True(t, q.Empty())
}

func TestRateLimitQueue_Push(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	item := q.Push("req-1", func(*Item) error { return nil })

	assert.NotNil(t, item)
	assert.Equal(t, "req-1", item.Key())
	assert.Equal(t, 1, q.Len())
	assert.False(t, q.Empty())
}

func TestRateLimitQueue_Push_Duplicate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	item1 := q.Push("req-1", func(*Item) error { return nil })
	item2 := q.Push("req-1", func(*Item) error { return errors.New("other") })

	// Should return the existing item, not create a new one
	assert.Same(t, item1, item2)
	assert.Equal(t, 1, q.Len())
}

func TestRateLimitQueue_GetItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	q.Push("req-1", func(*Item) error { return nil })

	assert.NotNil(t, q.GetItem("req-1"))
	assert.Nil(t, q.GetItem("nonexistent"))
}

func TestRateLimitQueue_RemoveItem(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	q.Push("req-1", func(*Item) error { return nil })
	assert.Equal(t, 1, q.Len())

	q.RemoveItem("req-1")
	assert.Equal(t, 0, q.Len())
	assert.Nil(t, q.GetItem("req-1"))

	// Remove non-existent item should not panic
	assert.NotPanics(t, func() {
		q.RemoveItem("nonexistent")
	})
}

func TestRateLimitQueue_FIFO_Order(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	for i := 0; i < 5; i++ {
		q.Push(fmt.Sprintf("req-%d", i), func(*Item) error { return nil })
	}

	for i := 0; i < 5; i++ {
		item := q.WaitPeek()
		require.NotNil(t, item)
		assert.Equal(t, fmt.Sprintf("req-%d", i), item.Key())
		q.RemoveItem(item.Key())
	}
}

// ============================================================
//  RateLimitQueue — WaitPeek
// ============================================================

func TestRateLimitQueue_WaitPeek_Normal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	q.Push("req-1", func(*Item) error { return nil })

	result := make(chan *Item, 1)
	go func() { result <- q.WaitPeek() }()

	select {
	case item := <-result:
		require.NotNil(t, item)
		assert.Equal(t, "req-1", item.Key())
	case <-time.After(time.Second):
		t.Fatal("WaitPeek did not return in time")
	}
}

func TestRateLimitQueue_WaitPeek_BlocksUntilPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)

	result := make(chan *Item, 1)
	go func() { result <- q.WaitPeek() }()

	// Should be blocking on empty queue
	select {
	case <-result:
		t.Fatal("WaitPeek should block on empty queue")
	case <-time.After(50 * time.Millisecond):
		// Expected: still blocking
	}

	// Push an item to unblock
	q.Push("req-1", func(*Item) error { return nil })

	select {
	case item := <-result:
		require.NotNil(t, item)
		assert.Equal(t, "req-1", item.Key())
	case <-time.After(time.Second):
		t.Fatal("WaitPeek did not return after Push")
	}
}

func TestRateLimitQueue_WaitPeek_SkipsExpired(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueueWithTimeout(ctx, 10*time.Millisecond)

	// Push item that will expire quickly
	expiredItem := q.Push("req-expired", func(*Item) error { return nil })

	// Wait for expiration
	time.Sleep(20 * time.Millisecond)

	// Push a fresh item (still uses 10ms timeout, but we'll peek immediately)
	q.Push("req-fresh", func(*Item) error { return nil })

	result := make(chan *Item, 1)
	go func() { result <- q.WaitPeek() }()

	select {
	case item := <-result:
		require.NotNil(t, item)
		assert.Equal(t, "req-fresh", item.Key(), "should skip expired and return fresh item")
	case <-time.After(time.Second):
		t.Fatal("WaitPeek did not return in time")
	}

	// Expired item should have timeout error and be notified
	assert.ErrorIs(t, expiredItem.Error(), consts.ErrorRateLimitQueueTimeOut)
	select {
	case <-expiredItem.Done():
	default:
		t.Fatal("expired item should be notified done")
	}
}

func TestRateLimitQueue_WaitPeek_ContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	q := newTestQueue(ctx)

	result := make(chan *Item, 1)
	go func() { result <- q.WaitPeek() }()

	// Give WaitPeek time to block
	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case item := <-result:
		assert.Nil(t, item, "WaitPeek should return nil on context cancel")
	case <-time.After(time.Second):
		t.Fatal("WaitPeek did not return after cancel")
	}
}

// ============================================================
//  RateLimitQueue — Cleaner
// ============================================================

func TestRateLimitQueue_Cleaner_RemovesDoneItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueueFull(ctx, time.Hour, 10*time.Millisecond)

	item1 := q.Push("req-1", func(*Item) error { return nil })
	q.Push("req-2", func(*Item) error { return nil })

	item1.NotifyDone()

	// Both items should still be present initially
	assert.Equal(t, 2, q.Len())
	assert.Equal(t, 2, q.TrackedCount())

	// Wait for cleaner to remove the done item
	time.Sleep(60 * time.Millisecond)

	assert.Equal(t, 1, q.Len(), "cleaner should remove done item from list")
	assert.Equal(t, 1, q.TrackedCount(), "cleaner should remove done item from dict")
	assert.Nil(t, q.GetItem("req-1"), "done item should be cleaned")
	assert.NotNil(t, q.GetItem("req-2"), "active item should remain")
}

func TestRateLimitQueue_TwoTierCleanup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueueFull(ctx, time.Hour, 10*time.Millisecond)

	// Push an item
	q.Push("req-1", func(*Item) error { return nil })
	assert.Equal(t, 1, q.Len(), "item should be in list")
	assert.Equal(t, 1, q.TrackedCount(), "item should be in dict")

	// WaitPeek removes from list, keeps in dict
	result := make(chan *Item, 1)
	go func() { result <- q.WaitPeek() }()
	item := <-result
	require.NotNil(t, item)
	assert.Equal(t, 0, q.Len(), "WaitPeek should remove from list")
	assert.Equal(t, 1, q.TrackedCount(), "WaitPeek should keep in dict")
	assert.NotNil(t, q.GetItem("req-1"), "should still be findable by key")

	// Simulate worker done
	item.NotifyDone()
	assert.Equal(t, 0, q.Len(), "list still empty")
	assert.Equal(t, 1, q.TrackedCount(), "dict still has item until cleaner runs")

	// Wait for cleaner to remove from dict
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, 0, q.TrackedCount(), "cleaner should remove from dict")
	assert.True(t, q.Empty(), "queue should be fully empty")
	assert.Nil(t, q.GetItem("req-1"), "item should be gone from dict")
}

func TestRateLimitQueue_Cleaner_StopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	q := newTestQueueFull(ctx, time.Hour, 10*time.Millisecond)
	_ = q

	// Cancel should not panic and cleaner goroutine should exit
	assert.NotPanics(t, func() {
		cancel()
		time.Sleep(30 * time.Millisecond)
	})
}

// ============================================================
//  RateLimitWorker
// ============================================================

func TestRateLimitWorker_CallbackSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	w := NewRateLimitWorker(q, func() time.Duration { return time.Millisecond })
	go w.Run(ctx)

	called := make(chan struct{})
	item := q.Push("req-1", func(_ *Item) error {
		close(called)
		return nil
	})

	select {
	case <-called:
		// Callback invoked
	case <-time.After(time.Second):
		t.Fatal("callback not invoked")
	}

	item.WaitDone()
	assert.Nil(t, item.Error())
}

func TestRateLimitWorker_CallbackRetryUntilSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueueWithTimeout(ctx, 5*time.Second)
	w := NewRateLimitWorker(q, func() time.Duration { return time.Millisecond })
	go w.Run(ctx)

	var callCount int32
	item := q.Push("req-1", func(_ *Item) error {
		n := atomic.AddInt32(&callCount, 1)
		if n < 3 {
			return errors.New("not ready yet")
		}
		return nil
	})

	item.WaitDone()
	assert.Nil(t, item.Error())
	assert.GreaterOrEqual(t, atomic.LoadInt32(&callCount), int32(3))
}

func TestRateLimitWorker_DeadlineExpiration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueueWithTimeout(ctx, 50*time.Millisecond)
	w := NewRateLimitWorker(q, func() time.Duration { return 5 * time.Millisecond })
	go w.Run(ctx)

	item := q.Push("req-1", func(_ *Item) error {
		return errors.New("always fail")
	})

	item.WaitDone()
	assert.ErrorIs(t, item.Error(), consts.ErrorRateLimitQueueTimeOut)
}

func TestRateLimitWorker_ContextCancelStopsWorker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	q := newTestQueue(ctx)
	w := NewRateLimitWorker(q, func() time.Duration { return time.Millisecond })

	workerDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(workerDone)
	}()

	cancel()

	select {
	case <-workerDone:
		// Worker stopped
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after context cancel")
	}
}

func TestRateLimitWorker_ProcessesMultipleItems(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	w := NewRateLimitWorker(q, func() time.Duration { return time.Millisecond })
	go w.Run(ctx)

	const n = 5
	items := make([]*Item, n)
	for i := 0; i < n; i++ {
		items[i] = q.Push(fmt.Sprintf("req-%d", i), func(*Item) error { return nil })
	}

	for i, item := range items {
		item.WaitDone()
		assert.Nil(t, item.Error(), "item %d should succeed", i)
		q.RemoveItem(item.Key())
	}
}

func TestRateLimitWorker_ContextCancelDuringRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	q := newTestQueueWithTimeout(ctx, 5*time.Second)
	w := NewRateLimitWorker(q, func() time.Duration { return time.Millisecond })

	workerDone := make(chan struct{})
	go func() {
		w.Run(ctx)
		close(workerDone)
	}()

	// Push item that always fails; worker will be retrying
	q.Push("req-1", func(_ *Item) error {
		return errors.New("always fail")
	})

	// Let worker enter retry loop
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-workerDone:
		// Worker stopped during retry
	case <-time.After(time.Second):
		t.Fatal("worker did not stop during retry after context cancel")
	}
}

// ============================================================
//  Concurrency
// ============================================================

func TestRateLimitQueue_ConcurrentPushGetRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	const n = 50
	var wg sync.WaitGroup

	// Concurrent Push
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			q.Push(fmt.Sprintf("req-%d", i), func(*Item) error { return nil })
		}(i)
	}
	wg.Wait()

	assert.Equal(t, n, q.Len())

	// Concurrent Get + Remove
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("req-%d", i)
			_ = q.GetItem(key)
			q.RemoveItem(key)
		}(i)
	}
	wg.Wait()

	assert.Equal(t, 0, q.Len())
	assert.True(t, q.Empty())
}

func TestRateLimitQueue_ConcurrentWorkerAndPush(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	q := newTestQueue(ctx)
	w := NewRateLimitWorker(q, func() time.Duration { return time.Millisecond })
	go w.Run(ctx)

	const n = 20
	items := make([]*Item, n)
	for i := 0; i < n; i++ {
		items[i] = q.Push(fmt.Sprintf("req-%d", i), func(*Item) error { return nil })
	}

	// Wait for all items to complete
	for _, item := range items {
		select {
		case <-item.Done():
			assert.Nil(t, item.Error())
		case <-time.After(5 * time.Second):
			t.Fatalf("item %s not processed in time", item.Key())
		}
		q.RemoveItem(item.Key())
	}
}
