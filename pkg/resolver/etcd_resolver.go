package resolver

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"

	"go.etcd.io/etcd/client/v3"

	"llumnix/pkg/consts"
	"llumnix/pkg/etcd"
	"llumnix/pkg/types"
)

// etcdResolver implements LLMResolver using etcd Watch API for immediate updates.
// Unlike redisResolver which polls at fixed intervals, etcdResolver uses etcd's
// Watch mechanism to receive immediate notifications when instances change.
type etcdResolver struct {
	inferType consts.InferType

	mu                sync.RWMutex
	instances         types.LLMInstanceSlice
	podDiscoveryInfos map[string]*PodDiscoveryInfo

	watcher *Watcher

	etcdClient etcd.EtcdClient
	ctx        context.Context
	cancelFunc context.CancelFunc

	// etcd specific configuration
	leaseTTL           int // TTL in seconds for lease-based expiration
	refreshIntervalSec int // Periodic full-refresh interval in seconds (safety net for missed Watch events)
}

// newEtcdResolver creates a new etcd-based resolver using Watch for immediate updates.
func newEtcdResolver(
	inferType consts.InferType,
	endpoints []string,
	username string,
	password string,
	dialTimeout float64,
	leaseTTL int,
	refreshIntervalSec int) (*etcdResolver, error) {

	ctx, cancel := context.WithCancel(context.Background())

	// Create etcd client
	dialDuration := time.Duration(dialTimeout) * time.Second
	etcdClient, err := etcd.NewClient(endpoints, username, password, dialDuration)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	r := &etcdResolver{
		inferType:          inferType,
		etcdClient:         etcdClient,
		ctx:                ctx,
		cancelFunc:         cancel,
		watcher:            NewWatcher(),
		leaseTTL:           leaseTTL,
		refreshIntervalSec: refreshIntervalSec,
		podDiscoveryInfos:  make(map[string]*PodDiscoveryInfo),
	}

	// Do initial load of all instances
	r.refresh()

	// Start watching for changes
	go r.watchLoop()

	return r, nil
}

func (r *etcdResolver) GetLLMInstances() (types.LLMInstanceSlice, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	instances := make(types.LLMInstanceSlice, 0, len(r.instances))
	instances = append(instances, r.instances...)
	return instances, nil
}

func (r *etcdResolver) Watch(ctx context.Context) (<-chan types.LLMInstanceSlice, <-chan types.LLMInstanceSlice, error) {
	return r.watcher.Watch(ctx, r.GetLLMInstances)
}

// watchLoop watches for etcd changes and updates the resolver state.
// It combines two mechanisms:
//   - Watch: delivers real-time PUT/DELETE events for immediate updates.
//   - Periodic refresh: reconciles state every leaseTTL seconds to catch
//     events missed during Watch disconnections (e.g. lease-expiry DELETEs
//     that occurred while the gRPC stream was broken).
func (r *etcdResolver) watchLoop() {
	defer func() {
		if err := recover(); err != nil {
			klog.Errorf("Etcd Resolver panic: %v\n%s", err, string(debug.Stack()))
		}
	}()

	// Watch all keys with the discovery prefix
	watchCh := r.etcdClient.Watch(r.ctx, EtcdDiscoveryPrefix)

	// Periodic refresh as a safety net: the etcd Go client auto-reconnects
	// its Watch stream after a cluster disruption, but the reconnected stream
	// starts from the latest revision and does NOT replay DELETE events that
	// occurred while the stream was down (e.g. lease expiry).  A periodic
	// full refresh ensures the in-memory state converges with etcd within
	// one leaseTTL cycle.
	refreshInterval := time.Duration(r.refreshIntervalSec) * time.Second
	refreshTicker := time.NewTicker(refreshInterval)
	defer refreshTicker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			klog.Infof("etcd resolver watch loop stopped")
			return
		case <-refreshTicker.C:
			r.refresh()
		case resp, ok := <-watchCh:
			if !ok {
				klog.Warningf("etcd watch channel closed, reconnecting")
				time.Sleep(time.Second)
				r.refresh()
				watchCh = r.etcdClient.Watch(r.ctx, EtcdDiscoveryPrefix)
				continue
			}
			if resp.Canceled {
				klog.Warningf("etcd watch canceled: %v", resp.Err())
				time.Sleep(time.Second)
				r.refresh()
				watchCh = r.etcdClient.Watch(r.ctx, EtcdDiscoveryPrefix)
				continue
			}
			r.handleWatchResponse(&resp)
		}
	}
}

