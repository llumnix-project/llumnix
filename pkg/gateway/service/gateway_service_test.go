package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/consts"
	"llumnix/pkg/gateway/queue"
	"llumnix/pkg/gateway/router"
	"llumnix/pkg/types"
)

// --- helpers ---

func newTestRequestContext() *types.RequestContext {
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	ctx := types.NewRequestContext(r.Context(), r, w)
	ctx.LLMRequest.RawData = `{"model":"test","messages":[]}`
	ctx.LLMRequest.Model = "test"
	ctx.ResponseChan = make(chan *types.ResponseMsg, 100)
	return ctx
}

func newTestGatewayService(routePolicy, routeConfigRaw string) *LlmGatewayService {
	return &LlmGatewayService{
		router:       router.NewServiceRouter(routePolicy, routeConfigRaw),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// --- proxyToExternal tests ---

func TestProxyToExternal_Success(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"success"}`))
	}))
	defer backend.Close()

	lgs := newTestGatewayService("", "")
	ctx := newTestRequestContext()

	dst := &router.RouteEndpoint{URL: backend.URL, APIKey: "test-key"}
	err := lgs.proxyToExternal(ctx, dst)

	assert.NoError(t, err)
	assert.True(t, ctx.HttpRequest.HeaderResponded)

	recorder := ctx.HttpRequest.Writer.(*httptest.ResponseRecorder)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "success")
}

func TestProxyToExternal_SetsAuthHeader(t *testing.T) {
	var receivedAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	lgs := newTestGatewayService("", "")
	ctx := newTestRequestContext()

	dst := &router.RouteEndpoint{URL: backend.URL, APIKey: "sk-secret"}
	err := lgs.proxyToExternal(ctx, dst)

	assert.NoError(t, err)
	assert.Equal(t, "Bearer sk-secret", receivedAuth)
}

func TestProxyToExternal_UpstreamError(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal"}`))
	}))
	defer backend.Close()

	lgs := newTestGatewayService("", "")
	ctx := newTestRequestContext()

	dst := &router.RouteEndpoint{URL: backend.URL}
	err := lgs.proxyToExternal(ctx, dst)

	assert.Error(t, err)
	assert.False(t, ctx.HttpRequest.HeaderResponded)

	var upstreamErr *consts.UpstreamError
	assert.True(t, assert.ErrorAs(t, err, &upstreamErr))
	assert.Equal(t, http.StatusInternalServerError, upstreamErr.StatusCode)
}

func TestProxyToExternal_NetworkError(t *testing.T) {
	lgs := newTestGatewayService("", "")
	ctx := newTestRequestContext()

	dst := &router.RouteEndpoint{URL: "http://127.0.0.1:1"}
	err := lgs.proxyToExternal(ctx, dst)

	assert.Error(t, err)
	assert.False(t, ctx.HttpRequest.HeaderResponded)

	var netErr *consts.NetworkError
	assert.True(t, assert.ErrorAs(t, err, &netErr))
}

func TestProxyToExternal_429Error(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer backend.Close()

	lgs := newTestGatewayService("", "")
	ctx := newTestRequestContext()

	dst := &router.RouteEndpoint{URL: backend.URL}
	err := lgs.proxyToExternal(ctx, dst)

	assert.Error(t, err)
	var upstreamErr *consts.UpstreamError
	assert.True(t, assert.ErrorAs(t, err, &upstreamErr))
	assert.Equal(t, http.StatusTooManyRequests, upstreamErr.StatusCode)
}

// --- tryFallback tests ---

func TestTryFallback_Success(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"fallback ok"}`))
	}))
	defer backend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 100, Prefix: "*"},
		{URL: backend.URL, Prefix: "test*", IsFallback: true, Model: "fallback-model", APIKey: "key"},
	})

	lgs := newTestGatewayService(consts.RoutePolicyPrefix, string(routeConfigJSON))
	ctx := newTestRequestContext()

	err := lgs.tryFallback(ctx)
	assert.NoError(t, err)
	assert.True(t, ctx.HttpRequest.HeaderResponded)
}

func TestTryFallback_AllFail(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"fail"}`))
	}))
	defer backend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 100, Prefix: "*"},
		{URL: backend.URL, Prefix: "test*", IsFallback: true, Model: "m1"},
	})

	lgs := newTestGatewayService(consts.RoutePolicyPrefix, string(routeConfigJSON))
	ctx := newTestRequestContext()

	err := lgs.tryFallback(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "all fallback attempts failed")
	assert.False(t, ctx.HttpRequest.HeaderResponded)
}

