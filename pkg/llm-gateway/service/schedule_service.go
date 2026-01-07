package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/consts"
	loadbalancer "easgo/pkg/llm-gateway/load-balancer"
	"easgo/pkg/llm-gateway/lrs"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/resolver"
	schedule_policy "easgo/pkg/llm-gateway/schedule-policy"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
)

type ScheduleService struct {
	config *options.Config

	schedulePolicy   schedule_policy.SchedulePolicy
	reschedulePolicy schedule_policy.ReschedulePolicy

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

	// The active service discovery mechanism requires real-time updates
	// of the backend service instance IP address in the background
	if c.UseDiscovery == consts.DiscoveryMessageBus {
		go ss.SyncServiceEndpointsWithMessageBus()
	} else if c.UseDiscovery == consts.DiscoveryCacheServer && len(c.BackendService) > 0 {
		go ss.SyncServiceEndpointsWithCacheServer()
	}

	if c.LlumnixConfig.EnableMetrics {
		metrics.EnableLlumnixMetrics()
	}
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
// URL: /ws/gateway_keepalive
func (ss *ScheduleService) handleGatewayKeepalive(w http.ResponseWriter, r *http.Request) {
	// upgrade WebSocket connection.
	defaultUpgrader := websocket.Upgrader{}
	conn, err := defaultUpgrader.Upgrade(w, r, http.Header{})
	if err != nil {
		klog.Warningf("handle token: couldn't upgrade %s", err)
		return
	}
	defer conn.Close()

	wsConn := &wsConn{
		conn:         conn,
		writeMessage: make(chan *WriteMessage, 100),
	}

	// Read the first message from the WebSocket to obtain the address of the connected gateway
	// this address can be used to record the token usage associated with this gateway.
	_, data, err := conn.ReadMessage()
	if err != nil {
		klog.Warningf("handle token, could not read from gateway: %v", err)
		return
	}
	var remoteEndpoint structs.Endpoint
	if err := json.Unmarshal(data, &remoteEndpoint); err != nil {
		klog.Warningf("handle token, could not get remote endpoint from gateway: %v", err)
		return
	}
	if err := conn.WriteMessage(websocket.TextMessage, []byte("ok")); err != nil {
		klog.Warningf("handle token, write response to gateway: %v", err)
		return
	}

	// add gateway for lrs
	gatewayEp := &structs.GatewayEndpoint{Endpoint: remoteEndpoint}
	ss.lrsClient.AddGateway(gatewayEp)

	// If the goroutine terminates, it indicates that an anomaly occurred with the connection, which could be due to a ping pong
	// failure or an abnormal TCP disconnection. Ultimately, we need to reclaim the tokens that are in use.
	defer ss.lrsClient.RemoveGateway(gatewayEp.Id())

	go func() {
		for {
			_, message, err := wsConn.conn.ReadMessage()
			if err != nil {
				// abnormal TCP disconnection, received RST
				klog.Infof("disconnected with gateway %s: %v", remoteEndpoint.String(), err)
				wsConn.conn.Close()
				wsConn.writeMessage <- &WriteMessage{data: []byte("closed"), needClose: true}
				return
			}
			klog.Warningf("get/update token, unsupported this message: %s", string(message))
		}
	}()

	klog.Infof("start keepalive with a gateway: %s", remoteEndpoint.String())
	ss.writeWithPingPong(wsConn)
	klog.Infof("end keepalive with a gateway: %s", remoteEndpoint.String())
}

// handleExpectToken returns the token of the backend service with the fewest current request handling.
// URL: /expect_token
func (ss *ScheduleService) handleExpectToken(w http.ResponseWriter, r *http.Request) {
	tStart := time.Now()

	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("io read err: %v", err)
		return
	}

	var expectToken structs.TokenRequest
	if err := json.Unmarshal(body, &expectToken); err != nil {
		klog.Warningf("invalid expect token req: %s", body)
		http.Error(w, "invalid expect token req", http.StatusBadRequest)
		return
	}

	if expectToken.Count <= 0 {
		klog.Warningf("invalid expect token count: %d", expectToken.Count)
		http.Error(w, "invalid expect token count", http.StatusBadRequest)
		return
	}

	var statusCode int
	err = ss.schedulePolicy.GetToken(&expectToken)
	if errors.Is(err, consts.ErrorEndpointNotFound) {
		klog.Errorf("%vms| gateway(%s) request failed: no endpoint exits", time.Since(tStart).Milliseconds(), expectToken.LocalEndpoint.String())
		statusCode = http.StatusNotFound
	} else if err != nil {
		if errors.Is(err, consts.ErrorNoAvailableEndpoint) {
			statusCode = http.StatusTooManyRequests
			klog.Errorf("%s %vms| gateway(%s) request failed: no available endpoint", expectToken.Id, time.Since(tStart).Milliseconds(), expectToken.LocalEndpoint.String())
		} else {
			statusCode = http.StatusBadRequest
			klog.Errorf("%s %vms| gateway(%s) request failed: %v", expectToken.Id, time.Since(tStart).Milliseconds(), expectToken.LocalEndpoint.String(), err)
		}
	} else if len(expectToken.Tokens) == 0 {
		err = consts.ErrorNoAvailableEndpoint
		statusCode = http.StatusTooManyRequests
		klog.Errorf("%s %vms| gateway(%s) request failed: get endpoints empty", expectToken.Id, time.Since(tStart).Milliseconds(), expectToken.LocalEndpoint.String())
	}

	if err != nil {
		metrics.IncrLlumnixCounterByOne(metrics.LlumnixMetricScheduleFailedCount, structs.Labels{{"error_type", err.Error()}})
		w.WriteHeader(statusCode)
		w.Write([]byte(err.Error()))
		if ss.config.EnableAccessLog {
			utils.LogAccess("[%s] status_code:%d,response_time:%vms,error:%s", expectToken.Id, statusCode, time.Since(tStart).Milliseconds(), err.Error())
		}
		return
	}

	// record lrs for the allocated token
	gatewayEp := &structs.GatewayEndpoint{Endpoint: expectToken.LocalEndpoint}
	tokenNeedRelease := !ss.config.LlumnixConfig.EnableFullModeScheduling
	// TODO(sunbiao.sun): can be simplified
	if len(expectToken.Tokens2) > 0 {
		for i := 0; i < len(expectToken.Tokens); i++ {
			expectToken.Tokens[i].NeedRelease = tokenNeedRelease
			if tokenNeedRelease {
				reqState := lrs.NewRequestState(expectToken.Id, expectToken.MsgLength, expectToken.Tokens[i].Id(), gatewayEp.Id())
				err := ss.lrsClient.AllocateRequestState(consts.PrefillInferMode, reqState)
				if err != nil {
					klog.Errorf("Acquire prefill resource request failed: %v", err)
				}
			}
		}
		for i := 0; i < len(expectToken.Tokens2); i++ {
			expectToken.Tokens2[i].NeedRelease = tokenNeedRelease
			if tokenNeedRelease {
				resourceRequest := lrs.NewRequestState(expectToken.Id, expectToken.MsgLength, expectToken.Tokens2[i].Id(), gatewayEp.Id())
				err := ss.lrsClient.AllocateRequestState(consts.DecodeInferMode, resourceRequest)
				if err != nil {
					klog.Errorf("Acquire decode resource request failed: %v", err)
				}
			}
		}
	} else {
		for i := 0; i < len(expectToken.Tokens); i++ {
			expectToken.Tokens[i].NeedRelease = tokenNeedRelease
			if tokenNeedRelease {
				reqState := lrs.NewRequestState(expectToken.Id, expectToken.MsgLength, expectToken.Tokens[i].Id(), gatewayEp.Id())
				err := ss.lrsClient.AllocateRequestState(expectToken.InferMode, reqState)
				if err != nil {
					klog.Errorf("Acquire resource %s request failed: %v", expectToken.InferMode, err)
				}
			}
		}
	}

	expectToken.Message = ""
	retBytes, _ := json.Marshal(expectToken)
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(retBytes)
	if ss.config.EnableAccessLog {
		utils.LogAccess("[%s] status_code:%d,response_time:%vms,endpoints:%s", expectToken.Id, http.StatusOK, time.Since(tStart).Milliseconds(), expectToken.String())
	}
}

