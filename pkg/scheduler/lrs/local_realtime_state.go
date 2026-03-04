package lrs

import (
	"fmt"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	// DefaultStateTimeout is the default duration after which a request state is considered stale
	DefaultStateTimeout = 15 * time.Minute
	// DefaultCleanupInterval is the default interval for checking and cleaning up stale request states
	DefaultCleanupInterval = 2 * time.Minute
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

// InstanceView represents a view of an inference instance with its current state.
// It tracks the worker information, version, allocated tokens, and all active request states
// for the instance. This is used by the scheduler to make load-balancing decisions.
type InstanceView struct {
	worker        *types.LLMWorker
	version       int64
	numTokens     int64 // allocated number of tokens of the instance, sum(requestStates.numTokens)
	requestStates map[string]*RequestState
}

// RequestState represents the state of a single request being processed by an instance.
// It tracks the request ID, instance assignment, gateway ownership, token allocation,
// and prefill status. The update time is used for stale state detection.
type RequestState struct {
	reqId      string
	instanceId string
	gatewayId  string

	numTokens        int64
	prefillCompleted bool

	updateTime time.Time
}

// NewRequestState creates a new RequestState instance with the given parameters.
// The updateTime is automatically set to the current time.
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

// NewInstanceView creates a new InstanceView for the given worker.
// The version is initialized from the worker's version, and the request states map
// is initialized as empty.
func NewInstanceView(worker *types.LLMWorker) *InstanceView {
	return &InstanceView{
		worker:        worker,
		version:       worker.Version,
		numTokens:     0,
		requestStates: make(map[string]*RequestState),
	}
}

// GetInstance returns the LLMWorker associated with this instance view.
func (iv *InstanceView) GetInstance() *types.LLMWorker {
	return iv.worker
}

// GetInstanceId returns the unique identifier of the instance.
func (iv *InstanceView) GetInstanceId() string {
	return iv.worker.Id()
}

// GetInferMode returns the role/inference mode of the instance as a string.
func (iv *InstanceView) GetInferMode() string {
	return iv.worker.Role.String()
}

// NumTokens returns the total number of tokens currently allocated to this instance.
func (iv *InstanceView) NumTokens() int64 {
	return iv.numTokens
}

// NumRequests returns the total number of requests currently assigned to this instance.
func (iv *InstanceView) NumRequests() int64 {
	return int64(len(iv.requestStates))
}

// NumWaitingRequests returns the count of requests that haven't completed prefill phase.
// These requests are still in the waiting/prefill stage and not yet in decode phase.
func (iv *InstanceView) NumWaitingRequests() int64 {
	cnt := int64(0)
	for _, reqState := range iv.requestStates {
		if !reqState.prefillCompleted {
			cnt += 1
		}
	}
	return cnt
}

// NumWaitingTokens returns the total tokens for requests that haven't completed prefill.
// This represents the token load that is still in the waiting/prefill stage.
func (iv *InstanceView) NumWaitingTokens() int64 {
	cnt := int64(0)
	for _, reqState := range iv.requestStates {
		if !reqState.prefillCompleted {
			cnt += reqState.numTokens
		}
	}
	return cnt
}

// GetRequestIds returns a list of all request IDs currently assigned to this instance.
func (iv *InstanceView) GetRequestIds() []string {
	reqIds := make([]string, 0, len(iv.requestStates))
	for reqId := range iv.requestStates {
		reqIds = append(reqIds, reqId)
	}
	return reqIds
}

// AllocateRequestState adds a new request state to this instance.
// It updates the request states map and increments the total token count.
func (iv *InstanceView) AllocateRequestState(reqState *RequestState) {
	iv.requestStates[reqState.reqId] = reqState
	iv.numTokens += reqState.numTokens
	klog.V(3).Infof("instance %s add request %s, request num tokens: %d, instance num tokens: %d",
		reqState.instanceId, reqState.reqId, reqState.numTokens, iv.numTokens)
}

// MarkPrefillComplete marks a request's prefill phase as completed.
// This is a no-op if the request is not found in the instance.
func (iv *InstanceView) MarkPrefillComplete(reqState *RequestState) {
	innerReqState := iv.requestStates[reqState.reqId]
	if innerReqState == nil {
		return
	}
	innerReqState.prefillCompleted = true
	klog.V(3).Infof("instance %s mark request %s prefill complete", reqState.instanceId, reqState.reqId)
}

// UpdateRequestState updates an existing request state with new token information.
// It validates that the gateway ID hasn't changed and updates the token count.
// If the new token count is lower than before, it logs a warning but still processes the update.
func (iv *InstanceView) UpdateRequestState(reqState *RequestState) {
	innerReqState := iv.requestStates[reqState.reqId]
	if innerReqState == nil {
		return
	}
	if reqState.gatewayId != innerReqState.gatewayId {
		klog.Errorf("request %s gateway changed: %s -> %s", reqState.reqId, innerReqState.gatewayId, reqState.gatewayId)
		return
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
}

// ReleaseRequestState removes a request from this instance and decrements the token count.
// If the token count would go negative, it logs a warning and sets the count to 0.
func (iv *InstanceView) ReleaseRequestState(reqId string) {
	reqState := iv.requestStates[reqId]
	if reqState != nil {
		if iv.numTokens < reqState.numTokens {
			klog.Warningf("instance %s num tokens %d < request %s num tokens %d, set instance num tokens to 0",
				iv.worker.Endpoint.String(), iv.numTokens, reqId, reqState.numTokens)
			iv.numTokens = 0
		} else {
			iv.numTokens -= reqState.numTokens
		}
		delete(iv.requestStates, reqId)
	}
}

// ClearStates removes all request states and resets the token count to 0.
// This is typically used when an instance is being removed or reset.
func (iv *InstanceView) ClearStates() {
	for s := range iv.requestStates {
		delete(iv.requestStates, s)
	}
	iv.numTokens = 0
}

// GetModel returns the model name served by this instance.
func (iv *InstanceView) GetModel() string {
	return iv.worker.Model
}

// LocalRealtimeState records the request states allocated to instances, and tracks which gateway allocated
// these request states. This enables timely allocate, update and release of in-use request states
// when instances or gateways fail.
type LocalRealtimeState struct {
	mu sync.RWMutex

	// request id -> request state
	requestStates map[string]*RequestState
	// instance id -> instance view
	instanceViews map[string]*InstanceView
	// gateway id -> request set
	gatewayRequestSet map[string]map[string]struct{}

	// stateTimeout is the duration after which a request state is considered stale
	stateTimeout time.Duration
	// cleanupInterval is the interval for checking and cleaning up stale request states
	cleanupInterval time.Duration
	// stopCh is used to signal the cleanup goroutine to stop
	stopCh chan struct{}
	// cleanupRunning indicates whether the cleanup goroutine is running
	cleanupRunning bool
}

// NewLocalRealtimeState creates a new LocalRealtimeState with default timeout and cleanup interval.
func NewLocalRealtimeState() *LocalRealtimeState {
	return NewLocalRealtimeStateWithConfig(DefaultStateTimeout, DefaultCleanupInterval)
}

// NewLocalRealtimeStateWithConfig creates a new LocalRealtimeState with custom timeout and cleanup interval.
// The stateTimeout determines how long before a request state is considered stale.
// The cleanupInterval determines how often the stale state cleanup runs.
func NewLocalRealtimeStateWithConfig(stateTimeout, cleanupInterval time.Duration) *LocalRealtimeState {
	lrs := &LocalRealtimeState{
		requestStates:     make(map[string]*RequestState),
		instanceViews:     make(map[string]*InstanceView),
		gatewayRequestSet: make(map[string]map[string]struct{}),
		stateTimeout:      stateTimeout,
		cleanupInterval:   cleanupInterval,
		stopCh:            make(chan struct{}),
	}
	lrs.startCleanupLoop()
	return lrs
}

// startCleanupLoop starts the background goroutine that periodically cleans up stale request states.
func (lrs *LocalRealtimeState) startCleanupLoop() {
	lrs.cleanupRunning = true
	go lrs.cleanupLoop()
	klog.Infof("Local Realtime Request State cleanup loop started, timeout: %v, interval: %v",
		lrs.stateTimeout, lrs.cleanupInterval)
}

// StopCleanupLoop stops the background cleanup goroutine.
// This method is safe to call multiple times.
func (lrs *LocalRealtimeState) StopCleanupLoop() {
	lrs.mu.Lock()
	if !lrs.cleanupRunning {
		lrs.mu.Unlock()
		return
	}
	lrs.cleanupRunning = false
	lrs.mu.Unlock()

	close(lrs.stopCh)
	klog.Infof("Local Realtime Request State cleanup loop stopped")
}

// cleanupLoop runs in a separate goroutine and periodically cleans up stale request states.
func (lrs *LocalRealtimeState) cleanupLoop() {
	ticker := time.NewTicker(lrs.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-lrs.stopCh:
			return
		case <-ticker.C:
			lrs.cleanupStaleRequests()
		}
	}
}

// cleanupStaleRequests removes request states that haven't been updated within the stateTimeout duration.
func (lrs *LocalRealtimeState) cleanupStaleRequests() {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()

	now := time.Now()
	staleReqIds := make([]string, 0)

	// Identify stale request states
	for reqId, reqState := range lrs.requestStates {
		if now.Sub(reqState.updateTime) > lrs.stateTimeout {
			staleReqIds = append(staleReqIds, reqId)
		}
	}

	// Clean up stale request states
	for _, reqId := range staleReqIds {
		reqState := lrs.requestStates[reqId]
		if reqState == nil {
			continue
		}

		// Release from instance view
		instanceView := lrs.instanceViews[reqState.instanceId]
		if instanceView != nil {
			instanceView.ReleaseRequestState(reqId)
		}

		// Remove from gateway request set
		if gatewayReqs, exists := lrs.gatewayRequestSet[reqState.gatewayId]; exists {
			delete(gatewayReqs, reqId)
		}

		// Remove from request states
		delete(lrs.requestStates, reqId)

		klog.Infof("Cleaned up stale request state: reqId=%s, instanceId=%s, gatewayId=%s, lastUpdate=%v, staleFor=%v",
			reqId, reqState.instanceId, reqState.gatewayId, reqState.updateTime, now.Sub(reqState.updateTime))
	}

	if len(staleReqIds) > 0 {
		klog.Infof("Cleaned up %d stale request states", len(staleReqIds))
	}
}

// GetInstanceViews returns all instance views currently tracked by the state.
// The returned map is keyed by instance ID.
func (lrs *LocalRealtimeState) GetInstanceViews() map[string]*InstanceView {
	lrs.mu.RLock()
	defer lrs.mu.RUnlock()
	return lrs.instanceViews
}

// GetInstanceViewsByModel returns instance views filtered by model name.
// Instances with empty model name are included as they can serve any model.
func (lrs *LocalRealtimeState) GetInstanceViewsByModel(model string) map[string]*InstanceView {
	lrs.mu.RLock()
	defer lrs.mu.RUnlock()
	results := make(map[string]*InstanceView)
	for _, instance := range lrs.instanceViews {
		if instance.worker.Model == model || instance.worker.Model == "" {
			results[instance.GetInstanceId()] = instance
		}
	}
	return results
}

// GetInstanceView returns the instance view for the given instance ID.
// Returns nil if the instance does not exist.
func (lrs *LocalRealtimeState) GetInstanceView(instanceId string) *InstanceView {
	lrs.mu.RLock()
	defer lrs.mu.RUnlock()
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

// AddInstance adds a new instance to the state tracker or updates an existing one.
// If an instance with the same ID exists, it checks the version:
// - If the new version is higher, the old instance is removed and the new one is added
// - If the version is the same or lower, a warning is logged and no change is made
func (lrs *LocalRealtimeState) AddInstance(worker *types.LLMWorker) {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
	id := worker.Id()
	oldInstance := lrs.instanceViews[id]
	if oldInstance != nil {
		if worker.Version > oldInstance.version {
			klog.Infof("instance %s version changed: %d -> %d, remove old instance",
				id, oldInstance.version, worker.Version)
			lrs.removeInstance(id)
		} else {
			klog.Warningf("instance %s version not changed: %d -> %d", id, oldInstance.version, worker.Version)
			return
		}
	}

	klog.Infof("add new instance %s, version: %d", id, worker.Version)
	newInstanceView := NewInstanceView(worker)
	lrs.instanceViews[id] = newInstanceView
}

// RemoveInstance removes an instance and all its associated request states.
// This also cleans up the request states from the gateway request sets.
func (lrs *LocalRealtimeState) RemoveInstance(instanceId string) {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
	lrs.removeInstance(instanceId)
}

// AddGateway registers a new gateway in the state tracker.
// This creates an empty request set for the gateway to track its allocated requests.
func (lrs *LocalRealtimeState) AddGateway(gatewayId string) {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
	if lrs.gatewayRequestSet[gatewayId] == nil {
		lrs.gatewayRequestSet[gatewayId] = make(map[string]struct{})
	}
}

// RemoveGateway removes a gateway and releases all its associated request states.
// This is called when a gateway fails or restarts to clean up orphaned request states.
func (lrs *LocalRealtimeState) RemoveGateway(gatewayId string) {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
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

// PrintInstanceViews prints a summary of all instance views for debugging.
// This outputs the worker ID, request count, and token count for each instance.
func (lrs *LocalRealtimeState) PrintInstanceViews() {
	lrs.mu.RLock()
	defer lrs.mu.RUnlock()
	for _, instanceView := range lrs.instanceViews {
		fmt.Printf("worker: %s, reqs: %d, tokens: %d\n",
			instanceView.worker.Id(), instanceView.NumRequests(), instanceView.NumTokens())
	}
}

// AllocateRequestState allocates a new request state to an instance.
// This operation is atomic and validates that:
// - The request doesn't already exist
// - At least one instance exists
// - The target instance exists
// - The gateway exists
// Returns an error if any validation fails, otherwise nil.
func (lrs *LocalRealtimeState) AllocateRequestState(reqState *RequestState) error {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
	req := lrs.requestStates[reqState.reqId]
	if req != nil {
		klog.Errorf("allocate request %s already exists.", reqState.reqId)
		return consts.ErrorRequestExits
	}

	if len(lrs.instanceViews) == 0 {
		klog.Warningf("no instance exists.")
		return consts.ErrorNoAvailableEndpoint
	}

	if !lrs.instanceExists(reqState.instanceId) {
		klog.Warningf("allocate request %s allocated instance %s not exist.", reqState.reqId, reqState.instanceId)
		return consts.ErrorNoAvailableEndpoint
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

// UpdateRequestState updates an existing request state with new information.
// This operation is atomic and validates that:
// - The request exists
// - The instance exists
// - The gateway exists
// Returns an error if any validation fails, otherwise nil.
func (lrs *LocalRealtimeState) UpdateRequestState(reqState *RequestState) error {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
	if !lrs.requestExists(reqState.reqId) {
		klog.Errorf("update request %s not exist.", reqState.reqId)
		return consts.ErrorRequestNotExits
	}

	if !lrs.instanceExists(reqState.instanceId) {
		klog.Warningf("update request %s scheduled instance %s not exist.",
			reqState.reqId, reqState.instanceId)
		return consts.ErrorRequestNotExits
	}

	if !lrs.gatewayExists(reqState.gatewayId) {
		klog.Warningf("update request %s created gateway %s not exist.",
			reqState.reqId, reqState.gatewayId)
		return consts.ErrorGatewayNotFound
	}

	lrs.instanceViews[reqState.instanceId].UpdateRequestState(reqState)
	return nil
}

// MarkPrefillComplete marks the prefill phase of a resource request as complete.
func (lrs *LocalRealtimeState) MarkPrefillComplete(reqState *RequestState) error {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()
	if !lrs.requestExists(reqState.reqId) {
		klog.Errorf("update request %s not exist.", reqState.reqId)
		return consts.ErrorRequestNotExits
	}

	if !lrs.instanceExists(reqState.instanceId) {
		klog.Warningf("update request %s scheduled instance %s not exist.",
			reqState.reqId, reqState.instanceId)
		return consts.ErrorRequestNotExits
	}

	if !lrs.gatewayExists(reqState.gatewayId) {
		klog.Warningf("update request %s created gateway %s not exist.",
			reqState.reqId, reqState.gatewayId)
		return consts.ErrorGatewayNotFound
	}

	lrs.instanceViews[reqState.instanceId].MarkPrefillComplete(reqState)
	return nil
}

// ReleaseRequestState releases a request state from the system.
// This removes the request from:
// - The global request states map
// - The instance view
// - The gateway request set
// If the request doesn't exist, a warning is logged but no error is returned.
// This is safe to call for idempotent cleanup operations.
func (lrs *LocalRealtimeState) ReleaseRequestState(reqState *RequestState) {
	lrs.mu.Lock()
	defer lrs.mu.Unlock()

	innerReqState := lrs.requestStates[reqState.reqId]
	if innerReqState == nil {
		klog.Warningf("release request %s not exist.", reqState.reqId)
		innerReqState = reqState
	}

	// Release from instance view (if exists)
	instanceView := lrs.instanceViews[innerReqState.instanceId]
	if instanceView != nil {
		instanceView.ReleaseRequestState(reqState.reqId)
	}

	// Remove from gateway request set (if exists)
	if gatewayReqs, exists := lrs.gatewayRequestSet[innerReqState.gatewayId]; exists {
		delete(gatewayReqs, reqState.reqId)
	}

	// Remove from request states
	delete(lrs.requestStates, reqState.reqId)
}