// handleWatchResponse processes a single Watch response from etcd.
// It applies incremental changes on top of the existing podDiscoveryInfos
// rather than rebuilding from scratch, so pods not mentioned in this batch
// of events are preserved.
func (r *etcdResolver) handleWatchResponse(resp *clientv3.WatchResponse) {
	if len(resp.Events) == 0 {
		return
	}

	var added, removed types.LLMInstanceSlice

	r.mu.Lock()

	// Start from the current state and apply incremental changes
	for _, ev := range resp.Events {
		key := string(ev.Kv.Key)

		switch ev.Type {
		case clientv3.EventTypePut:
			podInfo := &PodDiscoveryInfo{}
			if err := proto.Unmarshal(ev.Kv.Value, podInfo); err != nil {
				klog.Errorf("Error unmarshaling pod info for key %s: %v", key, err)
				continue
			}

			if podInfo.TimestampMs < time.Now().UnixMilli()-int64(r.leaseTTL*1000) {
				klog.Warningf("Pod info for %s is expired", podInfo.PodName)
				continue
			}

			r.podDiscoveryInfos[podInfo.PodName] = podInfo

		case clientv3.EventTypeDelete:
			podName := strings.TrimPrefix(key, EtcdDiscoveryPrefix)
			klog.Infof("Pod %s deleted from etcd", podName)
			delete(r.podDiscoveryInfos, podName)
		}
	}

	// Rebuild the full instance list from the updated podDiscoveryInfos
	newInstances := r.buildInstanceList(r.podDiscoveryInfos)

	added, removed = DiffSets(r.instances, newInstances, func(w types.LLMInstance) string {
		return w.Id()
	})

	r.instances = newInstances

	r.mu.Unlock()

	if len(added) > 0 || len(removed) > 0 {
		klog.Infof("etcd resolver watch (inferType=%s): Added: %d, Removed: %d, Total: %d",
			r.inferType, len(added), len(removed), len(newInstances))
		r.watcher.notifyObservers(added, removed)
	}
}

// buildInstanceList converts a map of PodDiscoveryInfo into a flat instance
// slice, filtering by the resolver's inferType. This is shared by both the
// initial refresh and incremental Watch handler.
func (r *etcdResolver) buildInstanceList(pods map[string]*PodDiscoveryInfo) types.LLMInstanceSlice {
	var instances types.LLMInstanceSlice
	for _, podInfo := range pods {
		for _, instance := range podInfo.Instances {
			if consts.InferType(instance.InstanceType) != r.inferType && r.inferType != consts.InferTypeAll {
				continue
			}
			instances = append(instances, types.LLMInstance{
				Version:   instance.Version,
				ID:        fmt.Sprintf("%s_dp%d", podInfo.PodName, instance.DpRank),
				Model:     instance.Model,
				InferType: consts.InferType(instance.InstanceType),
				Endpoint: types.Endpoint{
					Host: instance.EntrypointIp,
					Port: int(instance.EntrypointPort),
				},
				AuxIp:   instance.KvTransferIp,
				AuxPort: int(instance.KvTransferPort),
				DPRank:  int(instance.DpRank),
				DPSize:  int(instance.DpSize),
			})
		}
	}
	return instances
}

