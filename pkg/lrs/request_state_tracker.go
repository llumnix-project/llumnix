package lrs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/types"
	"net"
	"net/http"
	"runtime/debug"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type RequestStateTracker struct {
	mux           sync.RWMutex
	reqTokenState map[string]*RequestTokenState

	schedulerResolver resolver.Resolver
	client            *http.Client
	config            *options.Config
}

var (
	once     sync.Once
	gTracker *RequestStateTracker
)

func NewRequestStateTracker(config *options.Config) *RequestStateTracker {
	once.Do(func() {
		gTracker = &RequestStateTracker{
			reqTokenState:     make(map[string]*RequestTokenState),
			schedulerResolver: resolver.CreateSchedulerResolver(config),
			client:            &http.Client{Timeout: 3 * time.Second},
			config:            config,
		}
		go gTracker.reportLoop()
	})
	return gTracker
}

func (r *RequestStateTracker) CreateRequestState(req *types.RequestContext) {
	var inferMode, instanceID string
	if instance := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleDecode); instance != nil {
		inferMode, instanceID = consts.DecodeInferMode, instance.Id()
	} else if instance := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal); instance != nil {
		inferMode, instanceID = consts.NormalInferMode, instance.Id()
	}

	reqState := RequestTokenState{
		Id:         req.Id,
		Model:      req.GetRequestModel(),
		InferMode:  inferMode,
		InstanceId: instanceID,
		GatewayId:  req.ScheduleCtx.GatewayId,
		NumTokens:  req.GetTotalTokenLen(),
	}

	r.mux.Lock()
	defer r.mux.Unlock()
	id := reqState.Id
	if _, ok := r.reqTokenState[id]; ok {
		klog.Warningf("Request id is duplicated: %s", id)
		return
	}
	r.reqTokenState[id] = &reqState
}

func (r *RequestStateTracker) ReportPrefillComplete(req *types.RequestContext) {
	go func() {
		// Only support normal infer mode, because prefill complete report is not needed for prefill/decode
		instance := req.ScheduleCtx.ScheduleResults.GetWorkerByRole(types.InferRoleNormal)
		if instance == nil {
			return
		}
		inferMode, instanceID := consts.NormalInferMode, instance.Id()

		reportData := []RequestTokenState{
			{
				Kind:       KindPrefillDone,
				Id:         req.Id,
				Model:      req.GetRequestModel(),
				InferMode:  inferMode,
				InstanceId: instanceID,
				GatewayId:  req.ScheduleCtx.GatewayId,
				NumTokens:  0,
			},
		}
		err := r.doSubmitReport(reportData)
		if err != nil {
			klog.Warningf("report prefill complete failed: %v", err)
		}
	}()
}

func (r *RequestStateTracker) UpdateRequestState(req *types.RequestContext) {
	r.mux.Lock()
	defer r.mux.Unlock()
	id := req.Id
	if _, ok := r.reqTokenState[id]; ok {
		r.reqTokenState[id].NumTokens = req.GetTotalTokenLen()
	}
}

func (r *RequestStateTracker) DeleteRequestState(id string) {
	r.mux.Lock()
	defer r.mux.Unlock()
	delete(r.reqTokenState, id)
}

type Kind string

const (
	KindStateUpdate Kind = "state_update"
	KindPrefillDone Kind = "prefill_done"
)

type RequestTokenState struct {
	Kind       Kind   `json:"kind"`
	Id         string `json:"id"`
	Model      string `json:"model"`
	InferMode  string `json:"infer_mode"`
	InstanceId string `json:"instance_id"`
	GatewayId  string `json:"gateway_id"`
	NumTokens  uint64 `json:"num_tokens"`
}

type RequestTokenStateArray = []RequestTokenState

func (r *RequestStateTracker) report() {
	reports := make(RequestTokenStateArray, 0, 32)
	r.mux.RLock()
	for _, rs := range r.reqTokenState {
		// check status value
		if rs.NumTokens == 0 {
			continue
		}
		rs.Kind = KindStateUpdate
		reports = append(reports, *rs)
	}
	r.mux.RUnlock()

	if len(reports) == 0 {
		return
	}

	err := r.doSubmitReport(reports)
	if err != nil {
		klog.Warningf("report request state failed: %v", err)
	}
}

