package kvs

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cespare/xxhash/v2"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
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

	// BatchQueryCacheHitKVSWorkers queries the cache locality for multiple prefix hashes concurrently and returns a mapping
	// from prefix hash to the list of kvs workers (identified by string) that have the corresponding cache.
	// Parameters:
	//   - prefixHashes: A slice of prefix hash strings.
	//
	// Returns:
	//   - A map where the key is the prefix hash and the value is a slice of string representing the kvs workers.
	BatchQueryCacheHitKVSWorkers(prefixHashes []string) map[string][]string

	// ConvertKVSWorkerToIp extracts the ip from a KVS worker string.
	// The KVS worker string is expected to be in the format "ip:port" or just "ip".
	// If the string contains a colon, it splits the string and returns the first part as the ip.
	// If there is no colon, it returns the entire string as the ip.
	//
	// Parameters:
	//   - kvsWorker: A string representing the KVS worker identifier, potentially including port information.
	//
	// Returns:
	//   - A string representing the extracted ip.
	ConvertKVSWorkerToIp(kvsWorker string) string

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

	// IsKVSMetaServiceDown checks if the KVS meta service is currently marked as down.
	// It returns true if the service is down and the downtime duration has not yet elapsed,
	// otherwise it returns false.
	//
	// Parameters:
	//   - None
	//
	// Returns:
	//   - bool: True if the KVS meta service is down, false otherwise.
	IsKVSMetaServiceDown() bool
}

var (
	client *KVSClient
	mu     sync.RWMutex // to protect client
)

type KVSClient struct {
	kvsMetaServiceClient       KVSMetaServiceClientInterface
	chunkSize                  int
	saveUnfullChunk            bool
	irisMetaPrefix             string
	vLLMBlockPrefix            string
	retryTimes                 int
	retryInterval              time.Duration
	kvsMetaServiceDownDuration time.Duration
	kvsMetaServiceStatus       atomic.Value
	kvsMetaServiceLastDownTime atomic.Value
}

type kvsMetaServiceStatus int

const (
	kvsMetaServiceHealthy kvsMetaServiceStatus = iota
	kvsMetaServiceDown
)

func CreateOrGetClient(
	configPath string,
	chunkSize int,
	saveUnfullChunk bool,
	irisMetaPrefix string,
	vLLMBlockPrefix string,
	kvsRetryTimes int,
	kvsRetryIntervalMs int,
	kvsMetaServiceDownDurationS int,
	kvsMetaServiceRedisClusterHosts string,
	kvsMetaServiceRedisClusterPassword string) (*KVSClient, error) {
	mu.Lock()
	defer mu.Unlock()

	if client != nil {
		return client, nil
	}

	kvsMetaServiceClient, err := newKVSMetaServiceClient(
		configPath, kvsMetaServiceRedisClusterHosts, kvsMetaServiceRedisClusterPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to create kvs meta service client: %w", err)
	}
	client, err = newKVSClient(
		kvsMetaServiceClient, chunkSize, saveUnfullChunk, irisMetaPrefix, vLLMBlockPrefix,
		kvsRetryTimes, kvsRetryIntervalMs, kvsMetaServiceDownDurationS)

	return client, err
}

func newKVSClient(
	KVSMetaServiceClient KVSMetaServiceClientInterface,
	chunkSize int,
	saveUnfullChunk bool,
	irisMetaPrefix string,
	vLLMBlockPrefix string,
	retryTimes int,
	retryIntervalMs int,
	kvsMetaServiceDownDurationS int) (*KVSClient, error) {
	if KVSMetaServiceClient == nil {
		return nil, fmt.Errorf("kvs meta service client cannot be nil")
	}

	client := &KVSClient{
		kvsMetaServiceClient: KVSMetaServiceClient,
		// NOTE(sunbiao.sun): Make sure that the inference engine share the same config.
		chunkSize:                  chunkSize,
		saveUnfullChunk:            saveUnfullChunk,
		irisMetaPrefix:             irisMetaPrefix,
		vLLMBlockPrefix:            vLLMBlockPrefix,
		retryTimes:                 retryTimes,
		retryInterval:              time.Duration(retryIntervalMs) * time.Millisecond,
		kvsMetaServiceDownDuration: time.Duration(kvsMetaServiceDownDurationS) * time.Second,
		kvsMetaServiceStatus:       atomic.Value{},
		kvsMetaServiceLastDownTime: atomic.Value{},
	}
	client.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
	client.kvsMetaServiceLastDownTime.Store(time.Now())

	klog.Info("KVSClient initialized")

	return client, nil
}

