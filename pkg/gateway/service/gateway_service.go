package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"llumnix/pkg/lrs"
	"llumnix/pkg/metrics"
	"strings"

	"llumnix/cmd/gateway/app/options"
	"net/http"
	"runtime/debug"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/gateway/batch"
	"llumnix/pkg/gateway/handler"
	balancer "llumnix/pkg/gateway/load-balancer"
	"llumnix/pkg/gateway/logging"
	"llumnix/pkg/gateway/mirror"
	"llumnix/pkg/gateway/queue"
	"llumnix/pkg/gateway/router"
	"llumnix/pkg/types"
)

// LlmGatewayService is the main service for LLM gateway that handles all inference requests
// It provides core features including load balancing, request queuing, and batch processing
type LlmGatewayService struct {
	// Gateway configuration
	config *options.GatewayConfig

	// Request buffer queue for traffic control
	bufferQueue *queue.QueueWorkerPool
	// fallbackRateLimitRetryQueue handles retries when a fallback endpoint returns 429
	fallbackRateLimitRetryQueue *queue.RateLimitRetryQueue
	// Request handler for parsing and processing requests
	reqHandler handler.RequestHandler

	balancer balancer.Balancer
	// Service router for request routing and load balancing
	router *router.ServiceRouter
	// Batch service for handling batch inference tasks
	batchService *batch.BatchService

	// Real-time request state tracking, periodically reported to llm scheduler
	requestStateTracker *lrs.RequestStateTracker

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
func NewGatewayService(c *options.GatewayConfig) *LlmGatewayService {
	lgs := &LlmGatewayService{
		config:          c,
		logInputEnabled: c.EnableLogInput,
		simpleClient:    &http.Client{Timeout: 30 * time.Second},
	}

	if c.EnableRequestStateTracking() {
		lgs.requestStateTracker = lrs.NewRequestStateTracker(c)
	}

	// Create request buffer queue for traffic control and request scheduling
	lgs.bufferQueue = queue.NewQueueWorkerPool(c.MaxRequestBufferQueueSize, c.WaitQueueThreads)
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
		lgs.fallbackRateLimitRetryQueue.Start()
		klog.Infof("Fallback 429 retry queue enabled: size=%d, maxRetries=%d, initDelay=%dms, maxDelay=%dms",
			c.FallbackRetryQueueSize, c.FallbackRetryMaxCount, c.FallbackRetryInitDelayMs, c.FallbackRetryMaxDelayMs)
	}

	// Create request handler for parsing OpenAI format requests
	reqHandler, handlerErr := handler.BuildHandler("openai", c)
	if handlerErr != nil {
		klog.Fatalf("failed to create request handler: %v", handlerErr)
	}
	lgs.reqHandler = reqHandler

	lgs.balancer = balancer.NewBalancer(c)

	lgs.router = router.NewServiceRouter(c.RoutePolicy, c.RouteConfigRaw)

	// Create traffic mirror component
	lgs.mirror = mirror.NewMirror(c.GetConfigManager())

	// Initialize batch service if OSS path is configured
	var err error
	if c.BatchOSSPath != "" {
		lgs.batchService, err = batch.NewBatchService(c)
		if err != nil {
			klog.Errorf("Failed to initialize batch service: %v", err)
		}
	}

	return lgs
}

