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
	worker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)
	if worker == nil {
		return nil, fmt.Errorf("%s no available worker for role: %s", req.Id, types.InferRoleNormal)
	}

	body, err := req.MarshalRequestWithArgs(nil)
	if err != nil {
		return nil, err
	}

	// Build backend request
	newReq, err := MakeNewBackendRequest(req, body, worker)
	if err != nil {
		return nil, err
	}

	// Execute request with retry
	respBody, err := DoRequest(newReq, b.client, body, worker)
	if err != nil {
		return nil, err
	}

	return StartStreamRead(req, respBody), nil
}

func (b *SimpleBackend) Inference(req *types.RequestContext) ([]byte, error) {
	worker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)
	if worker == nil {
		return nil, fmt.Errorf("%s no available worker for role: %s", req.Id, types.InferRoleNormal)
	}

	body, err := req.MarshalRequestWithArgs(nil)
	if err != nil {
		klog.Errorf("failed to marshal request body: %v", err)
		return nil, err
	}

	return ReadFromBackend(req, b.client, body, worker)
}
