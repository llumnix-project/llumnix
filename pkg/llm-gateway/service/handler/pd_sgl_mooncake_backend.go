package handler

import (
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/types"
	"encoding/json"
	"fmt"
	"io"
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

func (b *PdSplitSglMoonCakeBackend) buildRequestData(req *types.RequestContext, pWorker, dWorker *types.LLMWorker) ([]byte, error) {
	dpRank, dpSize := dWorker.DPRank, dWorker.DPSize
	bootstrapRoom := rand.Intn(1<<63 - 1)
	bootstrapRoom = bootstrapRoom/dpSize*dpSize + dpRank

	cmplReq := req.LLMRequest.CompletionRequest
	cmplReq.Rid = req.Id
	cmplReq.BootStrapHost = pWorker.Endpoint.Host
	cmplReq.BootStrapRoom = fmt.Sprintf("%d", bootstrapRoom)
	return json.Marshal(cmplReq)
}

func (b *PdSplitSglMoonCakeBackend) requestDecodeResponse(req *types.RequestContext, data []byte, worker *types.LLMWorker) (io.ReadCloser, error) {
	httpReq, err := MakeNewBackendRequest(req, data, worker)
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

func (b *PdSplitSglMoonCakeBackend) parallelRequestAndStream(req *types.RequestContext, body []byte, pWorker, dWorker *types.LLMWorker, chunkChan chan StreamChunk) {
	var (
		decodeResp  io.ReadCloser
		prefillResp io.ReadCloser
		eg          errgroup.Group
	)

	eg.Go(func() error {
		body, err := b.requestDecodeResponse(req, body, pWorker)
		if err != nil {
			return err
		}
		prefillResp = body
		return nil
	})

	eg.Go(func() error {
		body, err := b.requestDecodeResponse(req, body, dWorker)
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
		body, err := b.buildRequestData(req, pWorker, dWorker)
		if err != nil {
			klog.Errorf("[%s] failed to build request data: %v", req.Id, err)
			chunkChan <- StreamChunk{err: err}
			return
		}
		b.parallelRequestAndStream(req, body, pWorker, dWorker, chunkChan)
	}()

	return chunkChan, nil
}

// StreamInference implements InferBackend interface
// Performs streaming inference by forwarding request to backend and streaming response chunks
func (b *PdSplitSglMoonCakeBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	return b.BatchScheduleStreamInference(req)
}
