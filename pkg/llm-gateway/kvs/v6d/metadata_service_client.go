package v6d

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/redis/go-redis/v9"
	"k8s.io/klog/v2"

	redis2 "easgo/pkg/llm-gateway/redis"
)

// Only safeBatchZReadRange is actually used, other methods are only used in test.

type MetadataServiceClientInterface interface {
	// ZReadRange retrieves members from a Redis sorted set.
	//
	// Parameters:
	//   - zsetKey: the key of the sorted set to retrieve members from
	//   - topN: the number of top members to retrieve with the highest scores.
	//     If topN is less than or equal to 0, all members are returned.
	//
	// Returns:
	//   - []ZMember: a slice of ZMember containing the member names and their scores
	zReadRange(zsetKey string, topN int) ([]ZMember, error)

	// SafeBatchZReadRange concurrently reads the top N members from multiple sorted sets.
	// Parameters:
	//   - zsetKeys: a slice of strings representing the keys of the sorted sets to read from
	//   - topN: an integer indicating how many top members to retrieve from each sorted set
	//
	// Return:
	//   - map[string][]ZMember: a map where keys are the sorted set names and values are slices of ZMember
	//   - error: any error encountered during the operation
	safeBatchZReadRange(zsetKeys []string, topN int) (map[string][]ZMember, error)

	// batchZReadRange retrieves the top N members from multiple sorted sets in Redis.
	// If topN is positive, it fetches the top N members with the highest scores.
	// If topN is zero or negative, it fetches all members from the sorted sets.
	//
	// Parameters:
	//   - zsetKeys: A slice of strings representing the keys of the sorted sets to read from.
	//   - topN: An integer indicating how many top members to retrieve from each sorted set.
	//     If topN <= 0, all members are retrieved.
	//
	// Returns:
	//   - A map where the key is the name of the sorted set and the value is a slice of ZMember
	//     containing the members and their scores.
	//   - An error if the operation fails.
	batchZReadRange(zsetKeys []string, topN int) (map[string][]ZMember, error)

	// zWrite adds a member to a sorted set with the given score and key.
	// It calls zWriteWithTTL with a TTL of 0, meaning the entry will not expire.
	// Parameters:
	//   - zsetKey: the key of the sorted set
	//   - member: the member to be added to the sorted set
	//   - score: the score associated with the member
	//   - withSetName: the name of the set to be used in the operation
	//
	// Returns:
	//   - error: an error if the operation fails, otherwise nil
	zWrite(zsetKey, member string, score float64, withSetName string) error

	// zWriteWithTTL adds a member to a sorted set with the given score, key, and TTL.
	// Parameters:
	//   - zsetKey: the key of the sorted set
	//   - member: the member to be added to the sorted set
	//   - score: the score associated with the member
	//   - withSetName: the name of the set to be used in the operation
	//   - ttl: the time-to-live duration for the entry
	//
	// Returns:
	//   - error: an error if the operation fails, otherwise nil
	zWriteWithTTL(zsetKey, member string, score float64, withSetName string, ttl time.Duration) error

	// safeBatchZWrite writes multiple sorted set members to Redis with zero TTL.
	// Parameter:
	//   - keys: slice of sorted set keys
	//   - members: slice of members to add to the sorted sets
	//   - scores: slice of scores for the corresponding members
	//
	// Return:
	//   - error: returns an error if the operation fails, otherwise nil
	safeBatchZWrite(zsetKeys []string, members []string, scores []float64) error

	// safeBatchZWriteWithTTL writes multiple sorted set members with their scores to the specified keys with a TTL.
	// This function ensures that the operation is performed atomically for each key and handles errors appropriately.
	// Parameters:
	//   - keys: slice of keys where the members will be written
	//   - members: slice of members to be added to the sorted sets
	//   - scores: slice of scores for the corresponding members
	//   - ttl: time-to-live duration for the keys; if 0, no expiration is set
	//
	// Return:
	//   - error: returns an error if the operation fails, otherwise nil
	safeBatchZWriteWithTTL(zsetKeys []string, members []string, scores []float64, ttl time.Duration) error

	// batchZWriteWithTTL writes multiple sorted set members with their scores to Redis and sets a TTL for each key.
	// Parameters:
	//   - keys: slice of strings representing the Redis keys for the sorted sets
	//   - members: slice of strings representing the members to be added to the sorted sets
	//   - scores: slice of float64 representing the scores for each member
	//   - ttl: time.Duration representing the time-to-live for each key
	//
	// Returns:
	//   - error: returns an error if the operation fails, otherwise returns nil
	batchZWriteWithTTL(keys []string, members []string, scores []float64, ttl time.Duration) error

	// batchZWrite writes multiple members to sorted sets in Redis without TTL.
	// Parameters:
	//   - keys: the keys of the sorted sets
	//   - members: the members to be added to the sorted sets
	//   - scores: the scores of the members
	//
	// Returns:
	//   - error: returns an error if the operation fails, otherwise returns nil
	batchZWrite(keys []string, members []string, scores []float64) error

	// zDelete removes a member from a sorted set in Redis.
	// Parameters:
	//   - zsetKey: the key of the sorted set
	//   - member: the member to be removed from the sorted set
	//
	// Returns:
	//   - error: returns an error if the operation fails, otherwise returns nil
	zDelete(zsetKey, member string) error

	// SafeBatchZDelete concurrently removes members from multiple sorted sets.
	// Parameters:
	//   - zsetKeys: a slice of strings representing the sorted set keys
	//   - members: a slice of strings representing the members to remove, index corresponds to zsetKeys
	//
	// Return:
	//   - error: any error encountered during the operation
	safeBatchZDelete(zsetKeys []string, members []string) error

	// batchZDelete removes members from multiple sorted sets in Redis using a pipeline.
	// Parameters:
	//   - zsetKeys: A slice of strings representing the sorted set keys
	//   - members: A slice of strings representing the members to remove
	//
	// Returns:
	//   - An error if the operation fails.
	batchZDelete(zsetKeys []string, members []string) error

	// delete removes a key from Redis.
	//   - key: the key to be deleted
	//   - error: returns an error if the operation fails, otherwise returns nil
	delete(key string) error

	// safeBatchDelete concurrently deletes multiple keys from Redis.
	// Parameters:
	//   - keys: a slice of strings representing the keys to delete
	//
	// Return:
	//   - error: any error encountered during the operation
	safeBatchDelete(keys []string) error

	// batchDelete deletes multiple keys from Redis in a single pipeline.
	// Parameters:
	//   - keys: A slice of strings representing the keys to delete.
	//
	// Returns:
	//   - An error if the operation fails.
	batchDelete(keys []string) error

	close() error
}

