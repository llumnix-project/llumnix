package router

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strings"
	"sync"

	"k8s.io/klog/v2"

	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
)

var (
	baseVersionRegex = regexp.MustCompile(`/v\d+$`)
	pathVersionRegex = regexp.MustCompile(`^/v\d+`)
)

type RouteType int

func (rt RouteType) String() string {
	switch rt {
	case RouteInternal:
		return "Internal"
	case RouteExternal:
		return "External"
	default:
		return "Unknown"
	}
}

const (
	RouteUnknown RouteType = iota
	RouteInternal
	RouteExternal
)

type RouteEndpoint struct {
	URL    string
	APIKey string
	Model  string
}

func (re *RouteEndpoint) String() string { return fmt.Sprintf("%s", re.URL) }

type RouteResult struct {
	Endpoint  RouteEndpoint
	RouteType RouteType
}

// RouteConfig is the configuration for a route
type RouteConfig struct {
	URL        string `json:"base_url"`
	APIKey     string `json:"api_key"`
	Model      string `json:"model"`
	IsFallback bool   `json:"fallback"`

	Weight int    `json:"weight"`
	Prefix string `json:"prefix"`
}

// setupRoutingConfigs reads the routing config from the given string and returns a slice of RouteConfig types.
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
	routingPolicy  string
	routingConfigs []RouteConfig

	fallbackConfigs []RouteConfig
}

// newServiceRouter creates a new service router instance
func newServiceRouter(config ServiceRouterConfigInterface) *ServiceRouter {
	sr := &ServiceRouter{
		routingPolicy:  config.RoutePolicy(),
		routingConfigs: setupRoutingConfigs(config.RouteConfigRaw()),
	}
	sr.setupFallbackConfigs(sr.routingConfigs)

	return sr
}

var (
	gMutex         sync.Mutex
	gRouteInstance *ServiceRouter
	gRouterConfig  *ServiceRouterConfig
)

func GetServiceRouterConfig() *ServiceRouterConfig {
	gMutex.Lock()
	defer gMutex.Unlock()

	if gRouterConfig == nil {
		gRouterConfig = NewServiceRouterConfig()
	}
	return gRouterConfig
}

func GetServiceRouter() *ServiceRouter {
	gMutex.Lock()
	defer gMutex.Unlock()

	// Initialize global config and router on first call
	if gRouterConfig == nil {
		gRouterConfig = NewServiceRouterConfig()
	}
	if gRouteInstance == nil {
		gRouteInstance = newServiceRouter(gRouterConfig)
		return gRouteInstance
	}

	// Check if config changed and router needs rebuilding
	if gRouterConfig.NeedRebuild() {
		gRouteInstance = newServiceRouter(gRouterConfig)
		gRouterConfig.MarkRebuilt() // Reset rebuild flag
		return gRouteInstance
	}
	return gRouteInstance
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
func (sr *ServiceRouter) selectByWeight() (*RouteConfig, RouteType) {
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
	isInternal := sr.routingConfigs[selectedIndex].URL == consts.RouteInternalURL
	if isInternal {
		return nil, RouteInternal
	}
	return &sr.routingConfigs[selectedIndex], RouteExternal
}

// selectByPrefix selects a routing config based on model prefix matching
func (sr *ServiceRouter) selectByPrefix(req *types.RequestContext) (*RouteConfig, RouteType) {
	model := req.GetRequestModel()
	klog.V(3).Infof("service router select model by prefix, request model:%s", model)
	if model == "" {
		return nil, RouteInternal
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
		return nil, RouteUnknown
	}
	isInternal := sr.routingConfigs[selectedIndex].URL == consts.RouteInternalURL
	if isInternal {
		return nil, RouteInternal
	}
	return &sr.routingConfigs[selectedIndex], RouteExternal
}

// getByPolicy gets next tokens based on routing policy
func (sr *ServiceRouter) Route(req *types.RequestContext) (*RouteEndpoint, RouteType) {
	if len(sr.routingConfigs) == 0 || sr.routingPolicy == "" {
		return nil, RouteInternal
	}
	var selectedConfig *RouteConfig
	var rType RouteType

	// select routing config based on policy
	switch sr.routingPolicy {
	case consts.RoutePolicyWeight:
		selectedConfig, rType = sr.selectByWeight()
	case consts.RoutePolicyPrefix:
		selectedConfig, rType = sr.selectByPrefix(req)
	default:
		panic("not support this routing policy.")
	}

	klog.V(3).Infof("route with policy: %s, route type: %s, selected route: %s/%v", sr.routingPolicy, rType, selectedConfig.Model, selectedConfig.URL)

	if rType == RouteExternal {
		return &RouteEndpoint{
			URL:    selectedConfig.URL,
			APIKey: selectedConfig.APIKey,
			Model:  selectedConfig.Model,
		}, RouteExternal
	} else {
		return nil, rType
	}
}

// CanFallback checks if there are available fallback endpoints for the request.
func (sr *ServiceRouter) CanFallback(req *types.RequestContext) bool {
	fallbackAttempt := req.RequestStats.FallbackAttempt
	return len(sr.fallbackConfigs) > 0 && fallbackAttempt < len(sr.fallbackConfigs)
}

// Fallback gets fallback tokens from fallback configs
func (sr *ServiceRouter) Fallback(req *types.RequestContext) (*RouteEndpoint, error) {
	fallbackAttempt := req.RequestStats.FallbackAttempt
	if len(sr.fallbackConfigs) == 0 || fallbackAttempt >= len(sr.fallbackConfigs) {
		return nil, fmt.Errorf("no available fallback endpoint")
	}

	fallbackConfig := sr.fallbackConfigs[fallbackAttempt]
	req.RequestStats.FallbackAttempt = fallbackAttempt + 1

	externalEndpoint := &RouteEndpoint{
		URL:    fallbackConfig.URL,
		APIKey: fallbackConfig.APIKey,
		Model:  fallbackConfig.Model,
	}

	klog.V(3).Infof("get fallback route: %s/%s", fallbackConfig.Model, fallbackConfig.URL)
	return externalEndpoint, nil
}
