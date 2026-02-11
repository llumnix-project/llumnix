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

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/batch"
	"llm-gateway/pkg/consts"
	balancer "llm-gateway/pkg/load-balancer"
	"llm-gateway/pkg/logging"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/metrics"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/service/handler"
	"llm-gateway/pkg/service/mirror"
	"llm-gateway/pkg/service/queue"
	"llm-gateway/pkg/service/router"
	"llm-gateway/pkg/types"
)

// LlmGatewayService is the main service for LLM gateway that handles all inference requests
// It provides core features including load balancing, request queuing, and batch processing
type LlmGatewayService struct {
	// Gateway configuration
	config *options.Config

	// Request buffer queue for traffic control
	bufferQueue *queue.QueueWorkerPool
	// Request handler for parsing and processing requests
	openaiHandler    handler.RequestHandler
	anthropicHandler handler.RequestHandler

	// Request state tracker for tracking request states
	reqStateTracker *lrs.RequestStateTracker
	// Load balancer for distributing requests to backend services
	balancer balancer.Balancer
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
		reqStateTracker: lrs.NewRequestStateTracker(c),
	}

	// Create request buffer queue for traffic control and request scheduling
	lgs.bufferQueue = queue.NewQueueWorkerPool(c.MaxQueueSize, c.WaitQueueThreads)
	lgs.bufferQueue.Start()

	// Create request handler for parsing different format requests
	lgs.BuildHandler()

	// Create load balancer
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

// BuildHandler creates request handlers for different formats
func (lgs *LlmGatewayService) BuildHandler() {
	// Create request handler for parsing OpenAI format requests
	h, err := handler.BuildHandler("openai", lgs.config)
	if err != nil {
		klog.Fatalf("failed to create request handler: %v", err)
	}
	lgs.openaiHandler = h

	// Create request handler for parsing Anthropic format requests
	h, err = handler.BuildHandler("anthropic", lgs.config)
	if err != nil {
		klog.Fatalf("failed to create request handler: %v", err)
	}
	lgs.anthropicHandler = h
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
		return http.StatusServiceUnavailable, []byte(`{"error": {"code": 503, "message": "no available inference worker"}}`)
	case consts.ErrorRateLimitExceeded:
		return http.StatusTooManyRequests, []byte(`{"error": {"code": 429, "message": "rate limit exceeded"}}`)
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
			return http.StatusBadRequest, []byte(fmt.Sprintf(`{"error": {"code": 400, "message": "%s"}}`, msg.Err.Error()))
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
			req.HttpRequest.StatusCode = code
			writer.Header().Set("content-type", "application/json")
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

	writer.Write([]byte(response.Message))
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
				klog.V(3).Infof("request %s write response end", reqCtx.Id)
				return
			}
			stream := reqCtx.ClientStream()
			if stream {
				klog.V(3).Infof("request %s write stream response", reqCtx.Id)
				needClose := lgs.writeStreamResponse(reqCtx, response, reqCtx.HttpRequest.HeaderResponded)
				reqCtx.HttpRequest.HeaderResponded = true
				if needClose {
					return
				}
			} else {
				klog.V(3).Infof("request %s write response", reqCtx.Id)
				lgs.writeResponse(reqCtx, response)
				return
			}
		case <-reqCtx.Context.Done():
			// Client disconnected or request timeout
			klog.Errorf("request %s disconnected by client", reqCtx.Id)
			reqCtx.HttpRequest.StatusCode = 499
			return
		}
	}
}

func (lgs *LlmGatewayService) externalRouteRequest(reqCtx *types.RequestContext, dst *router.RouteEndpoint) {
	defer close(reqCtx.ResponseChan)

	originURL := reqCtx.HttpRequest.Request.URL.String()
	url := dst.JoinURL(originURL)
	SimpleHTTPProxy(lgs.simpleClient, url, reqCtx.HttpRequest.Writer, reqCtx.HttpRequest.Request)
	reqCtx.HttpRequest.HeaderResponded = true
}

func (lgs *LlmGatewayService) internalRouteRequest(reqCtx *types.RequestContext, handler handler.RequestHandler) {
	defer close(reqCtx.ResponseChan)
	reqCtx.TriggerPreRequest()
	handler.Handle(reqCtx)
	reqCtx.TriggerPostRequest()
}

