package types

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"easgo/pkg/llm-gateway/metrics"
	reasoning_parser "easgo/pkg/llm-gateway/processor/reasoning-parser"
	"easgo/pkg/llm-gateway/protocol"

	"github.com/google/uuid"
	"github.com/sglang/sglang-go-grpc-sdk"
	"k8s.io/klog/v2"
)

type RequestStats struct {
	// time point
	HandleTime        time.Time
	EnQueueTime       time.Time
	DeQueueTime       time.Time
	BalanceTime       time.Time
	PrefillTime       time.Time
	DecodeBalanceTime time.Time
	FirstTime         time.Time
	ProcessTime       time.Time

	// request processor cost time
	PreprocessCost  time.Duration
	PostprocessCost time.Duration

	// now we can not get the tokens, so only record the ITL metric
	ITLs []int64

	InputTokensLen     uint64
	OutputTokensLen    uint64
	ReasoningTokensLen uint64
	MaxTokensLimit     uint64 // max_tokens for content tokens len

	HasToolCalls bool

	// Current fallback attempt using the i-th config
	FallbackAttempt int

	// When the scheduled backend instance is abnormal, retry is performed.
	RetryCount int

	DefaultLabels metrics.Labels
}

func (req *RequestStats) OutputExceedMaxTokens() bool {
	if req.MaxTokensLimit == 0 {
		return false
	}
	return req.OutputTokensLen-req.ReasoningTokensLen >= req.MaxTokensLimit
}

func Avg(itls []int64) int64 {
	len := len(itls)
	if len == 0 {
		return 0
	}
	var sum int64
	for _, itl := range itls {
		sum += itl
	}
	return sum / int64(len)
}

func (m *RequestStats) TTFT() int64 {
	if !m.FirstTime.IsZero() && !m.BalanceTime.IsZero() {
		return m.FirstTime.Sub(m.BalanceTime).Milliseconds()
	} else {
		return 0
	}
}

func (m *RequestStats) String() string {
	var durationCost []string

	if !m.ProcessTime.IsZero() && !m.HandleTime.IsZero() {
		pCost := m.ProcessTime.Sub(m.HandleTime)
		durationCost = append(durationCost, fmt.Sprintf("RT:%vms", pCost.Milliseconds()))
	}

	if !m.FirstTime.IsZero() && !m.BalanceTime.IsZero() {
		firstCost := m.FirstTime.Sub(m.BalanceTime)
		durationCost = append(durationCost, fmt.Sprintf("TTFT:%vms", firstCost.Milliseconds()))
	}

	durationCost = append(durationCost, fmt.Sprintf("ITL:%vms|%d", Avg(m.ITLs), len(m.ITLs)))

	if !m.DeQueueTime.IsZero() && !m.EnQueueTime.IsZero() {
		qCost := m.DeQueueTime.Sub(m.EnQueueTime)
		durationCost = append(durationCost, fmt.Sprintf("QT:%dms", qCost.Milliseconds()))
	}

	if !m.BalanceTime.IsZero() && !m.DeQueueTime.IsZero() {
		lbCost := m.BalanceTime.Sub(m.DeQueueTime)
		durationCost = append(durationCost, fmt.Sprintf("ST:%dms", lbCost.Milliseconds()))
	}

	durationCost = append(durationCost, fmt.Sprintf("PRE:%dms", m.PreprocessCost.Milliseconds()))
	durationCost = append(durationCost, fmt.Sprintf("POST:%dms", m.PostprocessCost.Milliseconds()))

	if !m.PrefillTime.IsZero() && !m.BalanceTime.IsZero() {
		prefillCost := m.PrefillTime.Sub(m.BalanceTime)
		durationCost = append(durationCost, fmt.Sprintf("PF:%vms", int64(prefillCost/time.Millisecond)))
	}
	if !m.DecodeBalanceTime.IsZero() && !m.PrefillTime.IsZero() {
		decodeSchCost := m.DecodeBalanceTime.Sub(m.PrefillTime)
		durationCost = append(durationCost, fmt.Sprintf("DST:%vms", int64(decodeSchCost/time.Millisecond)))
	}
	if !m.FirstTime.IsZero() && !m.DecodeBalanceTime.IsZero() {
		fdtCost := m.FirstTime.Sub(m.DecodeBalanceTime)
		durationCost = append(durationCost, fmt.Sprintf("FDT:%vms", int64(fdtCost/time.Millisecond)))
	}

	return strings.Join(durationCost, ",")
}

type HttpRequest struct {
	// downstream http request information
	Request *http.Request
	Writer  http.ResponseWriter

	StatusCode      int
	HeaderResponded bool
}

