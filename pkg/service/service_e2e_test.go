package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/protocol"
	"llm-gateway/pkg/protocol/anthropic"
)

// ============================================================
//  Mock LLM Server - simulates a real LLM backend service
// ============================================================

// MockLLMServer implements a simple LLM backend server that handles both OpenAI and Anthropic protocols.
// It supports streaming and non-streaming responses.
type MockLLMServer struct {
	server         *httptest.Server
	requestCount   int
	mu             sync.Mutex
	streamDelay    time.Duration           // Delay between stream chunks
	shouldFail     bool                    // Simulate server errors
	responseStatus int                     // Custom response status code
	streamOptions  *protocol.StreamOptions // StreamOptions configuration
}

// NewMockLLMServer creates and starts a new mock LLM server.
func NewMockLLMServer() *MockLLMServer {
	mock := &MockLLMServer{
		streamDelay:    10 * time.Millisecond,
		responseStatus: http.StatusOK,
		streamOptions: &protocol.StreamOptions{
			IncludeUsage:         true,
			ContinuousUsageStats: true,
		},
	}

	mux := http.NewServeMux()

	// OpenAI chat completions endpoint
	mux.HandleFunc("/v1/chat/completions", mock.handleChatCompletions)

	// OpenAI text completions endpoint
	mux.HandleFunc("/v1/completions", mock.handleCompletions)

	// Anthropic messages endpoint
	// mux.HandleFunc("/v1/messages", mock.handleAnthropicMessages)

	// Models endpoint
	mux.HandleFunc("/v1/models", mock.handleModels)

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mock.server = httptest.NewServer(mux)
	return mock
}

// Close shuts down the mock server.
func (m *MockLLMServer) Close() {
	m.server.Close()
}

// URL returns the base URL of the mock server.
func (m *MockLLMServer) URL() string {
	return m.server.URL
}

// GetRequestCount returns the total number of requests received.
func (m *MockLLMServer) GetRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requestCount
}

// SetShouldFail configures the server to return errors.
func (m *MockLLMServer) SetShouldFail(fail bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.shouldFail = fail
}

// SetResponseStatus sets custom response status code.
func (m *MockLLMServer) SetResponseStatus(status int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responseStatus = status
}

// SetStreamOptions sets StreamOptions configuration.
func (m *MockLLMServer) SetStreamOptions(opts *protocol.StreamOptions) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.streamOptions = opts
}

// GetStreamOptions returns the current StreamOptions configuration.
func (m *MockLLMServer) GetStreamOptions() *protocol.StreamOptions {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streamOptions
}

func (m *MockLLMServer) incrementRequestCount() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requestCount++
}

func (m *MockLLMServer) shouldFailNow() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.shouldFail
}

func (m *MockLLMServer) getResponseStatus() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.responseStatus
}

