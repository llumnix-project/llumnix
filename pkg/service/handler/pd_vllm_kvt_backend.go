package handler

import (
	"fmt"
	"io"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
	"net/http"

	"k8s.io/klog/v2"
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

func (b *PdSplitVllmKvtBackend) buildTransferParams(req *types.RequestContext, pWorker, dWorker *types.LLMWorker) map[string]interface{} {
	p := map[string]interface{}{
		"remote_host": pWorker.Endpoint.Host,
		"remote_port": pWorker.AuxPort,
	}
	if b.scheduleMode == types.ScheduleModePDStaged {
		p["do_remote_prefill"] = true
	}
	return p
}

func (b *PdSplitVllmKvtBackend) BatchScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
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
		kvTransferParams := b.buildTransferParams(req, pWorker, dWorker)
		body, err := req.MarshalRequestWithArgs(map[string]interface{}{
			"kv_transfer_params": kvTransferParams,
		})
		if err != nil {
			klog.Errorf("failed to marshal request body: %v", err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		// build new backend request and stream read
		StreamReadFromBackend(req, b.client, body, dWorker, chunkChan)
	}()

	return chunkChan, nil
}

func (b *PdSplitVllmKvtBackend) buildPrefillRequestData(req *types.RequestContext) ([]byte, error) {
	kvTransferParams := map[string]interface{}{"do_remote_decode": true}
	return req.MarshalRequestWithArgs(map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
		"max_tokens":         1,
	})
}

func (b *PdSplitVllmKvtBackend) doPrefill(req *types.RequestContext, pWorker *types.LLMWorker) error {
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
	_, err = io.ReadAll(body)
	if err != nil {
		klog.Errorf("[%s] failed to read response: %v", req.Id, err)
		return err
	}
	return nil
}

func (b *PdSplitVllmKvtBackend) buildDecodeRequestData(req *types.RequestContext, worker *types.LLMWorker) ([]byte, error) {
	// TODO(wingo.zwt): may need to use kvtIP
	kvTransferParams := map[string]interface{}{
		"remote_host":       worker.Endpoint.Host,
		"remote_port":       worker.AuxPort,
		"do_remote_prefill": true,
	}
	return req.MarshalRequestWithArgs(map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
	})
}

func (b *PdSplitVllmKvtBackend) doDecode(req *types.RequestContext, chunkChan chan StreamChunk, dWorker *types.LLMWorker) {
	data, err := b.buildDecodeRequestData(req, dWorker)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		chunkChan <- StreamChunk{err: err}
		return
	}
	StreamReadFromBackend(req, b.client, data, dWorker, chunkChan)
}

func (b *PdSplitVllmKvtBackend) StagedScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
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
func (b *PdSplitVllmKvtBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	switch b.scheduleMode {
	case types.ScheduleModePDBatch:
		return b.BatchScheduleStreamInference(req)
	case types.ScheduleModePDStaged:
		return b.StagedScheduleStreamInference(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}

func (b *PdSplitVllmKvtBackend) BatchScheduleInference(req *types.RequestContext) ([]byte, error) {
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}
	dWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleDecode)
	if dWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled decode worker", req.Id)
	}

	kvTransferParams := b.buildTransferParams(req, pWorker, dWorker)
	body, err := req.MarshalRequestWithArgs(map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
	})
	if err != nil {
		klog.Errorf("failed to marshal request body: %v", err)
		return nil, err
	}

	return ReadFromBackend(req, b.client, body, dWorker)
}

func (b *PdSplitVllmKvtBackend) StagedScheduleInference(req *types.RequestContext) ([]byte, error) {
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

	data, err := b.buildDecodeRequestData(req, dWorker)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		return nil, err
	}
	return ReadFromBackend(req, b.client, data, dWorker)
}

func (b *PdSplitVllmKvtBackend) Inference(req *types.RequestContext) ([]byte, error) {
	switch b.scheduleMode {
	case types.ScheduleModePDBatch:
		return b.BatchScheduleInference(req)
	case types.ScheduleModePDStaged:
		return b.StagedScheduleInference(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}
