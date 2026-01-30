package lrs

import (
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
	"sync"
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

func NewLocalRealtimeStateClient(c *options.SchedulerConfig) *LocalRealtimeStateClient {
	w := &LocalRealtimeStateClient{
		multiModelSupport: c.MultiModelSupport,
		normalState:       NewLocalRealtimeState(),
		prefillState:      NewLocalRealtimeState(),
		decodeState:       NewLocalRealtimeState(),
	}

	go w.normalState.SubmitMetric()
	go w.prefillState.SubmitMetric()
	go w.decodeState.SubmitMetric()

	return w
}

func (lrsClient *LocalRealtimeStateClient) RLock() {
	lrsClient.mu.RLock()
}

func (lrsClient *LocalRealtimeStateClient) RUnlock() {
	lrsClient.mu.RUnlock()
}

func (lrsClient *LocalRealtimeStateClient) Lock() {
	lrsClient.mu.Lock()
}

func (lrsClient *LocalRealtimeStateClient) Unlock() {
	lrsClient.mu.Unlock()
}

func (lrsClient *LocalRealtimeStateClient) AddInstance(instance *types.LLMInstance) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	switch instance.Role.String() {
	case consts.DecodeInferMode:
		lrsClient.decodeState.AddInstance(instance)
	case consts.PrefillInferMode:
		lrsClient.prefillState.AddInstance(instance)
	default:
		lrsClient.normalState.AddInstance(instance)
	}
}

func (lrsClient *LocalRealtimeStateClient) RemoveInstance(inferMode string, instanceId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	switch inferMode {
	case consts.DecodeInferMode:
		lrsClient.decodeState.RemoveInstance(instanceId)
	case consts.PrefillInferMode:
		lrsClient.prefillState.RemoveInstance(instanceId)
	default:
		lrsClient.normalState.RemoveInstance(instanceId)
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
	s := lrsClient.getLocalRealtimeState(inferMode)
	if s == nil {
		return nil
	}
	return s.GetInstanceView(instanceId)
}

func (lrsClient *LocalRealtimeStateClient) PrintInstanceViews() {
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
