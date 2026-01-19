package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/keepalive"
	"easgo/pkg/llm-gateway/lrs"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/resolver"
	schedule_policy "easgo/pkg/llm-gateway/schedule-policy"
	"easgo/pkg/llm-gateway/types"
	"easgo/pkg/llm-gateway/utils"
)

type ScheduleService struct {
	config *options.Config

	schedulePolicy   schedule_policy.SchedulePolicy
	reschedulePolicy schedule_policy.ReschedulePolicy

	resolver  resolver.LLMResolver
	addChan   <-chan types.LLMWorkerSlice
	delChan   <-chan types.LLMWorkerSlice
	lrsClient *lrs.LocalRealtimeStateClient
}

func NewScheduleService(c *options.Config) *ScheduleService {
	lrsClient := lrs.NewLocalRealtimeStateClient(c)

	ss := &ScheduleService{
		config:         c,
		lrsClient:      lrsClient,
		schedulePolicy: schedule_policy.NewSchedulePolicy(c.SchedulePolicy, c, lrsClient),
	}
	if c.ColocatedRescheduleMode && c.LlumnixConfig.EnableRescheduling {
		ss.reschedulePolicy = schedule_policy.NewReschedulePolicy(c)
	}

	if c.LlumnixConfig.EnableMetrics {
		metrics.EnableLlumnixMetrics()
	}

	resolver := resolver.CreateBackendServiceResolver(ss.config, types.InferRoleAll)
	addChan, delChan, err := resolver.Watch(context.Background())
	if err != nil {
		klog.Errorf("failed to watch LLM workers: %v", err)
		return nil
	}
	ss.addChan = addChan
	ss.delChan = delChan

	go func() {
		for {
			select {
			case workers := <-ss.addChan:
				for _, w := range workers {
					// create realtime stats for this worker
					klog.Infof("add backend service endpoint: %s/%s", w.Role, w.String())
					ss.lrsClient.AddInstance(&w)
				}
			case workers := <-ss.delChan:
				for _, w := range workers {
					klog.Infof("remove backend service endpoint: %s/%s", w.Role, w.String())
					ss.lrsClient.RemoveInstance(w.Role.String(), w.Id())
				}
			}
		}
	}()

	return ss
}

type WriteMessage struct {
	data      []byte
	needClose bool
}

type wsConn struct {
	conn         *websocket.Conn
	writeMessage chan *WriteMessage
}

// writeWithPingPong do ping pong with service or gateway
func (ss *ScheduleService) writeWithPingPong(wsConn *wsConn) {
	var missedPongs atomic.Int32
	conn := wsConn.conn
	conn.SetPongHandler(func(appData string) error {
		missedPongs.Store(0)
		return nil
	})

	ticker := time.NewTicker(3 * time.Second) // send ping every 3 seconds
	defer func() {
		ticker.Stop()
		conn.Close()
	}()
OuterLoop:
	for {
		select {
		case message := <-wsConn.writeMessage:
			if err := conn.WriteMessage(websocket.TextMessage, message.data); err != nil {
				break OuterLoop
			}
			if message.needClose {
				break OuterLoop
			}
		case <-ticker.C:
			if missedPongs.Load() >= 2 {
				// two pongs missed, the connection may be broken
				klog.V(3).Infof("2 pongs missed: %v", wsConn.conn.LocalAddr().String())
				break OuterLoop
			}
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				break OuterLoop
			}
			missedPongs.Add(1)
		}
	}
}