type MetadataServiceClient struct {
	redisClient *redis.ClusterClient
	config      *redis2.Config
}

func NewMetadataServiceClient(
	configPath string,
	v6dMetadataServiceRedisClusterHosts string,
	v6dMetadataServiceRedisClusterPassword string) (*MetadataServiceClient, error) {
	clusterClient, cfg, err := redis2.NewRedisClusterClient(
		configPath, v6dMetadataServiceRedisClusterHosts, v6dMetadataServiceRedisClusterPassword)
	if err != nil {
		return nil, fmt.Errorf("failed to create Redis cluster client: %w", err)
	}
	v6dMetadataServiceClient := &MetadataServiceClient{
		redisClient: clusterClient,
		config:      cfg,
	}
	klog.Info("MetadataServiceClient initialized")

	return v6dMetadataServiceClient, nil
}

func (c *MetadataServiceClient) HashTokens(
	tokens []int64, chunkSize int, saveUnfullChunk bool,
	irisMetaPrefix string, vLLMBlockPrefix string) ([]string, error) {
	if len(tokens) == 0 || chunkSize <= 0 {
		return nil, fmt.Errorf("invalid hash input")
	}

	numCompleteBlocks := len(tokens) / chunkSize
	totalBlocks := numCompleteBlocks
	remainder := len(tokens) % chunkSize
	if remainder > 0 {
		totalBlocks++
	}

	// Core prefix hash computation using XXHash64 streaming
	blockHashes := make([]string, 0, totalBlocks)
	hasher := xxhash.NewWithSeed(0)
	defer hasher.Reset()

	// Process complete blocks
	for i := 0; i < numCompleteBlocks; i++ {
		if err := binary.Write(hasher, binary.LittleEndian, tokens[i*chunkSize:(i+1)*chunkSize]); err != nil {
			return nil, fmt.Errorf("failed to write complete block %d: %w", i, err)
		}
		blockHashes = append(blockHashes, c.buildBlockName(hasher.Sum64(), irisMetaPrefix, vLLMBlockPrefix))
	}

	// Process last incomplete block if it exists
	if saveUnfullChunk && remainder > 0 {
		if err := binary.Write(hasher, binary.LittleEndian, tokens[numCompleteBlocks*chunkSize:]); err != nil {
			return nil, fmt.Errorf("failed to write remainder block: %w", err)
		}

		// Pad with zeros to match original behavior
		padding := make([]int64, chunkSize-remainder)
		if err := binary.Write(hasher, binary.LittleEndian, padding); err != nil {
			return nil, fmt.Errorf("failed to write padding: %w", err)
		}

		blockHashes = append(blockHashes, c.buildBlockName(hasher.Sum64(), irisMetaPrefix, vLLMBlockPrefix))
	}

	return blockHashes, nil
}

