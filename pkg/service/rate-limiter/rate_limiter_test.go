package ratelimiter

import (
	"fmt"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/types"
	"testing"

	"github.com/stretchr/testify/assert"
)

// --- Test helpers ---

// makeInstanceView creates an InstanceView with controlled stats.
//   - totalReqs: total allocated request states
//   - tokensPerReq: tokens per request state
//   - completedPrefills: number of requests marked as prefill-complete
//
// Resulting stats:
//
//	NumRequests()        = totalReqs
//	NumTokens()          = totalReqs * tokensPerReq
//	NumWaitingRequests() = totalReqs - completedPrefills
//	NumWaitingTokens()   = (totalReqs - completedPrefills) * tokensPerReq
func makeInstanceView(id string, totalReqs int, tokensPerReq int64, completedPrefills int) *lrs.InstanceView {
	worker := &types.LLMWorker{
		ID:       id,
		Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8080},
	}
	iv := lrs.NewInstanceView(worker)
	for i := 0; i < totalReqs; i++ {
		rs := lrs.NewRequestState(fmt.Sprintf("req-%d", i), tokensPerReq, id, "gw")
		iv.AllocateRequestState(rs)
		if i < completedPrefills {
			iv.MarkPrefillComplete(rs)
		}
	}
	return iv
}

func makeScheduleRequest(id string, promptTokens int) *types.ScheduleRequest {
	return &types.ScheduleRequest{
		Id:              id,
		PromptNumTokens: promptTokens,
	}
}

// ============================================================
//  Instance-level limit rules
// ============================================================

func TestInstanceRequestsLimitRule(t *testing.T) {
	tests := []struct {
		name     string
		max      int64
		numReqs  int
		expected bool
	}{
		{"within limit", 10, 5, true},
		{"at limit (requests+1 > max)", 5, 5, false},
		{"exceed limit", 5, 8, false},
		{"zero requests", 10, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &InstanceRequestsLimitRule{
				MaxRequestsPerInstance: func() int64 { return tt.max },
			}
			iv := makeInstanceView("inst-1", tt.numReqs, 100, 0)
			schReq := makeScheduleRequest("r1", 100)
			assert.Equal(t, tt.expected, rule.WithInLimit(schReq, iv))
		})
	}
}

func TestInstanceTokensLimitRule(t *testing.T) {
	tests := []struct {
		name          string
		max           int64
		numReqs       int
		tokensPerReq  int64
		inputTokenLen int
		expected      bool
	}{
		{"within limit", 1000, 2, 100, 100, true},   // 200 + 100 = 300 <= 1000
		{"at limit", 1000, 9, 100, 100, true},       // 900 + 100 = 1000 <= 1000
		{"exceed limit", 1000, 10, 100, 100, false}, // 1000 + 100 = 1100 > 1000
		{"zero existing", 500, 0, 0, 100, true},     // 0 + 100 = 100 <= 500
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &InstanceTokensLimitRule{
				MaxTokensPerInstance: func() int64 { return tt.max },
			}
			iv := makeInstanceView("inst-1", tt.numReqs, tt.tokensPerReq, 0)
			schReq := makeScheduleRequest("r1", tt.inputTokenLen)
			assert.Equal(t, tt.expected, rule.WithInLimit(schReq, iv))
		})
	}
}

func TestInstanceWaitingRequestsLimitRule(t *testing.T) {
	tests := []struct {
		name              string
		max               int64
		totalReqs         int
		completedPrefills int
		expected          bool
	}{
		{"within limit", 10, 5, 0, true},                  // 5 waiting + 1 = 6 <= 10
		{"at limit", 5, 5, 0, false},                      // 5 waiting + 1 = 6 > 5
		{"some completed reduces waiting", 5, 5, 2, true}, // 3 waiting + 1 = 4 <= 5
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &InstanceWaitingRequestsLimitRule{
				MaxWaitingRequestsPerInstance: func() int64 { return tt.max },
			}
			iv := makeInstanceView("inst-1", tt.totalReqs, 100, tt.completedPrefills)
			schReq := makeScheduleRequest("r1", 100)
			assert.Equal(t, tt.expected, rule.WithInLimit(schReq, iv))
		})
	}
}

func TestInstanceWaitingTokensLimitRule(t *testing.T) {
	tests := []struct {
		name              string
		max               int64
		totalReqs         int
		tokensPerReq      int64
		completedPrefills int
		inputTokenLen     int
		expected          bool
	}{
		{"within limit", 1000, 3, 100, 0, 100, true},                     // 300 waiting + 100 = 400 <= 1000
		{"exceed limit", 500, 5, 100, 0, 100, false},                     // 500 waiting + 100 = 600 > 500
		{"completed prefills reduce waiting", 500, 5, 100, 3, 100, true}, // 200 waiting + 100 = 300 <= 500
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &InstanceWaitingTokensLimitRule{
				MaxWaitingTokensPerInstance: func() int64 { return tt.max },
			}
			iv := makeInstanceView("inst-1", tt.totalReqs, tt.tokensPerReq, tt.completedPrefills)
			schReq := makeScheduleRequest("r1", tt.inputTokenLen)
			assert.Equal(t, tt.expected, rule.WithInLimit(schReq, iv))
		})
	}
}

