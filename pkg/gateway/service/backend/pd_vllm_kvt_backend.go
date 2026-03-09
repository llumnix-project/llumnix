package backend

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
	pWorker, dWorker, err := GetPDWorkers(req)
	if err != nil {
		return nil, err
	}

	kvTransferParams := b.buildTransferParams(req, pWorker, dWorker)
	args := map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
	}
	if dWorker.Model != "" {
		args["model"] = dWorker.Model
	}
	body, err := req.MarshalRequestWithArgs(args)
	if err != nil {
		klog.Errorf("failed to marshal request body: %v", err)
		return nil, err
	}

	// Build backend request
	newReq, err := MakeNewBackendRequest(req, body, dWorker)
	if err != nil {
		klog.Errorf("failed to create new backend request: %v", err)
		return nil, err
	}

	// Execute request with retry
	respBody, err := DoRequest(newReq, b.client, body, dWorker)
	if err != nil {
		klog.Errorf("failed to do backend request: %v", err)
		return nil, err
	}

	return StartStreamRead(req, respBody), nil
}

func (b *PdSplitVllmKvtBackend) buildPrefillRequestData(req *types.RequestContext, pWorker *types.LLMWorker) ([]byte, error) {
	kvTransferParams := map[string]interface{}{"do_remote_decode": true}
	args := map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
		"max_tokens":         1,
	}
	if pWorker.Model != "" {
		args["model"] = pWorker.Model
	}
	return req.MarshalRequestWithArgs(args)
}

func (b *PdSplitVllmKvtBackend) doPrefill(req *types.RequestContext, pWorker *types.LLMWorker) error {
	// build prefill request
	data, err := b.buildPrefillRequestData(req, pWorker)
	if err != nil {
		klog.Errorf("[%s] failed to build prefill request data: %v", err, req.Id)
		return err
	}

	newReq, err := MakeNewBackendRequest(req, data, pWorker)
	if err != nil {
		klog.Errorf("[%s] failed to make new backend request: %v", err, req.Id)
		return err
	}
	body, err := DoRequest(newReq, b.client, data, pWorker)
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
	args := map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
	}
	if worker.Model != "" {
		args["model"] = worker.Model
	}
	return req.MarshalRequestWithArgs(args)
}

func (b *PdSplitVllmKvtBackend) StagedScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill worker", req.Id)
	}

	err := b.doPrefill(req, pWorker)
	if err != nil {
		return nil, err
	}

	// start decode scheduling
	dWorker, err := ScheduleAndGetDecodeWorker(req)
	if err != nil {
		return nil, err
	}

	body, err := b.buildDecodeRequestData(req, dWorker)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", err, req.Id)
		return nil, err
	}

	// Build backend request
	newReq, err := MakeNewBackendRequest(req, body, dWorker)
	if err != nil {
		klog.Errorf("failed to create new backend request: %v", err)
		return nil, err
	}

	// Execute request with retry
	respBody, err := DoRequest(newReq, b.client, body, dWorker)
	if err != nil {
		klog.Errorf("failed to do backend request: %v", err)
		return nil, err
	}

	return StartStreamRead(req, respBody), nil
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
	pWorker, dWorker, err := GetPDWorkers(req)
	if err != nil {
		return nil, err
	}

	kvTransferParams := b.buildTransferParams(req, pWorker, dWorker)
	args := map[string]interface{}{
		"kv_transfer_params": kvTransferParams,
	}
	if dWorker.Model != "" {
		args["model"] = dWorker.Model
	}
	body, err := req.MarshalRequestWithArgs(args)
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
	dWorker, err := ScheduleAndGetDecodeWorker(req)
	if err != nil {
		return nil, err
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

func (b *PdSplitVllmKvtBackend) Name() string {
	return consts.SplitModeVllmKvt
}