// handleReturnToken accepts the token returned by the gateway
// URL: /return_token
func (ss *ScheduleService) handleReturnToken(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("io read err: %v", err)
		return
	}

	var returnToken structs.TokenRequest
	if err := json.Unmarshal(body, &returnToken); err != nil {
		klog.Warningf("invalid return token: %s", body)
		http.Error(w, "invalid return token", http.StatusBadRequest)
		return
	}

	if len(returnToken.Tokens) == 0 {
		klog.Warningf("ignore, return 0 token.")
		http.Error(w, "ignore, return 0 token.", http.StatusBadRequest)
		return
	}

	tStart := time.Now()

	gatewayEp := &structs.GatewayEndpoint{Endpoint: returnToken.LocalEndpoint}
	for _, token := range returnToken.Tokens {
		reqState := lrs.NewRequestState(returnToken.Id, 0, token.Id(), gatewayEp.Id())
		ss.lrsClient.ReleaseRequestState(token.InferMode, reqState)
	}

	klog.V(3).Infof("%vms| do return endpoints by %s: %v", time.Since(tStart).Milliseconds(), returnToken.LocalEndpoint.String(), string(body))
	w.WriteHeader(http.StatusOK)
}

func (ss *ScheduleService) handleRequestReport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("io read err: %v", err)
		return
	}

	var reqDatas lrs.ReqReportDataArray
	if err := json.Unmarshal(body, &reqDatas); err != nil {
		klog.Warningf("invalid return token: %s", body)
		http.Error(w, "invalid return token", http.StatusBadRequest)
		return
	}

	for _, reqData := range reqDatas {
		reqState := lrs.NewRequestState(reqData.Id, int64(reqData.NumTokens), reqData.InstanceId, reqData.GatewayId)
		ss.lrsClient.UpdateRequestState(reqData.InferMode, reqState)
	}
}

