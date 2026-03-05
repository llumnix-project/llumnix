package forwarder

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func init() {
	registerForwarder(consts.ForwarderTypeVllmKvt, func(schedulingMode types.SchedulingMode) (Forwarder, error) {
		return newPDDisaggVllmKvtForwarder(schedulingMode)
	})
}

type PDDisaggVllmKvtForwarder struct {
	client         *http.Client
	schedulingMode types.SchedulingMode
}

func newPDDisaggVllmKvtForwarder(schMode types.SchedulingMode) (Forwarder, error) {
	if schMode != types.SchedulingModePDBatch && schMode != types.SchedulingModePDStaged {
		return nil, fmt.Errorf("unsupported scheduling mode: %s", schMode)
	}
	return &PDDisaggVllmKvtForwarder{
		client:         newLlmForwardClient(),
		schedulingMode: schMode,
	}, nil
}

func (b *PDDisaggVllmKvtForwarder) buildTransferParams(pInstance *types.LLMInstance) map[string]interface{} {
	p := map[string]interface{}{
		"remote_host": pInstance.AuxIp,
		"remote_port": pInstance.AuxPort,
	}
	if b.schedulingMode == types.SchedulingModePDStaged {
		p["do_remote_prefill"] = true
	}
	return p
}

func (b *PDDisaggVllmKvtForwarder) batchSchedulingForward(req *types.RequestContext) (<-chan StreamChunk, error) {
	pInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypePrefill)
	if pInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill instance", req.Id)
	}
	dInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeDecode)
	if dInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled decode instance", req.Id)
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)
		req.LLMRequest.CompletionRequest.KvTransferParams = b.buildTransferParams(pInstance)

		body, err := json.Marshal(req.LLMRequest.CompletionRequest)
		if err != nil {
			klog.Errorf("failed to marshal request body: %v", err)
			chunkChan <- StreamChunk{Err: err}
			return
		}

		streamBackendResponse(req, b.client, body, dInstance, chunkChan)
	}()

	return chunkChan, nil
}

func (b *PDDisaggVllmKvtForwarder) buildPrefillRequestData(req *types.RequestContext) ([]byte, error) {
	cmplReq := *(req.LLMRequest.CompletionRequest)
	maxTokens := uint64(1)
	cmplReq.MaxTokens = &maxTokens
	if cmplReq.KvTransferParams == nil {
		cmplReq.KvTransferParams = make(map[string]interface{})
	}
	cmplReq.KvTransferParams["do_remote_decode"] = true
	return json.Marshal(cmplReq)
}

func (b *PDDisaggVllmKvtForwarder) doPrefill(req *types.RequestContext, pInstance *types.LLMInstance) error {
	data, err := b.buildPrefillRequestData(req)
	if err != nil {
		klog.Errorf("[%s] failed to build prefill request data: %v", err, req.Id)
		return err
	}

	newReq, err := makeBackendRequest(req, data, pInstance)
	if err != nil {
		klog.Errorf("[%s] failed to make backend request: %v", req.Id, err)
		return err
	}
	body, err := doRequest(newReq, b.client, data)
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

func (b *PDDisaggVllmKvtForwarder) buildDecodeRequestData(req *types.RequestContext, instance *types.LLMInstance) ([]byte, error) {
	cmplReq := req.LLMRequest.CompletionRequest
	if cmplReq.KvTransferParams == nil {
		cmplReq.KvTransferParams = make(map[string]interface{})
	}
	cmplReq.KvTransferParams["remote_host"] = instance.AuxIp
	cmplReq.KvTransferParams["remote_port"] = instance.AuxPort
	cmplReq.KvTransferParams["do_remote_prefill"] = true
	return json.Marshal(cmplReq)
}

func (b *PDDisaggVllmKvtForwarder) doDecode(
	req *types.RequestContext,
	chunkChan chan StreamChunk,
	pInstance *types.LLMInstance,
	dInstance *types.LLMInstance,
) {
	data, err := b.buildDecodeRequestData(req, pInstance)
	if err != nil {
		klog.Errorf("[%s] failed to build decode request data: %v", req.Id, err)
		chunkChan <- StreamChunk{Err: err}
		return
	}
	streamBackendResponse(req, b.client, data, dInstance, chunkChan)
}

func (b *PDDisaggVllmKvtForwarder) stagedSchedulingForward(req *types.RequestContext) (<-chan StreamChunk, error) {
	return stagedScheduleForward(req,
		b.doPrefill,
		func(req *types.RequestContext, chunkChan chan StreamChunk, pInstance, dInstance *types.LLMInstance) {
			b.doDecode(req, chunkChan, pInstance, dInstance)
		},
	)
}

// Forward implements Forwarder interface.
func (b *PDDisaggVllmKvtForwarder) Forward(req *types.RequestContext) (<-chan StreamChunk, error) {
	switch b.schedulingMode {
	case types.SchedulingModePDBatch:
		return b.batchSchedulingForward(req)
	case types.SchedulingModePDStaged:
		return b.stagedSchedulingForward(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported scheduling mode: %s", req.Id, b.schedulingMode)
	}
}
