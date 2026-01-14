package service

import (
	"io"
	"net/http"

	"k8s.io/klog/v2"
)

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