func (r *RequestStateTracker) doSubmitReport(reports RequestTokenStateArray) error {
	endpoint, err := r.schedulerResolver.GetEndpoints()
	if len(endpoint) == 0 || err != nil {
		return fmt.Errorf("no scheduler endpoint found")
	}

	data, _ := json.Marshal(reports)
	klog.V(3).Infof("report request data: %s", string(data))

	url := fmt.Sprintf("http://%s/report", endpoint[0].String())
	retry := 0

RETRY:
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("could not create request: %v", err)
	}
	if len(r.config.ServiceToken) > 0 {
		req.Header.Add("Authorization", r.config.ServiceToken)
	}

	response, err := r.client.Do(req)
	if err != nil {
		klog.Warningf("report: could not send request: %v, url: %s, retry: %d", err, url, retry)
		var netErr net.Error
		if !(errors.As(err, &netErr) && netErr.Timeout()) && retry < 3 {
			retry++
			time.Sleep(100 * time.Millisecond)
			goto RETRY
		}
		return fmt.Errorf("could not send request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("request state tracker report failed: %s", response.Status)
	}
	return nil
}

func (r *RequestStateTracker) reportLoop() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("request report loop crashed , err: %s\ntrace:%s", e, string(debug.Stack()))
			go r.reportLoop()
		}
	}()
	for {
		time.Sleep(time.Duration(r.config.RequestsReporterDuration) * time.Second)
		r.report()
	}
}

// // RequestTokenState indicates the token length of the current request processing
// // it record token length when enable tokenizer, otherwise it records message text count
// type RequestTokenState struct {
// 	req        *types.RequestContext
// 	Model      string
// 	InferMode  string
// 	InstanceId string
// 	GatewayId  string

// 	mx sync.Mutex

// 	numTokens     uint64 // directly update with current num tokens
// 	lastNumTokens uint64 // last num tokens
// 	ResponseText  string // current response text
// }

// func NewRequestTokenState(
// 	req *types.RequestContext, model string, inferMode string, instanceId string, gatewayId string) *RequestTokenState {
// 	var numTokens uint64
// 	promptTokens, err1 := req.GetPromptTokens()
// 	if err1 != nil {
// 		// fallback to use string length as an alternative
// 		promptText, err2 := req.GetPromptString()
// 		if err2 != nil {
// 			klog.Errorf("Failed to create request: %s, get prompt failed: %v, %v", req.Id, err1, err2)
// 			return nil
// 		}
// 		numTokens = uint64(len(promptText))
// 	} else {
// 		numTokens = uint64(len(promptTokens))
// 	}

// 	return &RequestTokenState{
// 		req:           req,
// 		lastNumTokens: numTokens,
// 		Model:         model,
// 		InferMode:     inferMode,
// 		InstanceId:    instanceId,
// 		GatewayId:     gatewayId,
// 	}
// }

// func (rs *RequestTokenState) UpdateNumTokens(num uint64) {
// 	rs.mx.Lock()
// 	defer rs.mx.Unlock()
// 	rs.numTokens = num
// }

// func (rs *RequestTokenState) AppendResponseText(text string) {
// 	rs.mx.Lock()
// 	defer rs.mx.Unlock()
// 	rs.ResponseText += text
// }

// func (rs *RequestTokenState) GetNumTokens() uint64 {
// 	rs.mx.Lock()
// 	defer rs.mx.Unlock()
// 	count := rs.numTokens
// 	if count != 0 {
// 		return count
// 	}
// 	tk, err := tokenizer.GetTokenizer()
// 	if err != nil {
// 		count = uint64(len(rs.ResponseText))
// 	} else {
// 		tokens, err := tk.Encode(rs.ResponseText, false)
// 		if err != nil {
// 			klog.Warningf("Failed to encode response text: %v", err)
// 		}
// 		count = uint64(len(tokens))
// 	}
// 	rs.ResponseText = ""
// 	rs.lastNumTokens += count
// 	return rs.lastNumTokens
// }
