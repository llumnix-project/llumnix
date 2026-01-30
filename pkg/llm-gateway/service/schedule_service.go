package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/keepalive"
	"llumnix/pkg/llm-gateway/lrs"
	"llumnix/pkg/llm-gateway/metrics"
	"llumnix/pkg/llm-gateway/resolver"
	policy "llumnix/pkg/llm-gateway/schedule-policy"
	"llumnix/pkg/llm-gateway/types"
	"llumnix/pkg/llm-gateway/utils"
)

type ScheduleService struct {
	config *options.SchedulerConfig

	schedulePolicy   policy.SchedulePolicy
	reschedulePolicy policy.ReschedulePolicy

	resolver  resolver.LLMResolver
	addChan   <-chan types.LLMInstanceSlice
	delChan   <-chan types.LLMInstanceSlice
	lrsClient *lrs.LocalRealtimeStateClient
}

func NewScheduleService(c *options.SchedulerConfig) *ScheduleService {
	lrsClient := lrs.NewLocalRealtimeStateClient(c)

	ss := &ScheduleService{
		config:         c,
		lrsClient:      lrsClient,
		schedulePolicy: policy.NewSchedulePolicy(c.SchedulePolicy, c, lrsClient),
	}

	if c.ColocatedRescheduleMode {
		ss.reschedulePolicy = policy.NewReschedulePolicy(c)
	}

	if c.EnableMetrics {
		metrics.EnableLlumnixMetrics()
	}

	r := resolver.CreateBackendServiceResolver(&c.DiscoveryConfig, types.InferRoleAll)
	addChan, delChan, err := r.Watch(context.Background())
	if err != nil {
		klog.Errorf("failed to watch LLM instances: %v", err)
		return nil
	}
	ss.addChan = addChan
	ss.delChan = delChan

	go func() {
		for {
			select {
			case instances := <-ss.addChan:
				for _, w := range instances {
					// create realtime stats for this instance
					klog.Infof("add backend service endpoint: %s/%s", w.Role, w.String())
					ss.lrsClient.AddInstance(&w)
				}
			case instances := <-ss.delChan:
				for _, w := range instances {
					klog.Infof("remove backend service endpoint: %s/%s", w.Role, w.String())
					ss.lrsClient.RemoveInstance(w.Role.String(), w.Id())
				}
			}
		}
	}()

	return ss
}

// handleKeepalive do keepalive with gateway.
// URL: /keepalive
func (ss *ScheduleService) handleKeepalive(w http.ResponseWriter, r *http.Request) {
	// upgrade WebSocket connection.
	defaultUpgrader := websocket.Upgrader{}
	conn, err := defaultUpgrader.Upgrade(w, r, http.Header{})
	if err != nil {
		klog.Warningf("handle keepalive: couldn't upgrade %s", err)
		return
	}
	defer conn.Close()

	kac := keepalive.NewKeepAliveServer(conn)
	remoteEndpoint, err := kac.HandShake()
	if err != nil {
		klog.Warningf("handshake failed: %v", err)
		return
	}

	// add gateway for local realtime state
	ss.lrsClient.AddGateway(remoteEndpoint.String())

	// block and do keepalive with gateway
	kac.StartKeepAlive(func() {
		// If the goroutine terminates, it indicates that an anomaly occurred with the connection, which could be due to a ping pong
		// failure or an abnormal TCP disconnection. Ultimately, we need to reclaim the request states that are in use.
		ss.lrsClient.RemoveGateway(remoteEndpoint.String())
	})
}

