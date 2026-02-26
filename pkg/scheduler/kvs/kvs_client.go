package kvs

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/scheduler/kvs/mooncake"
	"llumnix/pkg/scheduler/kvs/v6d"
)

type KVSClientInterface interface {
	// BatchQueryCacheHitKVSInstances queries the cache locality for multiple prefix hashes concurrently and returns a mapping
	// from prefix hash to the list of kvs instances (identified by string) that have the corresponding cache.
	// Parameters:
	//   - prefixHashes: A slice of prefix hash strings.
	//
	// Returns:
	//   - A map where the key is the prefix hash and the value is a slice of string representing the kvs instances.
	BatchQueryCacheHitKVSInstances(prefixHashes []string) map[string][]string

	// IsKVSMetadataServiceDown checks if the KVS metadata service is currently marked as down.
	// It returns true if the service is down and the downtime duration has not yet elapsed,
	// otherwise it returns false.
	//
	// Parameters:
	//   - None
	//
	// Returns:
	//   - bool: True if the KVS metadata service is down, false otherwise.
	IsKVSMetadataServiceDown() bool
}

var (
	client *KVSClient
	mu     sync.RWMutex // to protect client
)

type KVSClient struct {
	kvsMetadataServiceClient       KVSMetadataServiceClientInterface
	retryTimes                     int
	retryInterval                  time.Duration
	kvsMetadataServiceDownDuration time.Duration
	kvsMetadataServiceStatus       atomic.Value
	kvsMetadataServiceLastDownTime atomic.Value
}

type kvsMetadataServiceStatus int

const (
	kvsMetadataServiceHealthy kvsMetadataServiceStatus = iota
	kvsMetadataServiceDown
)

func CreateOrGetClient(
	kvsBackend string,
	configPath string,
	kvsRetryTimes int,
	kvsRetryIntervalMs int,
	kvsMetadataServiceDownDurationS int,
	kvsMetadataServiceRedisClusterHosts string,
	kvsMetadataServiceRedisClusterPassword string,
	kvsMetadataServiceHttpServerHost string,
	kvsMetadataServiceHttpServerPort string) (*KVSClient, error) {
	mu.Lock()
	defer mu.Unlock()

	if client != nil {
		return client, nil
	}
	var kvsMetadataServiceClient KVSMetadataServiceClientInterface
	var err error
	if kvsBackend == consts.KvsBackendV6d {
		kvsMetadataServiceClient, err = v6d.NewMetadataServiceClient(
			configPath, kvsMetadataServiceRedisClusterHosts, kvsMetadataServiceRedisClusterPassword)
	} else if kvsBackend == consts.KvsBackendMooncake {
		port, err2 := strconv.Atoi(kvsMetadataServiceHttpServerPort)
		if err2 != nil {
			return nil, fmt.Errorf("invalid kvs metadata service http server port: %w", err2)
		}
		kvsMetadataServiceClient, err = mooncake.NewMetadataServiceClient(
			kvsMetadataServiceHttpServerHost, port)
	} else {
		return nil, fmt.Errorf("invalid kvs backend: %s, only support %s and %s",
			kvsBackend, consts.KvsBackendV6d, consts.KvsBackendMooncake)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create kvs metadata service client: %w", err)
	}

	client, err = newKVSClient(
		kvsMetadataServiceClient,
		kvsRetryTimes, kvsRetryIntervalMs, kvsMetadataServiceDownDurationS)

	return client, err
}

func newKVSClient(
	KVSMetadataServiceClient KVSMetadataServiceClientInterface,
	retryTimes int,
	retryIntervalMs int,
	kvsMetadataServiceDownDurationS int) (*KVSClient, error) {
	if KVSMetadataServiceClient == nil {
		return nil, fmt.Errorf("kvs metadata service client cannot be nil")
	}

	client := &KVSClient{
		kvsMetadataServiceClient:       KVSMetadataServiceClient,
		retryTimes:                     retryTimes,
		retryInterval:                  time.Duration(retryIntervalMs) * time.Millisecond,
		kvsMetadataServiceDownDuration: time.Duration(kvsMetadataServiceDownDurationS) * time.Second,
		kvsMetadataServiceStatus:       atomic.Value{},
		kvsMetadataServiceLastDownTime: atomic.Value{},
	}
	client.kvsMetadataServiceStatus.Store(kvsMetadataServiceHealthy)
	client.kvsMetadataServiceLastDownTime.Store(time.Now())

	klog.Info("KVSClient initialized")

	return client, nil
}

func (c *KVSClient) BatchQueryCacheHitKVSInstances(prefixHashes []string) map[string][]string {
	if len(prefixHashes) == 0 {
		return nil
	}

	if c.IsKVSMetadataServiceDown() {
		klog.V(4).Infof("KVS metadata service is down, returning empty results for %d prefix hashes", len(prefixHashes))
		return nil
	}

	var lastErr error
	for i := 0; i < c.retryTimes; i++ {
		hitKVSInstances, err := c.kvsMetadataServiceClient.BatchQueryPrefixHashHitKVSInstances(prefixHashes)
		if err == nil {
			return hitKVSInstances
		}

		lastErr = err
		klog.Warningf("Batch query hit kvs instances failed, retry %d/%d: %v", i+1, c.retryTimes, err)
		time.Sleep(c.retryInterval)
		continue
	}

	c.markKVSMetadataServiceDown()

	klog.Errorf("Batch query hit kvs instances for %d prefix hash failed after %d retries: %v",
		len(prefixHashes), c.retryTimes, lastErr)
	return nil
}

func (c *KVSClient) IsKVSMetadataServiceDown() bool {
	if c.kvsMetadataServiceStatus.Load().(kvsMetadataServiceStatus) == kvsMetadataServiceDown {
		if time.Since(c.kvsMetadataServiceLastDownTime.Load().(time.Time)) > c.kvsMetadataServiceDownDuration {
			c.kvsMetadataServiceStatus.Store(kvsMetadataServiceHealthy)
			return false
		}
		return true
	}
	return false
}

func (c *KVSClient) markKVSMetadataServiceDown() {
	c.kvsMetadataServiceStatus.Store(kvsMetadataServiceDown)
	c.kvsMetadataServiceLastDownTime.Store(time.Now())
	klog.Warningf("KVS Metadata service marked as down, will recover after %v", c.kvsMetadataServiceDownDuration)
}


