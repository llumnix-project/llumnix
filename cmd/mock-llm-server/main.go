// Package main provides a standalone mock OpenAI-compatible LLM server for local integration debugging.
// Usage: go run ./cmd/mock-llm-server [--port 8080] [--model my-model] [--delay 20ms] [--fail-rate 0] [--response-delay 0]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/protocol"
)

// ============================================================
//  CLI flags
// ============================================================

var (
	flagPort          = flag.Int("port", 8080, "Port to listen on")
	flagModel         = flag.String("model", "mock-model", "Default model name returned in responses")
	flagStreamDelay   = flag.Duration("delay", 20*time.Millisecond, "Delay between streaming chunks")
	flagFailRate      = flag.Float64("fail-rate", 0, "Probability [0,1) of returning HTTP 500, for testing retry logic")
	flagResponseDelay = flag.Duration("response-delay", 0, "Extra delay before writing the first byte, for testing timeout logic")
)

// ============================================================
//  Server
// ============================================================

type mockServer struct {
	model         string
	streamDelay   time.Duration
	failRate      float64
	responseDelay time.Duration
	reqCounter    atomic.Int64
}

func newMockServer(model string, delay time.Duration, failRate float64, responseDelay time.Duration) *mockServer {
	return &mockServer{
		model:         model,
		streamDelay:   delay,
		failRate:      failRate,
		responseDelay: responseDelay,
	}
}

func (s *mockServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)
	mux.HandleFunc("/v1/completions", s.handleCompletions)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/healthz", s.handleHealth)
}

// shouldFail returns true randomly based on failRate.
func (s *mockServer) shouldFail() bool {
	return s.failRate > 0 && rand.Float64() < s.failRate
}

// applyResponseDelay sleeps for the configured response delay before the first byte.
func (s *mockServer) applyResponseDelay() {
	if s.responseDelay > 0 {
		time.Sleep(s.responseDelay)
	}
}

// requestID generates a monotonic request ID for logging.
func (s *mockServer) requestID() int64 {
	return s.reqCounter.Add(1)
}

// ============================================================
//  /v1/chat/completions
// ============================================================

func (s *mockServer) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	reqID := s.requestID()

	var req protocol.ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	model := req.Model
	if model == "" {
		model = s.model
	}

	// Log request summary
	msgSummary := summarizeMessages(req.Messages)
	klog.Infof("[mock #%d] POST /v1/chat/completions  model=%s stream=%v messages=%s",
		reqID, model, req.Stream, msgSummary)

	// Simulate failure
	if s.shouldFail() {
		klog.Warningf("[mock #%d] injecting 500 error (fail-rate=%.2f)", reqID, s.failRate)
		http.Error(w, `{"error":{"message":"mock server injected error","type":"server_error","code":500}}`,
			http.StatusInternalServerError)
		return
	}

	s.applyResponseDelay()

	if req.Stream {
		s.streamChatResponse(w, reqID, model, req.StreamOptions)
	} else {
		s.nonStreamChatResponse(w, reqID, model)
	}
}

func (s *mockServer) nonStreamChatResponse(w http.ResponseWriter, reqID int64, model string) {
	resp := protocol.ChatCompletionResponse{
		ID:      fmt.Sprintf("chatcmpl-mock-%06d", reqID),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []protocol.ChatCompletionChoice{
			{
				Index: 0,
				Message: protocol.ChatCompletionMessage{
					Role:    consts.ROLE_ASSISTANT,
					Content: "Hello! I am a mock LLM server. How can I help you?",
				},
				FinishReason: "stop",
			},
		},
		Usage: &protocol.Usage{
			PromptTokens:     12,
			CompletionTokens: 14,
			TotalTokens:      26,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
	klog.Infof("[mock #%d] chat/completions non-stream done", reqID)
}

func (s *mockServer) streamChatResponse(w http.ResponseWriter, reqID int64, model string, streamOpts *protocol.StreamOptions) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	includeUsage := streamOpts != nil && streamOpts.IncludeUsage
	continuousUsage := streamOpts != nil && streamOpts.ContinuousUsageStats

	id := fmt.Sprintf("chatcmpl-mock-%06d", reqID)
	chunks := []string{"Hello", "!", " I", " am", " a", " mock", " LLM", " server", "."}

	for i, chunk := range chunks {
		delta := protocol.ChatCompletionStreamChoiceDelta{Content: chunk}
		// Only include role in the first chunk, matching real OpenAI behavior
		if i == 0 {
			delta.Role = consts.ROLE_ASSISTANT
		}

		resp := protocol.ChatCompletionStreamResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []protocol.ChatCompletionStreamChoice{
				{Index: 0, Delta: delta},
			},
		}

		if continuousUsage {
			resp.Usage = &protocol.Usage{
				PromptTokens:     12,
				CompletionTokens: uint64(i + 1),
				TotalTokens:      uint64(12 + i + 1),
			}
		}

		data, _ := json.Marshal(resp)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		if i < len(chunks)-1 {
			time.Sleep(s.streamDelay)
		}
	}

	// Final chunk: empty delta + finish_reason="stop"
	final := protocol.ChatCompletionStreamResponse{
		ID:      id,
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []protocol.ChatCompletionStreamChoice{
			{
				Index:        0,
				Delta:        protocol.ChatCompletionStreamChoiceDelta{},
				FinishReason: "stop",
			},
		},
	}
	data, _ := json.Marshal(final)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	// Usage-only chunk (when include_usage=true)
	if includeUsage {
		usageChunk := protocol.ChatCompletionStreamResponse{
			ID:      id,
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []protocol.ChatCompletionStreamChoice{},
			Usage: &protocol.Usage{
				PromptTokens:     12,
				CompletionTokens: uint64(len(chunks)),
				TotalTokens:      uint64(12 + len(chunks)),
			},
		}
		usageData, _ := json.Marshal(usageChunk)
		fmt.Fprintf(w, "data: %s\n\n", usageData)
		flusher.Flush()
	}

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	klog.Infof("[mock #%d] chat/completions stream done (%d chunks, include_usage=%v)", reqID, len(chunks), includeUsage)
}