// handleChatCompletions handles OpenAI chat completion requests.
func (m *MockLLMServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	m.incrementRequestCount()

	if m.shouldFailNow() {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	var req protocol.ChatCompletionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.Stream {
		m.streamChatResponse(w, &req)
	} else {
		m.nonStreamChatResponse(w, &req)
	}
}

// handleCompletions handles OpenAI text completion requests.
func (m *MockLLMServer) handleCompletions(w http.ResponseWriter, r *http.Request) {
	m.incrementRequestCount()

	if m.shouldFailNow() {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Simple text completion response
	response := protocol.ChatCompletionResponse{
		ID:      "cmpl-test-123",
		Object:  "text_completion",
		Created: time.Now().Unix(),
		Model:   "test-model",
		Choices: []protocol.ChatCompletionChoice{
			{
				Index: 0,
				Message: protocol.ChatCompletionMessage{
					Role:    consts.ROLE_ASSISTANT,
					Content: "This is a test completion response.",
				},
				FinishReason: "stop",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(m.getResponseStatus())
	json.NewEncoder(w).Encode(response)
}

// handleAnthropicMessages handles Anthropic message requests.
func (m *MockLLMServer) handleAnthropicMessages(w http.ResponseWriter, r *http.Request) {
	m.incrementRequestCount()

	if m.shouldFailNow() {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	var req anthropic.Request
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if req.Stream {
		m.streamAnthropicResponse(w, &req)
	} else {
		m.nonStreamAnthropicResponse(w, &req)
	}
}

// handleModels handles model list requests.
func (m *MockLLMServer) handleModels(w http.ResponseWriter, r *http.Request) {
	m.incrementRequestCount()

	response := map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"id":       "test-model",
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "test",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// streamChatResponse sends a streaming chat completion response.
func (m *MockLLMServer) streamChatResponse(w http.ResponseWriter, req *protocol.ChatCompletionRequest) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(m.getResponseStatus())

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Determine if we should include usage based on request StreamOptions
	includeUsage := req.StreamOptions != nil && req.StreamOptions.IncludeUsage
	continuousUsage := req.StreamOptions != nil && req.StreamOptions.ContinuousUsageStats

	// Send multiple chunks
	chunks := []string{"Hello", " from", " mock", " LLM", " server"}
	for i, chunk := range chunks {
		response := protocol.ChatCompletionStreamResponse{
			ID:      "chatcmpl-test-123",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []protocol.ChatCompletionStreamChoice{
				{
					Index: 0,
					Delta: protocol.ChatCompletionStreamChoiceDelta{
						Role:    consts.ROLE_ASSISTANT,
						Content: chunk,
					},
				},
			},
		}

		// Add usage for continuous usage stats mode
		if continuousUsage {
			response.Usage = &protocol.Usage{
				PromptTokens:     10,
				CompletionTokens: uint64(i + 1), // Incremental tokens
				TotalTokens:      uint64(10 + i + 1),
			}
		}

		data, _ := json.Marshal(response)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		// Add delay between chunks to simulate real streaming
		if i < len(chunks)-1 {
			time.Sleep(m.streamDelay)
		}
	}

	// Send final chunk with finish_reason
	finalResponse := protocol.ChatCompletionStreamResponse{
		ID:      "chatcmpl-test-123",
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []protocol.ChatCompletionStreamChoice{
			{
				Index:        0,
				Delta:        protocol.ChatCompletionStreamChoiceDelta{},
				FinishReason: "stop",
			},
		},
	}
	data, _ := json.Marshal(finalResponse)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Send usage chunk if include_usage is enabled
	if includeUsage {
		usageResponse := protocol.ChatCompletionStreamResponse{
			ID:      "chatcmpl-test-123",
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   req.Model,
			Choices: []protocol.ChatCompletionStreamChoice{},
			Usage: &protocol.Usage{
				PromptTokens:     10,
				CompletionTokens: 5,
				TotalTokens:      15,
			},
		}
		usageData, _ := json.Marshal(usageResponse)
		fmt.Fprintf(w, "data: %s\n\n", usageData)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

// nonStreamChatResponse sends a non-streaming chat completion response.
func (m *MockLLMServer) nonStreamChatResponse(w http.ResponseWriter, req *protocol.ChatCompletionRequest) {
	response := protocol.ChatCompletionResponse{
		ID:      "chatcmpl-test-123",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   req.Model,
		Choices: []protocol.ChatCompletionChoice{
			{
				Index: 0,
				Message: protocol.ChatCompletionMessage{
					Role:    consts.ROLE_ASSISTANT,
					Content: "Hello from mock LLM server",
				},
				FinishReason: "stop",
			},
		},
		Usage: &protocol.Usage{
			PromptTokens:     10,
			CompletionTokens: 5,
			TotalTokens:      15,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(m.getResponseStatus())
	json.NewEncoder(w).Encode(response)
}

// streamAnthropicResponse sends a streaming Anthropic response.
func (m *MockLLMServer) streamAnthropicResponse(w http.ResponseWriter, req *anthropic.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(m.getResponseStatus())

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	// Send message start event
	fmt.Fprintf(w, "event: message_start\n")
	fmt.Fprintf(w, "data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test_123\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"%s\"}}\n\n", req.Model)
	flusher.Flush()

	// Send content chunks
	chunks := []string{"Hello", " from", " Anthropic", " mock"}
	for _, chunk := range chunks {
		fmt.Fprintf(w, "event: content_block_delta\n")
		fmt.Fprintf(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"%s\"}}\n\n", chunk)
		flusher.Flush()
		time.Sleep(m.streamDelay)
	}

	// Send message delta (stop reason)
	fmt.Fprintf(w, "event: message_delta\n")
	fmt.Fprintf(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":5}}\n\n")
	flusher.Flush()

	// Send message stop event
	fmt.Fprintf(w, "event: message_stop\n")
	fmt.Fprintf(w, "data: {\"type\":\"message_stop\"}\n\n")
	flusher.Flush()
}

// nonStreamAnthropicResponse sends a non-streaming Anthropic response.
func (m *MockLLMServer) nonStreamAnthropicResponse(w http.ResponseWriter, req *anthropic.Request) {
	response := anthropic.Response{
		ID:    "msg_test_123",
		Type:  "message",
		Role:  "assistant",
		Model: req.Model,
		Content: []any{
			anthropic.ContentBlockText{
				Type: "text",
				Text: "Hello from Anthropic mock server",
			},
		},
		StopReason: stringPtr("end_turn"),
		Usage: &anthropic.Usage{
			InputTokens:  10,
			OutputTokens: 5,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(m.getResponseStatus())
	json.NewEncoder(w).Encode(response)
}

// ============================================================
//  Test Helper Functions
// ============================================================

// stringPtr returns a pointer to the given string.
func stringPtr(s string) *string {
	return &s
}

// createTestGatewayConfig creates a minimal gateway configuration for testing.
func createTestGatewayConfig(localTestIPs string) *options.Config {
	return &options.Config{
		Host:                  "127.0.0.1",
		Port:                  0, // Random port
		LocalTestIPs:          localTestIPs,
		MaxQueueSize:          100,
		WaitQueueThreads:      4,
		RetryCount:            2,
		WaitScheduleTimeout:   5000,
		WaitScheduleTryPeriod: 100,
		EnableAccessLog:       false,
		EnableLogInput:        false,
		PDSplitMode:           "",
		SeparatePDSchedule:    false,
	}
}

// createTestGatewayConfigWithScheduler creates a gateway configuration for Gateway+Scheduler integration test.
// IMPORTANT: LocalTestIPs is empty to trigger CompositeBalancer, which creates SchedulerClient.
// LocalTestBackendIPs is used for backend resolver, LocalTestSchedulerIP for scheduler connection.
func createTestGatewayConfigWithScheduler(backendService, schedulerAddr string) *options.Config {
	return &options.Config{
		Host:                  "127.0.0.1",
		Port:                  0, // Random port
		LocalTestIPs:          "",
		LocalTestBackendIPs:   backendService,
		LocalTestSchedulerIP:  schedulerAddr,
		SchedulePolicy:        consts.SchedulePolicyLeastRequest,
		MaxQueueSize:          100,
		WaitQueueThreads:      4,
		RetryCount:            2,
		WaitScheduleTimeout:   5000,
		WaitScheduleTryPeriod: 100,
		EnableAccessLog:       false,
		EnableLogInput:        false,
		PDSplitMode:           "",
		SeparatePDSchedule:    false,
	}
}

// createTestSchedulerConfig creates a minimal scheduler configuration for testing.
func createTestSchedulerConfig(backendService string) *options.Config {
	return &options.Config{
		Host:            "127.0.0.1",
		Port:            0, // Random port
		ScheduleMode:    true,
		SchedulePolicy:  consts.SchedulePolicyLeastRequest,
		BackendService:  backendService,
		LocalTestIPs:    backendService,   // Use local test mode to bypass discovery
		LlmScheduler:    "test.scheduler", // Dummy scheduler name to satisfy validation
		LlmGateway:      "test.gateway",   // Dummy gateway name
		EnableAccessLog: false,
		LlumnixConfig: options.LlumnixConfig{
			AllowConcurrentSchedule:  true,
			EnableFullModeScheduling: false,
			EnableMetrics:            false,
			EnableRescheduling:       false,
		},
	}
}

// makeGatewayRequest sends a request to the gateway service and returns the response.
func makeGatewayRequest(t *testing.T, gatewayURL, path string, body interface{}) (*http.Response, []byte) {
	jsonBody, err := json.Marshal(body)
	require.NoError(t, err)

	req, err := http.NewRequest("POST", gatewayURL+path, bytes.NewBuffer(jsonBody))
	require.NoError(t, err)

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	resp.Body.Close()

	return resp, respBody
}

// ============================================================
//  Gateway Service E2E Tests
// ============================================================

// TestGatewayService_OpenAI_NonStreaming tests gateway service with OpenAI non-streaming requests.
func TestGatewayService_OpenAI_NonStreaming(t *testing.T) {
	// Start mock LLM server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	// Extract host:port from mock server URL (remove http://)
	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start gateway service
	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	// Start gateway in a goroutine
	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare test request
	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Hello, how are you?",
			},
		},
		Stream: false,
	}

	// Send request to gateway
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/chat/completions", chatReq)

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var chatResp protocol.ChatCompletionResponse
	err := json.Unmarshal(body, &chatResp)
	require.NoError(t, err)

	assert.Equal(t, "chatcmpl-test-123", chatResp.ID)
	assert.Equal(t, "chat.completion", chatResp.Object)
	assert.Len(t, chatResp.Choices, 1)
	assert.Equal(t, "Hello from mock LLM server", chatResp.Choices[0].Message.Content)
	assert.Equal(t, "stop", string(chatResp.Choices[0].FinishReason))

	// Verify usage information
	require.NotNil(t, chatResp.Usage, "Usage should not be nil in non-streaming response")
	assert.Equal(t, uint64(10), chatResp.Usage.PromptTokens, "PromptTokens should be 10")
	assert.Equal(t, uint64(5), chatResp.Usage.CompletionTokens, "CompletionTokens should be 5")
	assert.Equal(t, uint64(15), chatResp.Usage.TotalTokens, "TotalTokens should be 15")

	// Verify mock server received the request
	assert.Equal(t, 1, mockServer.GetRequestCount())
}

// TestGatewayService_OpenAI_Streaming tests gateway service with OpenAI streaming requests.
func TestGatewayService_OpenAI_Streaming(t *testing.T) {
	// Start mock LLM server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start gateway service
	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare streaming request with StreamOptions enabled
	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Tell me a story",
			},
		},
		Stream: true,
		StreamOptions: &protocol.StreamOptions{
			IncludeUsage:         true,
			ContinuousUsageStats: true,
		},
	}

	jsonBody, _ := json.Marshal(chatReq)
	req, _ := http.NewRequest("POST", gatewayServer.URL+"/v1/chat/completions", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	// Parse and verify all streaming chunks
	var allChunks []protocol.ChatCompletionStreamResponse
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			var chunk protocol.ChatCompletionStreamResponse
			if err := json.Unmarshal([]byte(data), &chunk); err == nil {
				allChunks = append(allChunks, chunk)
			} else {
				t.Logf("Failed to unmarshal chunk: %v, data: %s", err, data)
			}
		}
	}

	require.NoError(t, scanner.Err(), "Scanner should not error")
	assert.Greater(t, len(allChunks), 1, "Should receive multiple chunks")

	// Categorize chunks for verification
	var contentChunks []protocol.ChatCompletionStreamResponse
	var finishChunk *protocol.ChatCompletionStreamResponse
	var usageChunk *protocol.ChatCompletionStreamResponse

	for i := range allChunks {
		chunk := &allChunks[i]

		// Content chunk: has delta content
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			contentChunks = append(contentChunks, *chunk)
		} else if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != "" {
			// Finish chunk: has finish_reason
			finishChunk = chunk
		} else if len(chunk.Choices) == 0 && chunk.Usage != nil {
			// Usage chunk: empty choices with usage data
			usageChunk = chunk
		}
	}

	// Verify content chunks with continuous usage stats
	assert.Greater(t, len(contentChunks), 0, "Should have content chunks")
	t.Logf("Received %d content chunks", len(contentChunks))

	for i, chunk := range contentChunks {
		// Verify basic chunk structure
		assert.Equal(t, "chatcmpl-test-123", chunk.ID, "Chunk %d should have correct ID", i)
		assert.Equal(t, "chat.completion.chunk", chunk.Object, "Chunk %d should have correct object type", i)
		assert.Equal(t, "test-model", chunk.Model, "Chunk %d should have correct model", i)
		assert.Len(t, chunk.Choices, 1, "Chunk %d should have 1 choice", i)
		assert.NotEmpty(t, chunk.Choices[0].Delta.Content, "Chunk %d should have content", i)

		// Verify continuous usage stats in content chunks
		require.NotNil(t, chunk.Usage, "Content chunk %d should have usage (continuous_usage_stats enabled)", i)
		assert.Equal(t, uint64(10), chunk.Usage.PromptTokens, "Chunk %d: PromptTokens should be 10", i)
		assert.Equal(t, uint64(i+1), chunk.Usage.CompletionTokens, "Chunk %d: CompletionTokens should be %d", i, i+1)
		assert.Equal(t, uint64(10+i+1), chunk.Usage.TotalTokens, "Chunk %d: TotalTokens should be %d", i, 10+i+1)
	}

	// Verify finish chunk
	require.NotNil(t, finishChunk, "Should have finish chunk with finish_reason")
	assert.Equal(t, "stop", string(finishChunk.Choices[0].FinishReason), "Finish reason should be 'stop'")
	assert.Empty(t, finishChunk.Choices[0].Delta.Content, "Finish chunk should have empty content")

	// Verify final usage chunk (include_usage enabled)
	require.NotNil(t, usageChunk, "Should have final usage chunk (include_usage enabled)")
	assert.Equal(t, "chatcmpl-test-123", usageChunk.ID, "Usage chunk should have correct ID")
	assert.Equal(t, "test-model", usageChunk.Model, "Usage chunk should have correct model")
	assert.Len(t, usageChunk.Choices, 0, "Usage chunk should have empty choices")
	require.NotNil(t, usageChunk.Usage, "Usage chunk must have usage data")
	assert.Equal(t, uint64(10), usageChunk.Usage.PromptTokens, "Final usage: PromptTokens should be 10")
	assert.Equal(t, uint64(5), usageChunk.Usage.CompletionTokens, "Final usage: CompletionTokens should be 5")
	assert.Equal(t, uint64(15), usageChunk.Usage.TotalTokens, "Final usage: TotalTokens should be 15")

	// Verify mock server received the request
	assert.Equal(t, 1, mockServer.GetRequestCount())
}

// TestGatewayService_Anthropic_NonStreaming tests gateway service with Anthropic non-streaming requests.
func TestGatewayService_Anthropic_NonStreaming(t *testing.T) {
	// Start mock LLM server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start gateway service
	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/messages", gateway.HandleAnthropicRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare Anthropic request
	anthropicReq := anthropic.Request{
		Model:     "test-model",
		MaxTokens: 100,
		Messages: []anthropic.Message{
			{
				Role: consts.ROLE_USER,
				Content: []any{
					anthropic.ContentBlockText{
						Type: "text",
						Text: "Hello, Claude!",
					},
				},
			},
		},
		Stream: false,
	}

	// Send request to gateway
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/messages", anthropicReq)

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var anthropicResp anthropic.Response
	err := json.Unmarshal(body, &anthropicResp)
	require.NoError(t, err)

	// Verify response structure (ID is request ID, not fixed mock ID)
	assert.NotEmpty(t, anthropicResp.ID)
	assert.Equal(t, "message", anthropicResp.Type)
	assert.NotEmpty(t, anthropicResp.Content)

	// Verify content
	assert.Len(t, anthropicResp.Content, 1)
	if len(anthropicResp.Content) > 0 {
		if textBlock, ok := anthropicResp.Content[0].(map[string]interface{}); ok {
			assert.Equal(t, "text", textBlock["type"])
			assert.Contains(t, textBlock["text"], "Hello from mock LLM server")
		}
	}
}

// TestGatewayService_ErrorHandling tests gateway error handling.
func TestGatewayService_ErrorHandling(t *testing.T) {
	// Start mock LLM server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	// Configure server to fail
	mockServer.SetShouldFail(true)

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start gateway service
	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare test request
	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Hello",
			},
		},
		Stream: false,
	}

	// Send request to gateway
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/chat/completions", chatReq)

	// Gateway should handle backend error and return appropriate status
	// With retry enabled, it may succeed on retry or return error
	t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))

	// Server should have received at least one request
	assert.GreaterOrEqual(t, mockServer.GetRequestCount(), 1)
}