// handleSchedule returns the instance of the backend service with the minimum load.
// URL: /schedule
func (ss *ScheduleService) handleSchedule(w http.ResponseWriter, r *http.Request) {
	tStart := time.Now()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("io read err: %v", err)
		return
	}

	var schReq types.ScheduleRequest
	if err := json.Unmarshal(body, &schReq); err != nil {
		klog.Warningf("invalid schedule req: %s", body)
		http.Error(w, "invalid schedule req", http.StatusBadRequest)
		return
	}

	var statusCode int
	err = ss.schedulePolicy.Schedule(&schReq)
	if errors.Is(err, consts.ErrorEndpointNotFound) {
		klog.Errorf("%vms| gateway(%s) request failed: no endpoint exits", time.Since(tStart).Milliseconds(), schReq.GatewayId)
		statusCode = http.StatusNotFound
	} else if err != nil {
		if errors.Is(err, consts.ErrorNoAvailableEndpoint) {
			statusCode = http.StatusTooManyRequests
			klog.Errorf("%s %vms| gateway(%s) request failed: no available endpoint", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId)
		} else {
			statusCode = http.StatusBadRequest
			klog.Errorf("%s %vms| gateway(%s) request failed: %v", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId, err)
		}
	} else if len(schReq.ScheduleResult) == 0 {
		err = consts.ErrorNoAvailableEndpoint
		statusCode = http.StatusTooManyRequests
		klog.Errorf("%s %vms| gateway(%s) request failed: get endpoints empty", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId)
	}

	if err != nil {
		metrics.IncrLlumnixCounterByOne(
			metrics.LlumnixMetricScheduleFailedCount, metrics.Labels{{"error_type", err.Error()}})
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		if ss.config.EnableLogInput {
			utils.LogAccess("[%s] status_code:%d,response_time:%vms,error:%s", schReq.Id, statusCode, time.Since(tStart).Milliseconds(), err.Error())
		}
		return
	}

	// record realtime state for the scheduled instance
	if !ss.config.EnableRequestStateTracking() {
		for _, instance := range schReq.ScheduleResult {
			reqState := lrs.NewRequestState(schReq.Id, int64(schReq.PromptNumTokens), instance.Id(), schReq.GatewayId)
			err := ss.lrsClient.AllocateRequestState(instance.Role.String(), reqState)
			if err != nil {
				klog.Errorf("Allocate %s request state failed: %v", instance.Role, err)
			}
		}
	}

	retBytes, _ := json.Marshal(schReq)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(retBytes)
	if ss.config.EnableLogInput {
		utils.LogAccess("[%s] status_code:%d,response_time:%vms,schedule results:%s", schReq.Id, http.StatusOK, time.Since(tStart).Milliseconds(), schReq.String())
	}
}

// handleRelease accepts the request released by the gateway
// URL: /release
func (ss *ScheduleService) handleRelease(w http.ResponseWriter, r *http.Request) {
	tStart := time.Now()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("io read err: %v", err)
		return
	}

	var schReq types.ScheduleRequest
	if err := json.Unmarshal(body, &schReq); err != nil {
		klog.Warningf("invalid release request: %s", body)
		http.Error(w, "invalid release request", http.StatusBadRequest)
		return
	}

	if len(schReq.ScheduleResult) == 0 {
		klog.Warningf("ignore, release 0 request.")
		http.Error(w, "ignore, release 0 request.", http.StatusBadRequest)
		return
	}

	for _, instance := range schReq.ScheduleResult {
		reqState := lrs.NewRequestState(schReq.Id, 0, instance.Id(), schReq.GatewayId)
		ss.lrsClient.ReleaseRequestState(instance.Role.String(), reqState)
	}

	klog.V(3).Infof("%vms| do release request by %s: %v", time.Since(tStart).Milliseconds(), schReq.GatewayId, string(body))
	w.WriteHeader(http.StatusOK)
}

// handleReport accepts the request reported by the gateway
// URL: /report
func (ss *ScheduleService) handleReport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("io read err: %v", err)
		return
	}

	var reqDatas lrs.RequestReportDataArray
	if err := json.Unmarshal(body, &reqDatas); err != nil {
		klog.Warningf("invalid report request: %s", body)
		http.Error(w, "invalid report request", http.StatusBadRequest)
		return
	}

	for _, reqData := range reqDatas {
		reqState := lrs.NewRequestState(reqData.Id, int64(reqData.NumTokens), reqData.InstanceId, reqData.GatewayId)
		err := ss.lrsClient.UpdateRequestState(reqData.InferMode, reqState)
		if err != nil {
			klog.Errorf("update request state failed: %v", err)
		}
	}
}

func (ss *ScheduleService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/schedule":
		ss.handleSchedule(w, r)
	case "/release":
		ss.handleRelease(w, r)
	case "/keepalive":
		ss.handleKeepalive(w, r)
	case "/report":
		ss.handleReport(w, r)
	case "/healthz":
		w.WriteHeader(http.StatusOK)
	default:
		http.Error(w, "Not found", http.StatusNotFound)
	}
}

func (ss *ScheduleService) Start() error {
	go func() {
		address := fmt.Sprintf("%s:%d", ss.config.Host, ss.config.Port)
		klog.Infof("http service start listen on %s", address)
		klog.Fatal(http.ListenAndServe(address, ss))
	}()

	if ss.reschedulePolicy != nil {
		go ss.reschedulePolicy.RescheduleLoop()
	}

	return nil
}
