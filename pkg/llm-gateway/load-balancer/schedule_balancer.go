package loadbalancer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/valyala/fastjson"
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
)

const defaultExpectCount = 1
const defaultExpectTokenTimeout = 2 * time.Second

type ScheduleBalancer struct {
	config *options.Config

	wsConn *websocket.Conn

	mu                sync.RWMutex
	localEndpoint     structs.Endpoint
	llmSchedulerReady bool
	httpClient        *http.Client

	schedulerMu          sync.RWMutex
	schedulerEndpoint    structs.Endpoint
	llmSchedulerResolver resolver.Resolver

	fallbackLoadBalancer LoadBalancer
}

func tryConnectLlmScheduler(address string, accessToken string) (wsConn *websocket.Conn, err error) {
	url := fmt.Sprintf("ws://%s/ws/gateway_keepalive", address)
	h := http.Header{}
	if len(accessToken) > 0 {
		h.Add("Authorization", accessToken)
	}

	dialer := websocket.DefaultDialer
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()

	wsConn, _, err = dialer.DialContext(ctx, url, h)
	if err == nil {
		return
	}
	klog.Warningf("dial %s failed: %v", url, err)
	return
}

// checkPing check scheduler ping-pong
func checkPing(conn *websocket.Conn, done chan bool) {
	var missedPings atomic.Int32

	conn.SetPingHandler(func(appData string) error {
		missedPings.Store(0)
		conn.WriteMessage(websocket.PongMessage, []byte(appData))
		return nil
	})

	ticker := time.NewTicker(3 * time.Second) // scheduler send ping every 3 seconds
	defer func() {
		ticker.Stop()
		conn.Close()
	}()
OuterLoop:
	for {
		select {
		case <-done:
			break OuterLoop
		case <-ticker.C:
			if missedPings.Load() >= 2 {
				// two pongs missed, the connection may be broken
				klog.V(3).Infof("2 scheduler pings missed.")
				break OuterLoop
			}
			missedPings.Add(1)
		}
	}
	klog.V(3).Infof("the goroutine that check scheduler ping is finished.")
}

func createFallbackLoadBalancer(config *options.Config) LoadBalancer {
	var fallbackLB LoadBalancer
	if config.UseDiscovery != consts.DiscoveryMessageBus {
		fallbackLB = NewModelBalancer(config)
	} else {
		newCfg := *config
		newCfg.SchedulePolicy = consts.SchedulePolicyRoundRobin
		fallbackLB = NewLoadBalancerProxy(&newCfg)
	}
	klog.Info("create a fallback load balancer with round-robin policy.")
	return fallbackLB
}

func NewSchedulerBalancer(config *options.Config) *ScheduleBalancer {
	// create scheduler resolver
	if len(config.LlmScheduler) == 0 {
		klog.Error("llm scheduler service config is empty.")
		return nil
	}

	var r resolver.Resolver
	if config.LocalTestSchedulerIP != "" {
		r = resolver.NewIPListResolver(config.LocalTestSchedulerIP)
	} else {
		r = resolver.CreateSchedulerResolver(config)
	}

	cb := &ScheduleBalancer{
		config:               config,
		llmSchedulerResolver: r,
		llmSchedulerReady:    false,
		httpClient:           utils.NewHttpClient(),
		fallbackLoadBalancer: createFallbackLoadBalancer(config),
	}

	klog.Infof("try connect to remote scheduler(%s) ... ", config.LlmScheduler)
	doConnectLoop := func() *websocket.Conn {
		var conn *websocket.Conn
		var err error
		errCnt := int64(0)
		connectCnt := int64(0)
		for {
			for {
				endpoints := cb.llmSchedulerResolver.GetWeightEndpoints()
				if len(endpoints) > 0 {
					cb.setLlmSchedulerAddress(endpoints[0].Ep)
					errCnt = 0
					break
				}
				klog.Infof("wait llm scheduler(%s) endpoints ready ...", config.LlmScheduler)
				errCnt++
				if errCnt > 3 {
					originReady := cb.setSchedulerNoReady()
					if originReady {
						klog.Warningf("llm scheduler(%s) is not ready.", config.LlmScheduler)
					}
				}
				time.Sleep(2 * time.Second)
			}
			llmSchedulerAddress := cb.getLlmSchedulerAddress()
			conn, err = tryConnectLlmScheduler(llmSchedulerAddress.String(), cb.config.ServiceToken)
			if err == nil {
				klog.Infof("connected to llm scheduler(%s, %s).", config.LlmScheduler, llmSchedulerAddress.String())
				break
			}
			connectCnt++
			if connectCnt > 2 {
				originReady := cb.setSchedulerNoReady()
				if originReady {
					klog.Warningf("connect llm scheduler(%s), error: %v", config.LlmScheduler, err)
				}
			}
			time.Sleep(2 * time.Second)
			klog.Warningf("try connect to llm scheduler(%s): %s", config.LlmScheduler, llmSchedulerAddress.String())
		}
		return conn
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		first := true
		var conn *websocket.Conn = nil
		for {
			if conn != nil {
				conn.Close()
			}
			conn = doConnectLoop()
			cb.wsConn = conn

			localEndpoint, err := structs.NewEndpoint(conn.LocalAddr().String())
			if err != nil {
				klog.Warningf("new local endpoint failed: %v", err)
				continue
			}

			data, _ := json.Marshal(localEndpoint)
			if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
				klog.Warningf("send local endpoint to toke cache failed: %v", err)
				continue
			}
			_, msg, err := conn.ReadMessage()
			if err != nil || string(msg) != "ok" {
				klog.Warningf("send local endpoint to toke cache failed: %v, %s", err, string(msg))
				continue
			}

			cb.setSchedulerReady(localEndpoint)
			klog.Infof("llm scheduler is ready")

			if first {
				wg.Done()
				first = false
			}

			done := make(chan bool, 1)
			go checkPing(conn, done)
			for {
				_, _, err := conn.ReadMessage()
				if err != nil {
					llmSchedulerAddress := cb.getLlmSchedulerAddress()
					klog.Warningf("disconnected from llm scheduler(%s): %v", llmSchedulerAddress.String(), err)
					done <- true
					break
				}
			}
		}
	}()
	wg.Wait()

	klog.Infof("create llm scheduler balancer(%s) successfully.", config.LlmScheduler)
	return cb
}