// TestGatewayService_Retry tests gateway retry mechanism.
func TestGatewayService_Retry(t *testing.T) {
	// Create two mock servers
	mockServer1 := NewMockLLMServer()
	defer mockServer1.Close()
	mockServer1.SetShouldFail(true) // First server always fails

	mockServer2 := NewMockLLMServer()
	defer mockServer2.Close()
	// Second server works normally

	serverAddr1 := strings.TrimPrefix(mockServer1.URL(), "http://")
	serverAddr2 := strings.TrimPrefix(mockServer2.URL(), "http://")

	// Configure gateway with both servers, instance-level exclude scope
	config := createTestGatewayConfig(serverAddr2 + "," + serverAddr1)
	config.RetryCount = 2
	config.RetryExcludeScope = consts.RetryExcludeScopeInstance
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Test retry",
			},
		},
		Stream: false,
	}

	// Send request
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/chat/completions", chatReq)

	t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Both servers should have received exactly one request (one fail, one success)
	totalRequests := mockServer1.GetRequestCount() + mockServer2.GetRequestCount()
	assert.Equal(t, 2, totalRequests)
	assert.Equal(t, 1, mockServer1.GetRequestCount())
	assert.Equal(t, 1, mockServer2.GetRequestCount())
}

// TestGatewayService_Retry_HostScope tests gateway retry behavior when using host-level exclude scope.
// NOTE: In local test mode, both mock servers run on 127.0.0.1 with different ports, so host scope
// treats them as the same host. Once one instance on the host fails, all instances on that host are
// excluded, and retry cannot switch to the other server.
func TestGatewayService_Retry_HostScope(t *testing.T) {
	// Create two mock servers
	mockServer1 := NewMockLLMServer()
	defer mockServer1.Close()
	mockServer1.SetShouldFail(true) // First server always fails

	mockServer2 := NewMockLLMServer()
	defer mockServer2.Close()
	// Second server works normally, but will share the same host (127.0.0.1)

	serverAddr1 := strings.TrimPrefix(mockServer1.URL(), "http://")
	serverAddr2 := strings.TrimPrefix(mockServer2.URL(), "http://")

	// Configure gateway with both servers, host-level exclude scope
	config := createTestGatewayConfig(serverAddr2 + "," + serverAddr1)
	config.RetryCount = 2
	config.RetryExcludeScope = consts.RetryExcludeScopeHost
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Test retry host scope",
			},
		},
		Stream: false,
	}

	// Send request
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/chat/completions", chatReq)

	t.Logf("[HostScope] Response status: %d, body: %s", resp.StatusCode, string(body))
	// With host-level exclude scope and both servers on 127.0.0.1, retries cannot switch to the second server.
	// We only assert that the request does not panic and at least one backend is called.
	totalRequests := mockServer1.GetRequestCount() + mockServer2.GetRequestCount()

	assert.Equal(t, 1, totalRequests)
	assert.Equal(t, 1, mockServer1.GetRequestCount())
	assert.Equal(t, 0, mockServer2.GetRequestCount())
}

