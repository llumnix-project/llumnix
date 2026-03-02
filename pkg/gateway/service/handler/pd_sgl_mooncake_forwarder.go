package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
	"math/rand"
	"net/http"

	"golang.org/x/sync/errgroup"
	"k8s.io/klog/v2"
)

func init() {
	registerForwarder(consts.ForwarderTypeSglangMooncake, func(schedulingMode types.SchedulingMode) (Forwarder, error) {
		return newPDDisaggSglMoonCakeForwarder(schedulingMode)
	})
}

type PDDisaggSglMoonCakeForwarder struct {
	client       *http.Client
	schedulingMode types.SchedulingMode
}

func newPDDisaggSglMoonCakeForwarder(schMode types.SchedulingMode) (Forwarder, error) {
	if schMode != types.SchedulingModePDBatch {
		return nil, fmt.Errorf("unsupported scheduling mode: %s", schMode)
	}
	return &PDDisaggSglMoonCakeForwarder{
		client:       newLlmForwardClient(),
		schedulingMode: schMode,
	}, nil
}

func (b *PDDisaggSglMoonCakeForwarder) buildRequestData(req *types.RequestContext, pInstance, dInstance *types.LLMInstance) ([]byte, error) {
	dpRank, dpSize := dInstance.DPRank, dInstance.DPSize
	bootstrapRoom := rand.Intn(1<<63 - 1)
	bootstrapRoom = bootstrapRoom/dpSize*dpSize + dpRank

	cmplReq := req.LLMRequest.CompletionRequest
	cmplReq.Rid = req.Id
	cmplReq.BootStrapHost = pInstance.Endpoint.Host
	cmplReq.BootStrapRoom = fmt.Sprintf("%d", bootstrapRoom)
	return json.Marshal(cmplReq)
}

func (b *PDDisaggSglMoonCakeForwarder) requestDecodeResponse(req *types.RequestContext, data []byte, instance *types.LLMInstance) (io.ReadCloser, error) {
	httpReq, err := makeBackendRequest(req, data, instance)
	if err != nil {
		klog.Errorf("[%s] failed to make backend request: %v", err, req.Id)
		return nil, err
	}
	body, err := doRequest(httpReq, b.client, data)
	if err != nil {
		klog.Errorf("[%s] failed to do request: %v", req.Id, err)
		return nil, err
	}
	return body, nil
}

func (b *PDDisaggSglMoonCakeForwarder) parallelRequestAndStream(req *types.RequestContext, body []byte, pInstance, dInstance *types.LLMInstance, chunkChan chan StreamChunk) {
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

	if err := streamRead(req, chunkChan, decodeResp); err != nil {
		chunkChan <- StreamChunk{err: err}
		return
	}
}

func (b *PDDisaggSglMoonCakeForwarder) batchSchedulingForward(req *types.RequestContext) (<-chan StreamChunk, error) {
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

// Forward implements Forwarder interface.
func (b *PDDisaggSglMoonCakeForwarder) Forward(req *types.RequestContext) (<-chan StreamChunk, error) {
	return b.batchSchedulingForward(req)
}
