package kvs

import (
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
	"llumnix/pkg/scheduler/kvs/mooncake"
	"llumnix/pkg/scheduler/kvs/v6d"
)

type KVSClientInterface interface {
	// PrefixHash computes the hash values for the given tokens by dividing them into blocks of size chunkSize.
	// Each block is hashed using XXHash64, and the resulting hash is converted to a string.
	// If the tokens slice is empty or chunkSize is non-positive, an empty slice is returned.
	//
	// Parameters:
	//   - tokens: A slice of int64 representing the input tokens to be hashed.
	//
	// Returns:
	//   - []string: A slice of strings where each string represents the hash of a block of tokens.
	PrefixHash(tokens []int64) []string

	// BatchQueryCacheHitKVSInstances queries the cache locality for multiple prefix hashes concurrently and returns a mapping
	// from prefix hash to the list of kvs instances (identified by string) that have the corresponding cache.
	// Parameters:
	//   - prefixHashes: A slice of prefix hash strings.
	//
	// Returns:
	//   - A map where the key is the prefix hash and the value is a slice of string representing the kvs instances.
	BatchQueryCacheHitKVSInstances(prefixHashes []string) map[string][]string

	// CalcInstancesCacheHitLen calculates the total length of cache hits for each instance based on the provided prefix hashes
	// and their corresponding hit instances. It returns a map where the key is the instance ID and the value is the total
	// length of prefix hits for that instance.
	//
	// Parameters:
	//   - prefixHashes: A slice of prefix hash strings representing the order of chunks.
	//   - prefixHashHitInstances: A map where the key is the prefix hash and the value is a slice of instance IDs that have the corresponding cache.
	//
	// Returns:
	//   - A map where the key is the instance ID and the value is the total length of prefix hits for that instance.
	CalcInstancesCacheHitLen(prefixHashes []string, prefixHashHitInstances map[string]sets.String) map[string]int

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
	chunkSize                      int
	saveUnfullChunk                bool
	irisMetaPrefix                 string
	vLLMBlockPrefix                string
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
	chunkSize int,
	saveUnfullChunk bool,
	irisMetaPrefix string,
	vLLMBlockPrefix string,
	kvsRetryTimes int,
	kvsRetryIntervalMs int,
	kvsMetadataServiceDownDurationS int,
	kvsMetadataServiceRedisClusterHosts string,
	kvsMetadataServiceRedisClusterPassword string,
	kvsMetadataServiceHttpServerHost string,
	kvsMetadataServiceHttpServerPort string,
	kvsMetadataServiceHashAlgo string) (*KVSClient, error) {
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
			kvsMetadataServiceHttpServerHost, port, kvsMetadataServiceHashAlgo)
	} else {
		return nil, fmt.Errorf("invalid kvs backend: %s, only support %s and %s",
			kvsBackend, consts.KvsBackendV6d, consts.KvsBackendMooncake)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create kvs metadata service client: %w", err)
	}
	client, err = newKVSClient(
		kvsMetadataServiceClient, chunkSize, saveUnfullChunk, irisMetaPrefix, vLLMBlockPrefix,
		kvsRetryTimes, kvsRetryIntervalMs, kvsMetadataServiceDownDurationS)

	return client, err
}

func newKVSClient(
	KVSMetadataServiceClient KVSMetadataServiceClientInterface,
	chunkSize int,
	saveUnfullChunk bool,
	irisMetaPrefix string,
	vLLMBlockPrefix string,
	retryTimes int,
	retryIntervalMs int,
	kvsMetadataServiceDownDurationS int) (*KVSClient, error) {
	if KVSMetadataServiceClient == nil {
		return nil, fmt.Errorf("kvs metadata service client cannot be nil")
	}

	client := &KVSClient{
		kvsMetadataServiceClient: KVSMetadataServiceClient,
		// NOTE(sunbiao.sun): Make sure that the inference engine share the same config.
		chunkSize:                      chunkSize,
		saveUnfullChunk:                saveUnfullChunk,
		irisMetaPrefix:                 irisMetaPrefix,
		vLLMBlockPrefix:                vLLMBlockPrefix,
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

func (c *KVSClient) PrefixHash(tokens []int64) []string {
	if len(tokens) == 0 {
		return nil
	}

	prefixHashes, err := c.kvsMetadataServiceClient.HashTokens(tokens, c.chunkSize, c.saveUnfullChunk, c.irisMetaPrefix, c.vLLMBlockPrefix)
	klog.V(4).Infof("Hash tokens for %d tokens: %v", len(tokens), prefixHashes)
	if err != nil {
		klog.Errorf("Hash tokens failed: %v", err)
		return nil
	}

	return prefixHashes
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

func (c *KVSClient) CalcInstancesCacheHitLen(
	prefixHashes []string, prefixHashHitInstances map[string]sets.String) map[string]int {
	if len(prefixHashes) == 0 || prefixHashHitInstances == nil {
		return nil
	}

	instanceCacheHitLen := make(map[string]int)
	instanceLastHitIdx := make(map[string]int)
	instanceBroken := make(map[string]bool)

	for i, prefixHash := range prefixHashes {
		hitInstances := prefixHashHitInstances[prefixHash]
		if hitInstances == nil {
			continue
		}
		for instance := range hitInstances {
			if instanceBroken[instance] {
				continue
			}
			if lastSeenIdx, ok := instanceLastHitIdx[instance]; !ok {
				instanceLastHitIdx[instance] = i
				instanceCacheHitLen[instance] += c.chunkSize
			} else if lastSeenIdx == i-1 {
				instanceLastHitIdx[instance] = i
				instanceCacheHitLen[instance] += c.chunkSize
			} else {
				instanceBroken[instance] = true
			}
		}
	}

	return instanceCacheHitLen
}