func (c *KVSClient) PrefixHash(tokens []int64) []string {
	if len(tokens) == 0 {
		return nil
	}

	prefixHashes, err := c.hashV6d(tokens)
	if err != nil {
		klog.Errorf("Hash tokens failed: %v", err)
		return nil
	}

	return prefixHashes
}

func (c *KVSClient) BatchQueryCacheHitKVSWorkers(prefixHashes []string) map[string][]string {
	if len(prefixHashes) == 0 {
		return nil
	}

	if c.IsKVSMetaServiceDown() {
		klog.V(4).Infof("KVS meta service is down, returning empty results for %d prefix hashes", len(prefixHashes))
		return nil
	}

	var lastErr error
	for i := 0; i < c.retryTimes; i++ {
		hitKVSWorkers, err := c.batchQueryPrefixHashHitKVSWorkers(prefixHashes)
		if err == nil {
			return hitKVSWorkers
		}

		lastErr = err
		if errors.Is(err, context.DeadlineExceeded) {
			klog.Warningf("Batch query hit kvs workers timeout, retry %d/%d: %v", i+1, c.retryTimes, err)
			time.Sleep(c.retryInterval)
			continue
		}

		break // return empty results when encountering non-timeout error
	}

	// If all retries failed, mark the KVS meta service as down
	if errors.Is(lastErr, context.DeadlineExceeded) {
		c.markKVSMetaServiceDown()
	}

	klog.Errorf("Batch query hit kvs workers for %d prefix hash failed after %d retries: %v",
		len(prefixHashes), c.retryTimes, lastErr)
	return nil
}

func (c *KVSClient) IsKVSMetaServiceDown() bool {
	if c.kvsMetaServiceStatus.Load().(kvsMetaServiceStatus) == kvsMetaServiceDown {
		if time.Since(c.kvsMetaServiceLastDownTime.Load().(time.Time)) > c.kvsMetaServiceDownDuration {
			c.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
			return false
		}
		return true
	}
	return false
}

func (c *KVSClient) markKVSMetaServiceDown() {
	c.kvsMetaServiceStatus.Store(kvsMetaServiceDown)
	c.kvsMetaServiceLastDownTime.Store(time.Now())
	klog.Warningf("KVS Meta service marked as down, will recover after %v", c.kvsMetaServiceDownDuration)
}