// refresh does a full load of all instances from etcd and replaces the
// resolver's state. Called on startup and after Watch reconnection.
// It computes the diff against the previous state and notifies observers
// so that upstream consumers (e.g. Gateway load balancer) learn about
// instances that were added or removed while the Watch was disconnected.
func (r *etcdResolver) refresh() {
	podData, err := r.etcdClient.GetWithPrefix(r.ctx, EtcdDiscoveryPrefix)
	if err != nil {
		klog.Errorf("Error getting instances from etcd: %v", err)
		return
	}

	newPodDiscoveryInfos := make(map[string]*PodDiscoveryInfo)
	for key, data := range podData {
		if data == nil {
			klog.Warningf("Empty data for key: %s", key)
			continue
		}

		podInfo := &PodDiscoveryInfo{}
		if err := proto.Unmarshal(data, podInfo); err != nil {
			klog.Errorf("Error unmarshaling pod info for key %s: %v", key, err)
			continue
		}

		if podInfo.TimestampMs < time.Now().UnixMilli()-int64(r.leaseTTL*1000) {
			klog.Warningf("Pod info for %s is expired", podInfo.PodName)
			continue
		}

		if len(podInfo.Instances) > 0 {
			newPodDiscoveryInfos[podInfo.PodName] = podInfo
		}
	}

	newInstances := r.buildInstanceList(newPodDiscoveryInfos)

	r.mu.Lock()
	added, removed := DiffSets(r.instances, newInstances, func(w types.LLMInstance) string {
		return w.Id()
	})
	r.instances = newInstances
	r.podDiscoveryInfos = newPodDiscoveryInfos
	r.mu.Unlock()

	if len(added) > 0 || len(removed) > 0 {
		klog.Infof("etcd resolver refresh (inferType=%s): Added: %d, Removed: %d, Total: %d",
			r.inferType, len(added), len(removed), len(newInstances))
		r.watcher.notifyObservers(added, removed)
	} else {
		klog.Infof("etcd resolver refresh: loaded %d instances (no changes)", len(newInstances))
	}
}

// EtcdResolverBuilder implements the LLMResolverBuilder interface for etcd.
type EtcdResolverBuilder struct{}

// Schema returns the schema identifier for this builder.
func (b *EtcdResolverBuilder) Schema() string {
	return "etcd"
}

// Build creates a new etcdResolver instance from the provided arguments.
func (b *EtcdResolverBuilder) Build(uri string, args BuildArgs) (LLMResolver, error) {
	if uri == "" {
		return nil, fmt.Errorf("missing or invalid 'uri' argument")
	}

	// Parse URI: etcd://host1:port1,host2:port2,host3:port3
	if !strings.HasPrefix(uri, EtcdUriPrefix) {
		return nil, fmt.Errorf("invalid URI format: must start with '%s'", EtcdUriPrefix)
	}
	uri = strings.TrimPrefix(uri, EtcdUriPrefix)

	// Parse endpoints (comma-separated)
	endpoints := strings.Split(uri, ",")
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no etcd endpoints provided")
	}

	// Validate and normalize endpoints
	for i, ep := range endpoints {
		ep = strings.TrimSpace(ep)
		if !strings.HasPrefix(ep, "http://") && !strings.HasPrefix(ep, "https://") {
			ep = "http://" + ep
		}
		endpoints[i] = ep
	}

	instanceTypeStr, ok := args["instance_type"].(string)
	if !ok || instanceTypeStr == "" {
		return nil, fmt.Errorf("missing instance_type or invalid instance_type build args: %v", instanceTypeStr)
	}
	inferType := consts.InferType(instanceTypeStr)

	username, _ := args["etcd_username"].(string)
	password, _ := args["etcd_password"].(string)

	dialTimeout, ok := args["etcd_dial_timeout"].(float64)
	if !ok {
		dialTimeout = 5.0 // default 5 seconds
	}

	leaseTTL, ok := args["etcd_lease_ttl"].(int)
	if !ok {
		leaseTTL = 60 // default 60 seconds
	}

	refreshIntervalSec, ok := args["etcd_refresh_interval_sec"].(int)
	if !ok {
		refreshIntervalSec = 3600 // default 60 minutes; this is a rare-event safety net
	}

	return newEtcdResolver(inferType, endpoints, username, password, dialTimeout, leaseTTL, refreshIntervalSec)
}

const (
	// EtcdUriPrefix is the URI prefix for etcd resolver
	EtcdUriPrefix = "etcd://"
	// EtcdDiscoveryPrefix is the etcd key prefix for service discovery.
	// Uses path-style separators natural to etcd's key hierarchy.
	EtcdDiscoveryPrefix = "llumnix/discovery/"
)

func init() {
	RegisterLLM(&EtcdResolverBuilder{})
}