// ============================================================
//  /v1/completions
// ============================================================

func (s *mockServer) handleCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	reqID := s.requestID()

	var req protocol.CompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Bad Request: %v", err), http.StatusBadRequest)
		return
	}

	model := req.Model
	if model == "" {
		model = s.model
	}

	promptStr, _ := req.Prompt.GetString()
	klog.Infof("[mock #%d] POST /v1/completions  model=%s stream=%v prompt=%q",
		reqID, model, req.Stream, truncate(promptStr, 60))

	// Simulate failure
	if s.shouldFail() {
		klog.Warningf("[mock #%d] injecting 500 error (fail-rate=%.2f)", reqID, s.failRate)
		http.Error(w, `{"error":{"message":"mock server injected error","type":"server_error","code":500}}`,
			http.StatusInternalServerError)
		return
	}

	s.applyResponseDelay()

	if req.Stream {
		s.streamCompletionResponse(w, reqID, model, req.StreamOptions)
	} else {
		s.nonStreamCompletionResponse(w, reqID, model)
	}
}

func (s *mockServer) nonStreamCompletionResponse(w http.ResponseWriter, reqID int64, model string) {
	resp := protocol.CompletionResponse{
		ID:      fmt.Sprintf("cmpl-mock-%06d", reqID),
		Object:  "text_completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []protocol.CompletionChoice{
			{
				Text:         "This is a mock text completion response.",
				Index:        0,
				FinishReason: "stop",
			},
		},
		Usage: &protocol.Usage{
			PromptTokens:     8,
			CompletionTokens: 10,
			TotalTokens:      18,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
	klog.Infof("[mock #%d] completions non-stream done", reqID)
}

func (s *mockServer) streamCompletionResponse(w http.ResponseWriter, reqID int64, model string, streamOpts *protocol.StreamOptions) {
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}

	includeUsage := streamOpts != nil && streamOpts.IncludeUsage

	id := fmt.Sprintf("cmpl-mock-%06d", reqID)
	// /v1/completions stream uses CompletionResponse with text field (not message)
	chunks := []string{"This", " is", " a", " mock", " text", " completion", " response", "."}

	for i, chunk := range chunks {
		// Each stream chunk for /v1/completions is a CompletionResponse with a single choice
		resp := protocol.CompletionResponse{
			ID:      id,
			Object:  "text_completion",
			Created: time.Now().Unix(),
			Model:   model,
			Choices: []protocol.CompletionChoice{
				{
					Text:         chunk,
					Index:        0,
					FinishReason: "",
				},
			},
		}

		data, _ := json.Marshal(resp)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()

		if i < len(chunks)-1 {
			time.Sleep(s.streamDelay)
		}
	}

	// Final chunk with finish_reason
	finalResp := protocol.CompletionResponse{
		ID:      id,
		Object:  "text_completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []protocol.CompletionChoice{
			{
				Text:         "",
				Index:        0,
				FinishReason: "stop",
			},
		},
	}
	if includeUsage {
		finalResp.Usage = &protocol.Usage{
			PromptTokens:     8,
			CompletionTokens: uint64(len(chunks)),
			TotalTokens:      uint64(8 + len(chunks)),
		}
	}
	data, _ := json.Marshal(finalResp)
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()

	klog.Infof("[mock #%d] completions stream done (%d chunks)", reqID, len(chunks))
}

// ============================================================
//  /v1/models
// ============================================================

func (s *mockServer) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	klog.Infof("[mock] GET /v1/models")

	resp := map[string]interface{}{
		"object": "list",
		"data": []map[string]interface{}{
			{
				"id":       s.model,
				"object":   "model",
				"created":  time.Now().Unix(),
				"owned_by": "mock",
			},
		},
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// ============================================================
//  /healthz
// ============================================================

func (s *mockServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, "ok")
}

// ============================================================
//  Helpers
// ============================================================

// summarizeMessages returns a compact summary of the last user message for logging.
func summarizeMessages(msgs []protocol.ChatCompletionMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return fmt.Sprintf("[%s] %q", msgs[i].Role, truncate(msgs[i].Content, 60))
		}
	}
	if len(msgs) > 0 {
		return fmt.Sprintf("[%s] %q", msgs[0].Role, truncate(msgs[0].Content, 60))
	}
	return "(empty)"
}

// truncate cuts s to at most maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// ============================================================
//  main
// ============================================================

func main() {
	klog.InitFlags(nil)
	flag.Parse()

	srv := newMockServer(*flagModel, *flagStreamDelay, *flagFailRate, *flagResponseDelay)

	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	addr := fmt.Sprintf(":%d", *flagPort)

	httpSrv := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	klog.Infof("Mock LLM server starting on http://0.0.0.0%s", addr)
	klog.Infof("  Model         : %s", *flagModel)
	klog.Infof("  Stream delay  : %v", *flagStreamDelay)
	klog.Infof("  Fail rate     : %.2f", *flagFailRate)
	klog.Infof("  Response delay: %v", *flagResponseDelay)
	klog.Infof("  Routes:")
	klog.Infof("    POST /v1/chat/completions  (stream & non-stream)")
	klog.Infof("    POST /v1/completions       (stream & non-stream)")
	klog.Infof("    GET  /v1/models")
	klog.Infof("    GET  /healthz")

	go func() {
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			klog.Errorf("Server error: %v", err)
			os.Exit(1)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	klog.Infof("Mock LLM server shutting down...")
}
