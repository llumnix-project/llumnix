package service

import (
	"io"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/service/router"
	"llm-gateway/pkg/types"
	"net/http"

	"github.com/valyala/fastjson"
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
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		klog.Errorf("Failed to copy response body to client: %v", err)
	}
}

// EstimateTokens estimates input tokens from Anthropic request JSON
// It parses the request body and counts characters in system and message content,
// then estimates token count using a rough approximation (4 characters per token)
func EstimateTokens(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	// Parse request body
	var p fastjson.Parser
	v, err := p.ParseBytes(data)
	if err != nil {
		// Fallback to rough character-based estimation if parsing fails
		return len(data) / 4
	}
	totalChars := 0
	// Count system message characters
	if system := v.Get("system"); system != nil {
		if system.Type() == fastjson.TypeString {
			totalChars += len(system.GetStringBytes())
		} else if system.Type() == fastjson.TypeArray {
			systemArray, err := system.Array()
			if err == nil {
				for _, block := range systemArray {
					if text := block.GetStringBytes("text"); text != nil {
						totalChars += len(text)
					}
				}
			}
		}
	}
	// Count message characters
	if messages := v.Get("messages"); messages != nil && messages.Type() == fastjson.TypeArray {
		msgsArray, err := messages.Array()
		if err == nil {
			for _, msg := range msgsArray {
				if content := msg.Get("content"); content != nil {
					if content.Type() == fastjson.TypeString {
						totalChars += len(content.GetStringBytes())
					} else if content.Type() == fastjson.TypeArray {
						contentArray, err := content.Array()
						if err == nil {
							for _, block := range contentArray {
								if text := block.GetStringBytes("text"); text != nil {
									totalChars += len(text)
								}
							}
						}
					}
				}
			}
		}
	}
	// Rough estimation: 4 characters per token
	estimatedTokens := totalChars / 4
	if estimatedTokens < 1 {
		estimatedTokens = 1
	}
	return estimatedTokens
}

// PopulateExternalWorker populates the request context with the external worker information
func PopulateExternalWorker(reqCtx *types.RequestContext, dst *router.RouteEndpoint) {
	worker := &types.LLMWorker{
		Role:     consts.NormalInferMode,
		Model:    dst.Model,
		BaseURL:  dst.URL,
		APIKey:   dst.APIKey,
		Released: true,
	}

	// No scheduling needed for external routes: the target worker is already determined
	// by the router, so populate ScheduleResults directly to satisfy downstream processors
	// that expect a selected worker (e.g. access logging, metrics).
	reqCtx.ScheduleCtx.ScheduleResults = []*types.LLMWorker{worker}

	// External route requests should bypass pre-processors and post-processors
	// as they are forwarded directly to external endpoints without transformation
	reqCtx.SkipProcessors = true
}
