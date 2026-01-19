package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/batch"
	"easgo/pkg/llm-gateway/consts"
	balancer "easgo/pkg/llm-gateway/load-balancer"
	"easgo/pkg/llm-gateway/logging"
	"easgo/pkg/llm-gateway/metrics"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/service/handler"
	"easgo/pkg/llm-gateway/service/mirror"
	"easgo/pkg/llm-gateway/service/queue"
	"easgo/pkg/llm-gateway/service/router"
	"easgo/pkg/llm-gateway/types"
)

// LlmGatewayService is the main service for LLM gateway that handles all inference requests
// It provides core features including load balancing, request queuing, and batch processing
type LlmGatewayService struct {
	// Gateway configuration
	config *options.Config

	// Request buffer queue for traffic control
	bufferQueue *queue.QueueWorkerPool
	// Request handler for parsing and processing requests
	reqHandler handler.RequestHandler

	balancer balancer.Balancer
	// Service router for request routing and load balancing
	router *router.ServiceRouter
	// Batch service for handling batch inference tasks
	batchService *batch.BatchService

	// Traffic mirror component
	mirror *mirror.Mirror

	// a shared HTTP client for simple proxying to backend endpoints
	simpleClient *http.Client

	// Current number of requests being processed (atomic operation)
	numReqs atomic.Int32

	// Whether to enable input logging
	logInputEnabled bool
}

// NewGatewayService creates a new gateway service instance
// Parameters:
//   - c: Gateway configuration
//
// Returns:
//   - *LlmGatewayService: Gateway service instance, or nil if initialization fails
func NewGatewayService(c *options.Config) *LlmGatewayService {
	lgs := &LlmGatewayService{
		config:          c,
		logInputEnabled: c.EnableLogInput,
		simpleClient:    &http.Client{Timeout: 30 * time.Second},
	}

	// Create request buffer queue for traffic control and request scheduling
	lgs.bufferQueue = queue.NewQueueWorkerPool(c.MaxQueueSize, c.WaitQueueThreads)
	lgs.bufferQueue.Start()

	// Create request handler for parsing OpenAI format requests
	reqHandler, handlerErr := handler.BuildHandler("openai", c)
	if handlerErr != nil {
		klog.Fatalf("failed to create request handler: %v", handlerErr)
	}
	lgs.reqHandler = reqHandler

	// Create load balancer and service router
	var lb balancer.Balancer
	if len(c.LocalTestIPs) == 0 {
		lb = balancer.NewCompositeBalancer(c)
	} else {
		// Local debug mode: use fixed test IP list
		uri := fmt.Sprintf("llm+endpoints://%s", c.LocalTestIPs)
		r, err := resolver.BuildLlmResolver(uri, resolver.BuildArgs{"role": types.InferRoleNormal.String()})
		if err != nil {
			klog.Errorf("create resolver failed: %v", err)
			return nil
		}
		lb = balancer.NewRoundRobinBalancer(r)
	}
	lgs.balancer = lb
	lgs.router = router.NewServiceRouter(c.RoutePolicy, c.RouteConfigRaw)

	// Create traffic mirror component
	lgs.mirror = mirror.NewMirror(c)

	// Initialize batch service if OSS path is configured
	var err error
	if c.BatchOSSPath != "" {
		lgs.batchService, err = batch.NewBatchService(c)
		if err != nil {
			klog.Errorf("Failed to initialize batch service: %v", err)
			// Continue without batch service if initialization fails
		}
	}

	return lgs
}