// ============================================================
//  Service-level limit rules
// ============================================================

func TestServiceRequestsLimitRule(t *testing.T) {
	tests := []struct {
		name     string
		max      int64
		reqsPerI []int // requests per instance
		expected bool
	}{
		{"within limit", 10, []int{3, 4}, true},        // total=7, +1=8 <= 10*2=20
		{"at limit", 5, []int{5, 4}, true},             // total=9, +1=10 <= 5*2=10
		{"exceed limit", 3, []int{3, 4}, false},        // total=7, +1=8 > 3*2=6
		{"single instance within", 10, []int{5}, true}, // total=5, +1=6 <= 10*1=10
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &ServiceRequestsLimitRule{
				MaxRequestsPerInstance: func() int64 { return tt.max },
			}
			instances := make([]*lrs.InstanceView, len(tt.reqsPerI))
			for i, reqs := range tt.reqsPerI {
				instances[i] = makeInstanceView(fmt.Sprintf("inst-%d", i), reqs, 100, 0)
			}
			schReq := makeScheduleRequest("r1", 100)
			assert.Equal(t, tt.expected, rule.WithInLimit(schReq, instances))
		})
	}
}

func TestServiceTokensLimitRule(t *testing.T) {
	tests := []struct {
		name          string
		max           int64
		tokensPerI    []int64 // total tokens per instance (1 req each)
		inputTokenLen int
		expected      bool
	}{
		{"within limit", 1000, []int64{200, 300}, 100, true}, // 500 + 100 = 600 <= 1000*2=2000
		{"at limit", 300, []int64{200, 300}, 100, true},      // 500 + 100 = 600 <= 300*2=600
		{"exceed limit", 300, []int64{200, 300}, 200, false}, // 500 + 200 = 700 > 300*2=600
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rule := &ServiceTokensLimitRule{
				MaxTokensPerInstance: func() int64 { return tt.max },
			}
			instances := make([]*lrs.InstanceView, len(tt.tokensPerI))
			for i, tokens := range tt.tokensPerI {
				instances[i] = makeInstanceView(fmt.Sprintf("inst-%d", i), 1, tokens, 0)
			}
			schReq := makeScheduleRequest("r1", tt.inputTokenLen)
			assert.Equal(t, tt.expected, rule.WithInLimit(schReq, instances))
		})
	}
}

func TestServiceWaitingRequestsLimitRule(t *testing.T) {
	rule := &ServiceWaitingRequestsLimitRule{
		MaxWaitingRequestsPerInstance: func() int64 { return 5 },
	}
	schReq := makeScheduleRequest("r1", 100)

	t.Run("within limit", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 2, 100, 0), // 2 waiting
			makeInstanceView("i2", 1, 100, 0), // 1 waiting
		}
		// total waiting = 3, +1 = 4 <= 5*2 = 10
		assert.True(t, rule.WithInLimit(schReq, instances))
	})

	t.Run("exceed limit", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 5, 100, 0), // 5 waiting
			makeInstanceView("i2", 5, 100, 0), // 5 waiting
		}
		// total waiting = 10, +1 = 11 > 5*2 = 10
		assert.False(t, rule.WithInLimit(schReq, instances))
	})
}

func TestServiceWaitingTokensLimitRule(t *testing.T) {
	rule := &ServiceWaitingTokensLimitRule{
		MaxWaitingTokensPerInstance: func() int64 { return 500 },
	}
	schReq := makeScheduleRequest("r1", 100)

	t.Run("within limit", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 2, 100, 0), // 200 waiting tokens
			makeInstanceView("i2", 1, 100, 0), // 100 waiting tokens
		}
		// total waiting tokens = 300, +100 = 400 <= 500*2 = 1000
		assert.True(t, rule.WithInLimit(schReq, instances))
	})

	t.Run("exceed limit", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 3, 200, 0), // 600 waiting tokens
			makeInstanceView("i2", 2, 200, 0), // 400 waiting tokens
		}
		// total waiting tokens = 1000, +100 = 1100 > 500*2 = 1000
		assert.False(t, rule.WithInLimit(schReq, instances))
	})
}

// ============================================================
//  InstanceRateLimiter — Filter (multi-rule pipeline)
// ============================================================

