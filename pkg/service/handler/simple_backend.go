package handler

import (
	"fmt"
	"llm-gateway/pkg/types"
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

		body, err := req.MarshalRequestWithArgs(nil)
		if err != nil {
			klog.Errorf("failed to marshal request body: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		StreamReadFromBackend(req, b.client, body, worker, chunkChan)
	}()

	return chunkChan, nil
}

func (b *SimpleBackend) Inference(req *types.RequestContext) ([]byte, error) {
	worker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)
	if worker == nil {
		return nil, fmt.Errorf("no available worker for role: %s", types.InferRoleNormal)
	}

	body, err := req.MarshalRequestWithArgs(nil)
	if err != nil {
		klog.Errorf("failed to marshal request body: %v", err)
		return nil, err
	}

	return ReadFromBackend(req, b.client, body, worker)
}