// healthz is the health check endpoint for K8s liveness/readiness probes
func (lgs *LlmGatewayService) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// convertErrorResponse converts error response message to HTTP status code and response body
// Parameters:
//   - msg: Response message containing error information
//
// Returns:
//   - int: HTTP status code
//   - string: Response body in JSON format
func (lgs *LlmGatewayService) convertErrorResponse(msg *types.ResponseMsg) (int, []byte) {
	switch msg.Err {
	case consts.ErrorNoAvailableEndpoint:
		return http.StatusTooManyRequests, []byte(`{"error": {"code": 429, "message": "too many requests"}}`)
	case consts.ErrorEndpointNotFound:
		return http.StatusNotFound, []byte(`{"error": {"code": 404, "message": "no inference worker found"}}`)
	case consts.ErrorBackendBadRequest:
		// Parse backend error response to extract specific error details
		var response types.ErrorResponse
		err := json.Unmarshal([]byte(msg.Message), &response)
		if err != nil {
			klog.Warningf("invalid bad backend bad request: %s", msg.Message)
			return http.StatusBadRequest, []byte("{}")
		}
		if json.Valid([]byte(response.Error)) {
			return response.Code, []byte(response.Error)
		} else {
			return response.Code, []byte(fmt.Sprintf(`{"error": {"code": %d, "message": "%s"}}`, response.Code, response.Error))
		}

	default:
		if msg.Err == nil {
			return http.StatusOK, []byte(msg.Message)
		} else {
			return http.StatusBadRequest, []byte(msg.Err.Error())
		}
	}
}

// writeResponse writes non-streaming response to the client
// Parameters:
//   - req: Request context
//   - response: Response message
func (lgs *LlmGatewayService) writeResponse(req *types.RequestContext, response *types.ResponseMsg) {
	var (
		code int
		body []byte
	)
	if response.Err == nil {
		code, body = http.StatusOK, response.Message
	} else {
		code, body = lgs.convertErrorResponse(response)
	}
	req.HttpRequest.StatusCode = code
	writer := req.HttpRequest.Writer
	// Note: Must set header before WriteHeader
	writer.Header().Set("content-type", "application/json")
	writer.WriteHeader(code)
	writer.Write([]byte(body))
}

// writeStreamResponse writes streaming response in SSE (Server-Sent Events) format
// Parameters:
//   - req: Request context
//   - response: Response message
//   - headerResponded: Whether response header has been sent
//
// Returns:
//   - bool: Whether the connection should be closed
func (lgs *LlmGatewayService) writeStreamResponse(req *types.RequestContext, response *types.ResponseMsg, headerResponded bool) bool {
	writer := req.HttpRequest.Writer

	// Error response: append [DONE] marker and close the connection
	if response.Err != nil {
		if !headerResponded {
			code, body := lgs.convertErrorResponse(response)
			body = []byte("data: " + string(body) + "\n\ndata: [DONE]\n\n")
			req.HttpRequest.StatusCode = code
			writer.Header().Set("content-type", "text/event-stream; charset=utf-8")
			writer.WriteHeader(code)
			writer.Write([]byte(body))
		}
		// If header already sent, just close the connection
		return true
	}

	// Normal response: send SSE formatted data
	if !headerResponded {
		writer.Header().Set("content-type", "text/event-stream; charset=utf-8")
		req.HttpRequest.StatusCode = http.StatusOK
		writer.WriteHeader(http.StatusOK)
	}

	body := []byte("data: " + string(response.Message) + "\n\n")
	writer.Write([]byte(body))
	// Flush immediately to ensure data is sent to client in real-time
	writer.(http.Flusher).Flush()

	return false
}

// writeResponseUntilDone continuously writes responses until completion
// Supports both streaming and non-streaming modes, and handles client disconnection
// Parameters:
//   - reqCtx: Request context
func (lgs *LlmGatewayService) writeResponseUntilDone(reqCtx *types.RequestContext) {
	writer := reqCtx.HttpRequest.Writer
	for {
		select {
		case response, ok := <-reqCtx.ResponseChan:
			if !ok {
				// Response channel closed
				if !reqCtx.HttpRequest.HeaderResponded {
					klog.Warningf("request %s response channel closed before response sent", reqCtx.Id)
					writer.WriteHeader(http.StatusBadRequest)
				}
				return
			}
			stream := reqCtx.LLMRequest.ClientStream
			if stream {
				needClose := lgs.writeStreamResponse(reqCtx, response, reqCtx.HttpRequest.HeaderResponded)
				reqCtx.HttpRequest.HeaderResponded = true
				if needClose {
					return
				}
			} else {
				lgs.writeResponse(reqCtx, response)
				return
			}
		case <-reqCtx.Context.Done():
			// Client disconnected or request timeout
			klog.Infof("request %s disconnected by client", reqCtx.Id)
			reqCtx.HttpRequest.StatusCode = 499
			return
		}
	}
}