// healthz is the health check endpoint for K8s liveness/readiness probes
func (lgs *LlmGatewayService) Healthz(w http.ResponseWriter, _ *http.Request) {
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
		return http.StatusOK, msg.Message
	}

	var upstreamErr *consts.UpstreamError
	if errors.As(msg.Err, &upstreamErr) {
		return upstreamErr.StatusCode, upstreamErr.Body
	}

	switch msg.Err {
	case consts.ErrorNoAvailableEndpoint:
		return http.StatusServiceUnavailable, []byte(`{"error": {"code": 503, "message": "no available inference worker"}}`)
	case consts.ErrorEndpointNotFound:
		return http.StatusNotFound, []byte(`{"error": {"code": 404, "message": "no inference instance found"}}`)
	case consts.ErrorRateLimitExceeded:
		return http.StatusTooManyRequests, []byte(`{"error": {"code": 429, "message": "rate limit exceeded"}}`)
	case consts.ErrorBackendBadRequest:
		var response types.ErrorResponse
		err := json.Unmarshal(msg.Message, &response)
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
	writer.Write(body)
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
			writer.Write(body)
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
	writer.Write(body)
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

// proxyToExternal forwards a request to an external endpoint.
// Returns nil on success (response already written to client), or an error if the request failed
// before writing any response (safe to fallback).
func (lgs *LlmGatewayService) proxyToExternal(reqCtx *types.RequestContext, dst *router.RouteEndpoint) error {
	originURL := reqCtx.HttpRequest.Request.URL.String()
	url := dst.JoinURL(originURL)

	proxyReq, err := http.NewRequestWithContext(
		reqCtx.Context, reqCtx.HttpRequest.Request.Method, url,
		io.NopCloser(strings.NewReader(reqCtx.LLMRequest.RawData)),
	)
	if err != nil {
		return &consts.NetworkError{URL: url, Err: err}
	}

	for key, values := range reqCtx.HttpRequest.Request.Header {
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}
	if dst.APIKey != "" {
		proxyReq.Header.Set("Authorization", "Bearer "+dst.APIKey)
	}

	resp, err := lgs.simpleClient.Do(proxyReq)
	if err != nil {
		return &consts.NetworkError{URL: url, Err: err}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return consts.NewUpstreamError(url, resp.StatusCode, body, "")
	}

	if err := reqCtx.Context.Err(); err != nil {
		return &consts.NetworkError{URL: url, Err: err}
	}

	w := reqCtx.HttpRequest.Writer
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.WriteHeader(resp.StatusCode)

	flusher, canFlush := w.(http.Flusher)
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			break
		}
	}

	reqCtx.HttpRequest.HeaderResponded = true
	return nil
}

// externalRouteRequest handles routing to an external endpoint with fallback support.
func (lgs *LlmGatewayService) externalRouteRequest(reqCtx *types.RequestContext, dst *router.RouteEndpoint) {
	defer close(reqCtx.ResponseChan)

	err := lgs.proxyToExternal(reqCtx, dst)
	if err != nil {
		klog.V(3).Infof("request [%s] externalRouteRequest failed: %v", reqCtx.Id, err)
		if !lgs.router.CanFallback(reqCtx) {
			reqCtx.ResponseChan <- &types.ResponseMsg{Err: err}
			return
		}
		if ferr := lgs.tryFallback(reqCtx, dst.URL); ferr != nil {
			reqCtx.ResponseChan <- &types.ResponseMsg{Err: err}
		}
	}
}

// unknownRouteRequest handles requests with unknown route by trying fallback.
func (lgs *LlmGatewayService) unknownRouteRequest(reqCtx *types.RequestContext) {
	defer close(reqCtx.ResponseChan)

	if ferr := lgs.tryFallback(reqCtx); ferr != nil {
		reqCtx.ResponseChan <- &types.ResponseMsg{Err: ferr}
	}
}