func (c *MetadataServiceClient) buildBlockName(hash uint64, irisMetaPrefix string, vLLMBlockPrefix string) string {
	return irisMetaPrefix + vLLMBlockPrefix + strconv.FormatUint(hash, 10)
}

func (c *MetadataServiceClient) BatchQueryPrefixHashHitKVSInstances(hashKeys []string) (map[string][]string, error) {
	results, err := c.safeBatchZReadRange(hashKeys, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to query hit kvs instances for %d hash key: %w", len(hashKeys), err)
	}

	hitKVSInstances := c.parseResultsToHitMap(results)

	return hitKVSInstances, nil
}

func (c *MetadataServiceClient) QueryPrefixHashHitKVSInstances(hashKey string) ([]string, error) {
	result, err := c.zReadRange(hashKey, 0)
	if err != nil {
		return nil, fmt.Errorf("failed to query hit kvs instances for hash key %s: %w", hashKey, err)
	}

	hitKVSInstances := make([]string, len(result))
	for i, item := range result {
		hitKVSInstances[i] = c.parseToKVSInstanceIp(item.Member)
	}

	return hitKVSInstances, nil
}

func (c *MetadataServiceClient) parseResultsToHitMap(results map[string][]ZMember) map[string][]string {
	hitKVSInstances := make(map[string][]string, len(results))
	for hashKey, result := range results {
		hitKVSInstances[hashKey] = make([]string, len(result))
		for i, item := range result {
			hitKVSInstances[hashKey][i] = c.parseToKVSInstanceIp(item.Member)
		}
	}

	return hitKVSInstances
}

func (c *MetadataServiceClient) parseToKVSInstanceIp(kvsInstance string) string {
	return strings.Split(kvsInstance, ":")[0]
}

func (c *MetadataServiceClient) InsertPrefixHashHitKVSInstance(hashKey string, kvsInstance string) error {
	if err := c.zWrite(hashKey, kvsInstance, 0, ""); err != nil {
		return fmt.Errorf("failed to insert hit kvs worker for hash key %s: %v", hashKey, err)
	}
	return nil
}

// NOTE(sunbiao.sun): All functions below return empty results instead of nil when encountering invalid inputs or errors.
// This design ensures safe chaining of subsequent function calls by preventing nil pointer exceptions.

type ZMember struct {
	Member string  `json:"member"`
	Score  float64 `json:"score"`
}

