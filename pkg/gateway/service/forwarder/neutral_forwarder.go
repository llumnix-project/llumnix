package forwarder

import (
	"encoding/json"
	"fmt"
	"net/http"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func init() {
	registerForwarder(consts.ForwarderTypeNeutral, func(schedulingMode types.SchedulingMode) (Forwarder, error) {
		return newNeutralForwarder(), nil
	})
}

type NeutralForwarder struct {
	client *http.Client
}

func newNeutralForwarder() *NeutralForwarder {
	return &NeutralForwarder{
		client: newLlmForwardClient(),
	}
}

// Forward implements Forwarder interface.
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
			chunkChan <- StreamChunk{Err: err}
			return
		}

		newReq, err := makeBackendRequest(req, body, instance)
		if err != nil {
			klog.Errorf("failed to create backend request: %v", err)
			chunkChan <- StreamChunk{Err: err}
			return
		}

		respBody, err := doRequest(newReq, b.client, body)
		if err != nil {
			klog.Errorf("failed to do backend request: %v", err)
			chunkChan <- StreamChunk{Err: err}
			return
		}
		defer respBody.Close()

		if err := streamRead(req, chunkChan, respBody); err != nil {
			chunkChan <- StreamChunk{Err: err}
		}
	}()

	return chunkChan, nil
}
