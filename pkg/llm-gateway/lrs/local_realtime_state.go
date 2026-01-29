package lrs

import (
	"fmt"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/metrics"
	"llumnix/pkg/llm-gateway/types"
	"runtime/debug"
	"time"

	"k8s.io/klog/v2"
)

// Notes on the scheduler state store shard Algorithm
// Ensuring load balancing in LLM serving systems presents significant challenges. In operation:
// 1. Gateways schedule request to the instance with minimal load stored in the scheduler
// 2. Scheduler tracks request states per instance
// 3. Gateways MUST release request state by notifying scheduler upon request completion
//
// Critical reliability issue:
// Request state allocation/release mechanisms are unreliable due to:
//   - Network partitions
//   - Node failures (both gateway and inference nodes)
//
// Required failover mechanism must:
//  1. Reclaim request states held by failed gateways
//     (Gateway failure => requests lost => request states must be released)
//  2. Reclaim request states occupied by failed inference nodes
//
// Proposed solution:
//   - Scheduler maintains centralized states store tracking:
//     a) request states allocations per instance
//     b) gateway responsible for each allocation and release
//
// Failure handling:
// 1. Gateway failure/restart:
//   - Automatically release request states allocated for that gateway
//   - Achievable through gateway heartbeat monitoring
//
// 2. Inference node failure:
//   - Reclaim request states allocated to that instance
//   - Critical: Prevent stale releases by gateways via:
//     > Request states versioning system
//     > Each allocation is bound to a unique version
//     > Version-mismatched release requests are rejected

type InstanceView struct {
	instance      *types.LLMInstance
	version       int64
	numTokens     int64 // allocated number of tokens of the instance, sum(requestStates.numTokens)
	requestStates map[string]*RequestState
}

type RequestState struct {
	reqId      string
	instanceId string
	gatewayId  string

	numTokens int64

	updateTime time.Time
}

func NewRequestState(
	reqId string, numTokens int64, instanceId string, gatewayId string) *RequestState {
	return &RequestState{
		reqId:      reqId,
		instanceId: instanceId,
		gatewayId:  gatewayId,
		numTokens:  numTokens,
		updateTime: time.Now(),
	}
}

func NewInstanceView(instance *types.LLMInstance) *InstanceView {
	return &InstanceView{
		instance:      instance,
		version:       instance.Version,
		numTokens:     0,
		requestStates: make(map[string]*RequestState),
	}
}

func (iv *InstanceView) GetInstance() *types.LLMInstance {
	return iv.instance
}

func (iv *InstanceView) GetInstanceId() string {
	return iv.instance.Id()
}

func (iv *InstanceView) GetInferMode() string {
	return iv.instance.Role.String()
}

func (iv *InstanceView) NumTokens() int64 {
	return iv.numTokens
}

func (iv *InstanceView) NumRequests() int64 {
	return int64(len(iv.requestStates))
}

func (iv *InstanceView) GetRequestIds() []string {
	reqIds := make([]string, 0, len(iv.requestStates))
	for reqId := range iv.requestStates {
		reqIds = append(reqIds, reqId)
	}
	return reqIds
}

func (iv *InstanceView) AllocateRequestState(reqState *RequestState) {
	iv.requestStates[reqState.reqId] = reqState
	iv.numTokens += reqState.numTokens
	klog.V(3).Infof("instance %s add request %s, request num tokens: %d, instance num tokens: %d",
		reqState.instanceId, reqState.reqId, reqState.numTokens, iv.numTokens)
}

func (iv *InstanceView) UpdateRequestState(reqState *RequestState) error {
	innerReqState, exist := iv.requestStates[reqState.reqId]
	if !exist {
		return consts.ErrorRequestNotExits
	}
	if reqState.gatewayId != innerReqState.gatewayId {
		klog.Errorf("request %s gateway changed: %s -> %s", reqState.reqId, innerReqState.gatewayId, reqState.gatewayId)
		return consts.ErrorRequestGatewayChanged
	}
	addedNum := reqState.numTokens - innerReqState.numTokens
	if addedNum >= 0 {
		innerReqState.numTokens = reqState.numTokens
		innerReqState.updateTime = reqState.updateTime
		iv.numTokens += addedNum
		klog.V(3).Infof("update request %s state in instance %s: request num tokens: %d, "+
			"request add tokens: %d, instance num tokens: %d",
			reqState.reqId, reqState.instanceId, innerReqState.numTokens, addedNum, iv.numTokens)
	} else {
		klog.V(3).Infof("update request state %s in instance %s exception: request num tokens: %d, "+
			"request add tokens: %d, instance num tokens: %d",
			reqState.reqId, reqState.instanceId, innerReqState.numTokens, addedNum, iv.numTokens)
	}
	return nil
}