func (c *MetadataServiceClient) zReadRange(zsetKey string, topN int) ([]ZMember, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	var redisMembers []redis.Z
	var err error

	if topN > 0 {
		redisMembers, err = c.redisClient.ZRevRangeWithScores(ctx, zsetKey, 0, int64(topN-1)).Result()
	} else {
		redisMembers, err = c.redisClient.ZRangeWithScores(ctx, zsetKey, 0, -1).Result()
	}

	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read zset %s: %w", zsetKey, err)
	}

	var jsonMembers []ZMember
	for _, z := range redisMembers {
		if member, ok := z.Member.(string); ok {
			jsonMembers = append(jsonMembers, ZMember{
				Member: member,
				Score:  z.Score,
			})
		}
	}

	return jsonMembers, nil
}

func (c *MetadataServiceClient) safeBatchZReadRange(zsetKeys []string, topN int) (map[string][]ZMember, error) {
	if len(zsetKeys) == 0 {
		return nil, nil
	}

	result := make(map[string][]ZMember)
	var mu sync.Mutex

	var (
		wg          sync.WaitGroup
		errChan     = make(chan error, c.config.RedisCluster.MaxConcurrentBatches)
		ctx, cancel = context.WithCancel(context.Background())
	)
	defer cancel()

	batches := splitIntoBatches(zsetKeys, c.config.RedisCluster.MaxBatchSize)

	sem := make(chan struct{}, c.config.RedisCluster.MaxConcurrentBatches)

batchLoop:
	for i, batch := range batches {
		select {
		case <-ctx.Done():
			break batchLoop
		default:
			wg.Add(1)
			sem <- struct{}{}

			go func(batchIndex int, keys []string) {
				defer func() {
					<-sem
					wg.Done()
				}()

				batchResults, err := c.batchZReadRange(keys, topN)
				if err != nil {
					cancel()
					select {
					case errChan <- fmt.Errorf("batch %d failed: %w", batchIndex, err):
					default:
					}
					return
				}

				mu.Lock()
				for k, v := range batchResults {
					result[k] = v
				}
				mu.Unlock()
			}(i, batch)
		}
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	if err := <-errChan; err != nil {
		return nil, fmt.Errorf("safeBatchZReadRange failed: %w", err)
	}

	return result, nil
}

func (c *MetadataServiceClient) batchZReadRange(zsetKeys []string, topN int) (map[string][]ZMember, error) {
	if len(zsetKeys) == 0 {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	pipe := c.redisClient.Pipeline()
	cmds := make(map[string]*redis.ZSliceCmd, len(zsetKeys))

	for _, key := range zsetKeys {
		if topN > 0 {
			cmds[key] = pipe.ZRevRangeWithScores(ctx, key, 0, int64(topN-1))
		} else {
			cmds[key] = pipe.ZRangeWithScores(ctx, key, 0, -1)
		}
	}

	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return nil, fmt.Errorf("failed to batch read %d zset: %w", len(zsetKeys), err)
	}

	results := make(map[string][]ZMember, len(zsetKeys))
	for key, cmd := range cmds {
		members, err := cmd.Result()
		if err != nil && errors.Is(err, redis.Nil) {
			results[key] = nil
		} else {
			var jsonMembers []ZMember
			for _, z := range members {
				if member, ok := z.Member.(string); ok {
					jsonMembers = append(jsonMembers, ZMember{
						Member: member,
						Score:  z.Score,
					})
				}
			}
			results[key] = jsonMembers
		}
	}
	return results, nil
}

func (c *MetadataServiceClient) zWrite(zsetKey, member string, score float64, withSetName string) error {
	return c.zWriteWithTTL(zsetKey, member, score, withSetName, 0)
}

func (c *MetadataServiceClient) zWriteWithTTL(
	zsetKey, member string, score float64, withSetName string, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	err := c.redisClient.ZAdd(ctx, zsetKey, redis.Z{
		Score:  score,
		Member: member,
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to add member %s to zset %s: %w", member, zsetKey, err)
	}

	if ttl > 0 {
		if err := c.redisClient.Expire(ctx, zsetKey, ttl).Err(); err != nil {
			return fmt.Errorf("failed to set TTL for zset %s: %w", zsetKey, err)
		}
	}

	if withSetName != "" {
		if err := c.redisClient.SAdd(ctx, withSetName, zsetKey).Err(); err != nil {
			return fmt.Errorf("failed to add key %s to set %s: %w", zsetKey, withSetName, err)
		}
	}

	return nil
}

func (c *MetadataServiceClient) safeBatchZWrite(zsetKeys []string, members []string, scores []float64) error {
	return c.safeBatchZWriteWithTTL(zsetKeys, members, scores, 0)
}

func (c *MetadataServiceClient) safeBatchZWriteWithTTL(zsetKeys []string, members []string, scores []float64, ttl time.Duration) error {
	if len(zsetKeys) == 0 {
		return nil
	}
	if len(zsetKeys) != len(members) || len(members) != len(scores) {
		return fmt.Errorf("zsetKeys, members and scores length mismatch: %d != %d != %d",
			len(zsetKeys), len(members), len(scores))
	}

	var (
		wg          sync.WaitGroup
		errChan     = make(chan error, c.config.RedisCluster.MaxConcurrentBatches)
		ctx, cancel = context.WithCancel(context.Background())
	)
	defer cancel()

	type batchData struct {
		keys    []string
		members []string
		scores  []float64
	}

	batches := make([]batchData, 0)
	for i := 0; i < len(zsetKeys); i += c.config.RedisCluster.MaxBatchSize {
		end := i + c.config.RedisCluster.MaxBatchSize
		if end > len(zsetKeys) {
			end = len(zsetKeys)
		}
		batches = append(batches, batchData{
			keys:    zsetKeys[i:end],
			members: members[i:end],
			scores:  scores[i:end],
		})
	}

	sem := make(chan struct{}, c.config.RedisCluster.MaxConcurrentBatches)

batchLoop:
	for i, batch := range batches {
		select {
		case <-ctx.Done():
			break batchLoop
		default:
			wg.Add(1)
			sem <- struct{}{}

			go func(batchIndex int, batch batchData) {
				defer func() {
					<-sem
					wg.Done()
				}()

				if err := c.batchZWriteWithTTL(batch.keys, batch.members, batch.scores, ttl); err != nil {
					cancel()
					select {
					case errChan <- fmt.Errorf("batch %d failed: %w", batchIndex, err):
					default:
					}
				}
			}(i, batch)
		}
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	if err := <-errChan; err != nil {
		return fmt.Errorf("safeBatchZWriteWithTTL failed: %w", err)
	}

	return nil
}

func (c *MetadataServiceClient) batchZWriteWithTTL(zsetKeys []string, members []string, scores []float64, ttl time.Duration) error {
	if len(zsetKeys) == 0 {
		return nil
	}
	if len(zsetKeys) != len(members) || len(members) != len(scores) {
		return fmt.Errorf("zsetKeys, members and scores length mismatch: %d != %d != %d",
			len(zsetKeys), len(members), len(scores))
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	pipe := c.redisClient.Pipeline()

	for i := range zsetKeys {
		pipe.ZAdd(ctx, zsetKeys[i], redis.Z{
			Score:  scores[i],
			Member: members[i],
		})

		if ttl > 0 {
			pipe.Expire(ctx, zsetKeys[i], ttl)
		}
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to batch write zsets: %w", err)
	}

	return nil
}

func (c *MetadataServiceClient) batchZWrite(zsetKeys []string, members []string, scores []float64) error {
	return c.batchZWriteWithTTL(zsetKeys, members, scores, 0)
}

func (c *MetadataServiceClient) zDelete(zsetKey, member string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	// remove a specific member from zset
	err := c.redisClient.ZRem(ctx, zsetKey, member).Err()
	if err != nil {
		return fmt.Errorf("ZRem failed for zset %s member %s: %w", zsetKey, member, err)
	}

	return nil
}

func (c *MetadataServiceClient) safeBatchZDelete(zsetKeys []string, members []string) error {
	if len(zsetKeys) == 0 {
		return nil
	}
	if len(zsetKeys) != len(members) {
		return fmt.Errorf("length mismatch: got %d keys and %d members", len(zsetKeys), len(members))
	}

	var (
		wg          sync.WaitGroup
		errChan     = make(chan error, c.config.RedisCluster.MaxConcurrentBatches)
		ctx, cancel = context.WithCancel(context.Background())
	)
	defer cancel()

	keyBatches := splitIntoBatches(zsetKeys, c.config.RedisCluster.MaxBatchSize)
	memberBatches := splitIntoBatches(members, c.config.RedisCluster.MaxBatchSize)

	sem := make(chan struct{}, c.config.RedisCluster.MaxConcurrentBatches)

batchLoop:
	for i := range keyBatches {
		select {
		case <-ctx.Done():
			break batchLoop
		default:
			wg.Add(1)
			sem <- struct{}{}

			go func(batchIndex int, keys, mems []string) {
				defer func() {
					<-sem
					wg.Done()
				}()

				if err := c.batchZDelete(keys, mems); err != nil {
					cancel()
					select {
					case errChan <- fmt.Errorf("batch %d failed: %w", batchIndex, err):
					default:
					}
				}
			}(i, keyBatches[i], memberBatches[i])
		}
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	if err := <-errChan; err != nil {
		return fmt.Errorf("safeBatchZDelete failed: %w", err)
	}

	return nil
}

func (c *MetadataServiceClient) batchZDelete(zsetKeys []string, members []string) error {
	if len(zsetKeys) == 0 {
		return nil
	}
	if len(zsetKeys) != len(members) {
		return fmt.Errorf("length mismatch: got %d keys and %d members", len(zsetKeys), len(members))
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	pipe := c.redisClient.Pipeline()
	for i := range zsetKeys {
		pipe.ZRem(ctx, zsetKeys[i], members[i])
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to batch delete %d members: %w", len(zsetKeys), err)
	}

	return nil
}

func (c *MetadataServiceClient) delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	err := c.redisClient.Del(ctx, key).Err()
	if err != nil {
		return fmt.Errorf("delete key %s failed: %w", key, err)
	}

	return nil
}

func (c *MetadataServiceClient) safeBatchDelete(keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	var (
		wg          sync.WaitGroup
		errChan     = make(chan error, c.config.RedisCluster.MaxConcurrentBatches)
		ctx, cancel = context.WithCancel(context.Background())
	)
	defer cancel()

	batches := splitIntoBatches(keys, c.config.RedisCluster.MaxBatchSize)

	sem := make(chan struct{}, c.config.RedisCluster.MaxConcurrentBatches)

batchLoop:
	for i, batch := range batches {
		select {
		case <-ctx.Done():
			break batchLoop
		default:
			wg.Add(1)
			sem <- struct{}{}

			go func(batchIndex int, keys []string) {
				defer func() {
					<-sem
					wg.Done()
				}()

				if err := c.batchDelete(keys); err != nil {
					cancel()
					select {
					case errChan <- fmt.Errorf("batch %d delete failed: %w", batchIndex, err):
					default:
					}
				}
			}(i, batch)
		}
	}

	go func() {
		wg.Wait()
		close(errChan)
	}()

	if err := <-errChan; err != nil {
		return fmt.Errorf("SafeBatchDelete failed: %w", err)
	}

	return nil
}

func (c *MetadataServiceClient) batchDelete(keys []string) error {
	if len(keys) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.config.RedisCluster.QueryTimeout*time.Second)
	defer cancel()

	pipe := c.redisClient.Pipeline()
	for _, key := range keys {
		pipe.Del(ctx, key)
	}

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to batch delete %d keys: %w", len(keys), err)
	}
	return nil
}

func (c *MetadataServiceClient) close() error {
	if c.redisClient != nil {
		if err := c.redisClient.Close(); err != nil {
			return fmt.Errorf("failed to close Redis cluster client: %w", err)
		}
	}
	return nil
}

func splitIntoBatches(items []string, batchSize int) [][]string {
	var batches [][]string
	for i := 0; i < len(items); i += batchSize {
		end := i + batchSize
		if end > len(items) {
			end = len(items)
		}
		batches = append(batches, items[i:end])
	}
	return batches
}
