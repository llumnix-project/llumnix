package balancer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/keepalive"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/types"
)

const (
	// Be careful when modifying this value. When the rate-limiting wait strategy is enabled, the default polling interval is 2 seconds.
	// This value needs to be greater than that; otherwise, specific errors cannot be detected.
	scheduleRequestTimeout = 3 * time.Second

	releaseTryCount = 3
)

type SchedulerClient struct {
	config *options.Config

	schClient *http.Client

	kac *keepalive.KeepAliveClient
}

func NewSchedulerClient(config *options.Config) *SchedulerClient {
	// create scheduler resolver
	if len(config.LlmScheduler) == 0 && len(config.LocalTestSchedulerIP) == 0 {
		klog.Error("llm scheduler service config is empty.")
		return nil
	}

	schResolver := resolver.CreateSchedulerResolver(config)
	if schResolver == nil {
		klog.Error("create scheduler resolver failed.")
		return nil
	}

	// Use LocalTestSchedulerIP if available, otherwise use LlmScheduler
	schedulerName := config.LlmScheduler
	if len(config.LocalTestSchedulerIP) > 0 {
		schedulerName = config.LocalTestSchedulerIP
	}
	kac := keepalive.NewKeepAliveClient(schedulerName, config.ServiceToken, schResolver)

	cb := &SchedulerClient{
		config:    config,
		schClient: &http.Client{Timeout: scheduleRequestTimeout},
		kac:       kac,
	}

	kac.StartAndAwaitReady()

	klog.Infof("create llm scheduler balancer(%s) successfully.", schedulerName)
	return cb
}

func needPromptTokens(c *options.Config) bool {
	return c.LlumnixConfig.EnableFullModeScheduling &&
		(c.LlumnixConfig.EnableCacheAwareScheduling || c.LlumnixConfig.EnableInstanceStatusLocalAccount)
}

func needPromptString(c *options.Config) bool {
	return c.SchedulePolicy == consts.SchedulePolicyPrefixCache
}

func (cb *SchedulerClient) createScheduleRequest(req *types.RequestContext) *types.ScheduleRequest {
	localEndpoint := cb.kac.GetLocalEndpoint()
	// create scheduler request
	schRequest := &types.ScheduleRequest{
		Id:                req.Id,
		Model:             req.GetRequestModel(),
		GatewayId:         localEndpoint.String(),
		ScheduleMode:      req.ScheduleCtx.ScheduleMode,
		InferStage:        req.ScheduleCtx.InferStage,
		ExcludedInstances: req.ScheduleCtx.GetExcludedInstanceList(),
	}

	// In the case of tokenizer, prefer to use token ids, otherwise use string length as an alternative.
	if tokenIds, err := req.GetPromptTokens(); err == nil {
		schRequest.PromptNumTokens = len(tokenIds)
		if needPromptTokens(cb.config) {
			schRequest.PromptTokenIds = tokenIds
		}
	} else if promptText, err := req.GetPromptString(); err == nil {
		schRequest.PromptNumTokens = len(promptText)
		if needPromptString(cb.config) {
			schRequest.PromptText = promptText
		}
	} else {
		klog.Warningf("schedule request %s prompt string and token ids are empty or not support.", req.Id)
	}

	// record the borrow gateway
	req.ScheduleCtx.GatewayId = localEndpoint.String()
	return schRequest
}

func (cb *SchedulerClient) handleResponse(body []byte) (types.ScheduledResult, error) {
	var schReq types.ScheduleRequest
	err := json.Unmarshal(body, &schReq)
	if err != nil {
		klog.Warningf("schedule response: content format error: %s", string(body))
		return nil, err
	}
	if len(schReq.ScheduleResult) == 0 {
		klog.Warningf("schedule response: no available endpoint")
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
		return nil, err
	}

	defer resp.Body.Close()

	var body []byte
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		klog.Warningf("[%s] do request schedule: could not read body: %v", req.Id, err)
		return nil, err
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		retryAfter := resp.Header.Get("Retry-After")
		if len(retryAfter) > 0 {
			return nil, consts.ErrorRateLimitQueueTimeOut
		} else {
			// No fallback
			return nil, consts.ErrorRateLimitExceeded
		}
	case http.StatusOK:
		return cb.handleResponse(body)
	case http.StatusServiceUnavailable:
		return nil, consts.ErrorNoAvailableEndpoint
	default:
		klog.Warningf("[%s] do request schedule: %d, %s", req.Id, resp.StatusCode, string(body))
		return nil, consts.ErrorNoAvailableEndpoint
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
	if !cb.kac.IsRemoteReady() {
		return nil, consts.ErrorSchedulerNotReady
	}
	for {
		result, err := cb.doSchedule(req)
		if err == nil {
			return result, nil
		}

		if err == consts.ErrorRateLimitQueueTimeOut {
			time.Sleep(100 * time.Millisecond)
			continue
		} else if err == consts.ErrorRateLimitExceeded {
			return nil, err
		} else {
			// All other errors will follow the fallback logic, ensuring that as long as the backend service is available, the request can be forwarded.
			// Do Not return the error directly, as the scheduler may not have fully loaded all backend service instances right after a restart.
			klog.Warningf("[%s] service backend endpoint get failed, error: %v", req.Id, err)
			return nil, consts.ErrorSchedulerNotReady
		}
	}
}

func (cb *SchedulerClient) createReleaseRequest(req *types.RequestContext, worker *types.LLMWorker) *types.ScheduleRequest {
	localEndpoint := cb.kac.GetLocalEndpoint()
	return &types.ScheduleRequest{
		Id:             req.Id,
		Model:          req.GetRequestModel(),
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
		// Add retry capability to ensure the release succeeds as much as possible.
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
