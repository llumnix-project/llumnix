package resolver

import (
	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/consts"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	mapset "github.com/deckarep/golang-set/v2"
	"k8s.io/klog/v2"
)

// utils function
func getGroup(fullName string) string {
	parts := strings.Split(fullName, ".")
	return parts[0]
}

// EasResolver can work in two modes: grouping service and specifying backend service.
// If serviceName is set, it is considered to work in the mode of specifying the backend service,
// and if it is not set, it is considered to work in the grouping service mode
type EasResolver struct {
	backendService  string
	excludeServices []string
	lck             sync.RWMutex

	// The main purpose of testService is to obtain all the service names under the group,
	// because when there is no grouping service under the group, the cache server returns empty
	testService       string
	group             string
	discoveryEndpoint string
	cacheClient       *http.Client
	updateDuration    time.Duration

	mu        sync.RWMutex
	endpoints []structs.WeightEndpoint
}

func NewEasResolver(c *options.Config) *EasResolver {
	var r *EasResolver
	if len(c.BackendService) > 0 {
		r = createEasResolver(c.DiscoveryEndpoint, c.BackendService, "")
		klog.Infof("create default balancer with a backend service resolver: %s", c.BackendService)
	} else {
		excludeService := fmt.Sprintf("%s,%s", c.LlmScheduler, c.LlmGateway)
		if len(c.Redis) > 0 {
			excludeService = fmt.Sprintf("%s,%s", excludeService, c.Redis)
		}
		r = createEasResolver(c.DiscoveryEndpoint, "", excludeService)
		klog.Infof("create default balancer with a eas service group resolver")
	}
	return r
}

var (
	schOnce      sync.Once
	gSchResolver *EasResolver
)

func CreateSchedulerResolver(c *options.Config) *EasResolver {
	schOnce.Do(func() {
		klog.Infof("create a scheduler(%s) resolver", c.LlmScheduler)
		gSchResolver = createEasResolver(c.DiscoveryEndpoint, c.LlmScheduler, "")
	})
	return gSchResolver
}

func createEasResolver(discovery, backendService, excludeService string) *EasResolver {
	var excludes []string
	if len(excludeService) > 0 {
		excludes = strings.Split(excludeService, ",")
	}
	if len(backendService) == 0 && len(excludes) == 0 {
		panic("exception: config error, backendService or excludes must be set.")
	}
	if len(backendService) != 0 && len(excludes) != 0 {
		panic("exception: config error, backendService and excludes must not be set at the same time.")
	}

	var (
		testService string
		group       string
	)
	if len(excludes) > 0 {
		testService = excludes[0]
		group = getGroup(excludes[0])
	}
	r := &EasResolver{
		backendService:    backendService,
		excludeServices:   excludes,
		testService:       testService,
		group:             group,
		discoveryEndpoint: discovery,
		updateDuration:    5 * time.Second,
	}
	go r.syncLoop()
	return r
}

func (er *EasResolver) Name() string {
	if len(er.backendService) > 0 {
		return er.backendService
	} else {
		return er.group
	}
}

type EasEndpoint struct {
	Name    string `json:"name"`
	Service string `json:"service"`
	IP      string `json:"ip"`
	Port    int    `json:"port"`
	Weight  int    `json:"weight"`
}

type EasEndpointList struct {
	Items []EasEndpoint `json:"items"`
}

type EasUpstream struct {
	Correlative []string `json:"correlative"`

	List *EasEndpointList `json:"endpoints"`
}

func (er *EasResolver) AddExcludeService(serviceName string) {
	if len(er.group) == 0 {
		return
	}

	er.lck.Lock()
	defer er.lck.Unlock()

	groupName := er.group + "." + serviceName
	for _, name := range er.excludeServices {
		if name == groupName {
			return
		}
	}
	er.excludeServices = append(er.excludeServices, groupName)
	klog.Infof("add a exclude service: %s", groupName)
}

func (er *EasResolver) excludeServiceMatch(serviceName string) bool {
	er.lck.RLock()
	defer er.lck.RUnlock()
	for _, exclude := range er.excludeServices {
		parts := strings.Split(exclude, ".")
		if len(parts) != 2 {
			klog.Errorf("invalid exclude service name: %s", exclude)
			continue
		}
		if parts[1] == serviceName {
			return true
		}
	}
	return false
}

