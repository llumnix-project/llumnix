package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/gorilla/websocket"
	"github.com/valyala/fastjson"
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/batch"
	"easgo/pkg/llm-gateway/consts"
	loadbalancer "easgo/pkg/llm-gateway/load-balancer"
	"easgo/pkg/llm-gateway/lrs"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/protocol"
	"easgo/pkg/llm-gateway/request_processor"
	"easgo/pkg/llm-gateway/resolver"
	servicerouter "easgo/pkg/llm-gateway/service-router"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
)

type LlmGatewayService struct {
	config *options.Config

	mmpq *utils.MultiModelProxyQueue

	client *http.Client
	lb     loadbalancer.LoadBalancer

	sr *servicerouter.ServiceRouter

	upgrader *websocket.Upgrader

	tokenFilter     *TokenFilter
	requestReporter *lrs.RequestReporter

	batchService *batch.BatchService

	logInputEnabled bool
}

func NewGwService(c *options.Config) *LlmGatewayService {
	// DefaultUpgrader specifies the parameters for upgrading an HTTP
	// connection to a WebSocket connection.
	defaultUpgrader := &websocket.Upgrader{
		ReadBufferSize:  8 * 1024,
		WriteBufferSize: 8 * 1024,
	}

	hs := &LlmGatewayService{
		config:          c,
		upgrader:        defaultUpgrader,
		client:          utils.NewHttpClient(),
		tokenFilter:     nil,
		logInputEnabled: c.EnableLogInput,
		requestReporter: lrs.NewRequestReporter(c),
	}
	hs.mmpq = utils.NewMultiModelProxyQueue(c.ServerlessMode, c.MaxQueueSize, c.WaitQueueThreads, hs.QueueReadHandle)

	// create load balancer
	hs.lb = loadbalancer.NewLoadBalancerProxy(c)

	if hs.config.EnableTokenFilter {
		hs.tokenFilter = NewTokenFilter()
	}

	// Initialize batch service
	var err error
	if c.BatchOSSPath != "" {
		hs.batchService, err = batch.NewBatchService(c)
		if err != nil {
			klog.Errorf("Failed to initialize batch service: %v", err)
			// Continue without batch service
		}
	}

	// local debug mode
	if len(c.LocalTestIPs) != 0 {
		ipResolver := resolver.NewIPListResolver(c.LocalTestIPs)
		hs.lb = loadbalancer.NewSWRRLoadBalancer(ipResolver)
	}

	// create service router
	hs.sr = servicerouter.NewServiceRouter(hs.lb, c.RoutePolicy, c.RouteConfigRaw)

	return hs
}

func (hs *LlmGatewayService) filterModelTokens(nextToken *structs.NextTokens, model string) (*structs.Token, *structs.Token) {
	if hs.tokenFilter != nil {
		klog.V(3).Infof("get filtered tokens: %v, %v", nextToken.Tokens, nextToken.Tokens2)
		return hs.tokenFilter.FilterModelTokens(nextToken, model)
	}
	if len(nextToken.Tokens2) > 0 {
		return &nextToken.Tokens[0], &nextToken.Tokens2[0]
	} else {
		return &nextToken.Tokens[0], nil
	}
}

func (hs *LlmGatewayService) record(endpoint structs.Endpoint, model string) {
	if hs.tokenFilter != nil {
		hs.tokenFilter.Record(endpoint, model)
	}
}

func (hs *LlmGatewayService) erase(endpoint structs.Endpoint, model string) {
	if hs.tokenFilter != nil {
		hs.tokenFilter.Erase(endpoint, model)
	}
}

func (hs *LlmGatewayService) getTokenFilterCount(model string) int64 {
	if hs.tokenFilter != nil {
		return hs.tokenFilter.GetModelTotalCount(model)
	}
	return 0
}

func (hs *LlmGatewayService) tryNext(req *structs.Request) bool {
	// clear time
	req.DeQueueTime = time.Time{}
	req.BalanceTime = time.Time{}
	req.FirstTime = time.Time{}
	req.ProcessTime = time.Time{}

	q := hs.mmpq.GetOrCreateModelProxyQueue(req.Model)

	req.Retrying = true
	req.EnQueueTime = time.Now()
	return q.EnqueuePriority(req)
}

func (hs *LlmGatewayService) handleProxy(req *structs.Request, enableWorkerCount bool) {
	defer func() {
		if enableWorkerCount {
			req.ProxyQueue.AddBusyWorker(-1)
		}
		if !req.Retrying {
			req.Done <- 0
		}
		if e := recover(); e != nil {
			klog.Warningf("serve proxy crashed , err: %s , \ntrace:%s", e, string(debug.Stack()))
		}
	}()

	if enableWorkerCount {
		req.ProxyQueue.AddBusyWorker(1)
	}
	req.Retrying = false

	if structs.IsWebsocketRequest(req.Req) {
		hs.handleWebsocket(req)
	} else {
		hs.handleHttp(req)
	}
}

