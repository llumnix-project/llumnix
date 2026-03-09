package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"k8s.io/klog/v2"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/gateway/batch"
	balancer "llm-gateway/pkg/gateway/load-balancer"
	"llm-gateway/pkg/gateway/protocol/anthropic"
	"llm-gateway/pkg/gateway/service/backend"
	"llm-gateway/pkg/gateway/service/handler"
	"llm-gateway/pkg/gateway/service/mirror"
	"llm-gateway/pkg/gateway/service/queue"
	"llm-gateway/pkg/gateway/service/router"
	"llm-gateway/pkg/logging"
	"llm-gateway/pkg/metrics"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/scheduler/lrs"
	"llm-gateway/pkg/types"
)

// LlmGatewayService is the main service for LLM gateway that handles all inference requests
// It provides core features including load balancing, request queuing, and batch processing
type LlmGatewayService struct {
	// Gateway configuration
	config *options.Config

	// Request buffer queue for traffic control
	bufferQueue *queue.QueueWorkerPool
	// fallbackRateLimitRetryQueue handles retries when a fallback endpoint returns 429
	fallbackRateLimitRetryQueue *queue.RateLimitRetryQueue
	// Request handler for parsing and processing requests
	openaiHandler    handler.RequestHandler
	anthropicHandler handler.RequestHandler

	// externalBackend is a SimpleBackend instance used for external route handling.
	externalBackend backend.InferenceBackend
	// inferenceBackend is the primary inference backend used for processing requests.
	inferenceBackend backend.InferenceBackend

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

// statusCaptureResponseWriter wraps http.ResponseWriter to capture the status code
type statusCaptureResponseWriter struct {
	http.ResponseWriter
	statusCode *int
}

// WriteHeader captures the status code before writing it
func (rw *statusCaptureResponseWriter) WriteHeader(code int) {
	*rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
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

	// Create fallback 429 rate limit retry queue if enabled
	if c.FallbackRetryQueueEnabled {
		lgs.fallbackRateLimitRetryQueue = queue.NewRateLimitRetryQueue(
			c.FallbackRetryQueueSize,
			c.FallbackRetryWorkerSize,
			c.FallbackRetryMaxCount,
			time.Duration(c.FallbackRetryInitDelayMs)*time.Millisecond,
			time.Duration(c.FallbackRetryMaxDelayMs)*time.Millisecond,
		)
		// Inject a checker that recognises upstream 429 errors.
		lgs.fallbackRateLimitRetryQueue.SetRateLimitChecker(func(err error) bool {
			var upstreamErr *consts.UpstreamError
			return errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusTooManyRequests
		})
		lgs.fallbackRateLimitRetryQueue.Start()
		klog.Infof("Fallback 429 retry queue enabled: size=%d, maxRetries=%d, initDelay=%dms, maxDelay=%dms",
			c.FallbackRetryQueueSize, c.FallbackRetryMaxCount, c.FallbackRetryInitDelayMs, c.FallbackRetryMaxDelayMs)
	}

	// Create request handler for parsing different format requests
	lgs.BuildHandler()

	// Create inference backend
	lgs.BuildBackend()

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
		lb = balancer.NewRoundRobinBalancer(r, c.RetryExcludeScope)
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

func (lgs *LlmGatewayService) BuildBackend() {
	bType, scheduleMode := lgs.config.GetBackendParams()
	inferenceBackend, err := backend.BuildBackend(bType, scheduleMode)
	if err != nil {
		klog.Fatalf("build backend failed: %v", err)
	}
	lgs.inferenceBackend = inferenceBackend

	if inferenceBackend.Name() == backend.BackendTypeSimple {
		lgs.externalBackend = inferenceBackend
	} else {
		// inferenceBackend is not simple backend, need to create a new simple backend
		simpleBackend, err := backend.BuildBackend(backend.BackendTypeSimple, scheduleMode)
		if err != nil {
			klog.Fatalf("build simple backend failed: %v", err)
		}
		lgs.externalBackend = simpleBackend
	}
}

// Healthz is the health check endpoint for K8s liveness/readiness probes
func (lgs *LlmGatewayService) Healthz(w http.ResponseWriter, r *http.Request) {
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

	if msg.Err == nil {
		return http.StatusOK, []byte(msg.Message)
	}

	var upstreamErr *consts.UpstreamError
	if errors.As(msg.Err, &upstreamErr) {
		return upstreamErr.StatusCode, upstreamErr.Body
	}

	switch msg.Err {
	case consts.ErrorNoAvailableEndpoint:
		return http.StatusServiceUnavailable, []byte(`{"error": {"code": 503, "message": "no available inference worker"}}`)
	case consts.ErrorRateLimitExceeded:
		return http.StatusTooManyRequests, []byte(`{"error": {"code": 429, "message": "rate limit exceeded"}}`)
	default:
		return http.StatusBadRequest, []byte(msg.Err.Error())
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
				reqCtx.HttpRequest.HeaderResponded = true
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

// externalRouteRequest handles fallback routing with retry mechanism
// It tries the given fallback endpoint first, then tries other available fallback configurations
func (lgs *LlmGatewayService) externalRouteRequest(reqCtx *types.RequestContext, handler handler.RequestHandler) {
	defer func() {
		klog.V(3).Infof("request [%s] externalRouteRequest end", reqCtx.Id)
		close(reqCtx.ResponseChan)
		defer reqCtx.TriggerPostRequest()
	}()
	reqCtx.TriggerPreRequest()

	// Delegate the request handling to the provided handler using externalBackend (SimpleBackend)
	err := handler.Handle(reqCtx, lgs.externalBackend)
	if err != nil {
		klog.V(3).Infof("request [%s] externalRouteRequest failed: %v", reqCtx.Id, err)
		err := lgs.tryFallback(reqCtx, handler)
		if err != nil {
			reqCtx.WriteErrorResponse(err)
		}
	}
}

// unknownRouteRequest handles requests with unknown route by trying fallback
func (lgs *LlmGatewayService) unknownRouteRequest(reqCtx *types.RequestContext, handler handler.RequestHandler) {
	defer func() {
		klog.V(3).Infof("request [%s] unknownRouteRequest end", reqCtx.Id)
		close(reqCtx.ResponseChan)
		defer reqCtx.TriggerPostRequest()
	}()
	reqCtx.TriggerPreRequest()

	err := lgs.tryFallback(reqCtx, handler)
	if err != nil {
		reqCtx.WriteErrorResponse(err)
	}
}

// internalRouteRequest handles internal routing with retry mechanism.
// It executes the handler and retries on retryable errors (5xx, network errors)
// by rescheduling to different workers up to config.RetryCount times.
// When all retries are exhausted, it triggers router fallback if available.
func (lgs *LlmGatewayService) internalRouteRequest(reqCtx *types.RequestContext, handler handler.RequestHandler) {
	defer func() {
		klog.V(3).Infof("request [%s] internalRouteRequest end", reqCtx.Id)
		close(reqCtx.ResponseChan)
		defer reqCtx.TriggerPostRequest()
	}()
	reqCtx.TriggerPreRequest()

	var originErr error
	maxRetries := lgs.config.RetryCount
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// release previous resources
			for _, worker := range reqCtx.ScheduleCtx.ScheduleResults {
				lgs.balancer.Release(reqCtx, worker)
			}
			reqCtx.ScheduleCtx.ScheduleResults = nil

			// Reschedule to a different worker for retry
			schResult, err := lgs.balancer.Get(reqCtx)
			if err != nil {
				klog.V(3).Infof("request [%s] scheduling failed: %v", reqCtx.Id, err)
				if !router.GetServiceRouter().CanFallback(reqCtx) {
					reqCtx.WriteErrorResponse(originErr)
					return
				}
				if e := lgs.tryFallback(reqCtx, handler); e != nil {
					reqCtx.WriteErrorResponse(e)
				}
				return
			}
			klog.V(3).Infof("request [%s] scheduled to %s", reqCtx.Id, schResult.String())
			reqCtx.ScheduleCtx.ScheduleResults = schResult
		}

		// Execute the handler
		originErr = handler.Handle(reqCtx, lgs.inferenceBackend)
		if originErr == nil {
			return
		}

		// Check if error is retryable and get failed instance info
		var retryableErr consts.RetryableError
		if !errors.As(originErr, &retryableErr) || !retryableErr.IsRetryable() {
			klog.Warningf("request [%s] failed with error: %v", reqCtx.Id, originErr)
			reqCtx.WriteErrorResponse(originErr)
			return
		}

		// Check if this is the last attempt
		if attempt == maxRetries {
			klog.Warningf("request [%s] all %d retries exhausted, last error: %v", reqCtx.Id, maxRetries, originErr)
			lgs.tryFallback(reqCtx, handler)
			return
		}

		// Record failed instance for exclusion in next scheduling
		if instanceID := retryableErr.FailedInstanceID(); instanceID != "" {
			reqCtx.ScheduleCtx.AddExcludedInstance(instanceID)
			reqCtx.RequestStats.RecordFailedInstance(instanceID)
		}
		reqCtx.RequestStats.RetryCount++

		klog.Warningf("request [%s] attempt %d failed with retryable error: %v, will retry", reqCtx.Id, attempt, originErr)

		// try backoff: 10ms, 20ms, 40ms...
		backoff := time.Duration(10*(1<<attempt)) * time.Millisecond
		if backoff > 100*time.Millisecond {
			backoff = 100 * time.Millisecond
		}
		time.Sleep(backoff)
	}

}

// tryFallback attempts router fallback first, sends error if fallback fails or unavailable.
func (lgs *LlmGatewayService) tryFallback(reqCtx *types.RequestContext, handler handler.RequestHandler) error {
	var err error
	tryCnt := 0
	serviceRouter := router.GetServiceRouter()
	// Try all available fallback configurations
	for serviceRouter.CanFallback(reqCtx) {
		if reqCtx.HttpRequest.HeaderResponded {
			klog.Errorf("request [%s] HTTP headers already sent, cannot proxy.", reqCtx.Id)
			return nil
		}

		fdst, ferr := serviceRouter.Fallback(reqCtx)
		if ferr != nil {
			klog.Warningf("request [%s] fallback failed: %v, no more fallback endpoints", reqCtx.Id, ferr)
			return fmt.Errorf("all fallback attempts failed for request %s", reqCtx.Id)
		}
		klog.Infof("request [%s] will fallback to: %s", reqCtx.Id, fdst.URL)

		PopulateExternalWorker(reqCtx, fdst)
		err = handler.Handle(reqCtx, lgs.externalBackend)
		if err == nil {
			return nil
		}
		tryCnt += 1

		// Fallback failed, check if it is a 429 rate limit error that can be retried via queue.
		var upstreamErr *consts.UpstreamError
		if errors.As(err, &upstreamErr) && upstreamErr.StatusCode == http.StatusTooManyRequests {
			if lgs.fallbackRateLimitRetryQueue != nil {
				klog.Infof("request [%s] fallback to %s got 429 (too many requests), enqueuing for rate-limit retry", reqCtx.Id, fdst.URL)

				retryResultCh, enqueued := lgs.fallbackRateLimitRetryQueue.Enqueue(
					reqCtx.Context,
					func() error {
						return handler.Handle(reqCtx, lgs.externalBackend)
					},
				)
				if enqueued {
					// Block until retry succeeds, permanently fails, or the client disconnects.
					select {
					case result := <-retryResultCh:
						if result.Err == nil {
							return nil
						}
						klog.Warningf("request [%s] rate-limit retry for fallback %s ultimately failed: %v", reqCtx.Id, fdst.URL, result.Err)
						err = result.Err
					case <-reqCtx.Context.Done():
						klog.Warningf("request [%s] context cancelled while waiting for rate-limit retry", reqCtx.Id)
						return context.Canceled
					}
					// Retry exhausted; fall through to try next fallback or return error.
				} else {
					klog.Warningf("request [%s] rate-limit retry queue full, skipping queue for fallback %s", reqCtx.Id, fdst.URL)
				}
			}
		}

		// Fallback failed (non-429 or retry exhausted), try next one if available.
		klog.Warningf("request [%s] fallback to %s failed: %v", reqCtx.Id, fdst.URL, err)

		// Add small delay before trying next fallback (exponential backoff based on attempt count)
		attempt := reqCtx.RequestStats.FallbackAttempt - 1 // Current attempt count
		backoff := time.Duration(10*(1<<attempt)) * time.Millisecond
		if backoff > 100*time.Millisecond {
			backoff = 100 * time.Millisecond
		}
		time.Sleep(backoff)
	}
	klog.Warningf("request [%s] all %d fallback attempts failed: %v", reqCtx.Id, tryCnt, err)
	return fmt.Errorf("request %s all fallback attempts failed: %w", reqCtx.Id, err)
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
		// Match internal route
		// Note: Scheduling must occur before starting a new coroutine, and its operations must occupy a worker thread.
		// This is necessary to provide feedback and ensure the request is blocked in the queue.
		schResult, err := lgs.balancer.Get(reqCtx)
		if err != nil {
			klog.V(3).Infof("request [%s] scheduling failed: %v", reqCtx.Id, err)
			if serviceRouter.CanFallback(reqCtx) {
				if e := lgs.tryFallback(reqCtx, handler); e != nil {
					reqCtx.WriteErrorResponse(e)
				}
			} else {
				reqCtx.WriteErrorResponse(err)
			}
			return
		}
		klog.V(3).Infof("request [%s] scheduled to %s", reqCtx.Id, schResult.String())
		reqCtx.ScheduleCtx.ScheduleResults = schResult
		go lgs.internalRouteRequest(reqCtx, handler)
	case router.RouteExternal:
		// Match external route
		PopulateExternalWorker(reqCtx, dst)
		go lgs.externalRouteRequest(reqCtx, handler)
	case router.RouteUnknown:
		// Match unknown route, try fallback
		go lgs.unknownRouteRequest(reqCtx, handler)
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

	// Release all workers in the schedule results, to prevent resource leaks in some abnormal situations
	for _, worker := range req.ScheduleCtx.ScheduleResults {
		h.balancer.Release(req, worker)
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

// HandleCountTokens handles Anthropic token counting requests
// It estimates the number of input tokens from the request body without performing inference
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
func (lgs *LlmGatewayService) HandleCountTokens(w http.ResponseWriter, r *http.Request) {
	tStart := time.Now()

	// Get request ID from context
	reqCtx := types.NewRequestContext(r.Context(), r, w)
	requestId := reqCtx.Id

	// Wrap ResponseWriter to capture status code
	statusCode := http.StatusOK
	rw := &statusCaptureResponseWriter{ResponseWriter: w, statusCode: &statusCode}

	// Log access log with defer to ensure it's always recorded
	defer func() {
		logging.Logf("[%s] status_code:%d,response_time:%vms,method:%s,url:%s", requestId, statusCode, time.Since(tStart).Milliseconds(), r.Method, r.URL.Path)
	}()

	// Validate request method
	if r.Method != http.MethodPost {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read request body
	data, err := io.ReadAll(r.Body)
	if err != nil {
		klog.Errorf("[%s] Failed to read request body: %v", requestId, err)
		http.Error(rw, "failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Estimate tokens using the utility function
	estimatedTokens := EstimateTokens(data)

	// Create response
	response := anthropic.TokenCountResponse{
		InputTokens: estimatedTokens,
	}

	// Marshal response
	responseData, err := json.Marshal(response)
	if err != nil {
		klog.Errorf("[%s] Failed to marshal response: %v", requestId, err)
		http.Error(rw, "failed to marshal response", http.StatusInternalServerError)
		return
	}

	// Set response headers and write response
	rw.Header().Set("Content-Type", "application/json")
	rw.WriteHeader(http.StatusOK)
	if _, err := rw.Write(responseData); err != nil {
		klog.Errorf("[%s] Failed to write response: %v", requestId, err)
	}
}

// HandleSimpleRequest handles simple requests (e.g., /v1/models)
// It forwards the request to a backend endpoint and returns the response to the client
// Parameters:
//   - w: HTTP response writer
//   - r: HTTP request
func (lgs *LlmGatewayService) HandleSimpleRequest(w http.ResponseWriter, r *http.Request) {
	tStart := time.Now()

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

	// Wrap ResponseWriter to capture status code
	statusCode := http.StatusOK
	rw := &statusCaptureResponseWriter{ResponseWriter: w, statusCode: &statusCode}

	// Forward the request to the backend endpoint
	SimpleHTTPProxy(lgs.simpleClient, url, rw, r)

	// Log access log
	logging.Logf("[%s] status_code:%d,response_time:%vms,method:%s,url:%s,sch_results:%s", reqCtx.Id, statusCode, time.Since(tStart).Milliseconds(), r.Method, r.URL.Path, schResult.String())
}

// Run starts the HTTP server and listens for requests
// This method blocks until the server is shut down
func (lgs *LlmGatewayService) Run() {
	// Create router for handling different HTTP paths
	router := mux.NewRouter()

	// Register health check route
	router.HandleFunc("/healthz", lgs.Healthz)
	// Register Prometheus metrics endpoint
	prometheusHandler := metrics.NewPrometheusHandler()
	router.Handle("/metrics", prometheusHandler)
	// Register batch API routes if batch service is available
	if lgs.batchService != nil {
		lgs.batchService.RegisterRoutes(router)
	}
	// handle OpenAI inference requests
	router.HandleFunc("/v1/chat/completions", lgs.HandleOpenAIRequest)
	router.HandleFunc("/v1/completions", lgs.HandleOpenAIRequest)
	router.HandleFunc("/v1/models", lgs.HandleSimpleRequest)
	router.HandleFunc("/get_server_info", lgs.HandleSimpleRequest)

	// handle Anthropic inference requests
	router.HandleFunc("/v1/messages", lgs.HandleAnthropicRequest)
	router.HandleFunc("/v1/messages/count_tokens", lgs.HandleCountTokens)

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
		logging.AsyncLogf("Received request [%s] %s", req.Id, string(req.GetRequestRawData()))
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
	logging.Logf("Request completed [%s] status_code:%d,method:%s,url:%s;%s;input_tokens:%d,total_tokens:%d;sch_results:%s;queue:%d,sch:%d,work:%d,model:%s",
		req.Id,
		httpReq.StatusCode,
		httpReq.Request.Method,
		httpReq.Request.URL.String(),
		stats.String(),
		req.GetInputTokenLen(),
		req.GetTotalTokenLen(),
		req.ScheduleCtx.ScheduleResults.String(),
		queueSize,
		schSize,
		workSize,
		req.GetRequestModel())
}