func (lgs *LlmGatewayService) externalRouteRequest(reqCtx *types.RequestContext, dst *router.RouteEndpoint) {
	originURL := reqCtx.HttpRequest.Request.URL.String()
	url := dst.JoinURL(originURL)
	SimpleHTTPProxy(lgs.simpleClient, url, reqCtx.HttpRequest.Writer, reqCtx.HttpRequest.Request)
	reqCtx.HttpRequest.HeaderResponded = true
	close(reqCtx.ResponseChan)
}

// dispatchRequest routes the request and dispatches it to appropriate handler
// It supports three routing types:
//   - Internal: schedule to internal workers via balancer
//   - External: proxy to external service
//   - Fallback: use fallback service when no route matches
func (lgs *LlmGatewayService) dispatchRequest(reqCtx *types.RequestContext) {
	// If the router policy is enabled, the router will be used first.
	dst, rType := lgs.router.Route(reqCtx)
	switch rType {
	case router.RouteInternal:
		// schedule the request
		schResult, err := lgs.balancer.Get(reqCtx)
		if err != nil {
			reqCtx.ResponseChan <- &types.ResponseMsg{Err: err, Message: []byte(err.Error())}
			return
		}
		klog.V(3).Infof("request [%s] scheduled to %s", reqCtx.Id, schResult.String())
		reqCtx.ScheduleCtx.ScheduleResults = schResult
		go lgs.reqHandler.Handle(reqCtx)
	case router.RouteExternal:
		// Match external route
		go lgs.externalRouteRequest(reqCtx, dst)
	case router.RouteUnknown:
		// Not match external route, try fallback
		fdst, err := lgs.router.Fallback(reqCtx)
		if err != nil {
			reqCtx.ResponseChan <- &types.ResponseMsg{Err: err, Message: []byte(err.Error())}
			return
		}
		go lgs.externalRouteRequest(reqCtx, fdst)
	}
}

func (lgs *LlmGatewayService) scheduleMode() types.ScheduleMode {
	if !lgs.config.IsPDSplitMode() {
		return types.ScheduleModeNormal
	}
	if lgs.config.SeparatePDSchedule {
		return types.ScheduleModePDStaged
	} else {
		return types.ScheduleModePDBatch
	}
}

// balancerLifecycleHandler implements the RequestLifecycleHandler interface using a balancer
type balancerLifecycleHandler struct {
	balancer balancer.Balancer
}

// ScheduleDecode schedules the request for decoding using the balancer
func (h *balancerLifecycleHandler) ScheduleDecode(req *types.RequestContext) (types.ScheduledResult, error) {
	if req.ScheduleCtx.ScheduleMode != types.ScheduleModePDStaged {
		klog.Warningf("request %s is not in staged schedule mode, decode does not need to be scheduled separately.", req.Id)
		return nil, fmt.Errorf("not in staged schedule mode")
	}
	req.ScheduleCtx.InferStage = types.InferStageDecode
	results, err := h.balancer.Get(req)
	if err != nil {
		return nil, err
	}
	schResult := req.ScheduleCtx.ScheduleResults
	req.ScheduleCtx.ScheduleResults = append(schResult, results...)
	return results, nil
}

// OnPostPrefill is called after prefill stage completes
// Mainly used for resource cleanup or state transition after the prefill phase is completed
func (h *balancerLifecycleHandler) OnPostPrefill(req *types.RequestContext) {
	klog.V(3).Infof("[%s] Post-prefill lifecycle hook triggered", req.Id)
	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker != nil {
		h.balancer.Release(req, pWorker)
	}
	req.ScheduleCtx.InferStage = types.InferStageDecode
}

// OnPostRequest is called after the entire request completes
func (h *balancerLifecycleHandler) OnPostRequest(req *types.RequestContext) {
	klog.V(3).Infof("[%s] Post-request lifecycle hook triggered", req.Id)
	// For non-PD mode, resources also need to be released at the end of the request.
	nWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)
	if nWorker != nil {
		h.balancer.Release(req, nWorker)
	}
	// For PD separated mode, only decode resources need to be released
	dWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleDecode)
	if dWorker != nil {
		h.balancer.Release(req, dWorker)
	}
}