func (hs *LlmGatewayService) QueueReadHandle(item utils.Item, queue *utils.ProxyQueue) {
	req := item.(*structs.Request)
	req.ProxyQueue = queue
	req.DeQueueTime = time.Now()

	req.ProxyQueue.AddWaitScheduler(1)
	klog.V(3).Infof("get next token with model [%v]", req.Model)
	// GetNextTokensByModel may wait until an available service endpoint

	if hs.config.SeparatePDSchedule {
		req.ScheduleStage = consts.PrefillInferMode
	}
	nextTokens, err := hs.sr.GetNextTokensByRouter(req)
	if err != nil {
		if fallbackTokens, _err := hs.sr.GetFallbackTokens(req); _err == nil {
			nextTokens = fallbackTokens
		} else {
			req.ProxyQueue.AddWaitScheduler(-1)

			klog.Warningf("[%s] could not get a backend endpoint: %v", req.Id, err)
			req.StatusCode = http.StatusBadRequest
			switch err {
			case consts.ErrorNoAvailableEndpoint:
				req.StatusCode = http.StatusTooManyRequests
				http.Error(req.Writer, "too many requests", http.StatusTooManyRequests)
			case consts.ErrorEndpointNotFound:
				req.StatusCode = http.StatusNotFound
				http.Error(req.Writer, "no backend endpoint found", http.StatusNotFound)
			case consts.ErrorInvalidModel:
				req.StatusCode = http.StatusNotFound
				http.Error(req.Writer, "invalid model name", http.StatusNotFound)
			default:
				req.StatusCode = http.StatusBadRequest
				http.Error(req.Writer, err.Error(), http.StatusBadRequest)
			}
			hs.LogRequestAccess(req)
			req.Done <- 0
			return
		}
	}
	req.ProxyQueue.AddWaitScheduler(-1)

	if len(nextTokens.Tokens) > 0 {
		// pd split mode: token indicate the prefill node and token2 is decode node;
		// normal mode: token indicate the inference node and token2 must be nil.
		token, token2 := hs.filterModelTokens(nextTokens, req.Model)
		req.Token = token
		req.SecondToken = token2
		req.BalanceTime = time.Now()
	} else {
		req.ExternalEp = nextTokens.ExternalEp
	}

	go hs.handleProxy(req, true)
}

func (hs *LlmGatewayService) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (hs *LlmGatewayService) parseRequest(req *structs.Request) error {
	if len(req.Data) == 0 {
		return nil
	}

	switch req.Req.URL.Path {
	case "/v1/chat/completions":
		req.Protocol = protocol.ProtocolChat
	case "/v1/completions":
		req.Protocol = protocol.ProtocolCompletions
	}

	c := hs.config

	var p fastjson.Parser
	v, err := p.ParseBytes(req.Data)
	if err != nil {
		klog.Warningf("could not parse request: %v", err)
		return err
	}
	req.ReqObj = v

	if model := v.GetStringBytes("model"); model != nil {
		req.Model = string(model)
	}

	req.Stream = v.GetBool("stream") || (v.Get("stream") != nil && strings.ToLower(string(v.GetStringBytes("stream"))) == "true")
	req.InferStream = req.Stream
	klog.V(4).Infof("request : %s, stream: %v", v, req.Stream)

	if prompt := v.GetStringBytes("prompt"); prompt != nil {
		req.Prompt = prompt
	}

	if msgs := v.Get("messages"); msgs != nil && msgs.Type() == fastjson.TypeArray {
		if strings.HasSuffix(msgs.String(), "]") {
			req.Prompt = []byte(msgs.String())[:len(msgs.String())-1]
		} else {
			req.Prompt = []byte(msgs.String())
		}
	}

	if c.UseTokenizerProcessor() {
		processor, err := request_processor.NewRequestProcessor(c.TokenizerName, c.TokenizerPath, c.TokenizerMode, c.ToolCallParser)
		if err != nil {
			klog.Errorf("create tokenizer processor error: %v", err)
			return err
		}
		if err := processor.Preprocess(req); err == nil {
			req.RequestProcessor = processor
		} else {
			klog.Errorf("req %v failed to postprocess, err: %v", req.Id, err)
			return err
		}
	}
	return nil
}

func (hs *LlmGatewayService) preProcessRequest(req *structs.Request) error {
	if structs.IsWebsocketRequest(req.Req) {
		return nil
	}

	var (
		data []byte
		err  error
	)
	if data, err = io.ReadAll(req.Req.Body); err != nil {
		klog.Warningf("could not read body: %v", err)
		return err
	}

	req.RawData = data
	req.Data = data

	tStart := time.Now()
	err = hs.parseRequest(req)
	tCost := time.Since(tStart)
	if tCost > time.Millisecond {
		klog.V(3).Infof("parse request cost: %vms", tCost.Milliseconds())
	}
	return err
}