// internalRouteRequest handles internal routing with retry and fallback.
// It retries on retryable errors (5xx, network errors) by rescheduling via the balancer.
// When all retries are exhausted, it triggers router fallback if available.
func (lgs *LlmGatewayService) internalRouteRequest(reqCtx *types.RequestContext) {
	defer func() {
		close(reqCtx.ResponseChan)
		reqCtx.OnPostRequest()
	}()

	var originErr error
	maxRetries := lgs.config.RetryMaxCount
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			if reqCtx.Context.Err() != nil {
				klog.Infof("request [%s] context cancelled, aborting retry", reqCtx.Id)
				reqCtx.ResponseChan <- &types.ResponseMsg{Err: reqCtx.Context.Err()}
				return
			}

			// Release previous instances
			for i := range reqCtx.SchedulingCtx.SchedulingResults {
				lgs.balancer.Release(reqCtx, &reqCtx.SchedulingCtx.SchedulingResults[i])
			}
			reqCtx.SchedulingCtx.SchedulingResults = nil

			schResult, err := lgs.balancer.Get(reqCtx)
			if err != nil {
				klog.V(3).Infof("request [%s] rescheduling failed: %v", reqCtx.Id, err)
				if lgs.router.CanFallback(reqCtx) {
					if e := lgs.tryFallback(reqCtx); e != nil {
						reqCtx.ResponseChan <- &types.ResponseMsg{Err: e}
					}
					return
				}
				reqCtx.ResponseChan <- &types.ResponseMsg{Err: originErr}
				return
			}
			klog.V(3).Infof("request [%s] rescheduled to %s", reqCtx.Id, schResult.String())
			reqCtx.SchedulingCtx.SchedulingResults = schResult
		}

		originErr = lgs.reqHandler.Handle(reqCtx)
		if originErr == nil {
			return
		}

		// Check if error is retryable
		var retryableErr consts.RetryableError
		if !errors.As(originErr, &retryableErr) || !retryableErr.IsRetryable() {
			klog.Warningf("request [%s] failed with non-retryable error: %v", reqCtx.Id, originErr)
			reqCtx.ResponseChan <- &types.ResponseMsg{Err: originErr}
			return
		}

		// Headers already sent → can't retry
		if reqCtx.HttpRequest.HeaderResponded {
			return
		}

		// Last attempt → try fallback
		if attempt == maxRetries {
			klog.Warningf("request [%s] all %d retries exhausted, last error: %v", reqCtx.Id, maxRetries, originErr)
			if lgs.router.CanFallback(reqCtx) {
				if e := lgs.tryFallback(reqCtx); e != nil {
					reqCtx.ResponseChan <- &types.ResponseMsg{Err: e}
				}
			} else {
				reqCtx.ResponseChan <- &types.ResponseMsg{Err: originErr}
			}
			return
		}

		if instanceID := retryableErr.FailedInstanceID(); instanceID != "" {
			reqCtx.RequestStats.RecordFailedInstance(instanceID)
		}
		reqCtx.RequestStats.RetryCount++

		klog.Warningf("request [%s] attempt %d failed with retryable error: %v, will retry",
			reqCtx.Id, attempt, originErr)

		backoff := time.Duration(10*(1<<attempt)) * time.Millisecond
		if backoff > 100*time.Millisecond {
			backoff = 100 * time.Millisecond
		}
		select {
		case <-reqCtx.Context.Done():
			klog.Infof("request [%s] context cancelled during backoff, aborting retry", reqCtx.Id)
			reqCtx.ResponseChan <- &types.ResponseMsg{Err: reqCtx.Context.Err()}
			return
		case <-time.After(backoff):
		}
	}
}

// tryFallback attempts all available fallback endpoints.
// skipURLs contains URLs already tried (e.g. the primary route) that should be skipped
// to avoid redundant requests to the same endpoint.
// Returns nil if a fallback succeeded, or error if all attempts failed.
func (lgs *LlmGatewayService) tryFallback(reqCtx *types.RequestContext, skipURLs ...string) error {
	skip := make(map[string]bool, len(skipURLs))
	for _, u := range skipURLs {
		skip[u] = true
	}

	var err error
	tryCnt := 0

	for lgs.router.CanFallback(reqCtx) {
		if reqCtx.HttpRequest.HeaderResponded {
			klog.Errorf("request [%s] HTTP headers already sent, cannot fallback.", reqCtx.Id)
			return nil
		}

		fdst, ferr := lgs.router.Fallback(reqCtx)
		if ferr != nil {
			klog.Warningf("request [%s] fallback failed: %v, no more fallback endpoints", reqCtx.Id, ferr)
			return fmt.Errorf("all fallback attempts failed for request %s", reqCtx.Id)
		}

		if skip[fdst.URL] {
			klog.Infof("request [%s] skipping fallback %s (already tried as primary route)", reqCtx.Id, fdst.URL)
			continue
		}
		klog.Infof("request [%s] will fallback to: %s", reqCtx.Id, fdst.URL)

		err = lgs.proxyToExternal(reqCtx, fdst)
		if err == nil {
			return nil
		}
		tryCnt++

		skipBackoff := false

		// Handle 429 rate limit errors with retry queue
		if queue.IsRateLimitError(err) && lgs.fallbackRateLimitRetryQueue != nil {
			klog.Infof("request [%s] fallback to %s got 429, enqueuing for rate-limit retry",
				reqCtx.Id, fdst.URL)
			retryResultCh, enqueued := lgs.fallbackRateLimitRetryQueue.Enqueue(
				reqCtx.Context,
				func() error {
					return lgs.proxyToExternal(reqCtx, fdst)
				},
			)
			if enqueued {
				select {
				case result := <-retryResultCh:
					if result.Err == nil {
						return nil
					}
					klog.Warningf("request [%s] rate-limit retry for fallback %s failed: %v",
						reqCtx.Id, fdst.URL, result.Err)
					err = result.Err
				case <-reqCtx.Context.Done():
					klog.Warningf("request [%s] context cancelled while waiting for rate-limit retry", reqCtx.Id)
					return context.Canceled
				}
				skipBackoff = true
			} else {
				klog.Warningf("request [%s] rate-limit retry queue full, skipping for fallback %s",
					reqCtx.Id, fdst.URL)
			}
		}

		klog.Warningf("request [%s] fallback to %s failed: %v", reqCtx.Id, fdst.URL, err)

		if !skipBackoff {
			attempt := reqCtx.RequestStats.FallbackAttempt - 1
			backoff := time.Duration(10*(1<<attempt)) * time.Millisecond
			if backoff > 100*time.Millisecond {
				backoff = 100 * time.Millisecond
			}
			select {
			case <-reqCtx.Context.Done():
				return reqCtx.Context.Err()
			case <-time.After(backoff):
			}
		}
	}

	if err != nil {
		klog.Warningf("request [%s] all %d fallback attempts failed: %v", reqCtx.Id, tryCnt, err)
		return fmt.Errorf("request %s all fallback attempts failed: %w", reqCtx.Id, err)
	}
	return fmt.Errorf("no fallback endpoints available for request %s", reqCtx.Id)
}

