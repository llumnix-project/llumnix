package resolver

import (
	"context"
	"fmt"
	"llumnix/pkg/llm-gateway/cms"
	"llumnix/pkg/llm-gateway/types"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"

	"k8s.io/klog/v2"
)

type redisResolver struct {
	role string

	mu                sync.RWMutex
	instances         types.LLMInstanceSlice
	podDiscoveryInfos map[string]*PodDiscoveryInfo

	watcher *Watcher

	redisClient       *cms.RedisClient
	refreshIntervalMs int
	statusTTLMs       int
}

func newRedisResolver(
	role string,
	host string,
	port string,
	username string,
	password string,
	socketTimeout float64,
	retryTimes int,
	refreshIntervalMs int,
	StatusTTLMs int) (*redisResolver, error) {

	redisClient, error := cms.NewRedisClient(host, port, username, password, socketTimeout, retryTimes)
	if error != nil {
		return nil, error
	}

	r := &redisResolver{
		role:              role,
		redisClient:       redisClient,
		watcher:           NewWatcher(),
		refreshIntervalMs: refreshIntervalMs,
		statusTTLMs:       StatusTTLMs,
	}

	go r.refreshLoop()

	return r, nil
}

func (r *redisResolver) GetLLMInstances() (types.LLMInstanceSlice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	instances := make(types.LLMInstanceSlice, 0, len(r.instances))
	for idx := range r.instances {
		instances = append(instances, r.instances[idx])
	}
	return instances, nil
}

func (r *redisResolver) Watch(ctx context.Context) (<-chan types.LLMInstanceSlice, <-chan types.LLMInstanceSlice, error) {
	return r.watcher.Watch(ctx, r.GetLLMInstances)
}

func (r *redisResolver) refreshLoop() {
	defer func() {
		if err := recover(); err != nil {
			klog.Errorf("Redis Resolver panic: %v\n%s", err, string(debug.Stack()))
		}
	}()

	ticker := time.NewTicker(time.Millisecond * time.Duration(r.refreshIntervalMs))
	defer ticker.Stop()

	for range ticker.C {
		r.refresh()
	}
}

func (r *redisResolver) refresh() {
	PodInRedis, err := r.redisClient.GetKeysByPrefix(LlumnixDiscovery)
	if err != nil {
		klog.Fatalf("Error getting keys by prefix: %v", err)
		return
	}

	PodInfos, err := r.redisClient.MGetBytes(PodInRedis)
	if err != nil {
		klog.Fatalf("Error getting instance metadata: %v", err)
		return
	}

	newPodDiscoveryInfos := make(map[string]*PodDiscoveryInfo)
	newInstances := types.LLMInstanceSlice{}
	for idx, PodInfoBytes := range PodInfos {
		if PodInfoBytes == nil {
			klog.Warningf("Empty data for key: %s", PodInRedis[idx])
			continue
		}

		podInfo := &PodDiscoveryInfo{}
		if err := proto.Unmarshal(PodInfoBytes, podInfo); err != nil {
			klog.Errorf("Error unmarshaling pod info for key %s: %v", PodInRedis[idx], err)
			continue
		}

		if podInfo.TimestampMs < time.Now().UnixMilli()-int64(r.statusTTLMs) {
			klog.Warningf("Pod info for %s is expired", podInfo.PodName)
			r.redisClient.Remove(PodInRedis[idx])
			continue
		}

		var filteredInstances []*InstanceDiscoveryInfo
		for _, instance := range podInfo.Instances {
			if instance.Role != r.role && r.role != types.InferRoleAll.String() {
				// all dp ranks in a pod should be in the same role
				continue
			}

			newInstances = append(newInstances, types.LLMInstance{
				Version: instance.Version,
				ID:      fmt.Sprintf("%s_dp%d", podInfo.PodName, instance.DpRank),
				Model:   instance.Model,
				Role:    types.InferRole(instance.Role),
				Endpoint: types.Endpoint{
					Host: instance.EntrypointIp,
					Port: int(instance.EntrypointPort),
				},
				AuxPort: int(instance.KvTransferPort),
				DPRank:  int(instance.DpRank),
				DPSize:  int(instance.DpSize),
			})
			filteredInstances = append(filteredInstances, instance)
		}

		if len(filteredInstances) > 0 {
			podInfo.Instances = filteredInstances
			newPodDiscoveryInfos[podInfo.PodName] = podInfo
		}
	}

	r.mu.Lock()
	added, removed := DiffSets(r.instances, newInstances, func(w types.LLMInstance) string {
		return w.Id()
	})
	if len(added) > 0 || len(removed) > 0 {
		klog.V(4).Infof("redis resolover (role=%s): Added: %d, Removed: %d, Total: %d",
			r.role, len(added), len(removed), len(newInstances))
	}
	r.podDiscoveryInfos = newPodDiscoveryInfos
	r.instances = newInstances
	r.mu.Unlock()

	if len(added) > 0 || len(removed) > 0 {
		r.watcher.notifyObservers(added, removed)
	}
}

type RedisResolverBuilder struct{}

// Schema returns the schema identifier for this builder: "endpoints".
func (r *RedisResolverBuilder) Schema() string {
	return "redis"
}

// Build creates a new EndpointsResolver instance from the provided arguments.
// The args map must contain a "uri" key with a valid endpoints URI string.
func (r *RedisResolverBuilder) Build(uri string, args BuildArgs) (LLMResolver, error) {
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}
	// Ensure the URI has the correct prefix
	if !strings.HasPrefix(uri, RedisUriPrefix) {
		return nil, fmt.Errorf("invalid URI format: must start with '%s'", RedisUriPrefix)
	}
	uri = strings.TrimPrefix(uri, RedisUriPrefix)

	parts := strings.Split(uri, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid URI format: expected host:port, got %s", uri)
	}
	host := parts[0]
	port := parts[1]

	role, ok := args["role"].(string)
	if !ok || role == "" {
		return nil, fmt.Errorf("missing role or invalid role build args: %v", role)
	}

	username, ok := args["redis_username"].(string)
	if !ok {
		return nil, fmt.Errorf("missing username or invalid username build args: %v", username)
	}

	password, ok := args["redis_password"].(string)
	if !ok {
		return nil, fmt.Errorf("missing password or invalid password build args: %v", password)
	}

	socketTimeout, ok := args["redis_socketTimeout"].(float64)
	if !ok {
		return nil, fmt.Errorf("missing socketTimeout or invalid socketTimeout build args: %v", socketTimeout)
	}

	retryTimes, ok := args["redis_retryTimes"].(int)
	if !ok {
		return nil, fmt.Errorf("missing retryTimes or invalid retryTimes build args: %v", retryTimes)
	}

	refreshIntervalMs, ok := args["redis_discovery_refresh_interval_ms"].(int)
	if !ok {
		return nil, fmt.Errorf("missing refreshIntervalMs or invalid refreshIntervalMs build args: %v", refreshIntervalMs)
	}

	statusTTLMs, ok := args["redis_discovery_status_ttl"].(int)
	if !ok {
		return nil, fmt.Errorf("missing statusTTLMs or invalid statusTTLMs build args: %v", statusTTLMs)
	}

	return newRedisResolver(role, host, port, username, password, socketTimeout, retryTimes, refreshIntervalMs, statusTTLMs)
}

const (
	RedisUriPrefix   = "redis://"
	LlumnixDiscovery = "llumnix:discovery:"
)

func init() {
	RegisterLLM(&RedisResolverBuilder{})
}
