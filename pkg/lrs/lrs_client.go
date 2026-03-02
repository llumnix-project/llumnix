package lrs

import (
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
	"sync"
)

// LocalRealtimeStateClient manages different scheduler state stores for different inference inferTypes
type LocalRealtimeStateClient struct {
	neutralState  *LocalRealtimeState
	prefillState *LocalRealtimeState
	decodeState  *LocalRealtimeState

	multiModelSupport bool

	// NOTE(sunbiao.sun):
	// lrs could be written concurrently because scheduler could receive request token state data
	// sent by multiple gateways
	// All write functions are locked and unlocked inside the functions, and are called by scheduler service.
	// While all read functions are not locked and unlocked inside the functions, because they are only called during
	// scheduling, and the scheduling function will explicitly lock and unlock the read mutex outside.
	mu sync.RWMutex
}

func NewLocalRealtimeStateClient(c *options.SchedulerConfig) *LocalRealtimeStateClient {
	w := &LocalRealtimeStateClient{
		multiModelSupport: c.MultiModelSupport,
		neutralState:       NewLocalRealtimeState(),
		prefillState:      NewLocalRealtimeState(),
		decodeState:       NewLocalRealtimeState(),
	}

	go w.neutralState.SubmitMetric()
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

	switch instance.InferType {
	case consts.InferTypeDecode:
		lrsClient.decodeState.AddInstance(instance)
	case consts.InferTypePrefill:
		lrsClient.prefillState.AddInstance(instance)
	default:
		lrsClient.neutralState.AddInstance(instance)
	}
}

func (lrsClient *LocalRealtimeStateClient) RemoveInstance(inferType consts.InferType, instanceId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	switch inferType {
	case consts.InferTypeDecode:
		lrsClient.decodeState.RemoveInstance(instanceId)
	case consts.InferTypePrefill:
		lrsClient.prefillState.RemoveInstance(instanceId)
	default:
		lrsClient.neutralState.RemoveInstance(instanceId)
	}
}

func (lrsClient *LocalRealtimeStateClient) AddGateway(gatewayId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	lrsClient.neutralState.AddGateway(gatewayId)
	lrsClient.prefillState.AddGateway(gatewayId)
	lrsClient.decodeState.AddGateway(gatewayId)
}

func (lrsClient *LocalRealtimeStateClient) RemoveGateway(gatewayId string) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	lrsClient.neutralState.RemoveGateway(gatewayId)
	lrsClient.prefillState.RemoveGateway(gatewayId)
	lrsClient.decodeState.RemoveGateway(gatewayId)
}

// inferType-specific operations

func (lrsClient *LocalRealtimeStateClient) AllocateRequestState(inferType consts.InferType, request *RequestState) error {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	if inferType == "" {
		inferType = consts.InferTypeNeutral
	}
	s := lrsClient.getLocalRealtimeState(inferType)
	if s == nil {
		return consts.ErrorNoMatchInferType
	}
	return s.AllocateRequestState(request)
}

func (lrsClient *LocalRealtimeStateClient) UpdateRequestState(inferType consts.InferType, request *RequestState) error {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	s := lrsClient.getLocalRealtimeState(inferType)
	if s == nil {
		return consts.ErrorNoMatchInferType
	}
	return s.UpdateRequestState(request)
}

func (lrsClient *LocalRealtimeStateClient) ReleaseRequestState(inferType consts.InferType, request *RequestState) {
	lrsClient.mu.Lock()
	defer lrsClient.mu.Unlock()

	s := lrsClient.getLocalRealtimeState(inferType)
	if s == nil {
		return
	}
	s.ReleaseRequestState(request)
}

func (lrsClient *LocalRealtimeStateClient) GetGroupedInstanceViews() map[consts.InferType]map[string]*InstanceView {
	inferTypes := []consts.InferType{consts.InferTypeNeutral, consts.InferTypePrefill, consts.InferTypeDecode}
	groupedInstanceViews := make(map[consts.InferType]map[string]*InstanceView)
	for _, inferType := range inferTypes {
		s := lrsClient.getLocalRealtimeState(inferType)
		views := s.GetInstanceViews()
		if len(views) > 0 {
			groupedInstanceViews[inferType] = views
		}
	}
	return groupedInstanceViews
}

func (lrsClient *LocalRealtimeStateClient) GetInstanceViews(inferType consts.InferType) map[string]*InstanceView {
	s := lrsClient.getLocalRealtimeState(inferType)
	if s == nil {
		return nil
	}
	return s.GetInstanceViews()
}

func (lrsClient *LocalRealtimeStateClient) GetInstanceViewsByModel(model string, inferType consts.InferType) map[string]*InstanceView {
	lrsClient.mu.RLock()
	defer lrsClient.mu.RUnlock()

	s := lrsClient.getLocalRealtimeState(inferType)
	if s == nil {

		return nil
	}
	if lrsClient.multiModelSupport {
		return s.GetInstanceViewsByModel(model)
	} else {
		return s.GetInstanceViews()
	}
}

func (lrsClient *LocalRealtimeStateClient) GetInstanceView(inferType consts.InferType, instanceId string) *InstanceView {
	s := lrsClient.getLocalRealtimeState(inferType)
	if s == nil {
		return nil
	}
	return s.GetInstanceView(instanceId)
}

func (lrsClient *LocalRealtimeStateClient) PrintInstanceViews() {
	println("Neutral infer type instance views:")
	lrsClient.neutralState.PrintInstanceViews()
	println("\nPrefill infer type instance views:")
	lrsClient.prefillState.PrintInstanceViews()
	println("\nDecode infer type instance views:")
	lrsClient.decodeState.PrintInstanceViews()
}

func (lrsClient *LocalRealtimeStateClient) getLocalRealtimeState(inferType consts.InferType) *LocalRealtimeState {
	switch inferType {
	case consts.InferTypePrefill:
		return lrsClient.prefillState
	case consts.InferTypeDecode:
		return lrsClient.decodeState
	case consts.InferTypeNeutral:
		return lrsClient.neutralState
	}
	return nil
}