// dispatchRequest routes the request and dispatches it to appropriate handler.
// Supports three routing types:
//   - Internal: schedule to internal workers via balancer, with retry and fallback
//   - External: proxy to external service, with fallback on failure
//   - Unknown: try fallback endpoints directly
func (lgs *LlmGatewayService) dispatchRequest(reqCtx *types.RequestContext) {
	dst, rType := lgs.router.Route(reqCtx)
	klog.V(3).Infof("Request [%s] routed to %s (type: %s)", reqCtx.Id, dst, rType)

	switch rType {
	case router.RouteInternal:
		schResult, err := lgs.balancer.Get(reqCtx)
		if err != nil {
			klog.V(3).Infof("request [%s] scheduling failed: %v", reqCtx.Id, err)
			if lgs.router.CanFallback(reqCtx) {
				go func() {
					defer close(reqCtx.ResponseChan)
					if e := lgs.tryFallback(reqCtx); e != nil {
						reqCtx.ResponseChan <- &types.ResponseMsg{Err: e}
					}
				}()
			} else {
				reqCtx.ResponseChan <- &types.ResponseMsg{Err: err, Message: []byte(err.Error())}
				close(reqCtx.ResponseChan)
			}
			return
		}
		klog.V(3).Infof("request [%s] scheduled to %s", reqCtx.Id, schResult.String())
		reqCtx.SchedulingCtx.SchedulingResults = schResult
		go lgs.internalRouteRequest(reqCtx)
	case router.RouteExternal:
		go lgs.externalRouteRequest(reqCtx, dst)
	case router.RouteUnknown:
		go lgs.unknownRouteRequest(reqCtx)
	}
}

