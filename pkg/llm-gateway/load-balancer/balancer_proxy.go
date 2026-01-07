package loadbalancer

import (
	"k8s.io/klog/v2"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/structs"
)

type proxyType int

const (
	// local
	LocalBalancer proxyType = iota
	// local pd split
	PDLocalBalancer
	//remote scheduler
	RemoteBalancer
)

// LoadBalancerProxy is a proxy class that creates different load balancers based on configuration to adapt to different scenarios.
type LoadBalancerProxy struct {
	config *options.Config

	// how to choose the endpoint
	proxyType proxyType

	// local load balancer and remote load balancer(scheduler)
	localBalancer  LoadBalancer
	remoteBalancer LoadBalancer

	// local prefill and decode load balancer
	prefillLocalBalancer LoadBalancer
	decodeLocalBalancer  LoadBalancer
}

func NewLoadBalancerProxy(config *options.Config) *LoadBalancerProxy {
	lbp := &LoadBalancerProxy{
		config: config,
	}
	if config.IsPDSplitMode() {
		lbp.setupPDSplitBalancer(config)
	} else {
		lbp.setupNormalBalancer(config)
	}
	return lbp
}

func (lbp *LoadBalancerProxy) createResolver(config *options.Config, inferMode string) resolver.Resolver {
	var r resolver.Resolver
	switch config.UseDiscovery {
	case consts.DiscoveryMessageBus:
		r = resolver.NewMsgBusResolver(inferMode, config.PDSplitMode)
	default:
		r = resolver.NewEasResolver(config)
	}
	return r
}

func (lbp *LoadBalancerProxy) setupPDSplitBalancer(config *options.Config) {
	if !config.IsPDRoundRobin() {
		lbp.proxyType = RemoteBalancer
		// use DecodeInfer Mode as the instances can be connected
		lbp.localBalancer = NewSWRRLoadBalancer(lbp.createResolver(config, consts.DecodeInferMode))
		lbp.remoteBalancer = NewSchedulerBalancer(config)
		return
	}

	// Must use message-bus discovery when p d are all round-robin policies
	if config.UseDiscovery != consts.DiscoveryMessageBus {
		klog.Fatalf("not support discovery: %s, in split mode with all round robin policy.", config.UseDiscovery)
	}

	lbp.proxyType = PDLocalBalancer
	pResolver := lbp.createResolver(config, consts.PrefillInferMode)
	dResolver := lbp.createResolver(config, consts.DecodeInferMode)
	lbp.prefillLocalBalancer = NewSWRRLoadBalancer(pResolver)
	lbp.decodeLocalBalancer = NewSWRRLoadBalancer(dResolver)
	klog.Infof("create pd-split local round-robin load balancer successfully.")
}

func (lbp *LoadBalancerProxy) setupNormalBalancer(config *options.Config) {
	r := lbp.createResolver(config, consts.NormalInferMode)
	if config.SchedulePolicy != consts.SchedulePolicyRoundRobin {
		lbp.proxyType = RemoteBalancer
		lbp.localBalancer = NewSWRRLoadBalancer(r)
		lbp.remoteBalancer = NewSchedulerBalancer(config)
	} else {
		lbp.proxyType = LocalBalancer
		lbp.localBalancer = NewSWRRLoadBalancer(r)
	}
}

func (lbp *LoadBalancerProxy) GetNextTokens(req *structs.Request) (*structs.NextTokens, error) {
	if req.NoSchedule {
		switch lbp.proxyType {
		case RemoteBalancer:
			return lbp.localBalancer.GetNextTokens(req)
		case LocalBalancer:
			return lbp.localBalancer.GetNextTokens(req)
		case PDLocalBalancer:
			return lbp.decodeLocalBalancer.GetNextTokens(req)
		default:
			panic("not support this proxy type.")
		}
	}

	switch lbp.proxyType {
	case RemoteBalancer:
		return lbp.remoteBalancer.GetNextTokens(req)
	case LocalBalancer:
		return lbp.localBalancer.GetNextTokens(req)
	case PDLocalBalancer:
		switch req.ScheduleStage {
		case consts.PrefillInferMode:
			pTokens, err := lbp.prefillLocalBalancer.GetNextTokens(req)
			if err != nil {
				klog.Errorf("get next prefill error: %v", err)
				return nil, err
			}
			nt := &structs.NextTokens{}
			pTokens.Tokens[0].InferMode = consts.PrefillInferMode
			nt.Tokens = append(nt.Tokens, pTokens.Tokens[0])
			return nt, nil
		case consts.DecodeInferMode:
			dTokens, err := lbp.decodeLocalBalancer.GetNextTokens(req)
			if err != nil {
				klog.Errorf("get next decode error: %v", err)
				return nil, err
			}
			nt := &structs.NextTokens{}
			dTokens.Tokens[0].InferMode = consts.DecodeInferMode
			nt.Tokens = append(nt.Tokens, dTokens.Tokens[0])
			return nt, nil
		}
		pTokens, err := lbp.prefillLocalBalancer.GetNextTokens(req)
		if err != nil {
			klog.Errorf("get next prefill error: %v", err)
			return nil, err
		}
		dTokens, err := lbp.decodeLocalBalancer.GetNextTokens(req)
		if err != nil {
			klog.Errorf("get next decode error: %v", err)
			return nil, err
		}
		nt := &structs.NextTokens{}
		pTokens.Tokens[0].InferMode = consts.PrefillInferMode
		nt.Tokens = append(nt.Tokens, pTokens.Tokens[0])
		dTokens.Tokens[0].InferMode = consts.DecodeInferMode
		nt.Tokens2 = append(nt.Tokens2, dTokens.Tokens[0])
		return nt, nil
	default:
		panic("not support this proxy type.")
	}
}

func (lbp *LoadBalancerProxy) ReleaseToken(req *structs.Request, token *structs.Token) {
	if lbp.proxyType == RemoteBalancer {
		lbp.remoteBalancer.ReleaseToken(req, token)
	}
}

func (lbp *LoadBalancerProxy) ExcludeService(string) {}
