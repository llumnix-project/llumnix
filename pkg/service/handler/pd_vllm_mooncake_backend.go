package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
	"net/http"

	"k8s.io/klog/v2"
)

func init() {
	RegisterBackend(consts.SplitModeVllmMooncake, func(scheduleMode types.ScheduleMode) (InferenceBackend, error) {
		return NewPdSplitVllmMoonCakeBackend(scheduleMode)
	})
}

type PdSplitVllmMoonCakeBackend struct {
	client       *http.Client
	scheduleMode types.ScheduleMode
}

func NewPdSplitVllmMoonCakeBackend(schMode types.ScheduleMode) (InferenceBackend, error) {
	if schMode != types.ScheduleModePDBatch && schMode != types.ScheduleModePDStaged {
		return nil, fmt.Errorf("unsupported schedule mode: %s", schMode)
	}
	return &PdSplitVllmMoonCakeBackend{
		client:       NewLlmForwardClient(),
		scheduleMode: schMode,
	}, nil
}

func (b *PdSplitVllmMoonCakeBackend) buildPrefillRequestData(req *types.RequestContext) ([]byte, error) {
	// Set kv_transfer_params
	kvTransferParams := map[string]interface{}{
		"do_remote_decode":  true,
		"do_remote_prefill": false,
		"remote_engine_id":  nil,
		"remote_block_ids":  nil,
		"remote_host":       nil,
		"remote_port":       nil,
	}
	return req.MarshalRequestWithArgs(map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
		"max_tokens":         1,
		"stream":             false,
	})
}

func (b *PdSplitVllmMoonCakeBackend) doPrefill(req *types.RequestContext, pWorker *types.LLMWorker) error {
	// build prefill request
	data, err := b.buildPrefillRequestData(req)
	if err != nil {
		klog.Errorf("[%s] failed to build prefill request data: %v", err, req.Id)
		return err
	}

	newReq, err := MakeNewBackendRequest(req, data, pWorker)
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

	data, err = io.ReadAll(body)
	if err != nil {
		klog.Errorf("[%s] failed to read response: %v", req.Id, err)
		return err
	}

	type tmpResponse struct {
		KvTransferParams map[string]interface{} `json:"kv_transfer_params,omitempty"`
	}
	var tResp tmpResponse
	if err := json.Unmarshal(data, &tResp); err != nil {
		klog.Errorf("[%s] failed to unmarshal completion response: %v", req.Id, err)
		return err
	}
	req.SetKvTransferParams(tResp.KvTransferParams)

	return nil
}

func (b *PdSplitVllmMoonCakeBackend) BatchScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}

	dWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleDecode)
	if dWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled decode worker", req.Id)
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)

		err := b.doPrefill(req, pWorker)
		if err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}
		data, err := req.MarshalRequestWithArgs(nil)
		if err != nil {
			klog.Errorf("[%s] failed to marshal completion request: %v", req.Id, err)
			chunkChan <- StreamChunk{err: err}
			return
		}
		StreamReadFromBackend(req, b.client, data, dWorker, chunkChan)
	}()

	return chunkChan, nil
}

func (b *PdSplitVllmMoonCakeBackend) doDecode(req *types.RequestContext, chunkChan chan StreamChunk, dWorker *types.LLMWorker) {
	data, err := req.MarshalRequestWithArgs(nil)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		chunkChan <- StreamChunk{err: err}
		return
	}
	StreamReadFromBackend(req, b.client, data, dWorker, chunkChan)
}

func (b *PdSplitVllmMoonCakeBackend) StagedScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}
	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)
		err := b.doPrefill(req, pWorker)
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
		dWorker := results.GetWorkerByRole(types.InferRoleDecode)
		if dWorker == nil {
			klog.Errorf("[%s] decode worker not found", req.Id)
			chunkChan <- StreamChunk{err: fmt.Errorf("decode worker not found")}
			return
		}
		b.doDecode(req, chunkChan, dWorker)
	}()
	return chunkChan, nil
}

// StreamInference implements InferBackend interface
// Performs streaming inference by forwarding request to backend and streaming response chunks
func (b *PdSplitVllmMoonCakeBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	switch b.scheduleMode {
	case types.ScheduleModePDBatch:
		return b.BatchScheduleStreamInference(req)
	case types.ScheduleModePDStaged:
		return b.StagedScheduleStreamInference(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}

func (b *PdSplitVllmMoonCakeBackend) BatchScheduleInference(req *types.RequestContext) ([]byte, error) {
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}

	dWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleDecode)
	if dWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled decode worker", req.Id)
	}

	err := b.doPrefill(req, pWorker)
	if err != nil {
		return nil, err
	}
	data, err := req.MarshalRequestWithArgs(nil)
	if err != nil {
		klog.Errorf("[%s] failed to marshal completion request: %v", req.Id, err)
		return nil, err
	}
	return ReadFromBackend(req, b.client, data, dWorker)
}

func (b *PdSplitVllmMoonCakeBackend) StagedScheduleInference(req *types.RequestContext) ([]byte, error) {
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}
	err := b.doPrefill(req, pWorker)
	if err != nil {
		return nil, err
	}
	// start decode scheduling
	results, err := req.ScheduleDecode()
	if err != nil {
		klog.Errorf("[%s] decode scheduling failed: %v", req.Id, err)
		return nil, err
	}
	dWorker := results.GetWorkerByRole(types.InferRoleDecode)
	if dWorker == nil {
		klog.Errorf("[%s] decode worker not found", req.Id)
		return nil, fmt.Errorf("decode worker not found")
	}
	data, err := req.MarshalRequestWithArgs(nil)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		return nil, err
	}
	return ReadFromBackend(req, b.client, data, dWorker)
}

func (b *PdSplitVllmMoonCakeBackend) Inference(req *types.RequestContext) ([]byte, error) {
	switch b.scheduleMode {
	case types.ScheduleModePDBatch:
		return b.BatchScheduleInference(req)
	case types.ScheduleModePDStaged:
		return b.StagedScheduleInference(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}