// OpenAI API interface
type LLMRequest struct {
	// model name
	Model string

	// raw request body data
	RawData string

	// request type
	Protocol protocol.ProtocolType

	// whether the response is streamed
	ClientStream bool

	// chat completion API
	ChatCompletionRequest        *protocol.ChatCompletionRequest
	ChatCompletionResponse       *protocol.ChatCompletionResponse
	ChatCompletionStreamResponse *protocol.ChatCompletionStreamResponse

	// completion API
	CompletionRequest  *protocol.CompletionRequest
	CompletionResponse *protocol.CompletionResponse

	// reasoning parser
	ReasoningParser *reasoning_parser.ReasoningParser
	ToolParser      *sglang.ToolParser

	// To convert from streaming to non-streaming, the returned results need to be merged.
	BufferChatResp *protocol.ChatCompletionResponse
	// The last chunk of the streaming response, only include finish reason and not include content.
	LastChatStreamResp *protocol.ChatCompletionStreamResponse

	BufferCompletionResponse *protocol.CompletionResponse
}

func (req *LLMRequest) GetPromptTokens() ([]uint32, bool) {
	return req.CompletionRequest.Prompt.GetUint32Slice()
}

func (req *LLMRequest) GetPromptString() (string, bool) {
	return req.CompletionRequest.Prompt.GetString()
}

func (req *LLMRequest) Stream() bool {
	switch req.Protocol {
	case protocol.OpenAICompletion:
		return req.CompletionRequest.Stream
	case protocol.OpenAIChatCompletion:
		return req.ChatCompletionRequest.Stream
	default:
		return false
	}
}

// RequestLifecycleHandler handles lifecycle events for a request.
// It provides unified interface for scheduling and resource management across different stages.
type RequestLifecycleHandler interface {
	// ScheduleDecode schedules the request for decoding stage.
	ScheduleDecode(req *RequestContext) (ScheduledResult, error)

	// OnPostPrefill is called after prefill stage completes.
	// Used for resource cleanup or state transition.
	OnPostPrefill(req *RequestContext)

	// OnPostRequest is called after the entire request completes.
	// Used for final resource cleanup.
	OnPostRequest(req *RequestContext)
}

type ScheduleContext struct {
	// forward target worker
	ScheduleResults ScheduledResult

	// schedule mode
	ScheduleMode ScheduleMode

	// inference stage, prefill or decode
	InferStage InferStage

	// after the gateway obtains resources from the scheduler, it may return them. However,
	// due to network partitioning between the gateway and scheduler, the gateway's address
	// may change upon reconnection, causing a mismatch when returning resources. Therefore,
	// the gateway that borrowed the resources is recorded here to ensure correct return.
	GatewayId string

	// whether to schedule the request
	NeedSchedule bool
}

// ErrorResponse is used when the engine returns an error directly.
// At this time, the Err of ResponseMsg is `consts.ErrorBackendBadRequest`,
// and the Message is a serialized string.
type ErrorResponse struct {
	Code  int    `json:"code"`
	Error string `json:"error"`
}

// response message for the http request
type ResponseMsg struct {
	Err     error
	Message []byte
}

type RequestContext struct {
	// request context
	Context context.Context

	// request id
	Id string

	// request information
	HttpRequest *HttpRequest
	LLMRequest  *LLMRequest

	// request statistics
	RequestStats *RequestStats

	// output stream channel
	ResponseChan chan *ResponseMsg

	// Some information forwarded to a specific inference backend
	ScheduleCtx *ScheduleContext

	// Lifecycle handler for scheduling and resource management
	lifecycleHandler RequestLifecycleHandler
}

// SetLifecycleHandler sets the lifecycle handler for the request.
func (req *RequestContext) SetLifecycleHandler(handler RequestLifecycleHandler) {
	req.lifecycleHandler = handler
}

// ScheduleDecode schedules the request for decoding stage.
func (req *RequestContext) ScheduleDecode() (ScheduledResult, error) {
	if req.lifecycleHandler != nil {
		return req.lifecycleHandler.ScheduleDecode(req)
	}
	return nil, nil
}

// TriggerPostPrefill triggers the post-prefill lifecycle hook.
func (req *RequestContext) TriggerPostPrefill() {
	if req.lifecycleHandler != nil {
		req.lifecycleHandler.OnPostPrefill(req)
	}
}

// TriggerPostRequest triggers the post-request lifecycle hook.
func (req *RequestContext) TriggerPostRequest() {
	if req.lifecycleHandler != nil {
		req.lifecycleHandler.OnPostRequest(req)
	}
}

func NewRequestContext(ctx context.Context, r *http.Request, w http.ResponseWriter) *RequestContext {
	stats := &RequestStats{HandleTime: time.Now()}

	id := r.Header.Get("x-request-id")
	if len(id) == 0 {
		id = uuid.New().String()
		// NOTE(sunbiao.sun): The inference engine uses the request ID from the request header if present.
		// Set it here to keep the request ID consistent between the gateway and the inference engine.
		// The instance-status local accounting depends on this.
		r.Header.Set("x-request-id", id)
	}
	klog.Infof("Received request: %s", id)

	httpReq := &HttpRequest{Request: r, Writer: w}
	llmRequest := &LLMRequest{}
	inferCtx := &ScheduleContext{}

	// create a request context for the new request
	req := &RequestContext{
		Context:      ctx,
		Id:           id,
		HttpRequest:  httpReq,
		LLMRequest:   llmRequest,
		RequestStats: stats,
		ScheduleCtx:  inferCtx,
	}
	return req
}
