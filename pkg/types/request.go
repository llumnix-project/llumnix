package types

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/metrics"
	reasoning_parser "llm-gateway/pkg/processor/reasoning-parser"
	"llm-gateway/pkg/protocol"
	"llm-gateway/pkg/protocol/anthropic"

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

// OpenAI API Request
type LLMRequest struct {
	// raw request body data
	RawData string

	// request type
	Protocol        protocol.ProtocolType
	BackendProtocol protocol.ProtocolType

	// original request
	OriginCompletionRequest     *protocol.CompletionRequest
	OriginChatCompletionRequest *protocol.ChatCompletionRequest

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
	BufferCompletionResponse *protocol.CompletionResponse
	BufferChatResp           *protocol.ChatCompletionResponse
	// The last chunk of the streaming response, only include finish reason and not include content.
	LastChatStreamResp *protocol.ChatCompletionStreamResponse
}

func (req *LLMRequest) setKvTransferParams(params map[string]interface{}) {
	switch req.BackendProtocol {
	case protocol.OpenAICompletion:
		req.CompletionResponse.KvTransferParams = params
	case protocol.OpenAIChatCompletion:
		req.ChatCompletionResponse.KvTransferParams = params
	default:
		klog.Errorf("unsupported backend protocol: %v to get response kv transfer params", req.BackendProtocol)
	}
}

func (req *LLMRequest) getBackendRequestURL() string {
	switch req.BackendProtocol {
	case protocol.OpenAICompletion:
		return protocol.CompletionsPath
	case protocol.OpenAIChatCompletion:
		return protocol.ChatCompletionsPath
	default:
		klog.Errorf("unsupported backend protocol: %v to get backend request url", req.BackendProtocol)
		return ""
	}
}

func (req *LLMRequest) getRequestModel() string {
	switch req.Protocol {
	case protocol.OpenAICompletion:
		return req.OriginCompletionRequest.Model
	case protocol.OpenAIChatCompletion:
		return req.OriginChatCompletionRequest.Model
	default:
		klog.Errorf("get model unsupported protocol: %v", req.Protocol)
		return ""
	}
}

func (req *LLMRequest) getPromptTokens() ([]uint32, error) {
	// Here we read CompletionRequest.Prompt because after being processed by the tokenizer,
	// it becomes token IDs, not the original prompt string
	if tokens, ok := req.CompletionRequest.Prompt.GetUint32Slice(); ok {
		return tokens, nil
	} else {
		return nil, fmt.Errorf("failed to get prompt tokens")
	}
}

func (req *LLMRequest) getPromptString() (string, error) {
	switch req.Protocol {
	case protocol.OpenAICompletion:
		if promptStr, ok := req.OriginCompletionRequest.Prompt.GetString(); ok {
			return promptStr, nil
		} else {
			return "", fmt.Errorf("failed to get prompt string")
		}
	case protocol.OpenAIChatCompletion:
		jsonData, err := json.Marshal(req.OriginChatCompletionRequest.Messages)
		if err != nil {
			klog.Errorf("failed to marshal messages: %v", err)
			return "", fmt.Errorf("failed to marshal messages: %v", err)
		}
		// Here we remove the last ] mainly to ensure correct prefix matching, as the last ] may affect the matching result
		return string(jsonData[0 : len(jsonData)-1]), nil
	default:
		return "", fmt.Errorf("failed to get prompt string: unsupported protocol %v", req.Protocol)
	}
}

func (req *LLMRequest) clientStream() bool {
	switch req.Protocol {
	case protocol.OpenAICompletion:
		return req.OriginCompletionRequest.Stream
	case protocol.OpenAIChatCompletion:
		return req.OriginChatCompletionRequest.Stream
	default:
		return false
	}
}

func (req *LLMRequest) inferenceStream() bool {
	switch req.BackendProtocol {
	case protocol.OpenAICompletion:
		return req.CompletionRequest.Stream
	case protocol.OpenAIChatCompletion:
		return req.ChatCompletionRequest.Stream
	default:
		return false
	}
}

