package handler

import (
	"context"
	"easgo/pkg/llm-gateway/consts"
	"io"
	"time"

	"github.com/tmaxmax/go-sse"
	"k8s.io/klog/v2"
)

// TimeoutReader wraps an io.ReadCloser with a timeout mechanism
// to prevent blocking indefinitely on read operations
type TimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
	ctx     context.Context
}

// Close closes the underlying reader
// Returns any error encountered during closing
func (t *TimeoutReader) Close() error {
	return t.r.Close()
}

// Read reads data from the underlying reader with timeout protection
// p: byte slice to read data into
// Returns number of bytes read and any error encountered
// If read operation exceeds timeout, returns 0 and consts.ErrorReadTimeout
// If context is cancelled, returns 0 and context error
func (t *TimeoutReader) Read(p []byte) (int, error) {
	type result struct {
		n   int
		err error
	}
	done := make(chan result, 1)

	go func() {
		defer close(done)
		n, err := t.r.Read(p)
		done <- result{n, err}
	}()

	select {
	case res := <-done:
		return res.n, res.err
	case <-t.ctx.Done():
		return 0, t.ctx.Err()
	case <-time.After(t.timeout):
		return 0, consts.ErrorReadTimeout
	}
}

// ReadEvent represents the outcome of an SSE event processing operation
type ReadEvent struct {
	ev  *sse.Event
	err error
}

// SSEReader implements an io.ReadCloser for Server-Sent Events (SSE) streams
// It processes SSE events asynchronously and provides a standard Read interface
type SSEReader struct {
	r        io.ReadCloser
	resultCh chan *ReadEvent
	done     chan struct{}
	pending  *ReadEvent
}

// NewSSEReader creates a new SSE reader instance
// r: io.ReadCloser representing the SSE stream
// Returns a pointer to SSEReader that processes events asynchronously
func NewSSEReader(r io.ReadCloser) *SSEReader {
	sr := &SSEReader{
		r:        r,
		resultCh: make(chan *ReadEvent, 100),
		done:     make(chan struct{}),
		pending:  nil,
	}

	go sr.processEvents()
	return sr
}

// processEvents runs in a goroutine to continuously read and process SSE events
// from the underlying stream and send them to the result channel
func (s *SSEReader) processEvents() {
	defer close(s.resultCh)

	// Create a custom config with larger max event size to avoid "token too long" error
	// Default bufio.Scanner max token size is 64KB, we increase it to 1MB
	config := &sse.ReadConfig{
		MaxEventSize: 1 * 1024 * 1024, // 1MB
	}
	reader := sse.Read(s.r, config)

	for ev, err := range reader {
		select {
		case <-s.done:
			return
		default:
			s.resultCh <- &ReadEvent{&ev, err}
			if err != nil {
				return
			}
		}
	}
}

// Read reads data from the processed SSE events into the provided byte slice
// p: byte slice to read data into
// Returns number of bytes read and any error encountered
// Returns io.EOF when no more events are available
func (s *SSEReader) Read(p []byte) (n int, err error) {
	var res *ReadEvent
	if s.pending != nil {
		res = s.pending
		s.pending = nil
	} else {
		var ok bool
		res, ok = <-s.resultCh
		if !ok {
			return 0, io.EOF
		}
		if res.err != nil {
			return 0, res.err
		}
	}

	data := res.ev.Data
	dataLen := len(data)

	// Check if buffer is large enough
	if len(p) < dataLen {
		s.pending = res
		klog.V(3).Infof("buffer size (%d) is smaller than event data (%d), need larger buffer", len(p), dataLen)
		return dataLen, io.ErrShortBuffer
	}
	klog.V(5).Infof("read sse event %d/%d", dataLen, len(p))

	if dataLen == 0 {
		return 0, nil
	}
	n = copy(p[:dataLen], data)
	return n, nil
}

// Close closes the SSE reader and stops background processing
// Returns any error encountered during closing
func (s *SSEReader) Close() error {
	err := s.r.Close()
	close(s.done)
	return err
}

// NewSSEReaderWithTimeout creates a new SSE reader with timeout and context awareness
// ctx: context for cancellation and timeout control
// r: io.ReadCloser representing the SSE stream
// timeout: duration for read operation timeout
// Returns an io.ReadCloser that wraps SSE processing with timeout and context cancellation capabilities
func NewSSEReaderWithTimeout(ctx context.Context, r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	return &TimeoutReader{
		r:       NewSSEReader(r),
		timeout: timeout,
		ctx:     ctx,
	}
}
