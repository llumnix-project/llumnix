package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/keepalive"
	"llm-gateway/pkg/logging"
	"llm-gateway/pkg/metrics"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/scheduler/lrs"
	schedule_policy "llm-gateway/pkg/scheduler/schedule-policy"
	"llm-gateway/pkg/types"
)

type ScheduleService struct {
	config *options.Config

	schedulePolicy   schedule_policy.SchedulePolicy
	reschedulePolicy schedule_policy.ReschedulePolicy

	resolver  resolver.LLMResolver
	addChan   <-chan types.LLMWorkerSlice
	delChan   <-chan types.LLMWorkerSlice
	lrsClient *lrs.LocalRealtimeStateClient

	scheduleMutex     sync.Mutex
	prometheusHandler *metrics.PrometheusHandler
}

func NewScheduleService(c *options.Config) *ScheduleService {
	lrsClient := lrs.NewLocalRealtimeStateClient(c)

	ss := &ScheduleService{
		config:            c,
		lrsClient:         lrsClient,
		schedulePolicy:    schedule_policy.NewSchedulePolicy(c.SchedulePolicy, c, lrsClient),
		prometheusHandler: metrics.NewPrometheusHandler(),
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
	logging.Logf("Received schedule request: %s", schReq.String())

	// TODO(wingo.zwt) Reusing llumnix's configuration, concurrent scheduling is not allowed by default
	// because the scheduling reference data needs to be updated through LRS after the scheduling is completed again.
	if !ss.config.LlumnixConfig.AllowConcurrentSchedule {
		klog.V(3).Infof("concurrent scheduling is not allowed")
		ss.scheduleMutex.Lock()
		defer ss.scheduleMutex.Unlock()
	}

	var statusCode int
	err = ss.schedulePolicy.Schedule(&schReq)

	if err != nil {
		if errors.Is(err, consts.ErrorNoAvailableEndpoint) {
			statusCode = http.StatusServiceUnavailable
			logging.Logf("%s %vms| gateway(%s) request failed: no available endpoint", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId)
		} else if errors.Is(err, consts.ErrorRateLimitExceeded) {
			statusCode = http.StatusTooManyRequests
			logging.Logf("%s %vms| gateway(%s) request failed: rate limit", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId)
		} else if errors.Is(err, consts.ErrorRateLimitQueueTimeOut) {
			statusCode = http.StatusTooManyRequests
			w.Header().Set("Retry-After", "100")
			logging.Logf("%s %vms| gateway(%s) request need retry: rate limit queue timeout", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId)
		} else {
			statusCode = http.StatusBadRequest
			logging.Logf("%s %vms| gateway(%s) request failed: %v", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId, err)
		}
	} else if len(schReq.ScheduleResult) == 0 {
		err = consts.ErrorNoAvailableEndpoint
		statusCode = http.StatusServiceUnavailable
		logging.Logf("%s %vms| gateway(%s) request failed: empty schedule result", schReq.Id, time.Since(tStart).Milliseconds(), schReq.GatewayId)
	}

	if err != nil {
		metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricScheduleFailedCount, metrics.Labels{{"error_type", err.Error()}})
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		logging.Logf("[%s] status_code:%d,response_time:%vms,error:%s", schReq.Id, statusCode, time.Since(tStart).Milliseconds(), err.Error())
		return
	}

	// record realtime stats for the acquired token
	if !ss.config.LlumnixConfig.EnableFullModeScheduling {
		for _, worker := range schReq.ScheduleResult {
			reqState := lrs.NewRequestState(schReq.Id, int64(schReq.GetPromptLen()), worker.Id(), schReq.GatewayId)
			err := ss.lrsClient.AllocateRequestState(worker.Role.String(), reqState)
			if err != nil {
				klog.Errorf("[%s] acquire %s resource request failed: %v", schReq.Id, worker.Role, err)
			}
		}
	}

	retBytes, _ := json.Marshal(schReq)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(retBytes)
	logging.Logf("[%s] status_code:%d,response_time:%vms,schedule results:%s", schReq.Id, http.StatusOK, time.Since(tStart).Milliseconds(), schReq.String())
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

	logging.Logf("%vms| return local resource by %s: %v", time.Since(tStart).Milliseconds(), schReq.GatewayId, string(body))
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

	var reports lrs.RequestTokenStateArray
	if err := json.Unmarshal(body, &reports); err != nil {
		klog.Warningf("invalid report request: %s", body)
		http.Error(w, "invalid report request", http.StatusBadRequest)
		return
	}

	for _, reqData := range reports {
		var numTokens int64
		if reqData.UseTokenIds {
			numTokens = int64(reqData.NumTokens)
		} else {
			numTokens = int64(reqData.TextLen)
		}
		reqState := lrs.NewRequestState(reqData.Id, numTokens, reqData.InstanceId, reqData.GatewayId)
		var err error
		switch reqData.Kind {
		case lrs.KindPrefillDone:
			err = ss.lrsClient.MarkPrefillComplete(reqData.InferMode, reqState)
			if err != nil {
				klog.Errorf("mark prefill complete failed: %v", err)
			}
		default:
			// KindStateUpdate or default case
			err = ss.lrsClient.UpdateRequestState(reqData.InferMode, reqState)
			if err != nil {
				klog.Errorf("update request state failed: %v", err)
			}
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
	case "/ws/keepalive":
		ss.handleServiceKeepAlive(w, r)
	case "/healthz":
		w.WriteHeader(http.StatusOK)
	case "/metrics":
		ss.prometheusHandler.ServeHTTP(w, r)
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

// //////////////////////////////////////////////////////////////////
//
//	handleServiceKeepAlive do keepalive with the backend service,
//
// This feature has been DEPRECATED. To ensure compatibility with older servers,
// the interface has been retained here.
type StatsResponse struct {
	Success bool   `json:"success"`
	Data    string `json:"data,omitempty"`
}

func (ss *ScheduleService) getServiceInstanceStats(wsConn *websocket.Conn) error {
	_, _, err := wsConn.ReadMessage()
	if err != nil {
		klog.Errorf("could not read message from backend service: %v", err)
		return err
	}
	return nil
}

// handleServiceKeepAlive do keepalive with the backend service and check the service token
// URL: /ws/keepalive
func (ss *ScheduleService) handleServiceKeepAlive(w http.ResponseWriter, r *http.Request) {
	if len(ss.config.UseDiscovery) > 0 {
		klog.Warningf("enable use discovery: statistics reported by service instances are not processed")
		return
	}

	// upgrade WebSocket connection.
	defaultUpgrader := websocket.Upgrader{}
	conn, err := defaultUpgrader.Upgrade(w, r, http.Header{})
	if err != nil {
		klog.Warningf("do keepalive: couldn't upgrade %s", err)
		return
	}

	wsConn := &wsConn{
		conn:         conn,
		writeMessage: make(chan *WriteMessage, 100),
	}

	err = ss.getServiceInstanceStats(conn)
	if err != nil {
		closeMessage := websocket.FormatCloseMessage(websocket.CloseAbnormalClosure, string(err.Error()))
		conn.WriteMessage(websocket.CloseMessage, closeMessage)
		conn.Close()
		return
	}

	response := StatsResponse{Success: true}
	response.Success = true
	str, _ := json.Marshal(response)
	wsConn.writeMessage <- &WriteMessage{data: str, needClose: false}

	go func() {
		for {
			err := ss.getServiceInstanceStats(conn)
			if err != nil {
				wsConn.writeMessage <- &WriteMessage{data: []byte(err.Error()), needClose: true}
				break
			}

			needClose := false
			response := StatsResponse{Success: true}
			str, _ := json.Marshal(response)
			wsConn.writeMessage <- &WriteMessage{data: str, needClose: needClose}
			if needClose {
				break
			}
		}
	}()
	ss.writeWithPingPong(wsConn)
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

/////////////////////////////////////////////////////////////////
