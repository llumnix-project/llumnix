package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"k8s.io/klog/v2"

	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
)

func init() {
	RegisterBackend(consts.SplitModeVllmKvt, func(scheduleMode types.ScheduleMode) (InferenceBackend, error) {
		return NewPdSplitVllmKvtBackend(scheduleMode)
	})
}

type PdSplitVllmKvtBackend struct {
	client       *http.Client
	scheduleMode types.ScheduleMode
}

func NewPdSplitVllmKvtBackend(schMode types.ScheduleMode) (InferenceBackend, error) {
	if schMode != types.ScheduleModePDBatch && schMode != types.ScheduleModePDStaged {
		return nil, fmt.Errorf("unsupported schedule mode: %s", schMode)
	}
	return &PdSplitVllmKvtBackend{
		client:       NewLlmForwardClient(),
		scheduleMode: schMode,
	}, nil
}

func (b *PdSplitVllmKvtBackend) buildTransferParams(req *types.RequestContext, pInstance, dInstance *types.LLMInstance) map[string]interface{} {
	p := map[string]interface{}{
		"remote_host": pInstance.Endpoint.Host,
		"remote_port": pInstance.AuxPort,
	}
	if b.scheduleMode == types.ScheduleModePDStaged {
		p["do_remote_prefill"] = true
	}
	return p
}

func (b *PdSplitVllmKvtBackend) BatchScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	pInstance := req.ScheduleCtx.ScheduleResults.GetInstanceByRole(types.InferRolePrefill)
	if pInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill instance", req.Id)
	}
	dInstance := req.ScheduleCtx.ScheduleResults.GetInstanceByRole(types.InferRoleDecode)
	if dInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled decode instance", req.Id)
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)
		req.LLMRequest.CompletionRequest.KvTransferParams = b.buildTransferParams(req, pInstance, dInstance)

		body, err := json.Marshal(req.LLMRequest.CompletionRequest)
		if err != nil {
			klog.Errorf("failed to marshal request body: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		// build new backend request and stream read
		StreamResponseFromBackend(req, b.client, body, dInstance, chunkChan)
	}()

	return chunkChan, nil
}

func (b *PdSplitVllmKvtBackend) buildPrefillRequestData(req *types.RequestContext) ([]byte, error) {
	cmplReq := *(req.LLMRequest.CompletionRequest)
	maxTokens := 1
	cmplReq.MaxTokens = &maxTokens
	if cmplReq.KvTransferParams == nil {
		cmplReq.KvTransferParams = make(map[string]interface{})
	}
	cmplReq.KvTransferParams["do_remote_decode"] = true
	return json.Marshal(cmplReq)
}

func (b *PdSplitVllmKvtBackend) doPrefill(req *types.RequestContext, pInstance *types.LLMInstance) error {
	// build prefill request
	data, err := b.buildPrefillRequestData(req)
	if err != nil {
		klog.Errorf("[%s] failed to build prefill request data: %v", err, req.Id)
		return err
	}

	newReq, err := MakeNewBackendRequest(req, data, pInstance)
	if err != nil {
		klog.Errorf("[%s] failed to make new backend request: %v", err, req.Id)
		return err
	}
	body, err := DoRequest(newReq, b.client, data)
	if err != nil {
		klog.Errorf("[%s] failed to do request: %v", req.Id, err)
		return err
	}
	defer body.Close()
	_, err = io.ReadAll(body)
	if err != nil {
		klog.Errorf("[%s] failed to read response: %v", req.Id, err)
		return err
	}
	return nil
}

func (b *PdSplitVllmKvtBackend) buildDecodeRequestData(req *types.RequestContext, instance *types.LLMInstance) ([]byte, error) {
	cmplReq := req.LLMRequest.CompletionRequest
	if cmplReq.KvTransferParams == nil {
		cmplReq.KvTransferParams = make(map[string]interface{})
	}
	// TODO(wingo.zwt): may need to use kvtIP
	cmplReq.KvTransferParams["remote_host"] = instance.Endpoint.Host
	cmplReq.KvTransferParams["remote_port"] = instance.AuxPort
	cmplReq.KvTransferParams["do_remote_prefill"] = true
	return json.Marshal(cmplReq)
}

func (b *PdSplitVllmKvtBackend) doDecode(req *types.RequestContext, chunkChan chan StreamChunk, dInstance *types.LLMInstance) {
	data, err := b.buildDecodeRequestData(req, dInstance)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		chunkChan <- StreamChunk{err: err}
		return
	}
	StreamResponseFromBackend(req, b.client, data, dInstance, chunkChan)
}

func (b *PdSplitVllmKvtBackend) StagedScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	pInstance := req.ScheduleCtx.ScheduleResults.GetInstanceByRole(types.InferRolePrefill)
	if pInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill instance", req.Id)
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)

		err := b.doPrefill(req, pInstance)
		if err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}

		// start decode scheduling
		results, err := req.ScheduleDecode()
		if err != nil {
			klog.Errorf("[%s] decode scheduling failed: %v", req.Id, err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		dInstance := results.GetInstanceByRole(types.InferRoleDecode)
		if dInstance == nil {
			klog.Errorf("[%s] decode instance not found", req.Id)
			chunkChan <- StreamChunk{err: fmt.Errorf("decode instance not found")}
			return
		}

		b.doDecode(req, chunkChan, dInstance)
	}()

	return chunkChan, nil
}

// StreamInference implements InferBackend interface
// Performs streaming inference by forwarding request to backend and streaming response chunks
func (b *PdSplitVllmKvtBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	if b.scheduleMode == types.ScheduleModePDBatch {
		return b.BatchScheduleStreamInference(req)
	} else if b.scheduleMode == types.ScheduleModePDStaged {
		return b.StagedScheduleStreamInference(req)
	} else {
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}