func TestTryFallback_NoFallbackEndpoints(t *testing.T) {
	lgs := newTestGatewayService(consts.RoutePolicyPrefix, "")
	ctx := newTestRequestContext()

	err := lgs.tryFallback(ctx)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no fallback endpoints available")
}

func TestTryFallback_SkipsWhenHeadersSent(t *testing.T) {
	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Prefix: "*"},
		{URL: "http://127.0.0.1:1", Prefix: "test*", IsFallback: true, Model: "m1"},
	})

	lgs := newTestGatewayService(consts.RoutePolicyPrefix, string(routeConfigJSON))
	ctx := newTestRequestContext()
	ctx.HttpRequest.HeaderResponded = true

	err := lgs.tryFallback(ctx)
	assert.NoError(t, err)
}

func TestTryFallback_MultipleFallbacks_FirstFails_SecondSucceeds(t *testing.T) {
	callCount := 0
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"fail"}`))
	}))
	defer backend1.Close()

	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer backend2.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 100, Prefix: "*"},
		{URL: backend1.URL, Prefix: "test*", IsFallback: true, Model: "m1"},
		{URL: backend2.URL, Prefix: "test*", IsFallback: true, Model: "m2"},
	})

	lgs := newTestGatewayService(consts.RoutePolicyPrefix, string(routeConfigJSON))
	ctx := newTestRequestContext()

	err := lgs.tryFallback(ctx)
	assert.NoError(t, err)
	assert.True(t, ctx.HttpRequest.HeaderResponded)
	assert.Equal(t, 2, callCount)
}

func TestTryFallback_429WithRetryQueue(t *testing.T) {
	callCount := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount <= 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"ok"}`))
	}))
	defer backend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 100, Prefix: "*"},
		{URL: backend.URL, Prefix: "test*", IsFallback: true, Model: "m1"},
	})

	lgs := newTestGatewayService(consts.RoutePolicyPrefix, string(routeConfigJSON))
	lgs.fallbackRateLimitRetryQueue = queue.NewRateLimitRetryQueue(10, 2, 3, 10*time.Millisecond, 100*time.Millisecond)
	lgs.fallbackRateLimitRetryQueue.Start()
	defer lgs.fallbackRateLimitRetryQueue.Stop()

	ctx := newTestRequestContext()

	err := lgs.tryFallback(ctx)
	assert.NoError(t, err)
	assert.True(t, ctx.HttpRequest.HeaderResponded)
	assert.GreaterOrEqual(t, callCount, 2)
}

// --- dispatchRequest routing tests ---

type mockBalancer struct {
	getResult types.SchedulingResult
	getErr    error
}

func (m *mockBalancer) Get(req *types.RequestContext) (types.SchedulingResult, error) {
	return m.getResult, m.getErr
}

func (m *mockBalancer) Release(req *types.RequestContext, inst *types.LLMInstance) {}

type mockHandler struct {
	parseErr error
	handleFn func(req *types.RequestContext) error
}

func (m *mockHandler) ParseRequest(req *types.RequestContext) error {
	return m.parseErr
}

func (m *mockHandler) Handle(req *types.RequestContext) error {
	if m.handleFn != nil {
		return m.handleFn(req)
	}
	return nil
}

