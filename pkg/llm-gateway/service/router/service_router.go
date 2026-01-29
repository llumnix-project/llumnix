package router

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"regexp"
	"strings"

	"k8s.io/klog/v2"

	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
)

var (
	baseVersionRegex = regexp.MustCompile(`/v\d+$`)
	pathVersionRegex = regexp.MustCompile(`^/v\d+`)
)

type RouteType int

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

func (re *RouteEndpoint) JoinURL(path string) string {
	re.URL = strings.TrimSuffix(re.URL, "/")
	hasBaseVersion := baseVersionRegex.MatchString(re.URL)
	if hasBaseVersion && strings.HasPrefix(path, "/v") {
		path = pathVersionRegex.ReplaceAllString(path, "")
	}
	return fmt.Sprintf("%s%s", re.URL, path)
}

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

// NewServiceRouter creates a new service router instance
func NewServiceRouter(routingPolicy string, routingConfigRaw string) *ServiceRouter {
	sr := &ServiceRouter{
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
func (sr *ServiceRouter) selectByWeight() (*RouteConfig, RouteType) {
	if len(sr.routingConfigs) == 0 {
		return nil, RouteUnknown
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
	isInternal := sr.routingConfigs[selectedIndex].URL == consts.RouteInternalURL
	if isInternal {
		return nil, RouteInternal
	}
	return &sr.routingConfigs[selectedIndex], RouteExternal
}

// selectByPrefix selects a routing config based on model prefix matching
func (sr *ServiceRouter) selectByPrefix(req *types.RequestContext) (*RouteConfig, RouteType) {
	model := req.LLMRequest.Model
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

// Route gets next endpoint based on routing policy
func (sr *ServiceRouter) Route(req *types.RequestContext) (*RouteEndpoint, RouteType) {
	if len(sr.routingConfigs) == 0 {
		return nil, RouteInternal
	}
	var selectedConfig *RouteConfig
	var rType RouteType

	klog.V(3).Infof("get next endpoint with routing policy: %s", sr.routingPolicy)
	// select routing config based on policy
	switch sr.routingPolicy {
	case consts.RoutePolicyWeight:
		selectedConfig, rType = sr.selectByWeight()
	case consts.RoutePolicyPrefix:
		selectedConfig, rType = sr.selectByPrefix(req)
	default:
		panic("not support this routing policy.")
	}

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

// Fallback gets fallback endpoint from fallback configs
func (sr *ServiceRouter) Fallback(req *types.RequestContext) (*RouteEndpoint, error) {
	fallbackAttempt := req.RequestStats.FallbackAttempt
	if len(sr.fallbackConfigs) == 0 || fallbackAttempt >= len(sr.fallbackConfigs) {
		return nil, consts.ErrorNoAvailableEndpoint
	}

	fallbackConfig := sr.fallbackConfigs[fallbackAttempt]
	req.RequestStats.FallbackAttempt = fallbackAttempt + 1

	externalEndpoint := &RouteEndpoint{
		URL:    fallbackConfig.URL,
		APIKey: fallbackConfig.APIKey,
		Model:  fallbackConfig.Model,
	}

	klog.V(3).Infof("get fallback endpoint with external endpoint: %s", externalEndpoint.String())
	return externalEndpoint, nil
}