// TestGatewayService_HealthCheck tests health check endpoint.
func TestGatewayService_HealthCheck(t *testing.T) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/healthz", gateway.healthz)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Test health check endpoint
	resp, err := http.Get(gatewayServer.URL + "/healthz")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestGatewayService_ConcurrentRequests tests gateway handling concurrent requests.
func TestGatewayService_ConcurrentRequests(t *testing.T) {
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	config := createTestGatewayConfig(serverAddr)
	config.MaxQueueSize = 50
	config.WaitQueueThreads = 10
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	concurrency := 10
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			chatReq := protocol.ChatCompletionRequest{
				Model: "test-model",
				Messages: []protocol.ChatCompletionMessage{
					{
						Role:    consts.ROLE_USER,
						Content: fmt.Sprintf("Request %d", idx),
					},
				},
				Stream: false,
			}

			resp, _ := makeGatewayRequest(t, gatewayServer.URL, "/v1/chat/completions", chatReq)
			if resp.StatusCode == http.StatusOK {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// All requests should succeed
	assert.Equal(t, concurrency, successCount)
	assert.Equal(t, concurrency, mockServer.GetRequestCount())
}

// TestGatewayWithScheduler_Integration tests the integration of Gateway and Scheduler services.
// This test verifies that Gateway can communicate with Scheduler for request scheduling.
func TestGatewayWithScheduler_Integration(t *testing.T) {
	// Start mock LLM server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start scheduler service
	schedulerConfig := createTestSchedulerConfig(serverAddr)
	schedulerConfig.Port = 0 // Use random port
	scheduler := NewScheduleService(schedulerConfig)
	require.NotNil(t, scheduler)

	// Start scheduler server
	schedulerServer := httptest.NewServer(scheduler)
	defer schedulerServer.Close()

	schedulerAddr := strings.TrimPrefix(schedulerServer.URL, "http://")

	// Create and start gateway service with scheduler
	// IMPORTANT: Use createTestGatewayConfigWithScheduler to trigger CompositeBalancer with SchedulerClient.
	// This ensures GetPromptTokens() is called during scheduling.
	gatewayConfig := createTestGatewayConfigWithScheduler(serverAddr, schedulerAddr)
	gateway := NewGatewayService(gatewayConfig)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare test request
	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Test scheduler integration",
			},
		},
		Stream: false,
	}

	// Send request to gateway (which will use scheduler)
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/chat/completions", chatReq)

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var chatResp protocol.ChatCompletionResponse
	err := json.Unmarshal(body, &chatResp)
	require.NoError(t, err)

	assert.Equal(t, "chatcmpl-test-123", chatResp.ID)
	assert.Equal(t, "Hello from mock LLM server", chatResp.Choices[0].Message.Content)

	// Verify mock server received the request
	assert.Equal(t, 1, mockServer.GetRequestCount())
}

