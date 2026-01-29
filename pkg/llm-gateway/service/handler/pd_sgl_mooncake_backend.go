package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
	"math/rand"
	"net/http"

	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"
)

func init() {
	RegisterBackend(consts.SplitModeSGlangMooncake, func(scheduleMode types.ScheduleMode) (InferenceBackend, error) {
		return NewPdSplitSglMoonCakeBackend(scheduleMode)
	})
}

type PdSplitSglMoonCakeBackend struct {
	client       *http.Client
	scheduleMode types.ScheduleMode
}

func NewPdSplitSglMoonCakeBackend(schMode types.ScheduleMode) (InferenceBackend, error) {
	if schMode != types.ScheduleModePDBatch {
		return nil, fmt.Errorf("unsupported schedule mode: %s", schMode)
	}
	return &PdSplitSglMoonCakeBackend{
		client:       NewLlmForwardClient(),
		scheduleMode: schMode,
	}, nil
}

func (b *PdSplitSglMoonCakeBackend) buildRequestData(req *types.RequestContext, pInstance, dInstance *types.LLMInstance) ([]byte, error) {
	dpRank, dpSize := dInstance.DPRank, dInstance.DPSize
	bootstrapRoom := rand.Intn(1<<63 - 1)
	bootstrapRoom = bootstrapRoom/dpSize*dpSize + dpRank

	cmplReq := req.LLMRequest.CompletionRequest
	cmplReq.Rid = req.Id
	cmplReq.BootStrapHost = pInstance.Endpoint.Host
	cmplReq.BootStrapRoom = fmt.Sprintf("%d", bootstrapRoom)
	return json.Marshal(cmplReq)
}

func (b *PdSplitSglMoonCakeBackend) requestDecodeResponse(req *types.RequestContext, data []byte, instance *types.LLMInstance) (io.ReadCloser, error) {
	httpReq, err := MakeNewBackendRequest(req, data, instance)
	if err != nil {
		klog.Errorf("[%s] failed to make new backend request: %v", err, req.Id)
		return nil, err
	}
	body, err := DoRequest(httpReq, b.client, data)
	if err != nil {
		klog.Errorf("[%s] failed to do request: %v", req.Id, err)
		return nil, err
	}
	return body, nil
}

func (b *PdSplitSglMoonCakeBackend) parallelRequestAndStream(req *types.RequestContext, body []byte, pInstance, dInstance *types.LLMInstance, chunkChan chan StreamChunk) {
	var (
		decodeResp  io.ReadCloser
		prefillResp io.ReadCloser
		eg          errgroup.Group
	)

	eg.Go(func() error {
		body, err := b.requestDecodeResponse(req, body, pInstance)
		if err != nil {
			return err
		}
		prefillResp = body
		return nil
	})

	eg.Go(func() error {
		body, err := b.requestDecodeResponse(req, body, dInstance)
		if err != nil {
			return err
		}
		decodeResp = body
		return nil
	})

	if err := eg.Wait(); err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}

	defer func() {
		if decodeResp != nil {
			decodeResp.Close()
		}
		if prefillResp != nil {
			prefillResp.Close()
		}
	}()

	if err := StreamRead(req, chunkChan, decodeResp); err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}
}

func (b *PdSplitSglMoonCakeBackend) BatchScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
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
		body, err := b.buildRequestData(req, pInstance, dInstance)
		if err != nil {
			klog.Errorf("[%s] failed to build request data: %v", req.Id, err)
			chunkChan <- StreamChunk{err: err}
			return
		}
		b.parallelRequestAndStream(req, body, pInstance, dInstance, chunkChan)
	}()

	return chunkChan, nil
}

// StreamInference implements InferBackend interface
// Performs streaming inference by forwarding request to backend and streaming response chunks
func (b *PdSplitSglMoonCakeBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	return b.BatchScheduleStreamInference(req)
}