func TestDispatchRequest_InternalRoute_SchedulingFails_FallbackAvailable(t *testing.T) {
	fallbackBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"fallback"}`))
	}))
	defer fallbackBackend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 100, Prefix: "*"},
		{URL: fallbackBackend.URL, Prefix: "test*", IsFallback: true, Model: "m1"},
	})

	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter(consts.RoutePolicyPrefix, string(routeConfigJSON)),
		balancer:     &mockBalancer{getErr: fmt.Errorf("no workers available")},
		simpleClient: &http.Client{Timeout: 5 * time.Second},
		reqHandler:   &mockHandler{},
	}

	ctx := newTestRequestContext()

	lgs.dispatchRequest(ctx)

	// Wait for async goroutine
	time.Sleep(200 * time.Millisecond)

	// ResponseChan should be closed (either by fallback goroutine success or with msg)
	select {
	case msg, ok := <-ctx.ResponseChan:
		if ok && msg.Err != nil {
			t.Fatalf("Expected fallback success, got error: %v", msg.Err)
		}
	default:
	}

	assert.True(t, ctx.HttpRequest.HeaderResponded)
}

func TestDispatchRequest_InternalRoute_SchedulingFails_NoFallback(t *testing.T) {
	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter("", ""),
		balancer:     &mockBalancer{getErr: fmt.Errorf("no workers available")},
		simpleClient: &http.Client{Timeout: 5 * time.Second},
		reqHandler:   &mockHandler{},
	}

	ctx := newTestRequestContext()

	lgs.dispatchRequest(ctx)

	select {
	case msg := <-ctx.ResponseChan:
		require.NotNil(t, msg)
		assert.Error(t, msg.Err)
		assert.Contains(t, msg.Err.Error(), "no workers available")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for error response")
	}
}

func TestDispatchRequest_ExternalRoute(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: backend.URL, Weight: 100, Prefix: "test*", Model: "test-model"},
	})

	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter(consts.RoutePolicyPrefix, string(routeConfigJSON)),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
	}

	ctx := newTestRequestContext()

	lgs.dispatchRequest(ctx)

	// Wait for goroutine
	time.Sleep(200 * time.Millisecond)

	// Channel should be closed
	_, ok := <-ctx.ResponseChan
	assert.False(t, ok)
	assert.True(t, ctx.HttpRequest.HeaderResponded)
}

func TestDispatchRequest_UnknownRoute_FallbackSuccess(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer backend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Prefix: "internal*"},
		{URL: backend.URL, Prefix: "fallback*", IsFallback: true, Model: "m1"},
	})

	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter(consts.RoutePolicyPrefix, string(routeConfigJSON)),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
	}

	ctx := newTestRequestContext()
	ctx.LLMRequest.Model = "unknown-model"

	lgs.dispatchRequest(ctx)

	// Wait for goroutine
	time.Sleep(200 * time.Millisecond)

	assert.True(t, ctx.HttpRequest.HeaderResponded)
}

// --- internalRouteRequest retry tests ---

func TestInternalRouteRequest_Success(t *testing.T) {
	handleCalled := false
	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter("", ""),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
		reqHandler: &mockHandler{handleFn: func(req *types.RequestContext) error {
			handleCalled = true
			return nil
		}},
		config: &options.GatewayConfig{},
	}

	ctx := newTestRequestContext()
	ctx.SchedulingCtx = &types.SchedulingContext{}

	lgs.internalRouteRequest(ctx)

	assert.True(t, handleCalled)
	// Channel should be closed
	_, ok := <-ctx.ResponseChan
	assert.False(t, ok)
}

func TestInternalRouteRequest_RetryOnRetryableError(t *testing.T) {
	callCount := 0
	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter("", ""),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
		balancer: &mockBalancer{
			getResult: types.SchedulingResult{
				{ID: "inst-2", InferType: consts.InferTypeNeutral},
			},
		},
		reqHandler: &mockHandler{handleFn: func(req *types.RequestContext) error {
			callCount++
			if callCount <= 1 {
				return consts.NewUpstreamError("http://test", 500, []byte("error"), "inst-1")
			}
			return nil
		}},
		config: &options.GatewayConfig{},
	}
	lgs.config.RetryMaxCount = 2

	ctx := newTestRequestContext()
	ctx.SchedulingCtx = &types.SchedulingContext{
		SchedulingResults: types.SchedulingResult{
			{ID: "inst-1", InferType: consts.InferTypeNeutral},
		},
	}

	lgs.internalRouteRequest(ctx)

	assert.Equal(t, 2, callCount)
	assert.Equal(t, 1, ctx.RequestStats.RetryCount)
	_, ok := <-ctx.ResponseChan
	assert.False(t, ok)
}

func TestInternalRouteRequest_NonRetryableError(t *testing.T) {
	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter("", ""),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
		reqHandler: &mockHandler{handleFn: func(req *types.RequestContext) error {
			return consts.NewUpstreamError("http://test", 400, []byte("bad request"), "")
		}},
		config: &options.GatewayConfig{},
	}
	lgs.config.RetryMaxCount = 2

	ctx := newTestRequestContext()
	ctx.SchedulingCtx = &types.SchedulingContext{}

	lgs.internalRouteRequest(ctx)

	msg := <-ctx.ResponseChan
	assert.Error(t, msg.Err)
	var upstreamErr *consts.UpstreamError
	assert.True(t, errors.As(msg.Err, &upstreamErr))
	assert.Equal(t, 400, upstreamErr.StatusCode)
}

func TestInternalRouteRequest_RetriesExhausted_FallbackSuccess(t *testing.T) {
	fallbackBackend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"result":"fallback"}`))
	}))
	defer fallbackBackend.Close()

	routeConfigJSON, _ := json.Marshal([]router.RouteConfig{
		{URL: consts.RouteInternalURL, Weight: 100, Prefix: "*"},
		{URL: fallbackBackend.URL, Prefix: "test*", IsFallback: true, Model: "m1"},
	})

	lgs := &LlmGatewayService{
		router:       router.NewServiceRouter(consts.RoutePolicyPrefix, string(routeConfigJSON)),
		simpleClient: &http.Client{Timeout: 5 * time.Second},
		balancer: &mockBalancer{
			getResult: types.SchedulingResult{
				{ID: "inst-2", InferType: consts.InferTypeNeutral},
			},
		},
		reqHandler: &mockHandler{handleFn: func(req *types.RequestContext) error {
			return consts.NewUpstreamError("http://test", 500, []byte("error"), "inst-1")
		}},
		config: &options.GatewayConfig{},
	}
	lgs.config.RetryMaxCount = 1

	ctx := newTestRequestContext()
	ctx.SchedulingCtx = &types.SchedulingContext{
		SchedulingResults: types.SchedulingResult{
			{ID: "inst-1", InferType: consts.InferTypeNeutral},
		},
	}

	lgs.internalRouteRequest(ctx)

	// Should have fallen back successfully → no error in ResponseChan, channel closed
	_, ok := <-ctx.ResponseChan
	assert.False(t, ok)
	assert.True(t, ctx.HttpRequest.HeaderResponded)
}