// TestGatewayService_Anthropic_Complete tests Anthropic protocol with minimal dependencies.
func TestGatewayService_Anthropic_Complete(t *testing.T) {
	// Start mock LLM server
	mockServer := NewMockLLMServer()
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start gateway service
	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/messages", gateway.HandleAnthropicRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare Anthropic request
	anthropicReq := anthropic.Request{
		Model:     "test-model",
		MaxTokens: 100,
		Messages: []anthropic.Message{
			{
				Role: consts.ROLE_USER,
				Content: []any{
					anthropic.ContentBlockText{
						Type: "text",
						Text: "Hello, Claude!",
					},
				},
			},
		},
		Stream: false,
	}

	// Send request to gateway
	resp, body := makeGatewayRequest(t, gatewayServer.URL, "/v1/messages", anthropicReq)

	// Verify response
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var anthropicResp anthropic.Response
	err := json.Unmarshal(body, &anthropicResp)
	require.NoError(t, err)

	// Verify response structure (ID is request ID, not fixed mock ID)
	assert.NotEmpty(t, anthropicResp.ID)
	assert.Equal(t, "message", anthropicResp.Type)
	assert.NotEmpty(t, anthropicResp.Content)

	// Verify content
	assert.Len(t, anthropicResp.Content, 1)
	if len(anthropicResp.Content) > 0 {
		if textBlock, ok := anthropicResp.Content[0].(map[string]interface{}); ok {
			assert.Equal(t, "text", textBlock["type"])
			assert.Contains(t, textBlock["text"], "Hello from mock LLM server")
		}
	}
}

