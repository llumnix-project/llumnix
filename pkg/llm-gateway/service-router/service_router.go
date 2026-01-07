package servicerouter

import (
	"encoding/json"
	"math/rand"
	"strings"

	"k8s.io/klog/v2"

	"easgo/pkg/llm-gateway/consts"
	loadbalancer "easgo/pkg/llm-gateway/load-balancer"
	"easgo/pkg/llm-gateway/structs"
)

// RouteConfig is the configuration for a route
type RouteConfig struct {
	URL        string `json:"base_url"`
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`
	IsFallback bool   `json:"fallback"`

	Weight int    `json:"weight"`
	Prefix string `json:"prefix"`
}

// setupRoutingConfigs reads the routing config from the given string and returns a slice of RouteConfig structs.
func setupRoutingConfigs(routingConfigRaw string) []RouteConfig {
	var routingConfigs []RouteConfig
	if routingConfigRaw != "" {
		if err := json.Unmarshal([]byte(routingConfigRaw), &routingConfigs); err != nil {
			klog.Errorf("load routing configs, un marshal(%s) failed: %v", routingConfigRaw, err)
			return []RouteConfig{}
		}
	}
	return routingConfigs
}

// ServiceRouter handles service routing based on different policies
type ServiceRouter struct {
	lb loadbalancer.LoadBalancer

	routingPolicy  string
	routingConfigs []RouteConfig

	fallbackConfigs []RouteConfig
}

// NewServiceRouter creates a new service router instance
func NewServiceRouter(lb loadbalancer.LoadBalancer, routingPolicy string, routingConfigRaw string) *ServiceRouter {
	sr := &ServiceRouter{
		lb:             lb,
		routingPolicy:  routingPolicy,
		routingConfigs: setupRoutingConfigs(routingConfigRaw),
	}
	sr.setupFallbackConfigs(sr.routingConfigs)

	return sr
}

// setupFallbackConfigs sets up fallback configs
func (sr *ServiceRouter) setupFallbackConfigs(routingConfigs []RouteConfig) {
	sr.fallbackConfigs = make([]RouteConfig, 0)
	for _, config := range routingConfigs {
		// skip internal service
		if config.IsFallback && config.URL != consts.RouteInternalURL {
			sr.fallbackConfigs = append(sr.fallbackConfigs, config)
		}
	}
}

// selectByWeight selects a routing config based on weight distribution
func (sr *ServiceRouter) selectByWeight() (*RouteConfig, error) {
	if len(sr.routingConfigs) == 0 {
		return nil, consts.ErrorEndpointNotFound
	}

	totalWeight := 0
	for _, config := range sr.routingConfigs {
		totalWeight += config.Weight
	}

	randNum := rand.Intn(totalWeight)
	currentWeight, selectedIndex := 0, 0
	for i, config := range sr.routingConfigs {
		currentWeight += config.Weight
		if randNum < currentWeight {
			selectedIndex = i
			break
		}
	}
	return &sr.routingConfigs[selectedIndex], nil
}

// selectByPrefix selects a routing config based on model prefix matching
func (sr *ServiceRouter) selectByPrefix(req *structs.Request) (*RouteConfig, error) {
	klog.V(3).Infof("service router select model by prefix, request model:%s", req.Model)
	model := req.Model
	if len(sr.routingConfigs) == 0 || model == "" {
		return nil, consts.ErrorEndpointNotFound
	}

	currentLength, selectedIndex := -1, -1
	for i, config := range sr.routingConfigs {
		if strings.HasSuffix(config.Prefix, "*") {
			prefix := strings.TrimSuffix(config.Prefix, "*")
			if strings.HasPrefix(model, prefix) {
				if len(prefix) > currentLength {
					selectedIndex = i
					currentLength = len(prefix)
				}
			}
		} else if model == config.Prefix {
			selectedIndex = i
			break
		}
	}

	if selectedIndex == -1 {
		return nil, consts.ErrorEndpointNotFound
	}
	return &sr.routingConfigs[selectedIndex], nil
}

// getTokens gets next tokens based on routing policy
func (sr *ServiceRouter) getTokens(req *structs.Request) (nextTokens *structs.NextTokens, err error) {
	// select routing config based on policy
	var selectedConfig *RouteConfig
	switch sr.routingPolicy {
	case consts.RoutePolicyWeight:
		selectedConfig, err = sr.selectByWeight()
		if err != nil {
			klog.V(3).Infof("failed to select routing config by weight: %v", err)
			return nil, err
		}
	case consts.RoutePolicyPrefix:
		selectedConfig, err = sr.selectByPrefix(req)
		if err != nil {
			klog.V(3).Infof("failed to select routing config by prefix: %v", err)
			return nil, err
		}
	default:
		panic("not support this routing policy.")
	}
	// select internal service to handle request
	if selectedConfig.URL == consts.RouteInternalURL {
		return sr.lb.GetNextTokens(req)
	}
	klog.V(3).Infof("select routing config: %v", selectedConfig)

	// create external endpoint from selected routing config
	externalEndpoint := &structs.ExternalEndpoint{
		URL:    selectedConfig.URL,
		Model:  selectedConfig.Model,
		APIKey: selectedConfig.APIKey,
	}
	// return next tokens with external service
	nextTokens = &structs.NextTokens{
		ExternalEp: externalEndpoint,
	}
	return nextTokens, nil
}

// GetNextTokensByRouter gets next tokens based on service router
func (sr *ServiceRouter) GetNextTokensByRouter(req *structs.Request) (nextTokens *structs.NextTokens, err error) {
	if len(sr.routingConfigs) == 0 {
		return sr.lb.GetNextTokens(req)
	}

	// get next tokens based on routing policy
	klog.V(3).Infof("get next tokens with routing policy: %s", sr.routingPolicy)
	switch sr.routingPolicy {
	case consts.RoutePolicyWeight, consts.RoutePolicyPrefix:
		nextTokens, err = sr.getTokens(req)
	default:
		nextTokens, err = sr.lb.GetNextTokens(req)
	}

	return nextTokens, err
}

// GetFallbackLength gets fallback size
func (sr *ServiceRouter) GetFallbackLength() int {
	return len(sr.fallbackConfigs)
}

// GetFallbackTokens gets fallback tokens from fallback configs
func (sr *ServiceRouter) GetFallbackTokens(req *structs.Request) (nextTokens *structs.NextTokens, err error) {
	if len(sr.fallbackConfigs) == 0 || req.FallbackAttempt >= len(sr.fallbackConfigs) {
		return nil, consts.ErrorNoAvailableEndpoint
	}

	fallbackConfig := sr.fallbackConfigs[req.FallbackAttempt]
	req.FallbackAttempt = req.FallbackAttempt + 1

	externalEndpoint := &structs.ExternalEndpoint{
		URL:    fallbackConfig.URL,
		Model:  fallbackConfig.Model,
		APIKey: fallbackConfig.APIKey,
	}
	nextTokens = &structs.NextTokens{
		ExternalEp: externalEndpoint,
	}

	klog.V(3).Infof("get fallback tokens with extenal endpoint: %s", externalEndpoint.Description())
	return nextTokens, nil
}