func (cb *ScheduleBalancer) setLlmSchedulerAddress(ep structs.Endpoint) {
	cb.schedulerMu.Lock()
	defer cb.schedulerMu.Unlock()
	cb.schedulerEndpoint = ep
}

func (cb *ScheduleBalancer) getLlmSchedulerAddress() structs.Endpoint {
	cb.schedulerMu.RLock()
	defer cb.schedulerMu.RUnlock()
	return cb.schedulerEndpoint
}

func (cb *ScheduleBalancer) isSchedulerReady() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.llmSchedulerReady
}

func (cb *ScheduleBalancer) setSchedulerReady(localEndpoint structs.Endpoint) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.localEndpoint = localEndpoint
	cb.llmSchedulerReady = true
}

func (cb *ScheduleBalancer) setSchedulerNoReady() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	ready := cb.llmSchedulerReady
	cb.llmSchedulerReady = false
	return ready
}

func (cb *ScheduleBalancer) getLocalEndpoint() structs.Endpoint {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.localEndpoint
}

func getLlmMessage(req *structs.Request) []byte {
	if len(req.Data) == 0 {
		return nil
	}

	var p fastjson.Parser
	v, err := p.ParseBytes(req.Data)
	if err != nil {
		klog.Warningf("could not parse request: %v", err)
		return nil
	}

	if prompt := v.GetStringBytes("prompt"); prompt != nil {
		return prompt
	}

	if msgs := v.Get("messages"); msgs != nil && msgs.Type() == fastjson.TypeArray {
		elems := msgs.GetArray()
		if len(elems) == 0 {
			return nil
		}

		buf := make([]byte, 0, len(req.Data))
		for i, elem := range elems {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = elem.MarshalTo(buf)
		}
		return buf
	}

	return nil
}

func (cb *ScheduleBalancer) needSendMsg() bool {
	return cb.config.IsPDSplitMode()
}

func getPromptTokens(reqObj *fastjson.Value) ([]int64, error) {
	promptValue := reqObj.Get("prompt")
	if !promptValue.Exists() {
		return nil, fmt.Errorf("prompt field not found")
	}

	arr, err := promptValue.Array()
	if err != nil {
		return nil, fmt.Errorf("prompt is not an array: %v", err)
	}

	data := make([]int64, len(arr))
	for i, v := range arr {
		data[i] = int64(v.GetInt())
	}

	return data, nil
}

func (cb *ScheduleBalancer) createTokenRequest(req *structs.Request) *structs.TokenRequest {
	gatewayEp := cb.getLocalEndpoint()
	// record the borrow gateway
	req.GatewayEp = gatewayEp

	expectToken := &structs.TokenRequest{
		Id:            req.Id,
		Model:         req.Model,
		Count:         defaultExpectCount,
		InferMode:     req.ScheduleStage,
		LocalEndpoint: gatewayEp,
	}
	if cb.needSendMsg() && len(req.Prompt) > 0 {
		expectToken.Message = string(req.Prompt)
	}
	if cb.config.LlumnixConfig.EnableFullModeScheduling &&
		(cb.config.LlumnixConfig.EnableCacheAwareScheduling || cb.config.LlumnixConfig.EnableInstanceStatusLocalAccount) {
		tokens, err := getPromptTokens(req.ReqObj)
		if err != nil {
			klog.Warningf("req %v: %v", req.Id, err)
		} else {
			expectToken.PromptTokenIds = tokens
		}
	}
	expectToken.MsgLength = int64(req.PromptLength())

	return expectToken
}

