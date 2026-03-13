package mirror

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/gateway/property"
	"llumnix/pkg/types"
)

// --- test helpers ---

type mockConfigProvider struct {
	mu  sync.RWMutex
	cfg property.MirrorConfig
}

func (m *mockConfigProvider) GetMirrorConfig() property.MirrorConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *mockConfigProvider) Update(cfg property.MirrorConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

func newTestRequestContext(method, path, body string) *types.RequestContext {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Custom", "test-value")
	w := httptest.NewRecorder()
	ctx := types.NewRequestContext(r.Context(), r, w)
	ctx.LLMRequest.RawData = body
	ctx.ResponseChan = make(chan *types.ResponseMsg, 100)
	return ctx
}

func newMirrorWithConfig(cfg property.MirrorConfig) (*Mirror, *mockConfigProvider) {
	provider := &mockConfigProvider{cfg: cfg}
	return NewMirror(provider), provider
}

// --- NewMirror tests ---

func TestNewMirror(t *testing.T) {
	provider := &mockConfigProvider{}
	m := NewMirror(provider)

	assert.NotNil(t, m)
	assert.NotNil(t, m.client)
	assert.Equal(t, provider, m.configProvider)
}

// --- Enabled tests ---

func TestEnabled_NilProvider(t *testing.T) {
	m := &Mirror{configProvider: nil}
	assert.False(t, m.Enabled())
}

func TestEnabled_Disabled(t *testing.T) {
	m, _ := newMirrorWithConfig(property.MirrorConfig{Enable: false})
	assert.False(t, m.Enabled())
}

func TestEnabled_Enabled(t *testing.T) {
	m, _ := newMirrorWithConfig(property.MirrorConfig{Enable: true})
	assert.True(t, m.Enabled())
}

// --- TryMirror tests ---

func TestTryMirror_SkipsWhenTargetEmpty(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: "",
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int32(0), called.Load())
}

func TestTryMirror_SkipsWhenRatioZero(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  0,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int32(0), called.Load())
}

func TestTryMirror_SkipsWhenRatioNegative(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  -10,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(200 * time.Millisecond)

	assert.Equal(t, int32(0), called.Load())
}

func TestTryMirror_SendsRequestAtFullRatio(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, int32(1), called.Load())
}

func TestTryMirror_CorrectURL(t *testing.T) {
	var receivedPath string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, "/v1/chat/completions", receivedPath)
}

func TestTryMirror_CorrectMethod(t *testing.T) {
	var receivedMethod string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/completions", `{"prompt":"hello"}`)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, "POST", receivedMethod)
}

func TestTryMirror_BodyCopied(t *testing.T) {
	var receivedBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 4096)
		n, _ := r.Body.Read(buf)
		receivedBody = string(buf[:n])
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	body := `{"model":"gpt-4","messages":[{"role":"user","content":"hello"}]}`
	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", body)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, body, receivedBody)
}

func TestTryMirror_HeadersCopied(t *testing.T) {
	var mu sync.Mutex
	receivedHeaders := make(http.Header)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeaders = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, "application/json", receivedHeaders.Get("Content-Type"))
	assert.Equal(t, "test-value", receivedHeaders.Get("X-Custom"))
}

func TestTryMirror_AuthorizationOverride(t *testing.T) {
	var receivedAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:        true,
		Target:        backend.URL,
		Ratio:         100,
		Authorization: "Bearer mirror-token-123",
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	reqCtx.HttpRequest.Request.Header.Set("Authorization", "Bearer original-token")

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, "Bearer mirror-token-123", receivedAuth)
}

func TestTryMirror_NoAuthorizationOverrideWhenEmpty(t *testing.T) {
	var receivedAuth string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:        true,
		Target:        backend.URL,
		Ratio:         100,
		Authorization: "",
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	reqCtx.HttpRequest.Request.Header.Set("Authorization", "Bearer original-token")

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, "Bearer original-token", receivedAuth)
}

func TestTryMirror_TimeoutApplied(t *testing.T) {
	var requestReceived atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived.Add(1)
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:  true,
		Target:  backend.URL,
		Ratio:   100,
		Timeout: 100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, int32(1), requestReceived.Load(), "request should have been sent even though it times out")
}

func TestTryMirror_UsesRequestContextWhenTimeoutZero(t *testing.T) {
	var requestReceived atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:  true,
		Target:  backend.URL,
		Ratio:   100,
		Timeout: 0,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, int32(1), requestReceived.Load())
}

func TestTryMirror_CancelledContextStopsMirror(t *testing.T) {
	var requestCompleted atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		requestCompleted.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:  true,
		Target:  backend.URL,
		Ratio:   100,
		Timeout: 0,
	})

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test"}`))
	r = r.WithContext(ctx)
	w := httptest.NewRecorder()
	reqCtx := types.NewRequestContext(ctx, r, w)
	reqCtx.LLMRequest.RawData = `{"model":"test"}`

	cancel()
	m.TryMirror(reqCtx)
	time.Sleep(500 * time.Millisecond)

	assert.Equal(t, int32(0), requestCompleted.Load(), "mirror should not complete when context is cancelled")
}

func TestTryMirror_RatioSampling(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  50,
	})

	totalRequests := 200
	for i := 0; i < totalRequests; i++ {
		reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
		m.TryMirror(reqCtx)
	}

	time.Sleep(2 * time.Second)

	mirroredCount := int(called.Load())
	assert.Greater(t, mirroredCount, 40, "expected at least 20%% of requests to be mirrored")
	assert.Less(t, mirroredCount, 160, "expected at most 80%% of requests to be mirrored")
}

