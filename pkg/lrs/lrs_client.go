package lrs

import (
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/metrics"
	"llm-gateway/pkg/types"
	"runtime/debug"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// LocalRealtimeStateClient manages different scheduler state stores for different inference inferModes
type LocalRealtimeStateClient struct {
	normalState  *LocalRealtimeState
	prefillState *LocalRealtimeState
	decodeState  *LocalRealtimeState

	multiModelSupport bool

	// NOTE(sunbiao.sun):
	// lrs could be written concurrently because scheduler could receive request token state data
	// sent by multiple gateways
	// All write functions are locked and unlocked inside the functions, and are called by schedule service.
	// While all read functions are not locked and unlocked inside the functions, because they are only called during
	// scheduling, and the scheduling function will explicitly lock and unlock the read mutex outside.
	mu sync.RWMutex
}

func NewLocalRealtimeStateClient(c *options.Config) *LocalRealtimeStateClient {
	w := &LocalRealtimeStateClient{
		multiModelSupport: c.ServerlessMode,
		normalState:       NewLocalRealtimeState(),
		prefillState:      NewLocalRealtimeState(),
		decodeState:       NewLocalRealtimeState(),
	}

	go w.SubmitMetric()

	return w
}

// Note: These lock methods (RLock/RUnlock/Lock/Unlock) are retained for compatibility with ClusterViewClientInterface.
// For read-only Get operations, only a read lock is needed for data protection.
// A coarse-grained lock is used in handleSchedule to ensure atomicity of the Read-Modify-Write (RMW)
// pattern: reading LRS state for scheduling decisions and updating LRS after allocation must occur
// under the same lock. This prevents burst concurrency from corrupting LRS state consistency.
func (lrsClient *LocalRealtimeStateClient) RLock() {}

func (lrsClient *LocalRealtimeStateClient) RUnlock() {}

func (lrsClient *LocalRealtimeStateClient) Lock() {}

func (lrsClient *LocalRealtimeStateClient) Unlock() {}

func (lrsClient *LocalRealtimeStateClient) AddInstance(token *types.LLMWorker) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	switch token.Role.String() {
	case consts.DecodeInferMode:
		lrsClient.decodeState.AddInstance(token)
	case consts.PrefillInferMode:
		lrsClient.prefillState.AddInstance(token)
	default:
		lrsClient.normalState.AddInstance(token)
	}
}

func (lrsClient *LocalRealtimeStateClient) RemoveInstance(inferMode string, workerId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	switch inferMode {
	case consts.DecodeInferMode:
		lrsClient.decodeState.RemoveInstance(workerId)
	case consts.PrefillInferMode:
		lrsClient.prefillState.RemoveInstance(workerId)
	default:
		lrsClient.normalState.RemoveInstance(workerId)
	}
}

func (lrsClient *LocalRealtimeStateClient) AddGateway(gatewayId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	lrsClient.normalState.AddGateway(gatewayId)
	lrsClient.prefillState.AddGateway(gatewayId)
	lrsClient.decodeState.AddGateway(gatewayId)
}

func (lrsClient *LocalRealtimeStateClient) RemoveGateway(gatewayId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	lrsClient.normalState.RemoveGateway(gatewayId)
	lrsClient.prefillState.RemoveGateway(gatewayId)
	lrsClient.decodeState.RemoveGateway(gatewayId)
}

// inferMode-specific operations

func (lrsClient *LocalRealtimeStateClient) AllocateRequestState(inferMode string, request *RequestState) error {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	if inferMode == "" {
		inferMode = consts.NormalInferMode
	}
	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return consts.ErrorNoMatchInferMode
	}
	return s.AllocateRequestState(request)
}

func (lrsClient *LocalRealtimeStateClient) UpdateRequestState(inferMode string, request *RequestState) error {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return consts.ErrorNoMatchInferMode
	}
	return s.UpdateRequestState(request)
}

