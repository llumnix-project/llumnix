package queue

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/consts"
)

func new429Error() *consts.UpstreamError {
	return consts.NewUpstreamError("http://test", http.StatusTooManyRequests, []byte("rate limited"), "")
}

func new500Error() *consts.UpstreamError {
	return consts.NewUpstreamError("http://test", http.StatusInternalServerError, []byte("server error"), "")
}

func TestRateLimitRetryQueueBasic(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	q.Start()
	defer q.Stop()

	var callCount atomic.Int32
	resultCh, ok := q.Enqueue(context.Background(), func() error {
		callCount.Add(1)
		return nil
	})

	assert.True(t, ok)
	select {
	case result := <-resultCh:
		assert.NoError(t, result.Err)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for retry completion")
	}
	assert.Equal(t, int32(1), callCount.Load())
}

func TestRateLimitRetryQueueSuccessAfterRetries(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 5, 5*time.Millisecond, 50*time.Millisecond)
	q.Start()
	defer q.Stop()

	var callCount atomic.Int32
	resultCh, ok := q.Enqueue(context.Background(), func() error {
		count := callCount.Add(1)
		if count <= 2 {
			return new429Error()
		}
		return nil
	})

	assert.True(t, ok)
	select {
	case result := <-resultCh:
		assert.NoError(t, result.Err)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout")
	}
	assert.Equal(t, int32(3), callCount.Load())
}

func TestRateLimitRetryQueueMaxRetriesExceeded(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 2, 5*time.Millisecond, 50*time.Millisecond)
	q.Start()
	defer q.Stop()

	var callCount atomic.Int32
	resultCh, ok := q.Enqueue(context.Background(), func() error {
		callCount.Add(1)
		return new429Error()
	})

	assert.True(t, ok)
	select {
	case result := <-resultCh:
		assert.ErrorIs(t, result.Err, ErrRateLimitRetryExhausted)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout")
	}
	assert.Equal(t, int32(2), callCount.Load())
}

func TestRateLimitRetryQueueNonRetryableError(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	q.Start()
	defer q.Stop()

	err500 := new500Error()
	var callCount atomic.Int32
	resultCh, ok := q.Enqueue(context.Background(), func() error {
		callCount.Add(1)
		return err500
	})

	assert.True(t, ok)
	select {
	case result := <-resultCh:
		var upstreamErr *consts.UpstreamError
		assert.True(t, errors.As(result.Err, &upstreamErr))
		assert.Equal(t, http.StatusInternalServerError, upstreamErr.StatusCode)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout")
	}
	assert.Equal(t, int32(1), callCount.Load())
}

func TestRateLimitRetryQueueContextCancelled(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, -1, 500*time.Millisecond, 2*time.Second)
	q.Start()
	defer q.Stop()

	ctx, cancel := context.WithCancel(context.Background())
	resultCh, ok := q.Enqueue(ctx, func() error {
		return new429Error()
	})
	assert.True(t, ok)

	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	select {
	case result := <-resultCh:
		assert.Error(t, result.Err)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout")
	}
}

func TestRateLimitRetryQueueNotStarted(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	_, ok := q.Enqueue(context.Background(), func() error { return nil })
	assert.False(t, ok)
}

func TestRateLimitRetryQueueBackoffDelay(t *testing.T) {
	q := NewRateLimitRetryQueue(10, 2, 5, 10*time.Millisecond, 100*time.Millisecond)

	tests := []struct {
		retryCount int
		expected   time.Duration
	}{
		{0, 10 * time.Millisecond},
		{1, 20 * time.Millisecond},
		{2, 40 * time.Millisecond},
		{3, 80 * time.Millisecond},
		{4, 100 * time.Millisecond},
		{5, 100 * time.Millisecond},
	}

	for _, tc := range tests {
		delay := q.backoffDelay(tc.retryCount)
		assert.Equal(t, tc.expected, delay, "retryCount=%d", tc.retryCount)
	}
}