func (cb *ScheduleBalancer) handleResponse(body []byte) (nextTokens *structs.NextTokens, err error) {
	var expectToken structs.TokenRequest
	err = json.Unmarshal(body, &expectToken)
	if err != nil {
		err = consts.ErrorEndpointNotFound
		klog.Warningf("get next endpoint: content format error: %s", string(body))
		return nil, err
	}
	if len(expectToken.Tokens) == 0 {
		klog.Warningf("get next endpoint: no available endpoint")
		return nil, consts.ErrorNoAvailableEndpoint
	}

	nextTokens = &structs.NextTokens{}
	nextTokens.Tokens = expectToken.Tokens
	if len(expectToken.Tokens2) > 0 {
		nextTokens.Tokens2 = expectToken.Tokens2
	}
	klog.V(3).Infof("get token: %v, %v", printTokens(nextTokens.Tokens), printTokens(nextTokens.Tokens2))
	return
}

func printTokens(tokens []structs.Token) (res []string) {
	for _, token := range tokens {
		res = append(res, token.String())
	}
	return res
}

func (cb *ScheduleBalancer) doGetToken(req *structs.Request) (nextTokens *structs.NextTokens, err error) {
	// check scheduler ready or not
	if !cb.isSchedulerReady() {
		return nil, consts.ErrorSchedulerNotReady
	}

	// create token request to scheduler
	expectToken := cb.createTokenRequest(req)
	data, _ := json.Marshal(&expectToken)

	// send request to scheduler
	ctx, cancel := context.WithTimeout(context.Background(), defaultExpectTokenTimeout)
	defer cancel()
	endpoint := cb.getLlmSchedulerAddress()
	url := fmt.Sprintf("http://%s/expect_token", endpoint.String())
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
	if len(cb.config.ServiceToken) > 0 {
		httpReq.Header.Add("Authorization", cb.config.ServiceToken)
	}
	var resp *http.Response
	resp, err = cb.httpClient.Do(httpReq)
	if err != nil {
		klog.Warningf("[%s] get next endpoint fail: %v\n", req.Id, err)
		return nil, consts.ErrorMayNetworkBroken
	}

	defer resp.Body.Close()

	var body []byte
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		klog.Warningf("[%s] get next endpoint: could not read body: %v", req.Id, err)
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
		klog.Warningf("[%s] get next endpoint: %d, %s", req.Id, resp.StatusCode, string(body))
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

func (cb *ScheduleBalancer) getTokens(req *structs.Request) (nextTokens *structs.NextTokens, err error) {
	tStart := time.Now()

	if !cb.isSchedulerReady() {
		goto NotReady
	}

	for {
		nextTokens, err = cb.doGetToken(req)
		if err == nil {
			return
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
		break
	}

NotReady:
	nextTokens, err = cb.fallbackLoadBalancer.GetNextTokens(req)
	if err == nil {
		klog.Infof("[%s] fallback policy(round-robin) is applied, select endpoints: %s", req.Id, nextTokens.Description())
	}
	return nextTokens, err
}

func (cb *ScheduleBalancer) GetNextTokens(req *structs.Request) (*structs.NextTokens, error) {
	return cb.getTokens(req)
}

func (cb *ScheduleBalancer) ExcludeService(string) {}

func (cb *ScheduleBalancer) ReleaseToken(req *structs.Request, token *structs.Token) {
	if req == nil || token == nil {
		return
	}

	localEndpoint := cb.getLocalEndpoint()
	ready := cb.isSchedulerReady()
	// 1. The scheduling policy does not require resource release.
	// 2. The current gateway differs from the one at borrowing time, indicating the resources have been updated and no release is needed.
	// 3. If the scheduler is in a not-ready state, no release is required either, as subsequent new schedulers or connections will refresh these resources.
	if !token.NeedRelease || localEndpoint != req.GatewayEp || !ready {
		return
	}

	go func() {
		tokenRequest := structs.TokenRequest{
			Id:            req.Id,
			Count:         1,
			Model:         token.Model,
			Tokens:        []structs.Token{*token},
			LocalEndpoint: req.GatewayEp,
			MsgLength:     int64(req.PromptLength()),
		}

		klog.V(3).Infof("release token: %v", tokenRequest)
		data, _ := json.Marshal(tokenRequest)
		endpoint := cb.getLlmSchedulerAddress()
		url := fmt.Sprintf("http://%s/return_token", endpoint.String())

		// The timeout and retry settings should generally exceed the duration of
		// the scheduler's unready state to prevent resource leaks caused by temporary network timeouts.
		var resp *http.Response
		var err error
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(data))
		if len(cb.config.ServiceToken) > 0 {
			req.Header.Add("Authorization", cb.config.ServiceToken)
		}
		retry := 3
	RETRY:
		resp, err = cb.httpClient.Do(req)
		retry--
		if err != nil {
			// retry when some network error occurs
			if retry > 0 {
				time.Sleep(1 * time.Second)
				goto RETRY
			}
			klog.Warningf("release endpoint fail: %v\n", err)
			return
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			klog.Warningf("release endpoint: could not read body: %v", err)
			return
		}
		if resp.StatusCode != http.StatusOK {
			klog.Errorf("release endpoint error: %d, %s", resp.StatusCode, string(body))
			return
		}
	}()

	// Avoid multiple releases caused by multiple calls to the interface.
	token.NeedRelease = false
}
