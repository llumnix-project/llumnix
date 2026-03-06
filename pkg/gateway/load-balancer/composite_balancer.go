package balancer

import (
	"errors"
	"fmt"

	"k8s.io/klog/v2"

	"llumnix/cmd/gateway/app/options"
	"llumnix/pkg/resolver"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

// balanceMode defines the load balancing strategy mode
type balanceMode int

const (
	// LocalBalancer uses local round-robin load balancing
	LocalBalancer balanceMode = iota
	// PDLocalBalancer uses local round-robin with prefill-decode split mode
	PDLocalBalancer
	// RemoteBalancer uses remote scheduler for load balancing
	RemoteBalancer
	// PDRemoteBalancer uses remote scheduler with prefill-decode split mode
	PDRemoteBalancer
)

// CompositeBalancer combines multiple load balancers and routes requests to the appropriate
// balancer based on configuration and request context.
// It supports local/remote balancing, prefill-decode split mode, and fallback strategies.
type CompositeBalancer struct {
	config *options.GatewayConfig

	// balanceMode determines which balancing strategy to use
	balanceMode balanceMode

	// localBalancer handles local round-robin load balancing
	// remoteBalancer delegates to remote scheduler service
	localBalancer  Balancer
	remoteBalancer Balancer

	// prefillLocalBalancer and decodeLocalBalancer are used in PD-disagg mode
	// to separately balance prefill and decode stage requests
	prefillLocalBalancer Balancer
	decodeLocalBalancer  Balancer
}

// NewCompositeBalancer creates a new CompositeBalancer instance based on configuration.
// It automatically sets up the appropriate balancers for the configured mode.
func NewCompositeBalancer(config *options.GatewayConfig) *CompositeBalancer {
	bp := &CompositeBalancer{
		config: config,
	}
	if config.IsPDDisagg() {
		bp.setupPDDisaggBalancer(config)
	} else {
		bp.setupNeutralBalancer(config)
	}
	return bp
}

// setupPDDisaggBalancer initializes balancers for prefill-decode split mode.
// It creates separate balancers for prefill and decode stages.
func (bp *CompositeBalancer) setupPDDisaggBalancer(config *options.GatewayConfig) {
	prefillResolver := resolver.CreateBackendServiceResolver(&config.DiscoveryConfig, consts.InferTypePrefill)
	bp.prefillLocalBalancer = NewRoundRobinBalancer(prefillResolver)
	decodeResolver := resolver.CreateBackendServiceResolver(&config.DiscoveryConfig, consts.InferTypeDecode)
	bp.decodeLocalBalancer = NewRoundRobinBalancer(decodeResolver)

	if config.IsPDRoundRobin() {
		bp.balanceMode = PDLocalBalancer
	} else {
		bp.balanceMode = PDRemoteBalancer
		bp.remoteBalancer = NewSchedulerClient(config)
	}
}

// setupNeutralBalancer initializes balancers for neutral (non-split) mode.
// It creates a local balancer and optionally a remote scheduler balancer.
func (bp *CompositeBalancer) setupNeutralBalancer(config *options.GatewayConfig) {
	bp.balanceMode = LocalBalancer
	r := resolver.CreateBackendServiceResolver(&config.DiscoveryConfig, consts.InferTypeNeutral)
	bp.localBalancer = NewRoundRobinBalancer(r)
	if config.SchedulingPolicy != consts.SchedulingPolicyRoundRobin {
		bp.balanceMode = RemoteBalancer
		bp.remoteBalancer = NewSchedulerClient(config)
	}
}

// pdDisaggLocalGet handles endpoint selection in PD-disagg mode.
// For staged scheduling, it routes to the appropriate stage balancer.
// For non-staged, it combines results from both prefill and decode balancers.
func (bp *CompositeBalancer) pdDisaggLocalGet(req *types.RequestContext) (types.SchedulingResult, error) {
	if req.SchedulingCtx.SchedulingMode == types.SchedulingModePDStaged {
		switch req.SchedulingCtx.SchedulingStage {
		case consts.SchedulingStagePrefill:
			return bp.prefillLocalBalancer.Get(req)
		case consts.SchedulingStageDecode:
			return bp.decodeLocalBalancer.Get(req)
		default:
			return nil, fmt.Errorf("invalid scheduling stage: %s", req.SchedulingCtx.SchedulingStage)
		}
	} else {
		pResult, err := bp.prefillLocalBalancer.Get(req)
		if err != nil {
			klog.Errorf("get next local prefill error: %v", err)
			return nil, err
		}
		dResult, err := bp.decodeLocalBalancer.Get(req)
		if err != nil {
			klog.Errorf("get next local decode error: %v", err)
			return nil, err
		}
		pResult = append(pResult, dResult...)
		return pResult, nil
	}
}

// getWithFallback tries remote scheduler first, falls back to local balancer if scheduler is not ready.
func (bp *CompositeBalancer) getWithFallback(req *types.RequestContext) (types.SchedulingResult, error) {
	result, err := bp.remoteBalancer.Get(req)
	if !errors.Is(err, consts.ErrorSchedulerNotReady) {
		return result, err
	}
	switch bp.balanceMode {
	case RemoteBalancer:
		result, err = bp.localBalancer.Get(req)
	case PDRemoteBalancer:
		result, err = bp.pdDisaggLocalGet(req)
	default:
		panic("unsupported balance mode for fallback")
	}

	if err == nil {
		klog.Infof("[%s] fallback policy(round-robin) is applied, select endpoints: %s", req.Id, result.String())
	}
	return result, err
}

func (bp *CompositeBalancer) localGet(req *types.RequestContext) (types.SchedulingResult, error) {
	switch bp.balanceMode {
	case LocalBalancer, RemoteBalancer:
		return bp.localBalancer.Get(req)
	case PDLocalBalancer, PDRemoteBalancer:
		return bp.pdDisaggLocalGet(req)
	default:
		panic("unsupported balance mode")
	}
}

// Get selects appropriate endpoints for the request based on the current balance mode.
// It implements the Balancer interface.
func (bp *CompositeBalancer) Get(req *types.RequestContext) (types.SchedulingResult, error) {
	if req.SchedulingCtx.NeedScheduling {
		return bp.localGet(req)
	}

	switch bp.balanceMode {
	case RemoteBalancer, PDRemoteBalancer:
		return bp.getWithFallback(req)
	case LocalBalancer:
		return bp.localBalancer.Get(req)
	case PDLocalBalancer:
		return bp.pdDisaggLocalGet(req)
	default:
		panic("unsupported balance mode")
	}
}

// Release releases the instance back to the balancer pool.
// Only remote balancer modes need to release resources.
// It implements the Balancer interface.
func (bp *CompositeBalancer) Release(req *types.RequestContext, instance *types.LLMInstance) {
	balanceMode := bp.balanceMode
	if balanceMode == RemoteBalancer || balanceMode == PDRemoteBalancer {
		bp.remoteBalancer.Release(req, instance)
	}
}
