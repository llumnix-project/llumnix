package structs

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"easgo/pkg/llm-gateway/protocol"
	"easgo/pkg/llm-gateway/utils"

	"github.com/google/uuid"
	"github.com/valyala/fastjson"
	"k8s.io/klog/v2"
)

type RequestState interface {
	UpdateNumTokens(num uint64)
	AppendResponseText(text string)
	GetNumTokens() uint64
}

type Request struct {
	// request id
	Id string

	// http request interface
	RawData []byte
	Data    []byte
	Prompt  []byte

	// used in vllm-mooncake
	KVTransferParams map[string]interface{}

	Arena  fastjson.Arena
	ReqObj *fastjson.Value

	Req            *http.Request
	Writer         http.ResponseWriter
	ResponseHeader http.Header

	StatusCode int
	DownStream string

	Done chan int

	// access log
	Logged bool

	// using request processor
	RequestProcessor RequestProcessor

	// schedule request token, include upstream endpoint
	Token *Token
	// for split mode, `Token` is a prefill node, and `SecondToken` is a decode node.
	SecondToken *Token
	// for external requests
	ExternalEp *ExternalEndpoint

	// current fallback attempt using the i-th config
	FallbackAttempt int

	// separate scheduling for p and d
	ScheduleStage string

	// for non-inference requests, the request does not require scheduling
	NoSchedule bool

	// after the gateway obtains resources from the scheduler, it must return them. However,
	//  due to network partitioning between the gateway and scheduler, the gateway's address
	// may change upon reconnection, causing a mismatch when returning resources. Therefore,
	// the gateway that borrowed the resources is recorded here to ensure correct return.
	GatewayEp Endpoint

	// model queue
	ProxyQueue *utils.ProxyQueue

	// stream copy interrupt failed, then retry
	HeaderResponded bool
	Retrying        bool
	ReScheduleCount int

	// request type
	Protocol              protocol.ProtocolType
	ChaCompletionsRequest *protocol.ChatCompletionRequest
	CompletionsRequest    *protocol.CompletionRequest
	PrefillResponse       *protocol.PDSplitCompletionsPrefillResponse
	Stream                bool // downstream: client stream or not
	InferStream           bool // upstream: llm server inference stream or not

	Model         string
	DefaultLabels Labels

	// time point
	HandleTime        time.Time
	EnQueueTime       time.Time
	DeQueueTime       time.Time
	BalanceTime       time.Time
	PrefillTime       time.Time
	DecodeBalanceTime time.Time
	FirstTime         time.Time
	ProcessTime       time.Time

	// processor cost time
	PreprocessCost  time.Duration
	PostprocessCost time.Duration

	// now we can not get the tokens, so only record the ITL metric
	Itls []int64

	MaxTokensLimit     uint64 // max_tokens for content tokens len
	InputTokensLen     uint64
	OutputTokensLen    uint64
	ReasoningTokensLen uint64

	// record current request input and generated content count
	// it record token length when enable tokenizer, otherwise  it record message text count
	RequestState              RequestState
	CurrentCompletionResponse interface{} // include completion and chat completion response

	ChatCompletionResponse *protocol.ChatCompletionResponse // for chat completion response in tokenized chat non-stream request
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

func (req *Request) SprintDuration() string {
	var durationCost []string

	if !req.ProcessTime.IsZero() && !req.HandleTime.IsZero() {
		pCost := req.ProcessTime.Sub(req.HandleTime)
		durationCost = append(durationCost, fmt.Sprintf("RT:%vms", pCost.Milliseconds()))
	}

	if !req.FirstTime.IsZero() && !req.BalanceTime.IsZero() {
		firstCost := req.FirstTime.Sub(req.BalanceTime)
		durationCost = append(durationCost, fmt.Sprintf("TTFT:%vms", firstCost.Milliseconds()))
	}

	durationCost = append(durationCost, fmt.Sprintf("ITL:%vms|%d", Avg(req.Itls), len(req.Itls)))

	if !req.DeQueueTime.IsZero() && !req.EnQueueTime.IsZero() {
		qCost := req.DeQueueTime.Sub(req.EnQueueTime)
		durationCost = append(durationCost, fmt.Sprintf("QT:%dms", qCost.Milliseconds()))
	}

	if !req.BalanceTime.IsZero() && !req.DeQueueTime.IsZero() {
		lbCost := req.BalanceTime.Sub(req.DeQueueTime)
		durationCost = append(durationCost, fmt.Sprintf("ST:%dms", lbCost.Milliseconds()))
	}

	if !req.ProcessTime.IsZero() && !req.FirstTime.IsZero() {
		durationCost = append(durationCost, fmt.Sprintf("PREPROCESS:%dms", req.PreprocessCost.Milliseconds()))
		durationCost = append(durationCost, fmt.Sprintf("POSTPROCESS:%dms", req.PostprocessCost.Milliseconds()))
	}
	if !req.PrefillTime.IsZero() && !req.BalanceTime.IsZero() {
		prefillCost := req.PrefillTime.Sub(req.BalanceTime)
		durationCost = append(durationCost, fmt.Sprintf("PF:%vms", int64(prefillCost/time.Millisecond)))
	}
	if !req.DecodeBalanceTime.IsZero() && !req.PrefillTime.IsZero() {
		decodeSchCost := req.DecodeBalanceTime.Sub(req.PrefillTime)
		durationCost = append(durationCost, fmt.Sprintf("DST:%vms", int64(decodeSchCost/time.Millisecond)))
	}
	if !req.FirstTime.IsZero() && !req.DecodeBalanceTime.IsZero() {
		fdtCost := req.FirstTime.Sub(req.DecodeBalanceTime)
		durationCost = append(durationCost, fmt.Sprintf("FDT:%vms", int64(fdtCost/time.Millisecond)))
	}

	return strings.Join(durationCost, ",")
}

func IsWebsocketRequest(r *http.Request) bool {
	return utils.HeaderContainsValue(r.Header, "Connection", "upgrade") &&
		utils.HeaderContainsValue(r.Header, "Upgrade", "websocket")
}

func NeedSchedule(req *http.Request) bool {
	return req.URL.String() != "/v1/models"
}

func NewRequest(r *http.Request, w http.ResponseWriter) *Request {
	tStart := time.Now()

	id := r.Header.Get("x-request-id")
	if len(id) == 0 {
		id = uuid.New().String()
	}

	// create a request context for the new request
	req := &Request{
		Id:                 id,
		Req:                r,
		Writer:             w,
		Done:               make(chan int, 1),
		FallbackAttempt:    0,
		HeaderResponded:    false,
		Retrying:           false,
		Protocol:           protocol.ProtocolUnknown,
		ReScheduleCount:    0,
		HandleTime:         tStart,
		EnQueueTime:        time.Now(),
		Arena:              fastjson.Arena{},
		MaxTokensLimit:     0,
		OutputTokensLen:    0,
		InputTokensLen:     0,
		ReasoningTokensLen: 0,
		NoSchedule:         !NeedSchedule(r),
	}

	return req
}

func (req *Request) OutputExceedMaxTokens() bool {
	if req.MaxTokensLimit == 0 {
		return false
	}
	return req.OutputTokensLen-req.ReasoningTokensLen >= req.MaxTokensLimit
}

// PromptLength return prompt token length if tokenizer is enabled, otherwise return input length
func (req *Request) PromptLength() uint64 {
	promptLen := req.InputTokensLen
	if promptLen == 0 {
		promptLen = uint64(len(req.Prompt))
	}
	return promptLen
}

func (req *Request) ParseResponse(data []byte) {
	if string(data) == "[DONE]" {
		req.CurrentCompletionResponse = nil
		return
	}
	r, err := utils.UnmarshalResponse(req.Protocol, data)
	if err != nil {
		klog.Warningf("parse response: unmarshal response failed: %v: %v", req.Protocol, string(data))
		return
	}
	switch req.Protocol {
	case protocol.ProtocolCompletions:
		req.CurrentCompletionResponse = r.(*protocol.CompletionResponse)
	case protocol.ProtocolTokenizedChat, protocol.ProtocolChat:
		req.CurrentCompletionResponse = r.(*protocol.ChatCompletionResponse)
	default:
		panic("parse response, not support this protocol type")
	}
}

func (req *Request) UpdateTokensState(data []byte) {
	// last done response is not a completion response, ignore it
	if req.CurrentCompletionResponse == nil {
		return
	}
	usage := utils.GetResponseUsage(req.CurrentCompletionResponse)
	if usage != nil {
		req.RequestState.UpdateNumTokens(usage.TotalTokens)
		klog.V(3).Infof("%s usage current total tokens len: %v", req.Id, usage.TotalTokens)
	} else {
		text := utils.GetResponseText(req.Protocol, req.CurrentCompletionResponse)
		req.RequestState.AppendResponseText(text)
	}
}
