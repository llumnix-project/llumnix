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
		return newNeutralForwarder(), nil
	})
}

type NeutralForwarder struct {
	client *http.Client
}

// newNeutralForwarder creates a new NeutralForwarder instance.
func newNeutralForwarder() *NeutralForwarder {
	return &NeutralForwarder{
		client: newLlmForwardClient(),
	}
}

// Forward implements Forwarder interface.
// Forwards the request to the inference engine and streams response chunks.
func (b *NeutralForwarder) Forward(req *types.RequestContext) (<-chan StreamChunk, error) {
	chunkChan := make(chan StreamChunk, 100)

	instance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeNeutral)
	if instance == nil {
		return nil, fmt.Errorf("no available instance for infer type: %s", consts.InferTypeNeutral)
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
