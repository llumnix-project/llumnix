package handler

import (
	"fmt"
	"io"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
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

	return req.MarshalRequestWithArgs(map[string]interface{}{
		"rid":            req.Id,
		"bootstrap_host": pWorker.Endpoint.Host,
		"bootstrap_room": bootstrapRoom,
	})
}

func (b *PdSplitSglMoonCakeBackend) doRequest(req *types.RequestContext, data []byte, worker *types.LLMWorker) (io.ReadCloser, error) {
	httpReq, err := MakeNewBackendRequest(req, data, worker)
	if err != nil {
		klog.Errorf("[%s] failed to make new backend request: %v", err, req.Id)
		return nil, err
	}
	body, err := DoRequest(httpReq, b.client, data, worker)
	if err != nil {
		klog.Errorf("[%s] failed to do request: %v", req.Id, err)
		return nil, err
	}
	return body, nil
}

// parallelDoRequests sends requests to prefill and decode workers in parallel.
// IMPORTANT: Caller is responsible for closing the returned responses.
func (b *PdSplitSglMoonCakeBackend) parallelDoRequests(req *types.RequestContext, body []byte, pWorker, dWorker *types.LLMWorker) (prefillResp, decodeResp io.ReadCloser, err error) {
	var eg errgroup.Group

	eg.Go(func() error {
		resp, reqErr := b.doRequest(req, body, pWorker)
		if reqErr != nil {
			return reqErr
		}
		prefillResp = resp
		return nil
	})

	eg.Go(func() error {
		resp, reqErr := b.doRequest(req, body, dWorker)
		if reqErr != nil {
			return reqErr
		}
		decodeResp = resp
		return nil
	})

	if err = eg.Wait(); err != nil {
		if prefillResp != nil {
			prefillResp.Close()
		}
		if decodeResp != nil {
			decodeResp.Close()
		}
		return nil, nil, err
	}

	return prefillResp, decodeResp, nil
}

func (b *PdSplitSglMoonCakeBackend) parallelRequestAndStream(req *types.RequestContext, body []byte, pWorker, dWorker *types.LLMWorker, chunkChan chan StreamChunk) {
	prefillResp, decodeResp, err := b.parallelDoRequests(req, body, pWorker, dWorker)
	if err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}
	defer func() {
		prefillResp.Close()
		decodeResp.Close()
	}()

	if err := StreamRead(req, chunkChan, decodeResp); err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}
}

// getPDWorkersAndBuildRequest retrieves prefill/decode workers and builds request data.
func (b *PdSplitSglMoonCakeBackend) getPDWorkersAndBuildRequest(req *types.RequestContext) (pWorker, dWorker *types.LLMWorker, body []byte, err error) {
	pWorker, dWorker, err = GetPDWorkers(req)
	if err != nil {
		return nil, nil, nil, err
	}

	body, err = b.buildRequestData(req, pWorker, dWorker)
	if err != nil {
		klog.Errorf("[%s] failed to build request data: %v", req.Id, err)
		return nil, nil, nil, err
	}
	return pWorker, dWorker, body, nil
}

func (b *PdSplitSglMoonCakeBackend) BatchScheduleStreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	pWorker, dWorker, body, err := b.getPDWorkersAndBuildRequest(req)
	if err != nil {
		return nil, err
	}

	prefillResp, decodeResp, err := b.parallelDoRequests(req, body, pWorker, dWorker)
	if err != nil {
		return nil, err
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer func() {
			close(chunkChan)
			prefillResp.Close()
			decodeResp.Close()
		}()

		if err := StreamRead(req, chunkChan, decodeResp); err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}
	}()

	return chunkChan, nil
}

// StreamInference implements InferBackend interface
// Performs streaming inference by forwarding request to backend and streaming response chunks
func (b *PdSplitSglMoonCakeBackend) StreamInference(req *types.RequestContext) (<-chan StreamChunk, error) {
	switch b.scheduleMode {
	case types.ScheduleModePDBatch:
		return b.BatchScheduleStreamInference(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}

func (b *PdSplitSglMoonCakeBackend) BatchScheduleInference(req *types.RequestContext) ([]byte, error) {
	pWorker, dWorker, body, err := b.getPDWorkersAndBuildRequest(req)
	if err != nil {
		return nil, err
	}
	prefillResp, decodeResp, err := b.parallelDoRequests(req, body, pWorker, dWorker)
	if err != nil {
		return nil, err
	}
	defer func() {
		prefillResp.Close()
		decodeResp.Close()
	}()

	return io.ReadAll(decodeResp)
}

func (b *PdSplitSglMoonCakeBackend) Inference(req *types.RequestContext) ([]byte, error) {
	switch b.scheduleMode {
	case types.ScheduleModePDBatch:
		return b.BatchScheduleInference(req)
	default:
		return nil, fmt.Errorf("[%s] unsupported schedule mode: %s", req.Id, b.scheduleMode)
	}
}
