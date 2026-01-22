package balancer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/keepalive"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/types"
)

const (
	scheduleRequestTimeout = 2 * time.Second
	releaseTryCount        = 3
)

type SchedulerClient struct {
	config *options.Config

	schClient *http.Client

	kac *keepalive.KeepAliveClient
}

func NewSchedulerClient(config *options.Config) *SchedulerClient {
	// create scheduler resolver
	if len(config.LlmScheduler) == 0 {
		klog.Error("llm scheduler service config is empty.")
		return nil
	}

	schResolver := resolver.CreateSchedulerResolver(config)
	kac := keepalive.NewKeepAliveClient(config.LlmScheduler, config.ServiceToken, schResolver)

	cb := &SchedulerClient{
		config:    config,
		schClient: &http.Client{Timeout: scheduleRequestTimeout},
		kac:       kac,
	}

	kac.StartAndAwaitReady()

	klog.Infof("create llm scheduler balancer(%s) successfully.", config.LlmScheduler)
	return cb
}

func (cb *SchedulerClient) createScheduleRequest(req *types.RequestContext) *types.ScheduleRequest {
	localEndpoint := cb.kac.GetLocalEndpoint()
	// create scheduler request
	schRequest := &types.ScheduleRequest{
		Id:           req.Id,
		Model:        req.LLMRequest.Model,
		GatewayId:    localEndpoint.String(),
		ScheduleMode: req.ScheduleCtx.ScheduleMode,
		InferStage:   req.ScheduleCtx.InferStage,
	}

	if tokenIds, ok := req.LLMRequest.GetPromptTokens(); ok {
		if cb.config.LlumnixConfig.EnableFullModeScheduling &&
			(cb.config.LlumnixConfig.EnableCacheAwareScheduling || cb.config.LlumnixConfig.EnableInstanceStatusLocalAccount) {
			schRequest.PromptTokenIds = tokenIds
		} else {
			schRequest.PromptNumTokens = len(tokenIds)
		}
	} else {
		klog.Warningf("schedule request %s prompt string and token ids are empty or not support: %v", req.Id, req.LLMRequest.CompletionRequest.Prompt)
	}
	// record the borrow gateway
	req.ScheduleCtx.GatewayId = localEndpoint.String()
	return schRequest
}

func (cb *SchedulerClient) handleResponse(body []byte) (types.ScheduledResult, error) {
	var schReq types.ScheduleRequest
	err := json.Unmarshal(body, &schReq)
	if err != nil {
		err = consts.ErrorEndpointNotFound
		klog.Warningf("do schedule: content format error: %s", string(body))
		return nil, err
	}
	if len(schReq.ScheduleResult) == 0 {
		klog.Warningf("get next endpoint: no available endpoint")
		return nil, consts.ErrorNoAvailableEndpoint
	}

	klog.V(3).Infof("schedule result: %v", schReq.ScheduleResult.String())
	return schReq.ScheduleResult, nil
}

func (cb *SchedulerClient) doSchedule(req *types.RequestContext) (types.ScheduledResult, error) {
	// check scheduler ready or not
	if !cb.kac.IsRemoteReady() {
		return nil, consts.ErrorSchedulerNotReady
	}

	// create token request to scheduler
	schReq := cb.createScheduleRequest(req)
	data, err := json.Marshal(&schReq)
	if err != nil {
		klog.Errorf("failed to marshal schedule request: %v", err)
		return nil, err
	}

	// send request to scheduler
	url := fmt.Sprintf("http://%s/schedule", cb.kac.GetRemoteEndpoint().String())
	httpReq, _ := http.NewRequest("POST", url, bytes.NewBuffer(data))
	if len(cb.config.ServiceToken) > 0 {
		httpReq.Header.Add("Authorization", cb.config.ServiceToken)
	}
	resp, err := cb.schClient.Do(httpReq)
	if err != nil {
		klog.Warningf("[%s] do request schedule fail: %v\n", req.Id, err)
		return nil, consts.ErrorMayNetworkBroken
	}

	defer resp.Body.Close()

	var body []byte
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		klog.Warningf("[%s] do request schedule: could not read body: %v", req.Id, err)
		return nil, consts.ErrorMayNetworkBroken
	}

	switch resp.StatusCode {
	case http.StatusOK:
		return cb.handleResponse(body)
	case http.StatusTooManyRequests:
		return nil, consts.ErrorNoAvailableEndpoint
	case http.StatusNotFound:
		return nil, consts.ErrorEndpointNotFound
	default:
		klog.Warningf("[%s] do request schedule: %d, %s", req.Id, resp.StatusCode, string(body))
		return nil, consts.ErrorEndpointNotFound
	}
}

