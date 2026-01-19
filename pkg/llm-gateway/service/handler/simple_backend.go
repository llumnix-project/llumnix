package handler

import (
	"easgo/pkg/llm-gateway/types"
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/klog/v2"
)

const (
	BackendTypeSimple = "simple"
)

func init() {
	RegisterBackend(BackendTypeSimple, func(scheduleMode types.ScheduleMode) (InferenceBackend, error) {
		return NewSimpleBackend(), nil
	})
}

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

	worker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)
	if worker == nil {
		return nil, fmt.Errorf("no available worker for role: %s", types.InferRoleNormal)
	}

	go func() {
		defer close(chunkChan)

		body, err := json.Marshal(req.LLMRequest.CompletionRequest)
		if err != nil {
			klog.Errorf("failed to marshal request body: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		// Build backend request
		newReq, err := MakeNewBackendRequest(req, body, worker)
		if err != nil {
			klog.Errorf("failed to create new backend request: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		// Execute request with retry
		respBody, err := DoRequest(newReq, b.client, body)
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
	}()

	return chunkChan, nil
}
