package handler

import (
	"easgo/pkg/llm-gateway/types"
	"encoding/json"
	"net/http"

	"k8s.io/klog/v2"
)

type SimpleBackend struct {
	client *http.Client
}

// NewSimpleBackend creates a new SimpleBackend instance
func NewSimpleBackend() *SimpleBackend {
	return &SimpleBackend{
		client: NewLlmForwardClient(),
	}
}

// StreamInference implements InferBackend interface
// Performs streaming inference by forwarding request to backend and streaming response chunks
func (b *SimpleBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	chunkChan := make(chan StreamChunk, 100)

	go func() {
		defer close(chunkChan)

		// Build backend request
		worker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)

		body, err := json.Marshal(req.LLMRequest.CompletionRequest)
		if err != nil {
			klog.Errorf("failed to marshal request body: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		newReq, err := MakeNewBackendRequest(req, body, worker)
		if err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}

		// Execute request with retry
		respBody, err := DoRequest(newReq, b.client, body)
		if err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}

		// Stream read response
		if err := StreamRead(req, chunkChan, respBody); err != nil {
			chunkChan <- StreamChunk{err: err}
		}
	}()

	return chunkChan, nil
}