// HandleOpenAIRequest is the main entry point for handling LLM inference requests
// It handles request parsing, queue scheduling, response processing, and logging
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
func (lgs *LlmGatewayService) HandleOpenAIRequest(w http.ResponseWriter, r *http.Request) {
	// Increment request counter for monitoring and traffic control
	lgs.numReqs.Add(1)
	defer lgs.numReqs.Add(-1)

	ctx := r.Context()
	reqCtx := types.NewRequestContext(ctx, r, w)
	// Set schedule mode based on configuration
	scheduleMode := lgs.scheduleMode()
	reqCtx.ScheduleCtx.ScheduleMode = scheduleMode
	reqCtx.ScheduleCtx.InferStage = types.InferStagePrefill
	reqCtx.SetLifecycleHandler(&balancerLifecycleHandler{balancer: lgs.balancer})

	// Parse and validate OpenAI format request parameters
	if err := lgs.reqHandler.ParseRequest(reqCtx); err != nil {
		logging.Logf("Received request [%s]: %v", reqCtx.Id, err)
		http.Error(w, "Invalid OpenAI request", http.StatusBadRequest)
		return
	}
	// Log request input
	lgs.LogRequestInput(reqCtx)

	// Mirror the request to another target if traffic mirroring is enabled
	if lgs.mirror.Enabled() {
		lgs.mirror.TryMirror(reqCtx)
	}

	// Request handler does not directly process HTTP return messages
	// All messages are returned through responseChan for unified message handling
	responseChan := make(chan *types.ResponseMsg, 100)
	reqCtx.ResponseChan = responseChan

	// Enter buffer queue, then perform preprocessing and scheduling
	reqCtx.RequestStats.EnQueueTime = time.Now()
	lgs.bufferQueue.Dispatch(func() {
		reqCtx.RequestStats.DeQueueTime = time.Now()
		lgs.dispatchRequest(reqCtx)
		reqCtx.RequestStats.BalanceTime = time.Now()
	}, false)

	// Wait for and write responses (supports both streaming and non-streaming)
	lgs.writeResponseUntilDone(reqCtx)

	// Log request access logs and metrics
	lgs.LogRequestAccess(reqCtx)
}

// HandleSimpleRequest handles simple requests (e.g., /v1/models)
// It forwards the request to a backend endpoint and returns the response to the client
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
func (lgs *LlmGatewayService) HandleSimpleRequest(w http.ResponseWriter, r *http.Request) {
	// For this scenario, create a request context but disable scheduling
	reqCtx := types.NewRequestContext(r.Context(), r, w)
	reqCtx.ScheduleCtx.NeedSchedule = false

	// Get an available endpoint from the balancer
	schResult, err := lgs.balancer.Get(reqCtx)
	if err != nil || len(schResult) == 0 {
		klog.Errorf("Failed to get endpoint: %v", err)
		http.Error(w, "failed to get endpoint", http.StatusServiceUnavailable)
		return
	}
	// Build the target URL with http:// scheme (endpoint format is host:port)
	endpoint := schResult[0].Endpoint
	url := fmt.Sprintf("http://%s%s", endpoint, r.URL.Path)
	if r.URL.RawQuery != "" {
		url = fmt.Sprintf("%s?%s", url, r.URL.RawQuery)
	}

	// Forward the request to the backend endpoint
	SimpleHTTPProxy(lgs.simpleClient, url, w, r)
}

// Run starts the HTTP server and listens for requests
// This method blocks until the server is shut down
func (lgs *LlmGatewayService) Run() {
	// Create router for handling different HTTP paths
	router := mux.NewRouter()

	// Register health check route
	router.HandleFunc("/healthz", lgs.healthz)
	// Register batch API routes if batch service is available
	if lgs.batchService != nil {
		lgs.batchService.RegisterRoutes(router)
	}
	// handle all LLM inference requests
	router.HandleFunc("/v1/chat/completions", lgs.HandleOpenAIRequest)
	router.HandleFunc("/v1/completions", lgs.HandleOpenAIRequest)
	router.HandleFunc("/v1/models", lgs.HandleSimpleRequest)

	address := fmt.Sprintf("%s:%d", lgs.config.Host, lgs.config.Port)
	klog.Infof("LLM Gateway start listen on %s", address)
	klog.Fatal(http.ListenAndServe(address, router))
}

