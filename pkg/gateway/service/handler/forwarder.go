package handler

import (
	"fmt"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
	"sync"

	"k8s.io/klog/v2"
)

// StreamChunk represents a single chunk of data from a streaming inference response
// It contains either data bytes or an error that occurred during streaming
type StreamChunk struct {
	// err holds any error that occurred while processing this chunk
	err error

	// Data contains the actual response bytes for this chunk
	Data []byte
}

// Forwarder defines the interface for forwarding requests to inference engines.
// It constructs protocol-specific request bodies, forwards them to the target engine,
// and streams response chunks back.
type Forwarder interface {
	// Forward sends the request to the target inference engine and returns a channel
	// that streams response chunks back to the caller.
	Forward(req *types.RequestContext) (<-chan StreamChunk, error)
}

// ForwarderFactory is a factory function that creates a Forwarder instance.
// It takes a scheduling mode as parameter and returns the corresponding forwarder.
type ForwarderFactory func(schedulingMode types.SchedulingMode) (Forwarder, error)

// forwarderRegistry holds registered forwarder factories indexed by type key.
var (
	forwarderRegistry = make(map[string]ForwarderFactory)
	registryMu        sync.RWMutex
)

// registerForwarder registers a forwarder factory with the specified type key.
// The key is typically a ForwarderType* constant (e.g., ForwarderTypeNormal, ForwarderTypeVllmKvt).
func registerForwarder(forwarderType string, factory ForwarderFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	forwarderRegistry[forwarderType] = factory
}

// buildForwarder creates a Forwarder instance based on the forwarder type and scheduling mode.
// Returns an error if the forwarder type is not registered.
func buildForwarder(forwarderType string, schedulingMode types.SchedulingMode) (Forwarder, error) {
	registryMu.RLock()
	factory, exists := forwarderRegistry[forwarderType]
	registryMu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unknown forwarder type: %s", forwarderType)
	}

	return factory(schedulingMode)
}

// stagedScheduleForward extracts the common staged PD scheduling flow:
// get prefill instance → doPrefill → ScheduleDecode → get decode instance → doDecode.
func stagedScheduleForward(
	req *types.RequestContext,
	doPrefill func(req *types.RequestContext, pInstance *types.LLMInstance) error,
	doDecode func(req *types.RequestContext, chunkChan chan StreamChunk, pInstance, dInstance *types.LLMInstance),
) (<-chan StreamChunk, error) {
	pInstance := req.SchedulingCtx.SchedulingResults.GetInstanceByInferType(consts.InferTypePrefill)
	if pInstance == nil {
		return nil, fmt.Errorf("[%s] no scheduled prefill instance", req.Id)
	}

	chunkChan := make(chan StreamChunk, 100)
	go func() {
		defer close(chunkChan)

		if err := doPrefill(req, pInstance); err != nil {
			chunkChan <- StreamChunk{err: err}
			return
		}

		results, err := req.ScheduleDecode()
		if err != nil {
			klog.Errorf("[%s] decode scheduling failed: %v", req.Id, err)
			chunkChan <- StreamChunk{err: err}
			return
		}

		dInstance := results.GetInstanceByInferType(consts.InferTypeDecode)
		if dInstance == nil {
			klog.Errorf("[%s] decode instance not found", req.Id)
			chunkChan <- StreamChunk{err: fmt.Errorf("decode instance not found")}
			return
		}

		doDecode(req, chunkChan, pInstance, dInstance)
	}()

	return chunkChan, nil
}