func getServiceNameFromPodName(name string) string {
	parts := strings.Split(name, "-")
	if len(parts) <= 2 {
		return strings.ReplaceAll(name, "-", "_")
	}
	serviceParts := parts[:len(parts)-2]
	return strings.Join(serviceParts, "_")
}

func (cb *SchedulerClient) Get(req *types.RequestContext) (types.ScheduledResult, error) {
	tStart := time.Now()

	if !cb.kac.IsRemoteReady() {
		return nil, consts.ErrorSchedulerNotReady
	}

	for {
		result, err := cb.doSchedule(req)
		if err == nil {
			return result, nil
		}

		// don't return errors directly, as the scheduler may not have
		// fully loaded all backend service instances right after a restart.
		// if err == consts.ErrorEndpointNotFound {
		// 	return nil, err
		// }

		// all service endpoints are busy, wait a period for next try
		if errors.Is(err, consts.ErrorNoAvailableEndpoint) {
			if time.Since(tStart).Milliseconds() > int64(cb.config.WaitScheduleTimeout) {
				return nil, err
			} else {
				klog.Infof("[%s] all service endpoints are busy, try next after %dms", req.Id, cb.config.WaitScheduleTryPeriod)
				time.Sleep(time.Duration(cb.config.WaitScheduleTryPeriod) * time.Millisecond)
				continue
			}
		}

		// scheduler is not ready
		klog.Warningf("[%s] service backend endpoint get failed, error: %v", req.Id, err)
		return nil, consts.ErrorSchedulerNotReady
	}
}

func (cb *SchedulerClient) createReleaseRequest(req *types.RequestContext, worker *types.LLMWorker) *types.ScheduleRequest {
	localEndpoint := cb.kac.GetLocalEndpoint()
	return &types.ScheduleRequest{
		Id:             req.Id,
		Model:          req.LLMRequest.Model,
		GatewayId:      localEndpoint.String(),
		ScheduleMode:   req.ScheduleCtx.ScheduleMode,
		InferStage:     req.ScheduleCtx.InferStage,
		ScheduleResult: types.ScheduledResult{*worker},
	}
}

func (cb *SchedulerClient) Release(req *types.RequestContext, worker *types.LLMWorker) {
	if req == nil || worker == nil {
		return
	}
	// 1. The current gateway differs from the one at borrowing time, indicating the resources have been updated and no release is needed.
	// 2. If the scheduler is in a not-ready state, no release is required either, as subsequent new schedulers or connections will refresh these resources.
	localEndpoint := cb.kac.GetLocalEndpoint().String()
	ready := cb.kac.IsRemoteReady()
	if localEndpoint != req.ScheduleCtx.GatewayId || !ready {
		return
	}

	go func() {
		releaseRequest := cb.createReleaseRequest(req, worker)
		klog.V(3).Infof("release worker resource: %v", releaseRequest.String())
		data, err := json.Marshal(releaseRequest)
		if err != nil {
			klog.Errorf("marshal release request error: %v", err)
			return
		}

		endpoint := cb.kac.GetRemoteEndpoint()
		url := fmt.Sprintf("http://%s/release", endpoint.String())
		httpReq, _ := http.NewRequest("POST", url, bytes.NewBuffer(data))
		if len(cb.config.ServiceToken) > 0 {
			httpReq.Header.Add("Authorization", cb.config.ServiceToken)
		}
		retry := releaseTryCount
	RETRY:
		resp, err := cb.schClient.Do(httpReq)
		retry--
		if err != nil {
			// retry when some network error occurs
			if retry > 0 {
				time.Sleep(500 * time.Millisecond)
				goto RETRY
			}
			klog.Warningf("release worker for request %s fail: %v\n", req.Id, err)
			return
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			klog.Warningf("release worker for request %s: could not read body: %v", req.Id, err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			klog.Errorf("release worker for request %s: error: %d, %s", req.Id, resp.StatusCode, string(body))
			return
		}
	}()
}