func (hs *LlmGatewayService) HandleRequest(w http.ResponseWriter, r *http.Request) {
	req := structs.NewRequest(r, w)
	err := hs.preProcessRequest(req)
	if err != nil {
		klog.Errorf("req %v failed to preprocess, err: %v", req.Id, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Try to mirror the request if traffic_mirror is enabled
	if hs.EnableMirrorRequest() {
		hs.MirrorRequest(req)
	}

	hs.LogRequestInput(req)

	modelLabel := structs.Label{Name: "model", Value: ""}
	if hs.config.ServerlessMode {
		modelLabel = structs.Label{Name: "model", Value: req.Model}
	}
	req.DefaultLabels = req.DefaultLabels.Append(modelLabel)

	req.EnQueueTime = time.Now()
	modelQueue := hs.mmpq.GetOrCreateModelProxyQueue(req.Model)
	ok := modelQueue.Enqueue(req)
	if !ok {
		klog.Warningf("too many requests, the buffer queue overflow")
		http.Error(w, "requests buffer queue overflow", http.StatusTooManyRequests)
		return
	}
	<-req.Done
}

func (hs *LlmGatewayService) Run() {
	// Create a new Router for handling routes
	router := mux.NewRouter()

	// Register default routes
	router.HandleFunc("/healthz", hs.healthz)
	// Register batch API routes if batch service is available
	if hs.batchService != nil {
		hs.batchService.RegisterRoutes(router)
	}
	// Default to root handler
	router.PathPrefix("/").HandlerFunc(hs.HandleRequest)

	address := fmt.Sprintf("%s:%d", hs.config.Host, hs.config.Port)
	klog.Infof("http service start listen on %s", address)
	klog.Fatal(http.ListenAndServe(address, router))
}

func recordStatusValues(pq *utils.ProxyQueue, model string) {
	pendingRequests := float32(pq.Length() + pq.WaitSchedules())
	modelLabels := []structs.Label{{Name: "model", Value: model}}
	metrics.StatusValue("gateway_pending_requests", modelLabels).Set(pendingRequests)

	requests := pendingRequests + float32(pq.BusyWorkers())
	metrics.StatusValue("gateway_requests", modelLabels).Set(requests)
}

func (hs *LlmGatewayService) StartMetricRecord() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("submit metric loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go hs.StartMetricRecord()
		}
	}()

	for {
		time.Sleep(consts.MetricRecordDuration)
		modelQueues := hs.mmpq.GetModelProxyQueue()
		for model, pq := range modelQueues {
			recordStatusValues(pq, model)
		}
	}
}

func (hs *LlmGatewayService) Start() (err error) {
	go hs.Run()
	go hs.StartMetricRecord()

	// Start batch service reactors if batch service is available
	if hs.batchService != nil {
		// Create a context for the batch service
		ctx := context.Background()
		go hs.batchService.Start(ctx)
	}

	return nil
}

func (hs *LlmGatewayService) LogRequestInput(req *structs.Request) {
	if hs.logInputEnabled {
		utils.AsyncLog("Received request [%s] %s", req.Id, string(req.RawData))
	} else {
		utils.LogAccess("Received request [%s]", req.Id)
	}
}

func (hs *LlmGatewayService) LogRequestAccess(req *structs.Request) {
	if req.Logged {
		return
	}
	req.ProcessTime = time.Now()
	if req.StatusCode == http.StatusOK {
		if req.ExternalEp != nil {
			metrics.Latency("external_llm_response_time", req.DefaultLabels).Add(time.Since(req.HandleTime).Milliseconds())
		} else {
			metrics.Latency("llm_response_time", req.DefaultLabels).Add(time.Since(req.HandleTime).Milliseconds())
		}
	}
	requestsLabel := req.DefaultLabels.Append(structs.Label{Name: "status_code", Value: strconv.Itoa(req.StatusCode)})
	if req.ExternalEp != nil {
		metrics.Counter("external_llm_requests", requestsLabel).IncrByOne()
	} else {
		metrics.Counter("llm_requests", requestsLabel).IncrByOne()
	}

	var endpoint string
	if req.Token != nil {
		endpoint = req.Token.Endpoint.String()
	}
	if req.SecondToken != nil {
		endpoint = endpoint + "|" + req.SecondToken.Endpoint.String()
	}
	if req.ExternalEp != nil {
		endpoint = req.ExternalEp.String()
	}

	utils.LogAccess("Request completed [%s] %d,%s,%s,%s;%s;prompt_len:%d(%d),gen_token_len:%d,queue:%d,work:%d,sch:%d,nresp:%d;retry:%d,model:%s",
		req.Id,
		req.StatusCode,
		req.Req.Method,
		endpoint,
		req.Req.URL.String(),
		req.SprintDuration(),
		len(req.Prompt),
		req.InputTokensLen,
		req.OutputTokensLen,
		req.ProxyQueue.Length(),
		req.ProxyQueue.BusyWorkers(),
		req.ProxyQueue.WaitSchedules(),
		hs.getTokenFilterCount(req.Model),
		req.ReScheduleCount,
		req.Model)

	req.Logged = true
}