func (lrsClient *LocalRealtimeStateClient) MarkPrefillComplete(inferMode string, request *RequestState) error {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return consts.ErrorNoMatchInferMode
	}

	return s.MarkPrefillComplete(request)
}

func (lrsClient *LocalRealtimeStateClient) ReleaseRequestState(inferMode string, request *RequestState) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return
	}
	s.ReleaseRequestState(request)
}

func (lrsClient *LocalRealtimeStateClient) GetGroupedInstanceViews() map[string]map[string]*InstanceView {
	lrsClient.mu.RLock()
	defer lrsClient.mu.RUnlock()

	inferModes := []string{consts.NormalInferMode, consts.PrefillInferMode, consts.DecodeInferMode}
	groupedInstanceViews := make(map[string]map[string]*InstanceView)
	for _, inferMode := range inferModes {
		s := lrsClient.getLocalRealtimeState(inferMode)
		views := s.GetInstanceViews()
		if len(views) > 0 {
			groupedInstanceViews[inferMode] = views
		}
	}
	return groupedInstanceViews
}

func (lrsClient *LocalRealtimeStateClient) GetInstanceViews(inferMode string) map[string]*InstanceView {
	lrsClient.mu.RLock()
	defer lrsClient.mu.RUnlock()

	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return nil
	}
	return s.GetInstanceViews()
}

func (lrsClient *LocalRealtimeStateClient) GetInstanceViewsByModel(model string, inferMode string) map[string]*InstanceView {
	lrsClient.mu.RLock()
	defer lrsClient.mu.RUnlock()

	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {

		return nil
	}
	if lrsClient.multiModelSupport {
		return s.GetInstanceViewsByModel(model)
	} else {
		return s.GetInstanceViews()
	}
}

func (lrsClient *LocalRealtimeStateClient) GetInstanceView(inferMode string, instanceId string) *InstanceView {
	lrsClient.mu.RLock()
	defer lrsClient.mu.RUnlock()

	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return nil
	}
	return s.GetInstanceView(instanceId)
}

func (lrsClient *LocalRealtimeStateClient) PrintInstanceViews() {
	lrsClient.mu.RLock()
	defer lrsClient.mu.RUnlock()

	println("Normal infer mode instance views:")
	lrsClient.normalState.PrintInstanceViews()
	println("\nPrefill infer mode instance views:")
	lrsClient.prefillState.PrintInstanceViews()
	println("\nDecode infer mode instance views:")
	lrsClient.decodeState.PrintInstanceViews()
}

func (lrsClient *LocalRealtimeStateClient) getLocalRealtimeState(inferMode string) *LocalRealtimeState {
	switch inferMode {
	case consts.PrefillInferMode:
		return lrsClient.prefillState
	case consts.DecodeInferMode:
		return lrsClient.decodeState
	case consts.NormalInferMode:
		return lrsClient.normalState
	}
	return nil
}

func (lrsClient *LocalRealtimeStateClient) SubmitMetric() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("scheduler state store shard: submit metric loop crashed , err: %s\ntrace:%s",
				e, string(debug.Stack()))
			go lrsClient.SubmitMetric()
		}
	}()

	for {
		allInstanceViews := lrsClient.GetGroupedInstanceViews()
		for _, instanceViews := range allInstanceViews {
			for _, iv := range instanceViews {
				address := iv.GetInstance().Endpoint
				tokens := iv.NumTokens()
				reqs := iv.NumRequests()
				waitingReqs := iv.NumWaitingRequests()

				labels := metrics.Labels{
					{Name: "model", Value: iv.GetInstance().Model},
					{Name: "address", Value: address.String()},
					{Name: "infer_role", Value: iv.GetInferMode()},
				}
				metrics.StatusValue("instance_tokens", labels).Set(float32(tokens))
				metrics.StatusValue("instance_requests", labels).Set(float32(reqs))
				metrics.StatusValue("instance_waiting_requests", labels).Set(float32(waitingReqs))
			}
		}

		time.Sleep(5 * time.Second)
	}
}
