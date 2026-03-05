package forwarder

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/gateway/protocol"
	"llumnix/pkg/types"
)

const (
	maxIdleConns          = 1000
	idleConnTimeout       = 5 * time.Minute
	maxConnsPerHost       = 0 // no limit
	responseHeaderTimeout = 15 * time.Minute
	dialTimeout           = 3 * time.Second
)

// newLlmForwardClient creates a shared HTTP client optimized for forwarding LLM requests.
func newLlmForwardClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 0
	transport.MaxIdleConnsPerHost = maxIdleConns
	transport.MaxConnsPerHost = maxConnsPerHost
	transport.IdleConnTimeout = idleConnTimeout
	transport.DialContext = (&net.Dialer{
		Timeout:   dialTimeout,
		KeepAlive: 30 * time.Second,
	}).DialContext
	transport.ResponseHeaderTimeout = responseHeaderTimeout

	return &http.Client{Transport: transport}
}

// makeBackendRequest creates HTTP request for forwarding to the inference engine.
func makeBackendRequest(req *types.RequestContext, body []byte, instance *types.LLMInstance) (*http.Request, error) {
	httpReq := req.HttpRequest.Request
	if instance == nil {
		klog.Errorf("failed to get instance for request: %s", req.Id)
		return nil, errors.New("failed to get instance")
	}
	url := fmt.Sprintf("http://%s%s", instance.Endpoint.String(), protocol.CompletionsPath)
	klog.V(3).Infof("Forwarding request to %s body: %s", url, string(body))

	proxyReq, err := http.NewRequestWithContext(req.Context, "POST", url, bytes.NewBuffer(body))
	if err != nil {
		klog.Errorf("failed to create proxy request: %v", err)
		return nil, err
	}

	for key, values := range httpReq.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	return proxyReq, nil
}

// doRequest executes HTTP request with retry mechanism.
func doRequest(req *http.Request, client *http.Client, body []byte) (io.ReadCloser, error) {
	var resp *http.Response
	var err error

	for retry := 0; retry <= connectRetry; retry++ {
		resp, err = client.Do(req)
		if err == nil {
			break
		}

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
			time.Sleep(50 * time.Millisecond)
			req.Body = io.NopCloser(bytes.NewBuffer(body))
		}
	}

	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		klog.Errorf("failed to forward request to %s, status code: %d", req.URL, resp.StatusCode)
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to forward request to %s, status code: %d, body: %s", req.URL, resp.StatusCode, string(respBody))
	}
	return resp.Body, nil
}

// streamRead reads SSE stream and sends chunks to channel.
func streamRead(req *types.RequestContext, chunkChan chan StreamChunk, r io.ReadCloser) error {
	ctx := req.Context
	reader := newSSEReaderWithTimeout(ctx, r, ReadTimeout)
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
			}
			klog.Errorf("error while reading response SSE: %v\n", err)
			return err
		}
		data := make([]byte, n)
		copy(data, buf[0:n])
		chunkChan <- StreamChunk{Data: data}
	}
}

// streamBackendResponse starts streaming read from the inference engine and sends chunks to channel.
func streamBackendResponse(req *types.RequestContext, client *http.Client, body []byte, instance *types.LLMInstance, chunkChan chan StreamChunk) {
	newReq, err := makeBackendRequest(req, body, instance)
	if err != nil {
		chunkChan <- StreamChunk{Err: err}
		return
	}

	respBody, err := doRequest(newReq, client, body)
	if err != nil {
		chunkChan <- StreamChunk{Err: err}
		return
	}

	if err := streamRead(req, chunkChan, respBody); err != nil {
		chunkChan <- StreamChunk{Err: err}
	}
}