// StartMetricRecord starts the metric recording loop
// Periodically collects and reports gateway operational metrics (e.g., pending requests, active requests)
func (lgs *LlmGatewayService) StartMetricRecord() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("submit metric loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
		}
	}()

	for {
		time.Sleep(consts.MetricRecordDuration)
		// Calculate pending requests: queued + being scheduled
		pendingRequests := float32(lgs.bufferQueue.Length() + lgs.bufferQueue.BusyWorkers())
		metrics.StatusValue("gateway_pending_requests", []metrics.Label{}).Set(pendingRequests)
		// Record current total active requests
		requests := float32(lgs.numReqs.Load())
		metrics.StatusValue("gateway_requests", []metrics.Label{}).Set(requests)
	}
}

// Start starts all components of the gateway service
// Including HTTP server, metric recorder, and batch service (if available)
// Returns:
//   - error: Startup error, currently always returns nil
func (lgs *LlmGatewayService) Start() (err error) {
	// Start HTTP server (asynchronously)
	go lgs.Run()
	// Start metric recording loop (asynchronously)
	go lgs.StartMetricRecord()

	// Start batch service reactors if batch service is available
	if lgs.batchService != nil {
		// Create a context for the batch service
		ctx := context.Background()
		go lgs.batchService.Start(ctx)
	}

	return nil
}

// LogRequestInput logs request input
// Logs complete request data based on configuration
// Parameters:
//   - req: Request context
func (lgs *LlmGatewayService) LogRequestInput(req *types.RequestContext) {
	if lgs.logInputEnabled {
		logging.AsyncLogf("Received request [%s] %s", req.Id, string(req.LLMRequest.RawData))
	} else {
		logging.Logf("Received request [%s]", req.Id)
	}
}

// LogRequestAccess logs request access logs and metrics
// Including latency, status code, token count, queue status, etc.
// Parameters:
//   - req: Request context
func (lgs *LlmGatewayService) LogRequestAccess(req *types.RequestContext) {
	stats := req.RequestStats
	httpReq := req.HttpRequest
	stats.ProcessTime = time.Now()
	// Only record latency metrics for successful requests
	if httpReq.StatusCode == http.StatusOK {
		metrics.Latency("llm_response_time", stats.DefaultLabels).Add(time.Since(stats.HandleTime).Milliseconds())
	}
	// Record request counter metric with status code label
	requestsLabel := stats.DefaultLabels.Append(metrics.Label{Name: "status_code", Value: strconv.Itoa(httpReq.StatusCode)})
	metrics.Counter("llm_requests", requestsLabel).IncrByOne()

	metrics.Latency("llm_ttft", stats.DefaultLabels).Add(stats.TTFT())
	metrics.Latency("llm_tpot", stats.DefaultLabels).AddMany(req.RequestStats.ITLs)

	// Calculate request counts at different stages
	queueSize := lgs.bufferQueue.Length()                     // Requests waiting in queue
	schSize := lgs.bufferQueue.BusyWorkers()                  // Requests being scheduled
	workSize := int(lgs.numReqs.Load()) - queueSize - schSize // Requests being inferred

	// Log detailed request completion information
	logging.Logf("Request completed [%s] status:%d,method:%s,schedule:%s,url:%s;%s;input_tokens:%d,output_tokens:%d;queue:%d,sch:%d,work:%d;retry:%d,model:%s",
		req.Id,
		httpReq.StatusCode,
		httpReq.Request.Method,
		req.ScheduleCtx.ScheduleResults.String(),
		httpReq.Request.URL.String(),
		stats.String(),
		stats.InputTokensLen,
		stats.OutputTokensLen,
		queueSize,
		schSize,
		workSize,
		stats.RetryCount,
		req.LLMRequest.Model)
}
