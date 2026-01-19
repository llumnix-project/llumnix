package handler

import (
	"bytes"
	"easgo/pkg/llm-gateway/protocol"
	"easgo/pkg/llm-gateway/types"
	"errors"
	"fmt"
	"io"
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

func WriteErrorResponse(req *types.RequestContext, err error) {
	req.ResponseChan <- &types.ResponseMsg{Err: err}
}

func WriteResponse(req *types.RequestContext, chunk []byte) {
	req.ResponseChan <- &types.ResponseMsg{Err: nil, Message: chunk}
}

// MakeNewBackendRequest creates HTTP request for backend inference
func MakeNewBackendRequest(req *types.RequestContext, body []byte, worker *types.LLMWorker) (*http.Request, error) {
	httpReq := req.HttpRequest.Request
	if worker == nil {
		klog.Errorf("failed to get worker for request: %s", req.Id)
		return nil, errors.New("failed to get worker")
	}
	url := fmt.Sprintf("http://%s%s", worker.Endpoint.String(), protocol.CompletionsPath)
	klog.V(3).Infof("Forwarding request to %s body: %s", url, string(body))

	// Create a new request to forward to the backend
	proxyReq, err := http.NewRequestWithContext(req.Context, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		klog.Errorf("failed to create proxy request: %v", err)
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

// DoRequest executes HTTP request with retry mechanism
func DoRequest(req *http.Request, client *http.Client, body []byte) (io.ReadCloser, error) {
	var resp *http.Response
	var err error

	for retry := 0; retry <= connectRetry; retry++ {
		// Send the request to the backend using shared HTTP client
		resp, err = client.Do(req)
		if err == nil {
			break
		}

		// Log detailed error information for debugging
		if netErr, ok := err.(net.Error); ok {
			klog.Errorf("failed to forward request to %s: %v (is_timeout=%v), retry: %d",
				req.URL, err, netErr.Timeout(), retry)
			if netErr.Timeout() {
				return nil, fmt.Errorf("request to %s timed out: %v", req.URL, err)
			}
		} else {
			klog.Errorf("failed to forward request to %s: %v, retry: %d", req.URL, err, retry)
		}

		if retry < connectRetry {
			// Add a small delay before retry to allow transient issues to resolve
			time.Sleep(50 * time.Millisecond)
			// Recreate request body for retry
			req.Body = io.NopCloser(bytes.NewBuffer(body))
		}
	}

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("failed to forward request to %s, status code: %d", req.URL, resp.StatusCode)
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to forward request to %s, status code: %d, body: %s", req.URL, resp.StatusCode, string(body))
	}
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

// StreamResponseFromBackend starts streaming read from backend and sends chunks to channel
func StreamResponseFromBackend(req *types.RequestContext, client *http.Client, body []byte, worker *types.LLMWorker, chunkChan chan StreamChunk) {
	newReq, err := MakeNewBackendRequest(req, body, worker)
	if err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}
	// Execute request with retry
	respBody, err := DoRequest(newReq, client, body)
	if err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}
	// Stream read response
	if err := StreamRead(req, chunkChan, respBody); err != nil {
		chunkChan <- StreamChunk{err: err}
	}
}
