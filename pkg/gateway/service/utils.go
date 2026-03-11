package service

import (
	"io"
	"llumnix/pkg/gateway/protocol"
	"llumnix/pkg/types"
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

func GetResponseUsage(req *types.RequestContext) *protocol.Usage {
	var usage *protocol.Usage
	switch req.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		if req.LLMRequest.ClientStream {
			usage = req.LLMRequest.ChatCompletionStreamResponse.Usage
		} else {
			usage = req.LLMRequest.ChatCompletionResponse.Usage
		}
	case protocol.OpenAICompletion:
		usage = req.LLMRequest.CompletionResponse.Usage
	default:
		klog.Warningf("Unsupported protocol: %s", req.LLMRequest.Protocol)
		return nil
	}
	return usage
}

func GetChatCompletionStreamText(response *protocol.ChatCompletionStreamResponse) string {
	text := ""
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		if len(choice.Delta.ReasoningContent) > 0 {
			text += choice.Delta.ReasoningContent
		} else {
			text += choice.Delta.Content
		}
	} else {
		klog.Warningf("Invalid chat completion response: %v", response)
	}
	return text
}

func GetChatCompletionText(response *protocol.ChatCompletionResponse) string {
	text := ""
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		if len(choice.Message.ReasoningContent) > 0 {
			text += choice.Message.ReasoningContent
		} else {
			text += choice.Message.Content
		}
	} else {
		klog.Warningf("Invalid chat completion response: %v", response)
	}
	return text
}

func GetCompletionText(response *protocol.CompletionResponse) string {
	text := ""
	if response != nil && len(response.Choices) > 0 {
		choice := response.Choices[0]
		text += choice.Text
	} else {
		klog.Warningf("Invalid completion response: %v", response)
	}
	return text
}

func GetResponseText(req *types.RequestContext) string {
	var text string
	switch req.LLMRequest.Protocol {
	case protocol.OpenAIChatCompletion:
		if req.LLMRequest.ClientStream {
			text = GetChatCompletionStreamText(req.LLMRequest.ChatCompletionStreamResponse)
		} else {
			text = GetChatCompletionText(req.LLMRequest.ChatCompletionResponse)
		}
	case protocol.OpenAICompletion:
		text = GetCompletionText(req.LLMRequest.CompletionResponse)
	default:
		klog.Warningf("Unsupported protocol: %s", req.LLMRequest.Protocol)
		return ""
	}
	return text
}
