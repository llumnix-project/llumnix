package resolver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"llm-gateway/pkg/types"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	EasUriPrefix    = "eas://"
	EasLlmUriPrefix = "llm+eas://"
)

// Global configuration variables for EAS resolver
var (
	// discoveryEndpoint is the endpoint URL for the EAS discovery service.
	// It is read from the DISCOVERY_ENDPOINT environment variable.
	discoveryEndpoint = os.Getenv("DISCOVERY_ENDPOINT")

	// easSyncDuration defines the interval for synchronizing EAS endpoints.
	// Default is 5 seconds.
	easSyncDuration = time.Second * 5
)

// EasEndpoint represents a single endpoint in the EAS (Elastic Algorithm Service) system.
// It contains the network address and metadata for a service instance.
type EasEndpoint struct {
	Name    string `json:"name"`    // types.Endpoint name
	Service string `json:"service"` // Service name
	IP      string `json:"ip"`      // IP address
	Port    int    `json:"port"`    // Port number
	Weight  int    `json:"weight"`  // Load balancing weight
}

// EasEndpointList represents a collection of EAS endpoints.
// This is typically returned by the EAS discovery service.
type EasEndpointList struct {
	Items []EasEndpoint `json:"items"` // List of endpoints
}

// EasUpstream represents the upstream configuration for an EAS service.
// It includes both the service endpoints and correlated services.
type EasUpstream struct {
	Correlative []string `json:"correlative"` // List of correlated service names

	List *EasEndpointList `json:"endpoints"` // Endpoints for the service
}

// ServiceResult represents the result of fetching endpoints for a service.
// It contains either the endpoints or an error if the fetch failed.
type ServiceResult struct {
	err       error               // Error if endpoint fetch failed
	endpoints types.EndpointSlice // Endpoints if fetch succeeded
}

// GroupEndpointsFetcher defines the interface for fetching endpoints for a group of services.
// Implementations of this interface retrieve endpoint information from the EAS discovery service.
type GroupEndpointsFetcher interface {
	// FetchGroupEndpoints fetches endpoints for all services in a group.
	// Returns a map where keys are service names and values are ServiceResult objects.
	// The testService parameter is used to discover correlated services in the group.
	FetchGroupEndpoints(group, testService string) (map[string]ServiceResult, error)
}

// GroupEndpointsFetcherImpl is the default implementation of GroupEndpointsFetcher.
// It uses HTTP requests to communicate with the EAS discovery service.
type GroupEndpointsFetcherImpl struct {
	client *http.Client // HTTP client for making requests
}

