package handler

import (
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func init() {
	registerForwarder(consts.ForwarderTypeNormal, func(schedulingMode types.SchedulingMode) (Forwarder, error) {
		return newNormalForwarder(), nil
	})
}

type NormalForwarder struct {
	client *http.Client
}

// newNormalForwarder creates a new NormalForwarder instance.
func newNormalForwarder() *NormalForwarder {
	return &NormalForwarder{
		client: newLlmForwardClient(),
	}
}

// Forward implements Forwarder interface.
// Forwards the request to the inference engine and streams response chunks.
func (b *NormalForwarder) Forward(req *types.RequestContext) (<-chan StreamChunk, error) {
	chunkChan := make(chan StreamChunk, 100)

	instance := req.SchedulingCtx.SchedulingResults.GetInstanceByRole(types.InferRoleNormal)
	if instance == nil {
		return nil, fmt.Errorf("no available instance for role: %s", types.InferRoleNormal)
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
		newReq, err := makeBackendRequest(req, body, instance)
		if err != nil {
			klog.Errorf("failed to create backend request: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		// Execute request with retry
		respBody, err := doRequest(newReq, b.client, body)
		if err != nil {
			klog.Errorf("failed to do backend request: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}
		defer respBody.Close()

		// Stream read response
		if err := streamRead(req, chunkChan, respBody); err != nil {
			chunkChan <- StreamChunk{err: err}
		}
	}()

	return chunkChan, nil
}