// doGetEasUpstream read eas upstream from cache server
func (er *EasResolver) doGetEasUpstream(url string) (EasUpstream, error) {
	var easUpstream EasUpstream
	newReq, err := http.NewRequest("GET", url, nil)
	if err != nil {
		err = fmt.Errorf("create request to discovery service %s error: %v", url, err)
		return easUpstream, err
	}

	if er.cacheClient == nil {
		er.cacheClient = utils.NewHttpClient()
	}
	resp, err := er.cacheClient.Do(newReq)
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

// key: service name
// value: a list of service endpoints
type ResolverResults map[string][]structs.WeightEndpoint

// getGroupBackendServices get grouping service endpoints and related services from cache server.
func (er *EasResolver) getGroupBackendServices(group, testService string) (results ResolverResults, err error) {
	results = make(ResolverResults)

	url := fmt.Sprintf("http://%s/exported/apis/eas.alibaba-inc.k8s.io/v1/upstreams/%s?internal=true", er.discoveryEndpoint, testService)
	easUpstream, err := er.doGetEasUpstream(url)
	if err != nil {
		return results, err
	}

	// a gateway service and a scheduler service must be excluded from the group.
	for _, item := range easUpstream.Correlative {
		if !er.excludeServiceMatch(item) {
			service := fmt.Sprintf("%s.%s", group, item)
			results[service] = []structs.WeightEndpoint{}
		}
	}
	if len(results) == 0 {
		return results, consts.ErrorBackendServiceNoFound
	}

	if easUpstream.List == nil ||
		len(easUpstream.List.Items) == 0 {
		return results, nil
	}
	for _, item := range easUpstream.List.Items {
		if er.excludeServiceMatch(item.Service) {
			continue
		}
		ep := structs.Endpoint{
			IP:   item.IP,
			Port: item.Port,
		}
		wep := structs.WeightEndpoint{
			Ep:     ep,
			Weight: 100,
		}
		service := fmt.Sprintf("%s.%s", group, item.Service)
		results[service] = append(results[service], wep)
	}
	return results, nil
}

func (er *EasResolver) getEndpointsFromCacheServer() ([]structs.WeightEndpoint, error) {
	resolverResults := make(ResolverResults)
	if len(er.backendService) > 0 {
		// work in the mode of specifying the backend service
		resolverResults[er.backendService] = []structs.WeightEndpoint{}
	} else {
		// work in grouping mode
		var err error
		resolverResults, err = er.getGroupBackendServices(er.group, er.testService)
		if err != nil {
			klog.Errorf("get group backend service error: %v", err)
			return nil, err
		}
	}

	var wg sync.WaitGroup
	var lock sync.Mutex

	var weightEndpoints []structs.WeightEndpoint
	for serviceName, endpoints := range resolverResults {
		if len(endpoints) > 0 {
			lock.Lock()
			weightEndpoints = append(weightEndpoints, endpoints...)
			lock.Unlock()
			continue
		}
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			url := fmt.Sprintf("http://%s/exported/apis/eas.alibaba-inc.k8s.io/v1/upstreams/%s?internal=true", er.discoveryEndpoint, name)
			easUpstream, err := er.doGetEasUpstream(url)
			if err != nil {
				klog.Errorf("get service(%s) upstream error: %v", name, err)
				return
			}
			if easUpstream.List == nil ||
				len(easUpstream.List.Items) == 0 {
				return
			}

			var wEndpoints []structs.WeightEndpoint
			for _, item := range easUpstream.List.Items {
				ep := structs.Endpoint{
					IP:   item.IP,
					Port: item.Port,
				}
				wep := structs.WeightEndpoint{
					Ep:     ep,
					Weight: 100,
				}
				wEndpoints = append(wEndpoints, wep)
			}
			if len(wEndpoints) > 0 {
				lock.Lock()
				weightEndpoints = append(weightEndpoints, wEndpoints...)
				lock.Unlock()
			}
		}(serviceName)
	}
	wg.Wait()

	if len(weightEndpoints) == 0 {
		return weightEndpoints, consts.ErrorEndpointNotFound
	}

	return weightEndpoints, nil
}

func (er *EasResolver) endpointsDiff(origin, current []structs.WeightEndpoint) bool {
	diff := false

	set1 := mapset.NewSet(origin...)
	set2 := mapset.NewSet(current...)
	removed := set1.Difference(set2).ToSlice()
	added := set2.Difference(set1).ToSlice()

	var removedStr []string
	for _, r := range removed {
		removedStr = append(removedStr, r.String())
	}
	rStr := strings.Join(removedStr, ",")
	if len(rStr) > 0 {
		klog.Infof("resolve service(%s) remove: %s", er.Name(), rStr)
		diff = true
	}

	var addStr []string
	for _, r := range added {
		addStr = append(addStr, r.String())
	}
	aStr := strings.Join(addStr, ",")
	if len(aStr) > 0 {
		klog.Infof("resolve service(%s) add: %s", er.Name(), aStr)
		diff = true
	}
	return diff
}

func (er *EasResolver) syncLoop() {
	for {
		endpoints, err := er.getEndpointsFromCacheServer()
		if err == nil {
			sort.Slice(endpoints, func(i, j int) bool {
				return strings.Compare(endpoints[i].Ep.String(), endpoints[j].Ep.String()) < 0
			})
			diff := er.endpointsDiff(er.endpoints, endpoints)
			if diff {
				er.mu.Lock()
				er.endpoints = endpoints
				er.mu.Unlock()
			}
		} else if len(endpoints) == 0 {
			er.mu.Lock()
			er.endpoints = endpoints
			er.mu.Unlock()
		}

		time.Sleep(er.updateDuration)
	}
}

func (er *EasResolver) GetWeightEndpoints() (ret []structs.WeightEndpoint) {
	er.mu.RLock()
	defer er.mu.RUnlock()
	ret = er.endpoints
	return
}