// handleWsToken do keepalive with gateway
// URL: /keepalive
func (ss *ScheduleService) handleKeepalive(w http.ResponseWriter, r *http.Request) {
	// upgrade WebSocket connection.
	defaultUpgrader := websocket.Upgrader{}
	conn, err := defaultUpgrader.Upgrade(w, r, http.Header{})
	if err != nil {
		klog.Warningf("handle token: couldn't upgrade %s", err)
		return
	}
	defer conn.Close()

	kac := keepalive.NewKeepAliveServer(conn)
	remoteEndpoint, err := kac.HandShake()
	if err != nil {
		klog.Warningf("handshake failed: %v", err)
		return
	}

	// add borrower gateway for realtime stats
	ss.lrsClient.AddGateway(remoteEndpoint.String())

	// block and do keepalive with gateway
	kac.StartKeepAlive(func() {
		// If the goroutine terminates, it indicates that an anomaly occurred with the connection, which could be due to a ping pong
		// failure or an abnormal TCP disconnection. Ultimately, we need to reclaim the tokens that are in use.
		ss.lrsClient.RemoveGateway(remoteEndpoint.String())
	})
}

// handleSchedule returns the token of the backend service with the fewest current request handling.
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
		klog.Warningf("invalid expect token req: %s", body)
		http.Error(w, "invalid expect token req", http.StatusBadRequest)
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
		metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricScheduleFailedCount, metrics.Labels{{"error_type", err.Error()}})
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		if ss.config.EnableAccessLog {
			utils.LogAccess("[%s] status_code:%d,response_time:%vms,error:%s", schReq.Id, statusCode, time.Since(tStart).Milliseconds(), err.Error())
		}
		return
	}

	// record realtime stats for the acquired token
	for _, worker := range schReq.ScheduleResult {
		reqState := lrs.NewRequestState(schReq.Id, int64(schReq.PromptNumTokens), worker.Id(), schReq.GatewayId)
		err := ss.lrsClient.AllocateRequestState(worker.Role.String(), reqState)
		if err != nil {
			klog.Errorf("Acquire %s resource request failed: %v", worker.Role, err)
		}
	}

	retBytes, _ := json.Marshal(schReq)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(retBytes)
	if ss.config.EnableAccessLog {
		utils.LogAccess("[%s] status_code:%d,response_time:%vms,schedule results:%s", schReq.Id, http.StatusOK, time.Since(tStart).Milliseconds(), schReq.String())
	}
}

// handleRelease accepts the token returned by the gateway
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
		klog.Warningf("invalid return token: %s", body)
		http.Error(w, "invalid return token", http.StatusBadRequest)
		return
	}

	if len(schReq.ScheduleResult) == 0 {
		klog.Warningf("ignore, return 0 token.")
		http.Error(w, "ignore, return 0 token.", http.StatusBadRequest)
		return
	}

	for _, worker := range schReq.ScheduleResult {
		reqState := lrs.NewRequestState(schReq.Id, 0, worker.Id(), schReq.GatewayId)
		ss.lrsClient.ReleaseRequestState(worker.Role.String(), reqState)
	}

	klog.V(3).Infof("%vms| do return endpoints by %s: %v", time.Since(tStart).Milliseconds(), schReq.GatewayId, string(body))
	w.WriteHeader(http.StatusOK)
}

// func (ss *ScheduleService) handleRequestReport(w http.ResponseWriter, r *http.Request) {
// 	body, err := io.ReadAll(r.Body)
// 	if err != nil {
// 		klog.Errorf("io read err: %v", err)
// 		return
// 	}

// 	var reqDatas realtime_stats.ReqReportDataArray
// 	if err := json.Unmarshal(body, &reqDatas); err != nil {
// 		klog.Warningf("invalid return token: %s", body)
// 		http.Error(w, "invalid return token", http.StatusBadRequest)
// 		return
// 	}

// 	for _, reqData := range reqDatas {
// 		resourceReq := realtime_stats.NewResourceRequest(reqData.Id, int64(reqData.TotalTokens), string(reqData.WorkerId), string(reqData.BorrowerId))
// 		ss.realtimeStats.UpdateResourceRequest(reqData.InferMode, resourceReq)
// 	}
// }

func (ss *ScheduleService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/schedule":
		ss.handleSchedule(w, r)
	case "/release":
		ss.handleRelease(w, r)
	case "/keepalive":
		ss.handleKeepalive(w, r)
	// case "/request_report":
	// 	ss.handleRequestReport(w, r)
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