// Anthropic API Request
type AnthropicRequest struct {
	// raw request body data
	RawData string

	// Anthropic Request
	Request      *anthropic.Request
	ResponseData []byte
	// The reason Anthropic Response is not defined here is that the relevant
	// Anthropic conversion directly converts into the returned sequence data,
	// and it does not rely on the related data structures

	// OpenAI Request
	OpenAIRequest        *protocol.ChatCompletionRequest
	OpenAIResponse       *protocol.ChatCompletionResponse
	OpenAIStreamResponse *protocol.ChatCompletionStreamResponse

	// record first response chunk or not
	IsNotFirst bool // default value is false

	// streaming response buffer
	StreamResponseBuffer *anthropic.StreamingResponseBuffer
}

func (req *AnthropicRequest) getPromptString() (string, error) {
	jsonData, err := json.Marshal(req.Request.Messages)
	if err != nil {
		klog.Errorf("failed to marshal messages: %v", err)
		return "", fmt.Errorf("failed to marshal messages: %v", err)
	}
	return string(jsonData[0 : len(jsonData)-1]), nil
}

// RequestLifecycleHandler handles lifecycle events for a request.
// It provides unified interface for scheduling and resource management across different stages.
type RequestLifecycleHandler interface {
	// ScheduleDecode schedules the request for decoding stage.
	ScheduleDecode(req *RequestContext) (ScheduledResult, error)

	// OnPreRequest is called before the request is processed.
	OnPreRequest(req *RequestContext)

	// OnPostRequest is called after the entire request completes.
	// Used for final resource cleanup.
	OnPostRequest(req *RequestContext)

	// OnPostPrefill and OnPostDecode are called when the backend inference is streaming
	// OnPostPrefill is called after prefill stage completes.
	OnPostPrefill(req *RequestContext)

	// OnPostDecode is called after every decode chunk completes.
	OnPostDecode(req *RequestContext)
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
	// Request context
	Context context.Context

	// Request id
	Id string

	// request information
	HttpRequest *HttpRequest

	// Currently, the design is that a Handler uses one data structure for communication between processors,
	// but this may change in the future, and some processor adapters may be added for adaptation
	RequestType      string
	LLMRequest       *LLMRequest       // OpenAI
	AnthropicRequest *AnthropicRequest // Anthropic

	// Request statistics
	RequestStats *RequestStats

	// Output stream channel
	ResponseChan chan *ResponseMsg

	// Some information forwarded to a specific inference backend
	ScheduleCtx *ScheduleContext

	// Lifecycle handler for scheduling and resource management
	lifecycleHandler RequestLifecycleHandler
}

func (req *RequestContext) ClientStream() bool {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.LLMRequest.clientStream()
	case consts.AnthropicHandlerName:
		return req.AnthropicRequest.Request.Stream
	default:
		klog.Errorf("client stream: unsupported request type: %v", req.RequestType)
		return false
	}
}

// InferenceStream returns whether the request is an inference stream.
func (req *RequestContext) InferenceStream() bool {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.LLMRequest.inferenceStream()
	case consts.AnthropicHandlerName:
		return req.AnthropicRequest.OpenAIRequest.Stream
	default:
		klog.Errorf("inference stream: unsupported request type: %v", req.RequestType)
		return false
	}
}

func (req *RequestContext) marshalOpenAIRequest(args map[string]interface{}) ([]byte, error) {
	oaiReq := req.LLMRequest
	switch oaiReq.BackendProtocol {
	case protocol.OpenAICompletion:
		protocol.ApplyRequestArgs(oaiReq.CompletionRequest, args)
		data, err := json.Marshal(oaiReq.CompletionRequest)
		return data, err
	case protocol.OpenAIChatCompletion:
		protocol.ApplyRequestArgs(oaiReq.ChatCompletionRequest, args)
		data, err := json.Marshal(oaiReq.ChatCompletionRequest)
		return data, err
	default:
		klog.Errorf("unsupported protocol type: %v", req.LLMRequest.Protocol)
		return nil, fmt.Errorf("unsupported protocol type: %v", req.LLMRequest.Protocol)
	}
}