// NewGroupEndpointsFetcherImpl creates a new instance of GroupEndpointsFetcherImpl.
// The returned fetcher uses an HTTP client with a 10-second timeout.
func NewGroupEndpointsFetcherImpl() GroupEndpointsFetcher {
	return &GroupEndpointsFetcherImpl{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

// doGetEasUpstream retrieves EAS upstream configuration from the cache server.
// It makes an HTTP GET request to the specified URL and parses the JSON response.
// Returns the parsed EasUpstream or an error if the request fails.
func (f *GroupEndpointsFetcherImpl) doGetEasUpstream(url string) (EasUpstream, error) {
	var easUpstream EasUpstream
	newReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		err = fmt.Errorf("create request to discovery service %s error: %v", url, err)
		return easUpstream, err
	}

	resp, err := f.client.Do(newReq)
	if err != nil {
		err = fmt.Errorf("do request to discovery service %s error: %v", url, err)
		return easUpstream, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		err = fmt.Errorf("read the request %s error: %v", url, err)
		return easUpstream, err
	}
	if resp.StatusCode != http.StatusOK {
		err = fmt.Errorf("do request to discovery service %s error: response status code is %d, content: %s", url, resp.StatusCode, string(body))
		return easUpstream, err
	}

	if err := json.Unmarshal(body, &easUpstream); err != nil {
		err = fmt.Errorf("do Unmarshal(%s) fail: %v", string(body), err)
		return easUpstream, err
	}
	return easUpstream, nil
}

// getGroupServices retrieves grouping service endpoints and related services from the cache server.
// It first fetches the test service to discover correlated services in the group,
// then returns a map of service names to ServiceResult objects.
// The testService endpoints are included in the results if available.
func (f *GroupEndpointsFetcherImpl) getGroupServices(group, testService string) (results map[string]ServiceResult, err error) {
	if discoveryEndpoint == "" {
		panic("discovery endpoint is not set by env DISCOVERY_ENDPOINT")
	}
	results = make(map[string]ServiceResult)
	url := fmt.Sprintf("http://%s/exported/apis/eas.alibaba-inc.k8s.io/v1/upstreams/%s?internal=true", discoveryEndpoint, testService)
	easUpstream, err := f.doGetEasUpstream(url)
	if err != nil {
		return results, err
	}
	// a gateway service and a scheduler service must be excluded from the group.
	for _, item := range easUpstream.Correlative {
		service := fmt.Sprintf("%s.%s", group, item)
		results[service] = ServiceResult{}
	}
	if len(results) == 0 {
		return results, fmt.Errorf("no service found in group %s", group)
	}

	if easUpstream.List == nil ||
		len(easUpstream.List.Items) == 0 {
		return results, nil
	}
	var endpoints types.EndpointSlice
	for _, item := range easUpstream.List.Items {
		ep := types.Endpoint{
			Host: item.IP,
			Port: item.Port,
		}
		endpoints = append(endpoints, ep)
	}
	results[testService] = ServiceResult{
		endpoints: endpoints,
	}
	return results, nil
}

// FetchGroupEndpoints fetches endpoints for all services in a group.
// It first discovers correlated services using the testService,
// then concurrently fetches endpoints for each service that doesn't already have endpoints.
// Returns a map of service names to ServiceResult objects.
// Errors are logged but individual service failures don't stop the entire operation.
func (f *GroupEndpointsFetcherImpl) FetchGroupEndpoints(group, testService string) (map[string]ServiceResult, error) {
	resolverResults, err := f.getGroupServices(group, testService)
	if err != nil {
		klog.Errorf("get group backend service error: %v", err)
		return nil, err
	}

	var wg sync.WaitGroup
	var lock sync.Mutex

	for serviceName, serviceResult := range resolverResults {
		if len(serviceResult.endpoints) > 0 {
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			url := fmt.Sprintf("http://%s/exported/apis/eas.alibaba-inc.k8s.io/v1/upstreams/%s?internal=true", discoveryEndpoint, name)
			easUpstream, err := f.doGetEasUpstream(url)
			if err != nil {
				klog.Errorf("get service(%s) upstream error: %v", name, err)
				lock.Lock()
				resolverResults[name] = ServiceResult{
					err: fmt.Errorf("get service(%s) upstream error: %v", name, err),
				}
				lock.Unlock()
				return
			}
			if easUpstream.List == nil ||
				len(easUpstream.List.Items) == 0 {
				lock.Lock()
				resolverResults[name] = ServiceResult{
					err: fmt.Errorf("get service(%s) upstream error: empty", name),
				}
				lock.Unlock()
				return
			}

			var endpoints types.EndpointSlice
			for _, item := range easUpstream.List.Items {
				ep := types.Endpoint{
					Host: item.IP,
					Port: item.Port,
				}
				endpoints = append(endpoints, ep)
			}
			if len(endpoints) > 0 {
				lock.Lock()
				resolverResults[name] = ServiceResult{
					endpoints: endpoints,
				}
				lock.Unlock()
			}
		}(serviceName)
	}
	wg.Wait()

	return resolverResults, nil
}

// EasResolverBackend is the central backend that manages all EAS resolvers.
// It periodically synchronizes endpoint information from the EAS discovery service
// and distributes updates to registered resolvers.
type EasResolverBackend struct {
	fetcher GroupEndpointsFetcher // Fetcher for retrieving endpoint information

	// key is the resolver uri address
	resolvers map[string]*EasResolver // Map of URI to resolver instances
	rlMutex   sync.Mutex              // Mutex for protecting the resolvers map
}

var (
	easResolverBackend *EasResolverBackend // Singleton instance
	easOnce            sync.Once           // Ensures singleton initialization happens only once
)

// getOrCreateEasResolverBackend returns the singleton instance of EasResolverBackend.
// If it doesn't exist, it creates one and starts the synchronization loop.
func getOrCreateEasResolverBackend() *EasResolverBackend {
	easOnce.Do(
		func() {
			easResolverBackend = &EasResolverBackend{
				fetcher:   NewGroupEndpointsFetcherImpl(),
				resolvers: make(map[string]*EasResolver),
			}
			go easResolverBackend.syncLoop()
		},
	)
	return easResolverBackend
}

// getResolvers returns a copy of the current resolvers map.
// This ensures external modifications don't affect the internal state.
func (rb *EasResolverBackend) getResolvers() map[string]*EasResolver {
	rb.rlMutex.Lock()
	defer rb.rlMutex.Unlock()

	// Create a copy of the map to avoid external modifications
	resolversCopy := make(map[string]*EasResolver, len(rb.resolvers))
	for k, v := range rb.resolvers {
		resolversCopy[k] = v
	}
	return resolversCopy
}

// registerResolver adds a new resolver to the backend's registry.
func (rb *EasResolverBackend) registerResolver(r *EasResolver) {
	rb.rlMutex.Lock()
	defer rb.rlMutex.Unlock()
	rb.resolvers[r.uri] = r
}

// getUniqueGroupService extracts a unique group-to-service mapping from all registered resolvers.
// For each group, it selects the first includeService or excludeService as the test service.
func (rb *EasResolverBackend) getUniqueGroupService() map[string]string {
	uniqueGroupService := make(map[string]string)
	resolvers := rb.getResolvers()
	for _, resolver := range resolvers {
		if len(resolver.includeServices) > 0 {
			uniqueGroupService[resolver.group] = resolver.includeServices[0]
		} else if len(resolver.excludeServices) > 0 {
			uniqueGroupService[resolver.group] = resolver.excludeServices[0]
		} else {
			panic("could not run here")
		}
	}
	return uniqueGroupService
}

// syncGroupBackendServicesOnce performs a single synchronization cycle.
// It fetches endpoints for all unique group-service pairs and updates all resolvers.
func (rb *EasResolverBackend) syncGroupBackendServicesOnce() {
	servicesEndpoints := make(map[string]types.EndpointSlice)

	groupService := rb.getUniqueGroupService()
	for group, testService := range groupService {
		results, err := rb.fetcher.FetchGroupEndpoints(group, testService)
		if err != nil {
			continue
		}
		for service, result := range results {
			if result.err != nil {
				continue
			}
			servicesEndpoints[service] = result.endpoints
		}
	}

	resolvers := rb.getResolvers()
	for _, resolver := range resolvers {
		resolver.update(servicesEndpoints)
	}
}

// syncLoop runs the periodic synchronization loop.
// It calls syncGroupBackendServicesOnce at regular intervals defined by easSyncDuration.
// The loop includes panic recovery to ensure it continues running even if errors occur.
func (rb *EasResolverBackend) syncLoop() {
	defer func() {
		if err := recover(); err != nil {
			klog.Errorf("Eas Resolver backend resolver panic: %v\n%s", err, string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(easSyncDuration)
	defer ticker.Stop()

	// Then run on every tick
	for range ticker.C {
		rb.syncGroupBackendServicesOnce()
	}
}

// EasResolver implements both Resolver and LLMResolver interfaces for EAS service discovery.
// It maintains a list of endpoints for services in a specific group, with optional
// inclusion or exclusion filters. Endpoints are periodically updated by the backend.
type EasResolver struct {
	uri             string   // The original URI used to create this resolver
	group           string   // EAS group name
	role            string   // LLM infer role
	includeServices []string // Services to include (if non-empty, only these services are included)
	excludeServices []string // Services to exclude (if non-empty, all services except these are included)

	mu        sync.RWMutex        // Mutex for protecting endpoints
	endpoints types.EndpointSlice // Current list of endpoints

	// watcher provides common observer management functionality
	watcher *Watcher // Watcher for monitoring endpoint changes
}

// newEasResolver creates a new EAS resolver instance from a URI.
// The URI must follow the format: eas://{group}?include={services} or eas://{group}?exclude={services}
// Returns an error if the URI is invalid or if both include and exclude are specified.
func newEasResolver(uri, role string) (*EasResolver, error) {
	group, excludeServices, includeServices, err := checkParseEasURI(uri)
	if err != nil {
		return nil, fmt.Errorf("create eas resolver %s failed: %v", uri, err)
	}
	if len(excludeServices) == 0 && len(includeServices) == 0 {
		return nil, fmt.Errorf("create eas resolver failed: include or exclude must be set")
	}
	r := &EasResolver{
		uri:             uri,
		group:           group,
		role:            role,
		includeServices: includeServices,
		excludeServices: excludeServices,
		watcher:         NewWatcher(),
	}
	rb := getOrCreateEasResolverBackend()
	rb.registerResolver(r)
	rb.syncGroupBackendServicesOnce()
	return r, nil
}

// GetEndpoints implements the Resolver interface.
// It returns the current list of endpoints for this resolver.
// The method is thread-safe and provides a snapshot of the endpoint state.
func (er *EasResolver) GetEndpoints() ([]types.Endpoint, error) {
	er.mu.RLock()
	defer er.mu.RUnlock()

	return er.endpoints, nil
}

// GetLLMWorkers implements the LLMResolver interface.
// It converts the current endpoints to types.LLMWorker objects.
// Note: Only the types.Endpoint field is populated; other types.LLMWorker fields remain zero values.
func (er *EasResolver) GetLLMWorkers() (types.LLMWorkerSlice, error) {
	er.mu.RLock()
	defer er.mu.RUnlock()
	workers := make(types.LLMWorkerSlice, 0, len(er.endpoints))
	for _, ep := range er.endpoints {
		workers = append(workers, types.LLMWorker{
			Endpoint: ep,
			Role:     types.InferRole(er.role),
		})
	}
	return workers, nil
}

// Watch implements the LLMResolver interface.
// It returns channels for monitoring added and removed LLM workers.
// The watcher handles observer management and context cancellation.
func (er *EasResolver) Watch(ctx context.Context) (<-chan types.LLMWorkerSlice, <-chan types.LLMWorkerSlice, error) {
	return er.watcher.Watch(ctx, er.GetLLMWorkers)
}

// endpointExist checks if a service endpoint exists in the candidates list.
func endpointExist(endpoint string, candidates []string) bool {
	for _, e := range candidates {
		if endpoint == e {
			return true
		}
	}
	return false
}

// update updates the resolver's endpoints based on the provided service endpoints.
// It filters services according to the include/exclude rules, calculates differences
// with the current endpoints, and notifies observers if changes are detected.
func (er *EasResolver) update(serviceEndpoints map[string]types.EndpointSlice) {
	newEndpoints := make(types.EndpointSlice, 0, 8)
	for service, eps := range serviceEndpoints {
		if len(er.includeServices) > 0 {
			if endpointExist(service, er.includeServices) {
				newEndpoints = append(newEndpoints, eps...)
			}
		} else if len(er.excludeServices) > 0 {
			if !endpointExist(service, er.excludeServices) {
				newEndpoints = append(newEndpoints, eps...)
			}
		} else {
			panic("could not run here")
		}
	}

	er.mu.Lock()
	defer er.mu.Unlock()
	added, removed := DiffSets(er.endpoints, newEndpoints, func(ep types.Endpoint) string { return ep.String() })
	if len(added) > 0 || len(removed) > 0 {
		er.endpoints = newEndpoints
		// Notify all observers about the changes
		er.notifyObservers(added, removed)
	}
}

// notifyObservers sends added and removed endpoints to all registered observers.
// It converts endpoints to types.LLMWorkerSlice before notifying the watcher.
func (er *EasResolver) notifyObservers(added, removed []types.Endpoint) {
	// Convert endpoints to types.LLMWorkerSlice
	var addedWorkers, removedWorkers types.LLMWorkerSlice
	for _, ep := range added {
		addedWorkers = append(addedWorkers, types.LLMWorker{
			Endpoint: ep,
		})
	}
	for _, ep := range removed {
		removedWorkers = append(removedWorkers, types.LLMWorker{
			Endpoint: ep,
		})
	}
	er.watcher.notifyObservers(addedWorkers, removedWorkers)
}

// checkParseEasURI parses an EAS URI and extracts the group and service filters.
// URI format: eas://{group}?exclude={service1},{service2} or eas://{group}?include={service1},{service2}
// Returns: group name, exclude services list, include services list, error
// The function validates the URI format and ensures exclude and include are not both specified.
func checkParseEasURI(uri string) (string, []string, []string, error) {
	// Split host:port and query parameters
	parts := strings.Split(uri, "?")
	if len(parts) != 2 {
		return "", nil, nil, fmt.Errorf("invalid eas URI format: missing query parameters: %s", uri)
	}

	group := parts[0]
	query := parts[1]

	if group == "" {
		return "", nil, nil, fmt.Errorf("invalid eas URI format: group cannot be empty")
	}

	// Parse query parameters
	var excludeServices, includeServices []string
	queryParams := strings.Split(query, "&")
	for _, param := range queryParams {
		if param == "" {
			continue
		}

		paramParts := strings.Split(param, "=")
		if len(paramParts) != 2 {
			return "", nil, nil, fmt.Errorf("invalid query parameter format: %s", param)
		}

		key := paramParts[0]
		value := paramParts[1]

		switch key {
		case "exclude":
			// Split comma-separated services
			if value != "" {
				services := strings.Split(value, ",")
				for _, service := range services {
					if service != "" {
						excludeServices = append(excludeServices, service)
					}
				}
			}
		case "include":
			// Split comma-separated services
			if value != "" {
				services := strings.Split(value, ",")
				for _, service := range services {
					if service != "" {
						includeServices = append(includeServices, service)
					}
				}
			}
		default:
			return "", nil, nil, fmt.Errorf("unsupported query parameter: %s", key)
		}
	}

	// Validate that exclude and include are not both specified
	if len(excludeServices) > 0 && len(includeServices) > 0 {
		return "", nil, nil, fmt.Errorf("invalid eas URI: cannot specify both exclude and include parameters")
	}

	return group, excludeServices, includeServices, nil
}

// EasResolverBuilder implements ResolverBuilder for creating EAS resolvers.
// It registers with schema "eas" in the resolver builder registry.
type EasResolverBuilder struct{}

// Schema returns the schema identifier for this builder: "eas".
func (b *EasResolverBuilder) Schema() string {
	return "eas"
}

// Build creates a new EAS resolver instance from the provided arguments.
// The args map must contain a "uri" key with a valid EAS URI string.
func (b *EasResolverBuilder) Build(uri string, args BuildArgs) (Resolver, error) {
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}
	// Remove the eas:// prefix
	if !strings.HasPrefix(uri, EasUriPrefix) {
		return nil, fmt.Errorf("invalid eas URI format: must start with 'eas://'")
	}
	uri = strings.TrimPrefix(uri, EasUriPrefix)

	// start parse eas naming
	return newEasResolver(uri, types.InferRoleNormal.String())
}

// EasLlmResolverBuilder implements LLMResolverBuilder for creating EAS LLM resolvers.
// It registers with schema "eas+llm" in the resolver builder registry.
type EasLlmResolverBuilder struct{}

// Schema returns the schema identifier for this builder: "eas+llm".
func (b *EasLlmResolverBuilder) Schema() string {
	return "llm+eas"
}

// Build creates a new EAS LLM resolver instance from the provided arguments.
// The args map must contain a "uri" key with a valid EAS URI string.
func (b *EasLlmResolverBuilder) Build(uri string, args BuildArgs) (LLMResolver, error) {
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}

	// Remove the llm+eas:// prefix
	if !strings.HasPrefix(uri, EasLlmUriPrefix) {
		return nil, fmt.Errorf("invalid eas URI format: must start with 'llm+eas://'")
	}
	uri = strings.TrimPrefix(uri, EasLlmUriPrefix)

	// Read role build args
	role, ok := args["role"].(string)
	if !ok || role == "" {
		return nil, fmt.Errorf("missing role or invalid role build args: %v", role)
	}

	if role == types.InferRoleAll.String() {
		role = string(types.InferRoleNormal)
	}
	if role != types.InferRoleNormal.String() {
		return nil, fmt.Errorf("eas resolver does not support role: %s", role)
	}

	// start parse eas naming
	return newEasResolver(uri, role)
}

func init() {
	Register(&EasResolverBuilder{})
	RegisterLLM(&EasLlmResolverBuilder{})
}
