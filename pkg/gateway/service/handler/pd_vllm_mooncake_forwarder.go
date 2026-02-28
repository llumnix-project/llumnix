package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"llumnix/pkg/consts"
	"llumnix/pkg/gateway/protocol"
	"llumnix/pkg/types"
	"net/http"

	"k8s.io/klog/v2"
)

func init() {
	registerForwarder(consts.ForwarderTypeVllmMooncake, func(schedulingMode types.SchedulingMode) (Forwarder, error) {
		return newPDDisaggVllmMoonCakeForwarder(schedulingMode)
	})
}

type PDDisaggVllmMoonCakeForwarder struct {
	client       *http.Client
	schedulingMode types.SchedulingMode
}

func newPDDisaggVllmMoonCakeForwarder(schMode types.SchedulingMode) (Forwarder, error) {
	if schMode != types.SchedulingModePDBatch && schMode != types.SchedulingModePDStaged {
		return nil, fmt.Errorf("unsupported scheduling mode: %s", schMode)
	}
	return &PDDisaggVllmMoonCakeForwarder{
		client:       newLlmForwardClient(),
		schedulingMode: schMode,
	}, nil
}

func (b *PDDisaggVllmMoonCakeForwarder) buildPrefillRequestData(req *types.RequestContext) ([]byte, error) {
	cmplReq := *(req.LLMRequest.CompletionRequest)
	maxTokens := uint64(1)
	cmplReq.MaxTokens = &maxTokens
	cmplReq.Stream = false
	cmplReq.StreamOptions = nil

	// Set kv_transfer_params
	kvTransferParams := map[string]interface{}{
		"do_remote_decode":  true,
		"do_remote_prefill": false,
		"remote_engine_id":  nil,
		"remote_block_ids":  nil,
		"remote_host":       nil,
		"remote_port":       nil,
	}
	cmplReq.KvTransferParams = kvTransferParams
	return json.Marshal(cmplReq)
}

func (b *PDDisaggVllmMoonCakeForwarder) doPrefill(req *types.RequestContext, pInstance *types.LLMInstance) error {
	// build prefill request
	data, err := b.buildPrefillRequestData(req)
	if err != nil {
		klog.Errorf("[%s] failed to build prefill request data: %v", err, req.Id)
		return err
	}

	newReq, err := makeBackendRequest(req, data, pInstance)
	if err != nil {
		klog.Errorf("[%s] failed to make backend request: %v", err, req.Id)
		return err
	}
	body, err := doRequest(newReq, b.client, data)
	if err != nil {
		klog.Errorf("[%s] failed to do request: %v", req.Id, err)
		return err
	}
	defer body.Close()

	data, err = io.ReadAll(body)
	if err != nil {
		klog.Errorf("[%s] failed to read response: %v", req.Id, err)
		return err
	}

	var completionResponse protocol.CompletionResponse
	if err := json.Unmarshal(data, &completionResponse); err != nil {
		klog.Errorf("[%s] failed to unmarshal completion response: %v", req.Id, err)
		return err
	}
	req.LLMRequest.CompletionRequest.KvTransferParams = completionResponse.KvTransferParams

	return nil
}

func (b *PDDisaggVllmMoonCakeForwarder) doDecode(req *types.RequestContext, chunkChan chan StreamChunk, dInstance *types.LLMInstance) {
	data, err := json.Marshal(req.LLMRequest.CompletionRequest)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		chunkChan <- StreamChunk{err: err}
		return
	}
	streamBackendResponse(req, b.client, data, dInstance, chunkChan)
}

func (b *PDDisaggVllmMoonCakeForwarder) batchSchedulingForward(req *types.RequestContext) (<-chan StreamChunk, error) {
	pInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByRole(types.InferRolePrefill)
	if pInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill instance", req.Id)
	}

	dInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByRole(types.InferRoleDecode)
	if dInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled decode instance", req.Id)
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)

		err := b.doPrefill(req, pInstance)
		if err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}
		cmplReq := req.LLMRequest.CompletionRequest
		data, err := json.Marshal(cmplReq)
		if err != nil {
			klog.Errorf("[%s] failed to marshal completion request: %v", req.Id, err)
			chunkChan <- StreamChunk{err: err}
			return
		}
		streamBackendResponse(req, b.client, data, dInstance, chunkChan)
	}()

	return chunkChan, nil
}

func (b *PDDisaggVllmMoonCakeForwarder) stagedSchedulingForward(req *types.RequestContext) (<-chan StreamChunk, error) {
	return stagedScheduleForward(req,
		b.doPrefill,
		func(req *types.RequestContext, chunkChan chan StreamChunk, _, dInstance *types.LLMInstance) {
			b.doDecode(req, chunkChan, dInstance)
		},
	)
}

// Forward implements Forwarder interface.
func (b *PDDisaggVllmMoonCakeForwarder) Forward(req *types.RequestContext) (<-chan StreamChunk, error) {
	switch b.schedulingMode {
	case types.SchedulingModePDBatch:
		return b.batchSchedulingForward(req)
	case types.SchedulingModePDStaged:
		return b.stagedSchedulingForward(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported scheduling mode: %s", req.Id, b.schedulingMode)
	}
}
