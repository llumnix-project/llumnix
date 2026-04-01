package lrs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/consts"
	"llumnix/pkg/gateway/tokenizer"
	"llumnix/pkg/resolver"
	"llumnix/pkg/types"
)

type RequestStateTracker struct {
	mux           sync.RWMutex
	reqTokenState map[string]*RequestTokenState

	schedulerResolver resolver.Resolver
	client            *http.Client
	config            *options.GatewayConfig
}

var (
	once     sync.Once
	gTracker *RequestStateTracker
)

func NewRequestStateTracker(config *options.GatewayConfig) *RequestStateTracker {
	once.Do(func() {
		gTracker = &RequestStateTracker{
			reqTokenState:     make(map[string]*RequestTokenState),
			schedulerResolver: resolver.CreateSchedulerResolver(&config.DiscoveryConfig),
			client:            &http.Client{Timeout: 3 * time.Second},
			config:            config,
		}
		go gTracker.reportLoop()
	})
	return gTracker
}

func (r *RequestStateTracker) AddRequestState(rs *RequestTokenState) {
	r.mux.Lock()
	defer r.mux.Unlock()

	id := rs.req.Id
	if _, ok := r.reqTokenState[id]; ok {
		klog.Warningf("Request id is duplicated: %s", id)
		return
	}
	r.reqTokenState[id] = rs
}

func (r *RequestStateTracker) DeleteRequestState(id string) {
	r.mux.Lock()
	defer r.mux.Unlock()
	delete(r.reqTokenState, id)
}

type (
	Kind string
)

const (
	KindStateUpdate Kind = "state_update"
	KindPrefillDone Kind = "prefill_done"
)

type RequestReportData struct {
	Kind       Kind             `json:"kind"`
	Id         string           `json:"id"`
	Model      string           `json:"model"`
	InferType  consts.InferType `json:"infer_type"`
	InstanceId string           `json:"instance_id"`
	GatewayId  string           `json:"gateway_id"`
	NumTokens  uint64           `json:"num_tokens"`
}

type RequestReportDataArray = []RequestReportData

func (r *RequestStateTracker) report() {
	reportDatas := make(RequestReportDataArray, 0, 32)
	r.mux.RLock()
	for _, rs := range r.reqTokenState {
		reportDatas = append(reportDatas, RequestReportData{
			Kind:       KindStateUpdate,
			Id:         rs.req.Id,
			Model:      rs.req.LLMRequest.Model,
			InferType:  rs.InferType,
			InstanceId: rs.InstanceId,
			GatewayId:  rs.GatewayId,
			NumTokens:  rs.GetNumTokens(),
		})
	}
	r.mux.RUnlock()

	if err := r.doReport(reportDatas); err != nil {
		klog.Warningf("report: %v", err)
	}
}

func (r *RequestStateTracker) doReport(reportDatas RequestReportDataArray) error {
	endpoint, err := r.schedulerResolver.GetEndpoints()
	if len(endpoint) == 0 || err != nil {
		return fmt.Errorf("no scheduler endpoint available")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	url := fmt.Sprintf("http://%s/report", endpoint[0].String())
	data, _ := json.Marshal(reportDatas)
	klog.V(3).Infof("submit report data: %s", string(data))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("could not create request: %v, url: %s", err, url)
	}
	if len(r.config.ServiceToken) > 0 {
		req.Header.Add("Authorization", r.config.ServiceToken)
	}

	response, err := r.client.Do(req)
	if err != nil {
		return fmt.Errorf("could not send request: %v, url: %s", err, url)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("request failed: %s, url: %s", response.Status, url)
	}
	return nil
}

// ReportPrefillComplete reports to scheduler that a request's prefill phase is complete.
// Only used for neutral infer type since prefill/decode modes have separate instances.
func (r *RequestStateTracker) ReportPrefillComplete(reqCtx *types.RequestContext) {
	if r.schedulerResolver == nil {
		return
	}

	go func() {
		// Only support neutral infer type, because prefill complete report is not needed for prefill/decode
		instance := reqCtx.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypeNeutral)
		if instance == nil {
			return
		}

		reportData := RequestReportDataArray{
			{
				Kind:       KindPrefillDone,
				Id:         reqCtx.Id,
				Model:      reqCtx.LLMRequest.Model,
				InferType:  consts.InferTypeNeutral,
				InstanceId: instance.Id(),
				GatewayId:  reqCtx.SchedulingCtx.GatewayId,
				NumTokens:  0,
			},
		}
		err := r.doReport(reportData)
		if err != nil {
			klog.Warningf("report prefill complete failed: %v", err)
		}
	}()
}

func (r *RequestStateTracker) reportLoop() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("request report loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go r.reportLoop()
		}
	}()
	// report every 5 seconds
	for {
		time.Sleep(time.Duration(r.config.RequestStateReportInterval) * time.Second)
		r.report()
	}
}

// RequestTokenState indicates the token length of the current request processing
// it record token length when enable tokenizer, otherwise it records message text count
type RequestTokenState struct {
	req        *types.RequestContext
	Model      string
	InferType  consts.InferType
	InstanceId string
	GatewayId  string

	mx sync.Mutex

	numTokens     uint64 // directly update with current num tokens
	lastNumTokens uint64 // last num tokens
	ResponseText  string // current response text
}

func NewRequestTokenState(
	req *types.RequestContext, model string, inferType consts.InferType, instanceId string, gatewayId string) *RequestTokenState {
	promptTokens, ok := req.LLMRequest.GetPromptTokens()
	if !ok {
		klog.Errorf("Failed to get prompt tokens.")
		return nil
	}

	return &RequestTokenState{
		req:           req,
		lastNumTokens: uint64(len(promptTokens)),
		Model:         model,
		InferType:     inferType,
		InstanceId:    instanceId,
		GatewayId:     gatewayId,
	}
}

func (rs *RequestTokenState) UpdateNumTokens(num uint64) {
	rs.mx.Lock()
	defer rs.mx.Unlock()
	rs.numTokens = num
}

func (rs *RequestTokenState) AppendResponseText(text string) {
	rs.mx.Lock()
	defer rs.mx.Unlock()
	rs.ResponseText += text
}

func (rs *RequestTokenState) GetNumTokens() uint64 {
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