func (iv *InstanceView) ReleaseRequestState(reqId string) {
	reqState := iv.requestStates[reqId]
	if reqState != nil {
		if iv.numTokens < reqState.numTokens {
			klog.Warningf("instance %s num tokens %d < request %s num tokens %d, set instance num tokens to 0",
				iv.instance.Endpoint.String(), iv.numTokens, reqId, reqState.numTokens)
			iv.numTokens = 0
		} else {
			iv.numTokens -= reqState.numTokens
		}
		delete(iv.requestStates, reqId)
	}
}

func (iv *InstanceView) ClearStates() {
	for s := range iv.requestStates {
		delete(iv.requestStates, s)
	}
	iv.numTokens = 0
}

func (iv *InstanceView) GetModel() string {
	return iv.instance.Model
}

// LocalRealtimeState records the request states allocated to instances, and tracks which gateway allocated
// these request states. This enables timely allocate, update and release of in-use request states
// when instances or gateways fail.
type LocalRealtimeState struct {
	// request id -> request state
	requestStates map[string]*RequestState
	// instance id -> instance view
	instanceViews map[string]*InstanceView
	// gateway id -> request set
	gatewayRequestSet map[string]map[string]struct{}
}

func NewLocalRealtimeState() *LocalRealtimeState {
	return &LocalRealtimeState{
		requestStates:     make(map[string]*RequestState),
		instanceViews:     make(map[string]*InstanceView),
		gatewayRequestSet: make(map[string]map[string]struct{}),
	}
}

func (lrs *LocalRealtimeState) GetInstanceViews() map[string]*InstanceView {
	return lrs.instanceViews
}

func (lrs *LocalRealtimeState) GetInstanceViewsByModel(model string) map[string]*InstanceView {
	results := make(map[string]*InstanceView)
	for _, instance := range lrs.instanceViews {
		if instance.instance.Model == model || instance.instance.Model == "" {
			results[instance.GetInstanceId()] = instance
		}
	}
	return results
}

func (lrs *LocalRealtimeState) GetInstanceView(instanceId string) *InstanceView {
	return lrs.instanceViews[instanceId]
}

func (lrs *LocalRealtimeState) removeInstance(instanceId string) {
	instanceViews := lrs.instanceViews[instanceId]
	if instanceViews != nil {
		allRequests := instanceViews.GetRequestIds()
		for _, reqId := range allRequests {
			for _, requestSet := range lrs.gatewayRequestSet {
				delete(requestSet, reqId)
			}
			delete(lrs.requestStates, reqId)
			klog.V(4).Infof("delete request %s of instance %s", reqId, instanceId)
		}
		instanceViews.ClearStates()
		delete(lrs.instanceViews, instanceId)
		klog.Infof("delete instance %s, version: %d", instanceId, instanceViews.version)
	}
}

func (lrs *LocalRealtimeState) AddInstance(instance *types.LLMInstance) {
	id := instance.Id()
	oldInstance := lrs.instanceViews[id]
	if oldInstance != nil {
		if instance.Version > oldInstance.version {
			klog.Infof("instance %s version changed: %d -> %d, remove old instance",
				id, oldInstance.version, instance.Version)
			lrs.removeInstance(id)
		} else {
			klog.Warningf("instance %s version not changed: %d -> %d", id, oldInstance.version, instance.Version)
			return
		}
	}

	klog.Infof("add new instance %s", instance)
	newInstanceView := NewInstanceView(instance)
	lrs.instanceViews[id] = newInstanceView
}

func (lrs *LocalRealtimeState) RemoveInstance(instanceId string) {
	lrs.removeInstance(instanceId)
}

func (lrs *LocalRealtimeState) AddGateway(gatewayId string) {
	if lrs.gatewayRequestSet[gatewayId] == nil {
		lrs.gatewayRequestSet[gatewayId] = make(map[string]struct{})
	}
}

func (lrs *LocalRealtimeState) RemoveGateway(gatewayId string) {
	requestSet, exist := lrs.gatewayRequestSet[gatewayId]
	if !exist {
		return
	}

	for reqId := range requestSet {
		req := lrs.requestStates[reqId]
		if req != nil {
			instanceView := lrs.instanceViews[req.instanceId]
			if instanceView != nil {
				instanceView.ReleaseRequestState(reqId)
			}
			delete(lrs.requestStates, reqId)
			klog.V(4).Infof("remove request %s of gateway %s", reqId, gatewayId)
		}
	}
	delete(lrs.gatewayRequestSet, gatewayId)
}

func (lrs *LocalRealtimeState) requestExists(reqId string) bool {
	_, ok := lrs.requestStates[reqId]
	return ok
}