// TestGatewayService_StreamingWithChunks tests detailed streaming behavior.
func TestGatewayService_StreamingWithChunks(t *testing.T) {
	// Start mock LLM server with custom delay
	mockServer := NewMockLLMServer()
	mockServer.streamDelay = 5 * time.Millisecond // Faster for testing
	defer mockServer.Close()

	serverAddr := strings.TrimPrefix(mockServer.URL(), "http://")

	// Create and start gateway service
	config := createTestGatewayConfig(serverAddr)
	gateway := NewGatewayService(config)
	require.NotNil(t, gateway)

	// Create a router for the gateway
	router := http.NewServeMux()
	router.HandleFunc("/v1/chat/completions", gateway.HandleOpenAIRequest)

	gatewayServer := httptest.NewServer(router)
	defer gatewayServer.Close()

	// Prepare streaming request
	chatReq := protocol.ChatCompletionRequest{
		Model: "test-model",
		Messages: []protocol.ChatCompletionMessage{
			{
				Role:    consts.ROLE_USER,
				Content: "Stream test",
			},
		},
		Stream: true,
	}

	jsonBody, _ := json.Marshal(chatReq)
	req, _ := http.NewRequest("POST", gatewayServer.URL+"/v1/chat/completions", bytes.NewBuffer(jsonBody))
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, resp.Header.Get("Content-Type"), "text/event-stream")

	// Collect all chunks
	var chunks []string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			chunks = append(chunks, line)
		}
	}

	// Verify we received multiple chunks including [DONE]
	assert.Greater(t, len(chunks), 2, "Should receive multiple chunks")

	// Verify [DONE] marker is present
	lastChunk := chunks[len(chunks)-1]
	assert.Contains(t, lastChunk, "[DONE]")

	// Verify mock server received the request
	assert.Equal(t, 1, mockServer.GetRequestCount())
}

func init() {
	klog.InitFlags(nil)
	flag.Set("v", "4")
}