func (ss *ScheduleService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/expect_token":
		ss.handleExpectToken(w, r)
	case "/return_token":
		ss.handleReturnToken(w, r)
	case "/ws/gateway_keepalive":
		ss.handleGatewayKeepalive(w, r)
	case "/request_report":
		ss.handleRequestReport(w, r)
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

func (ss *ScheduleService) syncServiceEndpoints(preEndpoints, currentEndpoints []structs.WeightEndpoint, inferMode string) {

	if loadbalancer.SameEndpoints(preEndpoints, currentEndpoints) {
		klog.V(3).Infof("skip sync service endpoints, same endpoints")
		return
	}

	old := loadbalancer.EndpointsToMap(preEndpoints)
	new := loadbalancer.EndpointsToMap(currentEndpoints)
	removes := make(map[string]structs.WeightEndpoint)
	adds := make(map[string]structs.WeightEndpoint)
	for key, old := range old {
		if _, ok := new[key]; !ok {
			removes[key] = old
		}
	}
	for key, new := range new {
		if _, ok := old[key]; !ok {
			adds[key] = new
		}
	}

	klog.V(3).Infof("sync service endpoints, adds: %v, removes: %v", adds, removes)

	for _, add := range adds {
		Token := structs.Token{
			Model:     "",
			Version:   time.Now().Unix(),
			InferMode: inferMode,
			Endpoint:  add.Ep,
			Count:     consts.MaxConcurrentRequests,
		}
		// inject the worker info
		if add.Worker != nil {
			Token.InferMode = add.Worker.InferMode
			Token.InstName = add.Worker.InstName
			Token.WorkerId = add.Worker.WorkerId
		}

		// Create state for this instance
		ss.lrsClient.AddInstance(&Token)
		klog.Infof("add backend service endpoint: %s/%s", inferMode, add.String())
	}
	for _, remove := range removes {
		id := remove.Ep.Description()
		ss.lrsClient.RemoveInstance(inferMode, id)
		klog.Infof("remove backend service endpoint: %s/%s", inferMode, remove.String())
	}
}

func (ss *ScheduleService) SyncServiceEndpointsWithMessageBus() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("scheduler service: sync backend service(message bus) loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go ss.SyncServiceEndpointsWithMessageBus()
		}
	}()

	var preNormalEndpoints, prePrefillEndpoints, preDecodeEndpoints []structs.WeightEndpoint
	normalResolver := resolver.NewMsgBusResolver(consts.NormalInferMode, ss.config.PDSplitMode)
	prefillResolver := resolver.NewMsgBusResolver(consts.PrefillInferMode, ss.config.PDSplitMode)
	decodeResolver := resolver.NewMsgBusResolver(consts.DecodeInferMode, ss.config.PDSplitMode)

	for {
		currentNormalEndpoints := normalResolver.GetWeightEndpoints()
		ss.syncServiceEndpoints(preNormalEndpoints, currentNormalEndpoints, consts.NormalInferMode)
		preNormalEndpoints = currentNormalEndpoints

		currentPrefillEndpoints := prefillResolver.GetWeightEndpoints()
		ss.syncServiceEndpoints(prePrefillEndpoints, currentPrefillEndpoints, consts.PrefillInferMode)
		prePrefillEndpoints = currentPrefillEndpoints

		currentDecodeEndpoints := decodeResolver.GetWeightEndpoints()
		ss.syncServiceEndpoints(preDecodeEndpoints, currentDecodeEndpoints, consts.DecodeInferMode)
		preDecodeEndpoints = currentDecodeEndpoints

		time.Sleep(5 * time.Second)
	}
}

func (ss *ScheduleService) SyncServiceEndpointsWithCacheServer() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("scheduler service: sync backend service(cache server) loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go ss.SyncServiceEndpointsWithCacheServer()
		}
	}()
	var preEndpoints []structs.WeightEndpoint
	backendServiceResolver := resolver.NewEasResolver(ss.config)
	for {
		currentEndpoints := backendServiceResolver.GetWeightEndpoints()
		ss.syncServiceEndpoints(preEndpoints, currentEndpoints, consts.NormalInferMode)
		preEndpoints = currentEndpoints

		time.Sleep(5 * time.Second)
	}
}