func (lgs *LlmGatewayService) schedulingMode() types.SchedulingMode {
	if !lgs.config.IsPDDisagg() {
		return types.SchedulingModeNeutral
	}
	if lgs.config.SeparatePDScheduling {
		return types.SchedulingModePDStaged
	} else {
		return types.SchedulingModePDBatch
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
	// Set scheduling mode based on configuration
	schedulingMode := lgs.schedulingMode()
	reqCtx.SchedulingCtx.SchedulingMode = schedulingMode
	reqCtx.SchedulingCtx.SchedulingStage = consts.SchedulingStagePrefill
	reqCtx.SetPDSeparateSchedulingHooks(&PDSeparateSchedulingHooks{balancer: lgs.balancer})
	reqCtx.SetRequestStateManagementHooks(&RequestStateManagementHooks{gateway: lgs, balancer: lgs.balancer})

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

	taskFunc := func() {
		reqCtx.RequestStats.DeQueueTime = time.Now()
		lgs.dispatchRequest(reqCtx)
		reqCtx.RequestStats.BalanceTime = time.Now()
	}
	for {
		if lgs.bufferQueue.Dispatch(taskFunc, false) {
			break
		} else {
			// Buffer queue is full, wait for a while and try again
			klog.Warningf("buffer queue is full, waiting for a while and try again: request id %s", reqCtx.Id)
			time.Sleep(lgs.config.WaitQueueInterval)
		}
	}

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
	reqCtx.SchedulingCtx.NeedScheduling = false

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
	r := mux.NewRouter()
	// Register batch API routes if batch service is available
	if lgs.batchService != nil {
		lgs.batchService.RegisterRoutes(r)
	}

	// Register health check route
	r.HandleFunc("/healthz", lgs.Healthz)
	// handle all LLM inference requests
	r.HandleFunc("/v1/chat/completions", lgs.HandleOpenAIRequest)
	r.HandleFunc("/v1/completions", lgs.HandleOpenAIRequest)
	r.HandleFunc("/v1/models", lgs.HandleSimpleRequest)

	address := fmt.Sprintf("%s:%d", lgs.config.Host, lgs.config.Port)
	klog.Infof("LLM Gateway start listen on %s", address)
	klog.Fatal(http.ListenAndServe(address, r))
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
		logging.AsyncLogf("Received request [%s] %s", req.Id, req.LLMRequest.RawData)
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
		req.SchedulingCtx.SchedulingResults.String(),
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

type PDSeparateSchedulingHooks struct {
	balancer balancer.Balancer
}

func (h *PDSeparateSchedulingHooks) ScheduleDecode(req *types.RequestContext) (types.SchedulingResult, error) {
	if req.SchedulingCtx.SchedulingMode != types.SchedulingModePDStaged {
		klog.Warningf("request %s is not in staged scheduling mode, decode does not need to be scheduled separately.", req.Id)
		return nil, fmt.Errorf("not in staged scheduling mode")
	}
	req.SchedulingCtx.SchedulingStage = consts.SchedulingStageDecode
	results, err := h.balancer.Get(req)
	if err != nil {
		return nil, err
	}
	schResult := req.SchedulingCtx.SchedulingResults
	klog.V(3).Infof("decode request [%s] scheduled to %s", req.Id, results.String())
	req.SchedulingCtx.SchedulingResults = append(schResult, results...)
	return results, nil
}

type RequestStateManagementHooks struct {
	balancer balancer.Balancer
	gateway  *LlmGatewayService
}

func (h *RequestStateManagementHooks) OnPostPrefill(req *types.RequestContext) {
	if h.gateway.config.EnableRequestStateTracking() {
		klog.V(3).Infof("[%s] Post-prefill hook triggered", req.Id)

		pInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypePrefill)
		if pInstance != nil {
			h.balancer.Release(req, pInstance)
		}
		req.SchedulingCtx.SchedulingStage = consts.SchedulingStageDecode
	}
}

func (h *RequestStateManagementHooks) OnPostRequest(req *types.RequestContext) {
	if h.gateway.config.EnableRequestStateTracking() {
		klog.V(3).Infof("[%s] Post-request hook triggered", req.Id)

		nInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeNeutral)
		if nInstance != nil {
			h.balancer.Release(req, nInstance)
		}
		dInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeDecode)
		if dInstance != nil {
			h.balancer.Release(req, dInstance)
		}
	}
}

func (h *RequestStateManagementHooks) OnPostDecodeFirstStreamResponse(req *types.RequestContext) {
	if h.gateway.config.EnableRequestStateTracking() && req.LLMRequest.ClientStream {
		klog.V(3).Infof("[%s] Post-decode-first-stream-response hook triggered", req.Id)

		var inferType consts.InferType
		var instanceID string
		if instance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeDecode); instance != nil {
			inferType = consts.InferTypeDecode
			instanceID = instance.Id()
		} else if instance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeNeutral); instance != nil {
			inferType = consts.InferTypeNeutral
			instanceID = instance.Id()
		}
		reqTokenState := lrs.NewRequestTokenState(req, req.LLMRequest.Model, inferType, instanceID, req.SchedulingCtx.GatewayId)
		h.gateway.requestStateTracker.AddRequestState(reqTokenState)
		req.RequestTokenState = reqTokenState
		defer h.gateway.requestStateTracker.DeleteRequestState(req.Id)
	}
}

func (h *RequestStateManagementHooks) OnPostDecodeEachStreamResponse(req *types.RequestContext) {
	if h.gateway.config.EnableRequestStateTracking() && req.LLMRequest.ClientStream {
		klog.V(3).Infof("[%s] Post-decode-each-stream-response hook triggered", req.Id)

		usage := GetResponseUsage(req)
		if usage != nil {
			req.RequestTokenState.UpdateNumTokens(usage.TotalTokens)
		} else {
			text := GetResponseText(req)
			req.RequestTokenState.AppendResponseText(text)
		}
	}
}
