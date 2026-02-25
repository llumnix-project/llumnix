package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
	"net"
	"net/http"
	"time"

	"k8s.io/klog/v2"
)

const (
	maxIdleConns          = 1000
	idleConnTimeout       = 5 * time.Minute
	maxConnsPerHost       = 0 // no limit
	responseHeaderTimeout = 15 * time.Minute
	dialTimeout           = 3 * time.Second
)

// NewLlmForwardClient creates a shared HTTP client optimized for forwarding LLM requests
func NewLlmForwardClient() *http.Client {
	// Clone default transport to customize connection pooling and timeout settings
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Connection pool settings
	transport.MaxIdleConns = 0                   // unlimited idle connections globally
	transport.MaxIdleConnsPerHost = maxIdleConns // max idle connections per host
	transport.MaxConnsPerHost = maxConnsPerHost  // no limit on total connections per host
	transport.IdleConnTimeout = idleConnTimeout  // 5 minutes idle timeout

	// KeepAlive ensures idle connections remain valid and detects broken connections
	transport.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,      // 3 seconds connection timeout
		KeepAlive: 30 * time.Second, // send TCP keepalive probes every 30 seconds
	}).DialContext

	// Additional timeout settings
	transport.ResponseHeaderTimeout = responseHeaderTimeout // 15 minutes header timeout

	return &http.Client{Transport: transport}
}

// MakeNewBackendRequest creates HTTP request for backend inference
func MakeNewBackendRequest(req *types.RequestContext, body []byte, worker *types.LLMWorker) (*http.Request, error) {
	httpReq := req.HttpRequest.Request
	if worker == nil {
		return nil, errors.New("failed to get worker")
	}
	url := fmt.Sprintf("http://%s%s", worker.Endpoint.String(), req.GetBackendURLPath())
	klog.V(3).Infof("Forwarding request to %s body: %s", url, string(body))

	// Create a new request to forward to the backend
	proxyReq, err := http.NewRequestWithContext(req.Context, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		return nil, err
	}

	// Copy headers from original request
	for key, values := range httpReq.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	return proxyReq, nil
}

// DoRequest executes HTTP request with retry mechanism.
// The worker is used to identify which instance failed for exclusion during retry.
func DoRequest(req *http.Request, client *http.Client, body []byte, worker *types.LLMWorker) (io.ReadCloser, error) {
	var resp *http.Response
	var err error

	for retry := 0; retry <= connectRetry; retry++ {
		// Send the request to the backend using shared HTTP client
		resp, err = client.Do(req)
		if err == nil {
			break
		}

		klog.V(3).Infof("failed to do request: %v", err)

		// Log detailed error information for debugging
		if netErr, ok := err.(net.Error); ok {
			// Timeout connections do not need to be retried.
			// If retried, client timeouts may become more severe,
			// so generally retries are not performed.
			if netErr.Timeout() {
				break
			}
		}

		if retry < connectRetry {
			// Add a small delay before retry to allow transient issues to resolve
			time.Sleep(50 * time.Millisecond)
			// Recreate request body for retry
			req.Body = io.NopCloser(bytes.NewBuffer(body))
		}
	}

	if err != nil {
		return nil, consts.NewNetworkError(req.URL.String(), err, worker.Id())
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, consts.NewUpstreamError(req.URL.String(), resp.StatusCode, body, worker.Id())
	}

	klog.V(3).Infof("req %s content-type header: %s", req.URL, resp.Header.Get("Content-Type"))

	return resp.Body, nil
}

// StreamRead reads SSE stream and sends chunks to channel
func StreamRead(req *types.RequestContext, chunkChan chan StreamChunk, r io.ReadCloser) error {
	ctx := req.Context
	reader := NewSSEReaderWithTimeout(ctx, r, ReadTimeout)
	defer reader.Close()
	buf := make([]byte, ReadBufferSize)
	klog.V(3).Infof("start reading response Http SSE stream")
	for {
		n, err := reader.Read(buf)
		if err != nil {
			if err == io.EOF {
				return nil
			} else if errors.Is(err, context.Canceled) {
				// Not print error for context canceled as the request may be closed normally.
				return err
			} else if errors.Is(err, io.ErrShortBuffer) {
				klog.V(3).Infof("expanding buffer from %d to %d bytes", len(buf), n)
				buf = make([]byte, n)
				continue
			} else {
				klog.Errorf("error while reading response SSE: %v\n", err)
				return err
			}
		}
		// Allocate new slice to avoid data race
		data := make([]byte, n)
		copy(data, buf[0:n])
		chunkChan <- StreamChunk{Data: data}
	}
}

// StreamReadFromBackend starts streaming read from backend and sends chunks to channel
func StreamReadFromBackend(req *types.RequestContext, client *http.Client, body []byte, worker *types.LLMWorker, chunkChan chan StreamChunk) {
	// Build backend request
	newReq, err := MakeNewBackendRequest(req, body, worker)
	if err != nil {
		klog.Errorf("failed to create new backend request: %v", err)
		chunkChan <- StreamChunk{err: err}
		return
	}

	// Execute request with retry
	respBody, err := DoRequest(newReq, client, body, worker)
	if err != nil {
		klog.Errorf("failed to do backend request: %v", err)
		chunkChan <- StreamChunk{err: err}
		return
	}
	defer respBody.Close()

	// Stream read response
	if err := StreamRead(req, chunkChan, respBody); err != nil {
		chunkChan <- StreamChunk{err: err}
	}
}

// ReadFromBackend reads the response from the backend and returns the body as a byte slice
func ReadFromBackend(req *types.RequestContext, client *http.Client, body []byte, worker *types.LLMWorker) ([]byte, error) {
	newReq, err := MakeNewBackendRequest(req, body, worker)
	if err != nil {
		return nil, err
	}

	respBody, err := DoRequest(newReq, client, body, worker)
	if err != nil {
		return nil, err
	}
	defer respBody.Close()

	return io.ReadAll(respBody)
}

// GetPDWorkers retrieves prefill and decode workers from schedule results.
// Returns an error if either worker is not found.
func GetPDWorkers(req *types.RequestContext) (prefill, decode *types.LLMWorker, err error) {
	prefill = req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if prefill == nil {
		return nil, nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}
	decode = req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleDecode)
	if decode == nil {
		return nil, nil, fmt.Errorf("[%s] no scheduled decode worker", req.Id)
	}
	return prefill, decode, nil
}

// StartStreamRead creates a channel and starts streaming response in a goroutine.
// The respBody will be closed automatically when streaming completes or encounters an error.
func StartStreamRead(req *types.RequestContext, respBody io.ReadCloser) <-chan StreamChunk {
	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)
		defer respBody.Close()

		if err := StreamRead(req, chunkChan, respBody); err != nil {
			chunkChan <- StreamChunk{err: err}
		}
	}()
	return chunkChan
}

// ScheduleAndGetDecodeWorker performs decode scheduling and retrieves the decode worker.
// Returns an error if scheduling fails or decode worker is not found.
func ScheduleAndGetDecodeWorker(req *types.RequestContext) (*types.LLMWorker, error) {
	results, err := req.ScheduleDecode()
	if err != nil {
		klog.Errorf("[%s] decode scheduling failed: %v", req.Id, err)
		return nil, err
	}

	dWorker := results.GetWorkerByRole(types.InferRoleDecode)
	if dWorker == nil {
		klog.Errorf("[%s] decode worker not found", req.Id)
		return nil, fmt.Errorf("decode worker not found")
	}

	return dWorker, nil
}
