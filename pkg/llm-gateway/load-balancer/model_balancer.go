package loadbalancer

import (
	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/resolver"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/utils"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

const (
	kModelPath         = "/v1/models"
	kSyncModelDuration = 10
)

type ModelData struct {
	Id      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}
type ModelList struct {
	Object string      `json:"object"`
	Data   []ModelData `json:"data"`
}

type WEndpointsSlice []structs.WeightEndpoint

// body content:
//
//	{
//		"object": "list",
//		"data": [
//		  {
//			"id": "model-id-0",
//			"object": "model",
//			"created": 1686935002,
//			"owned_by": "organization-owner"
//		  }
//		]
//	}
func getModelsByEndpoints(client *http.Client, endpoints []structs.WeightEndpoint, authToken string) map[string]WEndpointsSlice {
	var mu sync.Mutex
	modelEndpoints := make(map[string]WEndpointsSlice)

	var wg sync.WaitGroup
	wg.Add(len(endpoints))

	for _, wep := range endpoints {
		go func(ep structs.WeightEndpoint) {
			defer wg.Done()
			url := "http://" + ep.Ep.String() + kModelPath
			req, err := http.NewRequest("GET", url, nil)
			if err != nil {
				klog.Errorf("could not new request for %s: %v", url, err)
				return
			}
			if len(authToken) > 0 {
				req.Header.Add("Authorization", authToken)
			}

			resp, err := client.Do(req)
			if err != nil {
				klog.Errorf("get %s error: %v", url, err)
				return
			}
			if resp.StatusCode != http.StatusOK {
				klog.Errorf("get %s status code: %d", url, resp.StatusCode)
				return
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				klog.Errorf("read %s content error: %v", url, err)
				return
			}

			var modelList ModelList
			err = json.Unmarshal(body, &modelList)
			if err != nil {
				klog.Errorf("model list Unmarshal failed: %v, from: %s content: %s", err, url, string(body))
				return
			}

			if len(modelList.Data) == 0 {
				klog.Warning("empty model list: %s", url)
				return
			}

			// TODO: check model is valid or not
			if len(modelList.Data[0].Id) == 0 {
				klog.Warningf("invalid model id: empty, %s", len(modelList.Data), url)
			}
			model := modelList.Data[0].Id

			mu.Lock()
			modelEndpoints[model] = append(modelEndpoints[model], wep)
			mu.Unlock()
		}(wep)
	}
	wg.Wait()

	return modelEndpoints
}

func deepCopyMapModelEndpoints(original map[string]WEndpointsSlice) map[string]WEndpointsSlice {
	copyMap := make(map[string]WEndpointsSlice, len(original))
	for key, value := range original {
		copyValue := make(WEndpointsSlice, len(value))
		copy(copyValue, value)
		copyMap[key] = copyValue
	}
	return copyMap
}

type ModelResolver struct {
	model string

	mu              sync.Mutex
	weightEndpoints []structs.WeightEndpoint
}

func NewModelResolver(model string) *ModelResolver {
	return &ModelResolver{model: model}
}

func (mr *ModelResolver) Start() error {
	return nil
}

func (mr *ModelResolver) GetWeightEndpoints() []structs.WeightEndpoint {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	return mr.weightEndpoints
}

func (mr *ModelResolver) SetWeightEndpoints(endpoints []structs.WeightEndpoint) {
	mr.mu.Lock()
	defer mr.mu.Unlock()
	mr.weightEndpoints = endpoints
}

type SingleModelLoadBalancer struct {
	lb LoadBalancer
	r  *ModelResolver
}

func NewSingleModelLoadBalancer(r *ModelResolver) *SingleModelLoadBalancer {
	return &SingleModelLoadBalancer{
		lb: NewSWRRLoadBalancer(r),
		r:  r,
	}
}

func (smlb *SingleModelLoadBalancer) UpdateWeightEndpoints(endpoints []structs.WeightEndpoint) {
	newEndpoints := make([]structs.WeightEndpoint, len(endpoints))
	copy(newEndpoints, endpoints)

	sort.Slice(newEndpoints, func(i, j int) bool {
		return strings.Compare(newEndpoints[i].Ep.String(), newEndpoints[j].Ep.String()) < 0
	})
	smlb.r.SetWeightEndpoints(newEndpoints)
}

func (smlb *SingleModelLoadBalancer) GetNextTokens(req *structs.Request) (*structs.NextTokens, error) {
	return smlb.lb.GetNextTokens(req)
}

type ModelBalancer struct {
	easResolver *resolver.EasResolver
	authToken   string

	mu         sync.Mutex
	modelLBMap map[string]*SingleModelLoadBalancer

	rwMu           sync.RWMutex
	modelEndpoints map[string]WEndpointsSlice

	modelClient *http.Client

	serverlessMode bool
}

func NewModelBalancer(c *options.Config) *ModelBalancer {
	mb := &ModelBalancer{
		easResolver:    resolver.NewEasResolver(c),
		authToken:      c.ServiceToken,
		modelLBMap:     make(map[string]*SingleModelLoadBalancer, 16),
		modelClient:    utils.NewHttpClient(),
		serverlessMode: c.ServerlessMode,
	}

	go mb.SyncWithModels()

	return mb
}

func (mb *ModelBalancer) GetNextTokens(req *structs.Request) (*structs.NextTokens, error) {
	model := req.Model
	if !mb.serverlessMode {
		model = ""
	}
	c := mb.getOrCreateSingleModelLoadBalancer(model)
	return c.GetNextTokens(req)
}

func (mb *ModelBalancer) getOrCreateSingleModelLoadBalancer(model string) *SingleModelLoadBalancer {
	mb.mu.Lock()
	defer mb.mu.Unlock()

	c, ok := mb.modelLBMap[model]
	if ok {
		return c
	} else {
		c = NewSingleModelLoadBalancer(NewModelResolver(model))
		mb.modelLBMap[model] = c
		return c
	}
}

func (mb *ModelBalancer) GetModelEndpoints() map[string]WEndpointsSlice {
	mb.rwMu.RLock()
	modelEndpoints := mb.modelEndpoints
	mb.rwMu.RUnlock()

	tStat := time.Now()
	newModelEndpoints := deepCopyMapModelEndpoints(modelEndpoints)
	dur := time.Since(tStat)
	if dur > time.Second {
		klog.Warningf("deep copy ModelEndpoints too long: %v", dur)
	}
	return newModelEndpoints
}

func (mb *ModelBalancer) ReleaseToken(*structs.Request, *structs.Token) {}

func (mb *ModelBalancer) ExcludeService(serviceName string) {
	mb.easResolver.AddExcludeService(serviceName)
}

func (mb *ModelBalancer) updateModelEndpoints() map[string]WEndpointsSlice {
	var modelEndpoints map[string]WEndpointsSlice
	endpoints := mb.easResolver.GetWeightEndpoints()
	if mb.serverlessMode {
		if mb.modelClient == nil {
			mb.modelClient = utils.NewHttpClient()
		}
		modelEndpoints = getModelsByEndpoints(mb.modelClient, endpoints, mb.authToken)
	} else {
		modelEndpoints = make(map[string]WEndpointsSlice)
		modelEndpoints[""] = endpoints
	}
	mb.rwMu.Lock()
	mb.modelEndpoints = modelEndpoints
	mb.rwMu.Unlock()
	return modelEndpoints
}

func (mb *ModelBalancer) SyncWithModels() {
	for {
		modelEndpoints := mb.updateModelEndpoints()
		for model, wEndpoints := range modelEndpoints {
			c := mb.getOrCreateSingleModelLoadBalancer(model)
			c.UpdateWeightEndpoints(wEndpoints)
		}

		time.Sleep(kSyncModelDuration * time.Second)
	}
}
