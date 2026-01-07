package service

import (
	"easgo/pkg/llm-gateway/consts"
	"errors"
	"io"
	"time"

	"github.com/tmaxmax/go-sse"
	"k8s.io/klog/v2"
)

var (
	ErrBufferTooSmall = errors.New("buffer too small for SSE event data")
)

// timeoutReader wraps an io.ReadCloser with a timeout mechanism
// to prevent blocking indefinitely on read operations
type timeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
}

// Close closes the underlying reader
// Returns any error encountered during closing
func (t *timeoutReader) Close() error {
	t.r.Close()
	return nil
}

// Read reads data from the underlying reader with timeout protection
// p: byte slice to read data into
// Returns number of bytes read and any error encountered
// If read operation exceeds timeout, returns 0 and consts.ErrorReadTimeout
func (t *timeoutReader) Read(p []byte) (int, error) {

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
	case <-time.After(t.timeout):
		return 0, consts.ErrorReadTimeout
	}
}

// result represents the outcome of an SSE event processing operation
type result struct {
	ev  *sse.Event
	err error
}

// sseReader implements an io.ReadCloser for Server-Sent Events (SSE) streams
// It processes SSE events asynchronously and provides a standard Read interface
type sseReader struct {
	r        io.ReadCloser
	resultCh chan *result
	done     chan struct{}
	pending  *result
}

// NewSSEReader creates a new SSE reader instance
// r: io.ReadCloser representing the SSE stream
// Returns a pointer to sseReader that processes events asynchronously
func NewSSEReader(r io.ReadCloser) *sseReader {
	sr := &sseReader{
		r:        r,
		resultCh: make(chan *result, 100),
		done:     make(chan struct{}),
		pending:  nil,
	}

	go sr.processEvents()
	return sr
}

// processEvents runs in a goroutine to continuously read and process SSE events
// from the underlying stream and send them to the result channel
func (s *sseReader) processEvents() {
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
			s.resultCh <- &result{&ev, err}
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
func (s *sseReader) Read(p []byte) (n int, err error) {
	var res *result
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

	dataLen := len(res.ev.Data)
	if len(p) < dataLen {
		s.pending = res
		klog.V(3).Infof("buffer size (%d) is smaller than event data (%d), need larger buffer", len(p), dataLen)
		return dataLen, ErrBufferTooSmall
	}
	klog.V(5).Infof("read sse event %d/%d", dataLen, len(p))
	copy(p, res.ev.Data)
	return dataLen, nil

}

// Close closes the SSE reader and stops background processing
// Returns any error encountered during closing
func (s *sseReader) Close() error {
	s.r.Close()
	close(s.done)
	return nil
}

// NewSSEReaderWithTimeout creates a new SSE reader with timeout protection
// r: io.ReadCloser representing the SSE stream
// timeout: duration for read operation timeout
// Returns an io.ReadCloser that wraps SSE processing with timeout capabilities
func NewSSEReaderWithTimeout(r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	return &timeoutReader{
		r:       NewSSEReader(r),
		timeout: timeout,
	}
}