func TestInstanceRateLimiter_Filter(t *testing.T) {
	// Limiter with requests limit = 5 and tokens limit = 500
	limiter := &InstanceRateLimiter{
		rules: []InstanceLimitRule{
			&InstanceRequestsLimitRule{MaxRequestsPerInstance: func() int64 { return 5 }},
			&InstanceTokensLimitRule{MaxTokensPerInstance: func() int64 { return 500 }},
		},
	}
	schReq := makeScheduleRequest("r1", 50)

	t.Run("all pass", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 2, 50, 0), // 2 reqs, 100 tokens
			makeInstanceView("i2", 3, 50, 0), // 3 reqs, 150 tokens
		}
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 2)
	})

	t.Run("some filtered by requests", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 2, 50, 0), // 2 reqs -> 2+1=3 <= 5 pass
			makeInstanceView("i2", 5, 50, 0), // 5 reqs -> 5+1=6 > 5 fail
		}
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
		assert.Equal(t, "i1", result[0].GetInstanceId())
	})

	t.Run("some filtered by tokens", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 1, 100, 0), // 100 tokens -> 100+50=150 <= 500 pass
			makeInstanceView("i2", 1, 460, 0), // 460 tokens -> 460+50=510 > 500 fail
		}
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
		assert.Equal(t, "i1", result[0].GetInstanceId())
	})

	t.Run("all filtered", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 5, 50, 0), // 5 reqs -> fail
			makeInstanceView("i2", 5, 50, 0), // 5 reqs -> fail
		}
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 0)
	})

	t.Run("empty input", func(t *testing.T) {
		result := limiter.Filter(schReq, nil)
		assert.Len(t, result, 0)
	})
}

// ============================================================
//  ServiceRateLimiter — Filter
// ============================================================

// ============================================================
//  Constructor coverage: NewInstanceScopeRateLimiter / NewServiceScopeRateLimiter
// ============================================================

func TestNewInstanceScopeRateLimiter(t *testing.T) {
	cfg := &mockConfig{}
	schReq := makeScheduleRequest("r1", 100)
	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}

	t.Run("normal mode", func(t *testing.T) {
		limiter := NewInstanceScopeRateLimiter(consts.NormalInferMode, cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})

	t.Run("prefill mode", func(t *testing.T) {
		limiter := NewInstanceScopeRateLimiter(consts.PrefillInferMode, cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})

	t.Run("decode mode", func(t *testing.T) {
		limiter := NewInstanceScopeRateLimiter(consts.DecodeInferMode, cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})

	t.Run("unknown mode returns empty rules", func(t *testing.T) {
		limiter := NewInstanceScopeRateLimiter("unknown", cfg)
		result := limiter.Filter(schReq, instances)
		// No rules → all instances pass through
		assert.Len(t, result, 1)
	})
}

func TestNewServiceScopeRateLimiter(t *testing.T) {
	cfg := &mockConfig{}
	schReq := makeScheduleRequest("r1", 100)
	instances := []*lrs.InstanceView{makeInstanceView("i1", 0, 0, 0)}

	t.Run("normal mode", func(t *testing.T) {
		limiter := NewServiceScopeRateLimiter(consts.NormalInferMode, cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})

	t.Run("prefill mode", func(t *testing.T) {
		limiter := NewServiceScopeRateLimiter(consts.PrefillInferMode, cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})

	t.Run("decode mode", func(t *testing.T) {
		limiter := NewServiceScopeRateLimiter(consts.DecodeInferMode, cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})

	t.Run("unknown mode returns empty rules", func(t *testing.T) {
		limiter := NewServiceScopeRateLimiter("unknown", cfg)
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 1)
	})
}

// ============================================================
//  ServiceRateLimiter — Filter
// ============================================================

func TestServiceRateLimiter_Filter(t *testing.T) {
	// Limiter with service-level requests limit = 10 per instance
	limiter := &ServiceRateLimiter{
		config: []ServiceLimitRule{
			&ServiceRequestsLimitRule{MaxRequestsPerInstance: func() int64 { return 10 }},
			&ServiceTokensLimitRule{MaxTokensPerInstance: func() int64 { return 1000 }},
		},
	}
	schReq := makeScheduleRequest("r1", 50)

	t.Run("all rules pass returns instances", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 3, 100, 0),
			makeInstanceView("i2", 4, 100, 0),
		}
		// total reqs = 7, +1 = 8 <= 10*2 = 20 -> pass
		// total tokens = 700, +50 = 750 <= 1000*2 = 2000 -> pass
		result := limiter.Filter(schReq, instances)
		assert.Len(t, result, 2)
	})

	t.Run("one rule fails returns nil", func(t *testing.T) {
		instances := []*lrs.InstanceView{
			makeInstanceView("i1", 10, 100, 0),
			makeInstanceView("i2", 10, 100, 0),
		}
		// total reqs = 20, +1 = 21 > 10*2 = 20 -> fail
		result := limiter.Filter(schReq, instances)
		assert.Nil(t, result)
	})

	t.Run("empty instances", func(t *testing.T) {
		// With no instances, max = 10*0 = 0, any request exceeds
		result := limiter.Filter(schReq, nil)
		assert.Nil(t, result)
	})
}