func (c *KVSClient) ConvertKVSWorkerToIp(kvsWorker string) string {
	return strings.Split(kvsWorker, ":")[0]
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

// batchQueryPrefixHashHitKVSWorkers retrieves the list of kvs workers (identified by string) associated with multiple hash keys from the kvs meta service.
// Parameters:
//   - hashKeys: A slice of strings representing the hash keys to query.
//
// Returns:
//   - map[string][]string: A map where the key is the hash key and the value is a slice of strings representing the kvs workers associated with that hash key.
//   - error: An error if the query fails, otherwise nil.
func (c *KVSClient) batchQueryPrefixHashHitKVSWorkers(hashKeys []string) (map[string][]string, error) {
	results, err := c.kvsMetaServiceClient.safeBatchZReadRange(hashKeys, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to query cache hit kvs workers for %d hash key: %w", len(hashKeys), err)
	}

	hitKVSWorkers := make(map[string][]string, len(hashKeys))
	for hashKey, result := range results {
		hitKVSWorkers[hashKey] = make([]string, len(result))
		for i, item := range result {
			hitKVSWorkers[hashKey][i] = item.Member
		}
	}

	return hitKVSWorkers, nil
}

// queryCacheHitKVSWorkers queries the cache locality for each prefix hash and returns a mapping
// from prefix hash to the list of kvs workers (identified by string) that have the corresponding cache.
// Parameters:
//   - prefixHashes: A slice of prefix hash strings.
//
// Returns:
//   - A map where the key is the prefix hash and the value is a slice of string representing kvs workers.
func (c *KVSClient) queryCacheHitKVSWorkers(prefixHashes []string) map[string][]string {
	if len(prefixHashes) == 0 {
		return nil
	}

	prefixHashHitKVSWorkers := make(map[string][]string)
	for _, prefixHash := range prefixHashes {
		if kvsWorkers, err := c.queryPrefixHashHitKVSWorkers(prefixHash); err != nil {
			klog.Errorf("Query hit kvs workers for prefix hash %s failed: %v", prefixHash, err)
			prefixHashHitKVSWorkers[prefixHash] = nil
		} else {
			prefixHashHitKVSWorkers[prefixHash] = kvsWorkers
		}
	}
	return prefixHashHitKVSWorkers
}

// NOTE(sunbiao.sun): All functions below return empty results instead of nil when encountering invalid inputs or errors.
// This design ensures safe chaining of subsequent function calls by preventing nil pointer exceptions.

// queryPrefixHashHitKVSWorkers retrieves the list of kvs workers (identified by string) associated with a given hash key from the kvs meta service.
// Parameters:
//   - hashKey: A string representing the hash key to query.
//
// Returns:
//   - []string: A slice of strings representing the kvs workers associated with the hash key.
//   - error: An error if the query fails, otherwise nil.
func (c *KVSClient) queryPrefixHashHitKVSWorkers(hashKey string) ([]string, error) {
	result, err := c.kvsMetaServiceClient.zReadRange(hashKey, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to query cache hit kvs workers for hash key %s: %w", hashKey, err)
	}

	hitKVSWorkers := make([]string, len(result))
	for i, item := range result {
		hitKVSWorkers[i] = item.Member
	}

	return hitKVSWorkers, nil
}

// insertCacheHitKVSWorkers inserts a cache hit kvs worker into the kvs meta service with the given hash key and kvs worker.
// Parameters:
//   - hashKey: A string representing the hash key for the cache entry.
//   - kvsWorker: A string representing the kvs worker to be inserted.
//
// Returns:
//   - error: An error if the insertion fails, otherwise nil.
func (c *KVSClient) insertCacheHitKVSWorkers(hashKey string, kvsWorker string) error {
	if err := c.kvsMetaServiceClient.zWrite(hashKey, kvsWorker, 0, ""); err != nil {
		return fmt.Errorf("failed to insert cache hit kvs worker for hash key %s: %v", hashKey, err)
	}
	return nil
}

// hashV6d computes the hash values for the given tokens by dividing them into blocks of size chunkSize.
// Each block is hashed using XXHash64, and the resulting hash is converted to a string.
// If the tokens slice is empty or chunkSize is non-positive, an empty slice is returned.
//
// Parameters:
//   - tokens: A slice of int64 representing the input tokens to be hashed.
//
// Returns:
//   - []string: A slice of strings where each string represents the hash of a block of tokens.
//   - error: An error if there was a problem writing to the hasher, otherwise nil.
//
// NOTE(sunbiao.sun): v6d lack of e2e hash function, this function is deducted manually based on
// GetBlockHashesFromTokens_PrefixHash function in MetaService, so the correctness should be tested in e2e test.
func (c *KVSClient) hashV6d(tokens []int64) ([]string, error) {
	if len(tokens) == 0 || c.chunkSize <= 0 {
		return nil, fmt.Errorf("invalid hash input")
	}

	numCompleteBlocks := len(tokens) / c.chunkSize
	totalBlocks := numCompleteBlocks
	remainder := len(tokens) % c.chunkSize
	if remainder > 0 {
		totalBlocks++
	}

	// Core prefix hash computation using XXHash64 streaming
	blockHashes := make([]string, 0, totalBlocks)
	hasher := xxhash.NewWithSeed(0)
	defer hasher.Reset()

	// Process complete blocks
	for i := 0; i < numCompleteBlocks; i++ {
		if err := binary.Write(hasher, binary.LittleEndian, tokens[i*c.chunkSize:(i+1)*c.chunkSize]); err != nil {
			return nil, fmt.Errorf("failed to write complete block %d: %w", i, err)
		}
		blockHashes = append(blockHashes, c.buildBlockName(hasher.Sum64()))
	}

	// Process last incomplete block if it exists
	if c.saveUnfullChunk && remainder > 0 {
		if err := binary.Write(hasher, binary.LittleEndian, tokens[numCompleteBlocks*c.chunkSize:]); err != nil {
			return nil, fmt.Errorf("failed to write remainder block: %w", err)
		}

		// Pad with zeros to match original behavior
		padding := make([]int64, c.chunkSize-remainder)
		if err := binary.Write(hasher, binary.LittleEndian, padding); err != nil {
			return nil, fmt.Errorf("failed to write padding: %w", err)
		}

		blockHashes = append(blockHashes, c.buildBlockName(hasher.Sum64()))
	}

	return blockHashes, nil
}

func (c *KVSClient) getBlockNamePrefix() string {
	return c.irisMetaPrefix + c.vLLMBlockPrefix
}

func (c *KVSClient) buildBlockName(hash uint64) string {
	return c.getBlockNamePrefix() + strconv.FormatUint(hash, 10)
}
