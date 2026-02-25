package service

import (
	"bytes"
	"fmt"
	"io"
	"llm-gateway/pkg/types"
	"net/http"

	"k8s.io/klog/v2"
)

// TrySimpleHTTPProxy attempts to proxy a request to the given URL
// Returns true if successful, false if failed
func TrySimpleHTTPProxy(client *http.Client, url string, reqCtx *types.RequestContext) error {
	r := reqCtx.HttpRequest.Request
	w := reqCtx.HttpRequest.Writer

	// Check if headers have already been sent
	if reqCtx.HttpRequest.HeaderResponded {
		klog.Errorf("request [%s] headers already sent, cannot proxy to %s", reqCtx.Id, url)
		return fmt.Errorf("headers already sent")
	}

	// Create a new request to forward to the backend
	body := []byte(reqCtx.GetRequestRawData())
	proxyReq, err := http.NewRequestWithContext(reqCtx.Context, r.Method, url, bytes.NewBuffer(body))
	if err != nil {
		klog.Errorf("request [%s] failed to create proxy request to %s: %v", reqCtx.Id, url, err)
		return fmt.Errorf("failed to create proxy request")
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Send the request to the backend using shared HTTP client
	resp, err := client.Do(proxyReq)
	if err != nil {
		klog.Errorf("request [%s] failed to forward request to %s: %v", reqCtx.Id, url, err)
		return fmt.Errorf("failed to forward request: %v", err)
	}
	defer resp.Body.Close()

	// Copy response headers to client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write response status code
	w.WriteHeader(resp.StatusCode)
	reqCtx.HttpRequest.HeaderResponded = true

	// Copy response body to client
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		klog.Errorf("request [%s] failed to copy response body from %s: %v", reqCtx.Id, url, err)
		// Headers already sent, can't recover
		return fmt.Errorf("failed to copy response body: %v", err)
	}

	return nil
}

// SimpleHTTPProxy forwards the request to a backend endpoint and returns the response to the client
// Parameters:
//   - url: endpoint URL
//   - w: HTTP response writer
//   - r: HTTP request
func SimpleHTTPProxy(client *http.Client, url string, w http.ResponseWriter, r *http.Request) {
	// Create a new request to forward to the backend
	proxyReq, err := http.NewRequestWithContext(r.Context(), r.Method, url, r.Body)
	if err != nil {
		klog.Errorf("Failed to create proxy request: %v", err)
		http.Error(w, "failed to create proxy request", http.StatusInternalServerError)
		return
	}

	// Copy headers from original request
	for key, values := range r.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Send the request to the backend using shared HTTP client
	resp, err := client.Do(proxyReq)
	if err != nil {
		klog.Errorf("Failed to forward request to %s: %v", url, err)
		http.Error(w, "failed to forward request", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Copy response headers to client
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write response status code
	w.WriteHeader(resp.StatusCode)

	// Copy response body to client
	io.Copy(w, resp.Body)
}
