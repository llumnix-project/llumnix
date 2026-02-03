package lrs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/tokenizer"
	"llm-gateway/pkg/types"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type RequestReporter struct {
	mux           sync.RWMutex
	reqTokenState map[string]*ReqTokenState

	schedulerResolver resolver.Resolver
	client            *http.Client
	config            *options.Config
}

var (
	once      sync.Once
	gReporter *RequestReporter
)

func NewRequestReporter(config *options.Config) *RequestReporter {
	if len(config.LocalTestIPs) > 0 || !config.EnableRequestReport() {
		return nil
	}
	once.Do(func() {
		gReporter = &RequestReporter{
			reqTokenState:     make(map[string]*ReqTokenState),
			schedulerResolver: resolver.CreateSchedulerResolver(config),
			client:            &http.Client{Timeout: 3 * time.Second},
			config:            config,
		}
		go gReporter.reportLoop()
	})
	return gReporter
}

func (r *RequestReporter) AddReqTokenState(rs *ReqTokenState) {
	r.mux.Lock()
	defer r.mux.Unlock()

	id := rs.req.Id
	if _, ok := r.reqTokenState[id]; ok {
		klog.Warningf("Request id is duplicated: %s", id)
		return
	}
	r.reqTokenState[id] = rs
}

func (r *RequestReporter) DeleteReqTokenState(id string) {
	r.mux.Lock()
	defer r.mux.Unlock()
	delete(r.reqTokenState, id)
}

type ReqReportData struct {
	Id         string `json:"id"`
	Model      string `json:"model"`
	InferMode  string `json:"infer_mode"`
	InstanceId string `json:"instance_id"`
	GatewayId  string `json:"gateway_id"`
	NumTokens  uint64 `json:"num_tokens"`
}

type ReqReportDataArray = []ReqReportData

func (r *RequestReporter) report() {
	reportDatas := make(ReqReportDataArray, 0, 32)
	r.mux.RLock()
	for _, rs := range r.reqTokenState {
		reportDatas = append(reportDatas, ReqReportData{
			Id:         rs.req.Id,
			Model:      rs.req.GetRequestModel(),
			InferMode:  rs.InferMode,
			InstanceId: rs.InstanceId,
			GatewayId:  rs.GatewayId,
			NumTokens:  rs.GetNumTokens(),
		})
	}
	r.mux.RUnlock()

	endpoint, err := r.schedulerResolver.GetEndpoints()
	if len(endpoint) == 0 || err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("http://%s/request_report", endpoint[0].String())
	data, _ := json.Marshal(reportDatas)
	klog.V(3).Infof("report request data: %s", string(data))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		klog.Warningf("report: could not create request: %v, url: %s", err, url)
		return
	}
	if len(r.config.ServiceToken) > 0 {
		req.Header.Add("Authorization", r.config.ServiceToken)
	}

	response, err := r.client.Do(req)
	if err != nil {
		klog.Warningf("report: could not send request: %v, url: %s", err, url)
		return
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		klog.Warningf("report: request failed: %s, url: %s", response.Status, url)
		return
	}
}

func (r *RequestReporter) reportLoop() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("request report loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go r.reportLoop()
		}
	}()
	// report every 5 seconds
	for {
		time.Sleep(time.Duration(r.config.RequestsReporterDuration) * time.Second)
		r.report()
	}
}

// ReqTokenState indicates the token length of the current request processing
// it record token length when enable tokenizer, otherwise it records message text count
type ReqTokenState struct {
	req        *types.RequestContext
	Model      string
	InferMode  string
	InstanceId string
	GatewayId  string

	mx sync.Mutex

	numTokens     uint64 // directly update with current num tokens
	lastNumTokens uint64 // last num tokens
	ResponseText  string // current response text
}

func NewReqTokenState(
	req *types.RequestContext, model string, inferMode string, instanceId string, gatewayId string) *ReqTokenState {
	promptTokens, err := req.GetPromptTokens()
	if err != nil {
		klog.Errorf("Failed to get prompt tokens: %v", err)
		return nil
	}

	return &ReqTokenState{
		req:           req,
		lastNumTokens: uint64(len(promptTokens)),
		Model:         model,
		InferMode:     inferMode,
		InstanceId:    instanceId,
		GatewayId:     gatewayId,
	}
}

func (rs *ReqTokenState) UpdateNumTokens(num uint64) {
	rs.mx.Lock()
	defer rs.mx.Unlock()
	rs.numTokens = num
}

func (rs *ReqTokenState) AppendResponseText(text string) {
	rs.mx.Lock()
	defer rs.mx.Unlock()
	rs.ResponseText += text
}

func (rs *ReqTokenState) GetNumTokens() uint64 {
	rs.mx.Lock()
	defer rs.mx.Unlock()
	count := rs.numTokens
	if count != 0 {
		return count
	}
	tk, err := tokenizer.GetTokenizer()
	if err != nil {
		count = uint64(len(rs.ResponseText))
	} else {
		tokens, err := tk.Encode(rs.ResponseText, false)
		if err != nil {
			klog.Warningf("Failed to encode response text: %v", err)
		}
		count = uint64(len(tokens))
	}
	rs.ResponseText = ""
	rs.lastNumTokens += count
	return rs.lastNumTokens
}
