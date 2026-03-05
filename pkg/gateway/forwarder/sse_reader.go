package forwarder

import (
	"context"
	"io"
	"llumnix/pkg/consts"
	"time"

	"github.com/tmaxmax/go-sse"
	"k8s.io/klog/v2"
)

const (
	// ReadTimeout sets the maximum duration to wait for reading from backend stream.
	ReadTimeout = 5 * time.Minute
)

// TimeoutReader wraps an io.ReadCloser with a timeout mechanism
// to prevent blocking indefinitely on read operations.
type TimeoutReader struct {
	r       io.ReadCloser
	timeout time.Duration
	ctx     context.Context
}

// Close closes the underlying reader.
func (t *TimeoutReader) Close() error {
	return t.r.Close()
}

// Read reads data from the underlying reader with timeout protection.
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

// ReadEvent represents the outcome of an SSE event processing operation.
type ReadEvent struct {
	ev  *sse.Event
	err error
}

// SSEReader implements an io.ReadCloser for Server-Sent Events (SSE) streams.
type SSEReader struct {
	r        io.ReadCloser
	resultCh chan *ReadEvent
	done     chan struct{}
	pending  *ReadEvent
}

func newSSEReader(r io.ReadCloser) *SSEReader {
	sr := &SSEReader{
		r:        r,
		resultCh: make(chan *ReadEvent, 100),
		done:     make(chan struct{}),
	}

	go sr.processEvents()
	return sr
}

func (s *SSEReader) processEvents() {
	defer close(s.resultCh)

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

func (s *SSEReader) Close() error {
	err := s.r.Close()
	close(s.done)
	return err
}

func newSSEReaderWithTimeout(ctx context.Context, r io.ReadCloser, timeout time.Duration) io.ReadCloser {
	return &TimeoutReader{
		r:       newSSEReader(r),
		timeout: timeout,
		ctx:     ctx,
	}
}