func (lrs *LocalRealtimeState) instanceExists(instanceId string) bool {
	_, ok := lrs.instanceViews[instanceId]
	return ok
}

func (lrs *LocalRealtimeState) gatewayExists(gatewayId string) bool {
	_, ok := lrs.gatewayRequestSet[gatewayId]
	return ok
}

func (lrs *LocalRealtimeState) PrintInstanceViews() {
	for _, instanceView := range lrs.instanceViews {
		fmt.Printf("instance: %s, reqs: %d, tokens: %d\n",
			instanceView.instance.Id(), instanceView.NumRequests(), instanceView.NumTokens())
	}
}

func (lrs *LocalRealtimeState) AllocateRequestState(reqState *RequestState) error {
	_, exist := lrs.requestStates[reqState.reqId]
	if exist {
		klog.Errorf("allocate request %s already exists.", reqState.reqId)
		return consts.ErrorRequestExits
	}

	if len(lrs.instanceViews) == 0 {
		klog.Warningf("no instance exists.")
		return consts.ErrorEndpointNotFound
	}

	if !lrs.instanceExists(reqState.instanceId) {
		klog.Warningf("allocate request %s allocated instance %s not exist.", reqState.reqId, reqState.instanceId)
		return consts.ErrorEndpointNotFound
	}

	if !lrs.gatewayExists(reqState.gatewayId) {
		klog.Warningf("allocate request %s created gateway %s not exist.", reqState.reqId, reqState.gatewayId)
		return consts.ErrorGatewayNotFound
	}

	if klog.V(3).Enabled() {
		klog.Infof("AllocateRequestState -------------  %d", reqState.numTokens)
		for id, instance := range lrs.instanceViews {
			klog.Infof("request id: %s, instance num tokens: %d, instance num reqs: %d",
				id, instance.NumTokens(), instance.NumRequests())
		}
	}

	lrs.requestStates[reqState.reqId] = reqState
	lrs.gatewayRequestSet[reqState.gatewayId][reqState.reqId] = struct{}{}
	lrs.instanceViews[reqState.instanceId].AllocateRequestState(reqState)

	return nil
}

func (lrs *LocalRealtimeState) UpdateRequestState(reqState *RequestState) error {
	if !lrs.requestExists(reqState.reqId) {
		klog.Errorf("update request %s not exist.", reqState.reqId)
		return consts.ErrorRequestNotExits
	}

	if !lrs.instanceExists(reqState.instanceId) {
		klog.Warningf("update request %s scheduled instance %s not exist.",
			reqState.reqId, reqState.instanceId)
		return consts.ErrorEndpointNotFound
	}

	if !lrs.gatewayExists(reqState.gatewayId) {
		klog.Warningf("update request %s created gateway %s not exist.",
			reqState.reqId, reqState.gatewayId)
		return consts.ErrorGatewayNotFound
	}

	return lrs.instanceViews[reqState.instanceId].UpdateRequestState(reqState)
}

func (lrs *LocalRealtimeState) ReleaseRequestState(reqState *RequestState) {
	if !lrs.requestExists(reqState.reqId) {
		klog.Warningf("release request %s not exist.", reqState.reqId)
		return
	}

	if !lrs.instanceExists(reqState.instanceId) {
		klog.Warningf("release request %s scheduled instance %s not exist.", reqState.reqId, reqState.instanceId)
		return
	}

	if !lrs.gatewayExists(reqState.gatewayId) {
		klog.Warningf("release request %s created gateway %s not exist.", reqState.reqId, reqState.gatewayId)
		return
	}

	lrs.instanceViews[reqState.instanceId].ReleaseRequestState(reqState.reqId)
	delete(lrs.requestStates, reqState.reqId)
	delete(lrs.gatewayRequestSet[reqState.gatewayId], reqState.reqId)
}

func (lrs *LocalRealtimeState) SubmitMetric() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("scheduler state store shard: submit metric loop crashed , err: %s\ntrace:%s",
				e, string(debug.Stack()))
			go lrs.SubmitMetric()
		}
	}()

	for {
		instanceViews := lrs.GetInstanceViews()
		for _, iv := range instanceViews {
			instanceAddress := iv.GetInstance().Endpoint
			tokens := iv.NumTokens()
			reqs := iv.NumRequests()

			labels := metrics.Labels{
				{Name: "model", Value: iv.GetInstance().Model},
				{Name: "instance_address", Value: instanceAddress.String()},
				{Name: "infer_mode", Value: iv.GetInferMode()},
			}
			metrics.StatusValue("endpoint_llm_token_count", labels).Set(float32(tokens))
			metrics.StatusValue("endpoint_active_token_count", labels).Set(float32(reqs))
		}

		time.Sleep(5 * time.Second)
	}
}