func (req *RequestContext) marshalAnthropicRequest(args map[string]interface{}) ([]byte, error) {
	protocol.ApplyRequestArgs(req.AnthropicRequest.OpenAIRequest, args)
	data, err := json.Marshal(req.AnthropicRequest.OpenAIRequest)
	return data, err
}

// MarshalRequestWithArgs marshals the request with the given args.
func (req *RequestContext) MarshalRequestWithArgs(args map[string]interface{}) ([]byte, error) {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.marshalOpenAIRequest(args)
	case consts.AnthropicHandlerName:
		return req.marshalAnthropicRequest(args)
	default:
		return nil, fmt.Errorf("marshal request: unsupported request type: %v", req.RequestType)
	}
}

func (req *RequestContext) SetKvTransferParams(params map[string]interface{}) {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		req.LLMRequest.setKvTransferParams(params)
	case consts.AnthropicHandlerName:
		req.AnthropicRequest.OpenAIRequest.KvTransferParams = params
	default:
		klog.Errorf("unsupported request type: %v to get kv transfer params", req.RequestType)
	}
}

// GetRequestRawData returns the raw data of the request.
func (req *RequestContext) GetRequestRawData() string {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.LLMRequest.RawData
	case consts.AnthropicHandlerName:
		return req.AnthropicRequest.RawData
	default:
		klog.Errorf("unsupported request type: %v to get request raw data", req.RequestType)
		return ""
	}
}

// GetURLPath returns the request url of the request.
func (req *RequestContext) GetURLPath() string {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return protocol.CompletionsPath
	case consts.AnthropicHandlerName:
		return protocol.ChatCompletionsPath
	default:
		klog.Errorf("unsupported request type: %v to get request url", req.RequestType)
		return ""
	}
}

// GetBackendURLPath returns the backend request url of the request.
func (req *RequestContext) GetBackendURLPath() string {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.LLMRequest.getBackendRequestURL()
	case consts.AnthropicHandlerName:
		return protocol.ChatCompletionsPath
	default:
		klog.Errorf("unsupported request type: %v to get backend request url", req.RequestType)
		return ""
	}
}

// GetModel returns the model name of the request.
func (req *RequestContext) GetRequestModel() string {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.LLMRequest.getRequestModel()
	case consts.AnthropicHandlerName:
		return req.AnthropicRequest.Request.Model
	default:
		klog.Errorf("unsupported request type: %v to get model", req.RequestType)
		return ""
	}
}

// GetPromptTokens returns the prompt tokens of the request.
func (req *RequestContext) GetPromptTokens() ([]uint32, error) {
	if req.RequestType == consts.OpenAIHandlerName {
		return req.LLMRequest.getPromptTokens()
	}
	return nil, fmt.Errorf("not support get tokens")
}

// GetPromptString returns the prompt string of the request.
func (req *RequestContext) GetPromptString() (string, error) {
	switch req.RequestType {
	case consts.OpenAIHandlerName:
		return req.LLMRequest.getPromptString()
	case consts.AnthropicHandlerName:
		return req.AnthropicRequest.getPromptString()
	}
	return "", fmt.Errorf("not support get prompt string")
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

// TriggerPostDecode triggers the post-decode lifecycle hook.
func (req *RequestContext) TriggerPostDecode() {
	if req.lifecycleHandler != nil {
		req.lifecycleHandler.OnPostDecode(req)
	}
}

// TriggerPreRequest triggers the pre-request lifecycle hook.
func (req *RequestContext) TriggerPreRequest() {
	if req.lifecycleHandler != nil {
		req.lifecycleHandler.OnPreRequest(req)
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
	}
	klog.Infof("Received request: %s", id)

	httpReq := &HttpRequest{Request: r, Writer: w}
	inferCtx := &ScheduleContext{}

	// create a request context for the new request
	req := &RequestContext{
		Context:      ctx,
		Id:           id,
		HttpRequest:  httpReq,
		RequestStats: stats,
		ScheduleCtx:  inferCtx,
	}
	return req
}