// --- convertErrorResponse tests ---

func TestConvertErrorResponse_UpstreamError(t *testing.T) {
	lgs := &LlmGatewayService{}
	msg := &types.ResponseMsg{Err: consts.NewUpstreamError("http://test", 502, []byte(`{"error":"bad gateway"}`), "")}
	code, body := lgs.convertErrorResponse(msg)
	assert.Equal(t, 502, code)
	assert.Contains(t, string(body), "bad gateway")
}

func TestConvertErrorResponse_NoAvailableEndpoint(t *testing.T) {
	lgs := &LlmGatewayService{}
	msg := &types.ResponseMsg{Err: consts.ErrorNoAvailableEndpoint}
	code, body := lgs.convertErrorResponse(msg)
	assert.Equal(t, http.StatusServiceUnavailable, code)
	assert.Contains(t, string(body), "no available inference worker")
}

func TestConvertErrorResponse_RateLimitExceeded(t *testing.T) {
	lgs := &LlmGatewayService{}
	msg := &types.ResponseMsg{Err: consts.ErrorRateLimitExceeded}
	code, body := lgs.convertErrorResponse(msg)
	assert.Equal(t, http.StatusTooManyRequests, code)
	assert.Contains(t, string(body), "rate limit exceeded")
}

// --- error type tests ---

func TestUpstreamError_IsRetryable(t *testing.T) {
	err5xx := consts.NewUpstreamError("http://test", 500, nil, "inst-1")
	assert.True(t, err5xx.IsRetryable())
	assert.Equal(t, "inst-1", err5xx.FailedInstanceID())

	err4xx := consts.NewUpstreamError("http://test", 400, nil, "")
	assert.False(t, err4xx.IsRetryable())
	assert.True(t, err4xx.IsClientError())
}

func TestNetworkError_IsRetryable(t *testing.T) {
	err := &consts.NetworkError{URL: "http://test", Err: fmt.Errorf("connection refused"), InstanceID: "inst-1"}
	assert.True(t, err.IsRetryable())
	assert.Equal(t, "inst-1", err.FailedInstanceID())
	assert.Contains(t, err.Error(), "connection refused")
}

func TestRetryableErrorInterface(t *testing.T) {
	var retryable consts.RetryableError

	upstream := consts.NewUpstreamError("http://test", 503, nil, "a")
	retryable = upstream
	assert.True(t, retryable.IsRetryable())

	network := &consts.NetworkError{URL: "http://test", Err: fmt.Errorf("timeout")}
	retryable = network
	assert.True(t, retryable.IsRetryable())
}

func TestRequestStats_RecordFailedInstance(t *testing.T) {
	stats := &types.RequestStats{}
	stats.RecordFailedInstance("inst-1")
	stats.RecordFailedInstance("inst-2")
	assert.Equal(t, []string{"inst-1", "inst-2"}, stats.FailedInstances)
}