// dispatchRequest routes the request and dispatches it to appropriate handler
// It supports three routing types:
//   - Internal: schedule to internal workers via balancer
//   - External: proxy to external service
//   - Fallback: use fallback service when no route matches
func (lgs *LlmGatewayService) dispatchRequest(reqCtx *types.RequestContext, handler handler.RequestHandler) {
	// If the router policy is enabled, the router will be used first.
	serviceRouter := router.GetServiceRouter()
	dst, rType := serviceRouter.Route(reqCtx)
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
		go lgs.internalRouteRequest(reqCtx, handler)
	case router.RouteExternal:
		// Match external route
		go lgs.externalRouteRequest(reqCtx, dst)
	case router.RouteUnknown:
		// Not match external route, try fallback
		fdst, err := serviceRouter.Fallback(reqCtx)
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

// PDSeparateSchedulerImpl implements the PDSeparateScheduler interface using a balancer.
// In PD-Separate (Prefill-Decode Separate) mode, prefill and decode stages run on different
// workers. This scheduler handles the decode-stage scheduling after prefill completes.
//
// NOTE: Only effective when ScheduleMode is ScheduleModePDStaged.
type PDSeparateSchedulerImpl struct {
	balancer balancer.Balancer
}

// ScheduleDecode schedules the request for decoding using the balancer
func (h *PDSeparateSchedulerImpl) ScheduleDecode(req *types.RequestContext) (types.ScheduledResult, error) {
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

// RequestLifecycleHooksImpl implements the RequestLifecycleHooks interface using a balancer.
type RequestLifecycleHooksImpl struct {
	reqStateTracker *lrs.RequestStateTracker
	balancer        balancer.Balancer
	lastTime        time.Time
	firstDecode     bool
}

func (h *RequestLifecycleHooksImpl) OnPreRequest(req *types.RequestContext) {
	klog.V(3).Infof("[%s] Pre-request hook triggered", req.Id)

	// init state variables
	h.firstDecode = true
}

// OnPostPrefillStream is called after prefill stage completes
// Mainly used for resource cleanup or state transition after the prefill phase is completed
func (h *RequestLifecycleHooksImpl) OnPostPrefillStream(req *types.RequestContext) {
	klog.V(3).Infof("[%s] Post-prefill hook triggered", req.Id)
	h.lastTime = time.Now()
	req.RequestStats.FirstTime = h.lastTime

	h.reqStateTracker.ReportPrefillComplete(req)

	pWorker := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRolePrefill)
	if pWorker != nil {
		h.balancer.Release(req, pWorker)
	}

	req.ScheduleCtx.InferStage = types.InferStageDecode
}

func (h *RequestLifecycleHooksImpl) OnPostDecodeStreamChunk(req *types.RequestContext) {
	klog.V(3).Infof("[%s] Post-decode hook triggered", req.Id)
	req.RequestStats.ITLs = append(req.RequestStats.ITLs, time.Since(h.lastTime).Milliseconds())
	h.lastTime = time.Now()

	if h.firstDecode {
		h.reqStateTracker.CreateRequestState(req)
		h.firstDecode = false
	} else {
		h.reqStateTracker.UpdateRequestState(req)
	}
}

// OnPostRequest is called after the entire request completes
func (h *RequestLifecycleHooksImpl) OnPostRequest(req *types.RequestContext) {
	klog.V(3).Infof("[%s] Post-request hook triggered", req.Id)

	// Delete request state after request completes
	h.reqStateTracker.DeleteRequestState(req.Id)

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

// HandleAPIEntry is the main entry point for handling LLM inference requests
// It handles request parsing, queue scheduling, response processing, and logging
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
func (lgs *LlmGatewayService) HandleAPIEntry(w http.ResponseWriter, r *http.Request, handler handler.RequestHandler) {
	// Increment request counter for monitoring and traffic control
	lgs.numReqs.Add(1)
	defer lgs.numReqs.Add(-1)

	ctx := r.Context()
	reqCtx := types.NewRequestContext(ctx, r, w)
	// Set schedule mode based on configuration
	scheduleMode := lgs.scheduleMode()
	reqCtx.ScheduleCtx.ScheduleMode = scheduleMode
	reqCtx.ScheduleCtx.InferStage = types.InferStagePrefill
	// Inject scheduler and hooks for lifecycle of the request
	schedulerImpl := &PDSeparateSchedulerImpl{balancer: lgs.balancer}
	reqCtx.SetPDSeparateScheduler(schedulerImpl)
	hooksImpl := &RequestLifecycleHooksImpl{reqStateTracker: lgs.reqStateTracker, balancer: lgs.balancer}
	reqCtx.SetLifecycleHooks(hooksImpl)
	// Set request type based on handler name
	reqCtx.RequestType = handler.Name()

	// Parse and validate OpenAI format request parameters
	if err := handler.ParseRequest(reqCtx); err != nil {
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
		lgs.dispatchRequest(reqCtx, handler)
		reqCtx.RequestStats.BalanceTime = time.Now()
	}, false)

	// Wait for and write responses (supports both streaming and non-streaming)
	lgs.writeResponseUntilDone(reqCtx)

	// Log request access logs and metrics
	lgs.LogRequestAccess(reqCtx)
}

func (lgs *LlmGatewayService) HandleOpenAIRequest(w http.ResponseWriter, r *http.Request) {
	lgs.HandleAPIEntry(w, r, lgs.openaiHandler)
}

func (lgs *LlmGatewayService) HandleAnthropicRequest(w http.ResponseWriter, r *http.Request) {
	lgs.HandleAPIEntry(w, r, lgs.anthropicHandler)
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
	// handle OpenAI inference requests
	router.HandleFunc("/v1/chat/completions", lgs.HandleOpenAIRequest)
	router.HandleFunc("/v1/completions", lgs.HandleOpenAIRequest)
	router.HandleFunc("/v1/models", lgs.HandleSimpleRequest)

	// handle Anthropic inference requests
	router.HandleFunc("/v1/messages", lgs.HandleAnthropicRequest)

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
		logging.AsyncLogf("Received request [%s] %s", req.Id, req.GetRequestRawData())
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
	logging.Logf("Request completed [%s] status:%d,method:%s,schedule:%s,url:%s;%s;input_tokens:%d,total_tokens:%d;queue:%d,sch:%d,work:%d;retry:%d,model:%s",
		req.Id,
		httpReq.StatusCode,
		httpReq.Request.Method,
		req.ScheduleCtx.ScheduleResults.String(),
		httpReq.Request.URL.String(),
		stats.String(),
		req.GetInputTokenLen(),
		req.GetTotalTokenLen(),
		queueSize,
		schSize,
		workSize,
		stats.RetryCount,
		req.GetRequestModel())
}