func TestTryMirror_NonBlockingExecution(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(1 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	start := time.Now()
	m.TryMirror(reqCtx)
	elapsed := time.Since(start)

	assert.Less(t, elapsed, 100*time.Millisecond, "TryMirror should return immediately without blocking")
}

func TestTryMirror_BackendError_DoesNotPanic(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"internal server error"}`))
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:    true,
		Target:    backend.URL,
		Ratio:     100,
		EnableLog: true,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	assert.NotPanics(t, func() {
		m.TryMirror(reqCtx)
		time.Sleep(500 * time.Millisecond)
	})
}

func TestTryMirror_UnreachableTarget_DoesNotPanic(t *testing.T) {
	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable:    true,
		Target:    "http://127.0.0.1:1",
		Ratio:     100,
		EnableLog: true,
	})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)

	assert.NotPanics(t, func() {
		m.TryMirror(reqCtx)
		time.Sleep(500 * time.Millisecond)
	})
}

func TestTryMirror_MultipleRequestsConcurrently(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
			m.TryMirror(reqCtx)
		}()
	}
	wg.Wait()
	time.Sleep(1 * time.Second)

	assert.Equal(t, int32(10), called.Load())
}

func TestTryMirror_DifferentPaths(t *testing.T) {
	paths := make(chan string, 10)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, _ := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})

	testCases := []string{"/v1/chat/completions", "/v1/completions", "/v1/embeddings"}
	for _, p := range testCases {
		reqCtx := newTestRequestContext("POST", p, `{"model":"test"}`)
		m.TryMirror(reqCtx)
	}

	time.Sleep(1 * time.Second)
	close(paths)

	receivedPaths := make(map[string]bool)
	for p := range paths {
		receivedPaths[p] = true
	}

	for _, expected := range testCases {
		assert.True(t, receivedPaths[expected], "expected path %s to be mirrored", expected)
	}
}

// --- Hot-reload tests ---

func TestHotReload_DisabledToEnabled(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, provider := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})

	// Initially disabled via empty target
	provider.Update(property.MirrorConfig{Enable: true, Target: "", Ratio: 100})
	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(0), called.Load(), "should not mirror when target is empty")

	// Hot-reload: enable with a valid target
	provider.Update(property.MirrorConfig{Enable: true, Target: backend.URL, Ratio: 100})
	reqCtx2 := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx2)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), called.Load(), "should mirror after config updated")
}

func TestHotReload_EnabledToDisabled(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, provider := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})

	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), called.Load(), "should mirror initially")

	// Hot-reload: disable by setting ratio to 0
	provider.Update(property.MirrorConfig{Enable: true, Target: backend.URL, Ratio: 0})
	reqCtx2 := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx2)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), called.Load(), "should not mirror after ratio set to 0")
}

func TestHotReload_ChangeTarget(t *testing.T) {
	var target1Called, target2Called atomic.Int32
	backend1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target1Called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend1.Close()
	backend2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target2Called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend2.Close()

	m, provider := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend1.URL,
		Ratio:  100,
	})

	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), target1Called.Load())
	assert.Equal(t, int32(0), target2Called.Load())

	// Hot-reload: switch to target2
	provider.Update(property.MirrorConfig{Enable: true, Target: backend2.URL, Ratio: 100})
	reqCtx2 := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx2)
	time.Sleep(300 * time.Millisecond)
	assert.Equal(t, int32(1), target1Called.Load(), "target1 should not receive more requests")
	assert.Equal(t, int32(1), target2Called.Load(), "target2 should receive the new request")
}

func TestHotReload_ChangeRatio(t *testing.T) {
	var called atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, provider := newMirrorWithConfig(property.MirrorConfig{
		Enable: true,
		Target: backend.URL,
		Ratio:  100,
	})

	for i := 0; i < 10; i++ {
		reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
		m.TryMirror(reqCtx)
	}
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(10), called.Load(), "all requests should be mirrored at ratio=100")

	// Hot-reload: change ratio to 0
	called.Store(0)
	provider.Update(property.MirrorConfig{Enable: true, Target: backend.URL, Ratio: 0})
	for i := 0; i < 10; i++ {
		reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
		m.TryMirror(reqCtx)
	}
	time.Sleep(500 * time.Millisecond)
	assert.Equal(t, int32(0), called.Load(), "no requests should be mirrored at ratio=0")
}

func TestHotReload_ChangeAuthorization(t *testing.T) {
	var mu sync.Mutex
	receivedAuths := []string{}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedAuths = append(receivedAuths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	m, provider := newMirrorWithConfig(property.MirrorConfig{
		Enable:        true,
		Target:        backend.URL,
		Ratio:         100,
		Authorization: "Bearer token-v1",
	})

	reqCtx := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx)
	time.Sleep(300 * time.Millisecond)

	// Hot-reload: change authorization
	provider.Update(property.MirrorConfig{
		Enable: true, Target: backend.URL, Ratio: 100, Authorization: "Bearer token-v2",
	})
	reqCtx2 := newTestRequestContext("POST", "/v1/chat/completions", `{"model":"test"}`)
	m.TryMirror(reqCtx2)
	time.Sleep(300 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 2, len(receivedAuths))
	assert.Equal(t, "Bearer token-v1", receivedAuths[0])
	assert.Equal(t, "Bearer token-v2", receivedAuths[1])
}

func TestHotReload_EnabledFlag(t *testing.T) {
	m, provider := newMirrorWithConfig(property.MirrorConfig{Enable: false})
	assert.False(t, m.Enabled())

	provider.Update(property.MirrorConfig{Enable: true})
	assert.True(t, m.Enabled())

	provider.Update(property.MirrorConfig{Enable: false})
	assert.False(t, m.Enabled())
}
