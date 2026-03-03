//go:build exclude
// +build exclude

package kvs

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"
)

func TestKVSClient_CacheOperations(t *testing.T) {
	// Test basic operations with mock Redis
	t.Run("basic operations", func(t *testing.T) {
		mockClient, mock := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient:       mockClient,
			chunkSize:                  4,
			saveUnfullChunk:            true,
			irisMetaPrefix:             "iris_meta_",
			vLLMBlockPrefix:            "vllm_block_",
			retryTimes:                 5,
			retryInterval:              time.Millisecond * 100,
			kvsMetaServiceStatus:       atomic.Value{},
			kvsMetaServiceLastDownTime: atomic.Value{},
		}
		kvsClient.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
		kvsClient.kvsMetaServiceLastDownTime.Store(time.Now())

		prefixHash1 := "prefix_hash_1"
		prefixHash2 := "prefix_hash_2"
		instance1 := "instance_1"
		instance2 := "instance_2"

		// Set up mock expectations for inserting data
		mock.ExpectZAdd(prefixHash1, redis.Z{Score: 0, Member: instance1}).SetVal(1)
		mock.ExpectZAdd(prefixHash1, redis.Z{Score: 0, Member: instance2}).SetVal(1)
		mock.ExpectZAdd(prefixHash2, redis.Z{Score: 0, Member: instance1}).SetVal(1)

		err := kvsClient.insertCacheHitKVSWorkers(prefixHash1, instance1)
		assert.NoError(t, err)
		err = kvsClient.insertCacheHitKVSWorkers(prefixHash1, instance2)
		assert.NoError(t, err)
		err = kvsClient.insertCacheHitKVSWorkers(prefixHash2, instance1)
		assert.NoError(t, err)

		// Test single query
		mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetVal([]redis.Z{
			{Score: 0, Member: instance1},
			{Score: 0, Member: instance2},
		})

		results := kvsClient.queryCacheHitKVSWorkers([]string{prefixHash1})
		assert.Equal(t, map[string][]string{
			prefixHash1: {instance1, instance2},
		}, results)

		// Test batch query
		mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetVal([]redis.Z{
			{Score: 0, Member: instance1},
			{Score: 0, Member: instance2},
		})
		mock.ExpectZRangeWithScores(prefixHash2, 0, -1).SetVal([]redis.Z{
			{Score: 0, Member: instance1},
		})

		results1 := kvsClient.BatchQueryCacheHitKVSWorkers([]string{prefixHash1, prefixHash2})
		assert.Equal(t, map[string][]string{
			prefixHash1: {instance1, instance2},
			prefixHash2: {instance1},
		}, results1)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test error handling scenarios
	t.Run("error handling", func(t *testing.T) {
		mockClient, mock := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient:       mockClient,
			chunkSize:                  4,
			saveUnfullChunk:            true,
			irisMetaPrefix:             "iris_meta_",
			vLLMBlockPrefix:            "vllm_block_",
			retryTimes:                 5,
			retryInterval:              time.Millisecond * 100,
			kvsMetaServiceStatus:       atomic.Value{},
			kvsMetaServiceLastDownTime: atomic.Value{},
		}
		kvsClient.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
		kvsClient.kvsMetaServiceLastDownTime.Store(time.Now())

		// Test Redis connection error
		mock.ExpectZAdd("prefix_hash", redis.Z{Score: 0, Member: "instance"}).
			SetErr(redis.ErrClosed)

		err := kvsClient.insertCacheHitKVSWorkers("prefix_hash", "instance")
		assert.Error(t, err)

		// Test Redis key not found
		mock.ExpectZRangeWithScores("nonexistent_hash", 0, -1).
			SetErr(redis.Nil)

		results := kvsClient.queryCacheHitKVSWorkers([]string{"nonexistent_hash"})
		assert.Equal(t, map[string][]string{"nonexistent_hash": {}}, results)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test with complex combinations of prefix hashes and instances
	t.Run("complex prefix hash and instances", func(t *testing.T) {
		mockClient, mock := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient:       mockClient,
			chunkSize:                  4,
			saveUnfullChunk:            true,
			irisMetaPrefix:             "iris_meta_",
			vLLMBlockPrefix:            "vllm_block_",
			retryTimes:                 5,
			retryInterval:              time.Millisecond * 100,
			kvsMetaServiceStatus:       atomic.Value{},
			kvsMetaServiceLastDownTime: atomic.Value{},
		}
		kvsClient.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
		kvsClient.kvsMetaServiceLastDownTime.Store(time.Now())

		// Create multiple prefix hashes and instances
		prefixHashes := make([]string, 5)
		instances := make([]string, 10)
		for i := 0; i < 5; i++ {
			prefixHashes[i] = fmt.Sprintf("prefix_hash_%d", i)
		}
		for i := 0; i < 10; i++ {
			instances[i] = fmt.Sprintf("instance_%d", i)
		}

		// Set up mock expectations for inserting data
		for _, hash := range prefixHashes {
			for i := 0; i < 3; i++ { // Each hash will have 3 random instances
				instance := instances[rand.Intn(len(instances))]
				mock.ExpectZAdd(hash, redis.Z{Score: 0, Member: instance}).SetVal(1)
				err := kvsClient.insertCacheHitKVSWorkers(hash, instance)
				assert.NoError(t, err)
			}
		}

		// Set up mock expectations for batch query
		expectedResults := make(map[string][]redis.Z)
		for _, hash := range prefixHashes {
			members := make([]redis.Z, 0)
			for i := 0; i < 3; i++ {
				members = append(members, redis.Z{
					Score:  0,
					Member: instances[rand.Intn(len(instances))],
				})
			}
			expectedResults[hash] = members
			mock.ExpectZRangeWithScores(hash, 0, -1).SetVal(members)
		}

		// Perform batch query
		results := kvsClient.BatchQueryCacheHitKVSWorkers(prefixHashes)

		// Verify results
		for hash, members := range expectedResults {
			expectedInstances := make([]string, len(members))
			for i, m := range members {
				expectedInstances[i] = m.Member.(string)
			}
			assert.ElementsMatch(t, expectedInstances, results[hash])
		}

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test empty and nil input handling
	t.Run("empty and nil inputs", func(t *testing.T) {
		mockClient, mock := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient:       mockClient,
			chunkSize:                  4,
			saveUnfullChunk:            true,
			irisMetaPrefix:             "iris_meta_",
			vLLMBlockPrefix:            "vllm_block_",
			retryTimes:                 5,
			retryInterval:              time.Millisecond * 100,
			kvsMetaServiceStatus:       atomic.Value{},
			kvsMetaServiceLastDownTime: atomic.Value{},
		}
		kvsClient.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
		kvsClient.kvsMetaServiceLastDownTime.Store(time.Now())

		// Test with nil inputs
		results := kvsClient.BatchQueryCacheHitKVSWorkers(nil)
		assert.Empty(t, results)

		// Test with empty slice
		results = kvsClient.BatchQueryCacheHitKVSWorkers([]string{})
		assert.Empty(t, results)

		mock.ExpectZRangeWithScores("", 0, -1).SetVal([]redis.Z{})

		// Test with empty string
		results = kvsClient.queryCacheHitKVSWorkers([]string{""})
		assert.Equal(t, map[string][]string{"": {}}, results)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test retry logic and service status transitions
	t.Run("retry and service status", func(t *testing.T) {
		mockClient, mock := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient:       mockClient,
			chunkSize:                  4,
			saveUnfullChunk:            true,
			irisMetaPrefix:             "iris_meta_",
			vLLMBlockPrefix:            "vllm_block_",
			retryTimes:                 3,
			retryInterval:              time.Millisecond * 10, // Use shorter interval to speed up tests
			kvsMetaServiceDownDuration: time.Second * 1,       // Use shorter recovery time for testing
			kvsMetaServiceStatus:       atomic.Value{},
			kvsMetaServiceLastDownTime: atomic.Value{},
		}
		kvsClient.kvsMetaServiceStatus.Store(kvsMetaServiceHealthy)
		kvsClient.kvsMetaServiceLastDownTime.Store(time.Now())

		prefixHash1 := "prefix_hash_1"
		prefixHash2 := "prefix_hash_2"
		instance1 := "instance_1"
		instance2 := "instance_2"

		// 1. Test successful retry scenario
		t.Run("retry success", func(t *testing.T) {
			// First two attempts timeout, third one succeeds
			mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetErr(context.DeadlineExceeded)
			mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetErr(context.DeadlineExceeded)
			mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetVal([]redis.Z{
				{Score: 0, Member: instance1},
				{Score: 0, Member: instance2},
			})

			results := kvsClient.BatchQueryCacheHitKVSWorkers([]string{prefixHash1})
			assert.Equal(t, map[string][]string{
				prefixHash1: {instance1, instance2},
			}, results)
		})

		// 2. Test service being marked as down after all retries fail
		t.Run("retry failure marks service down", func(t *testing.T) {
			// All retry attempts timeout
			for i := 0; i < kvsClient.retryTimes; i++ {
				mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetErr(context.DeadlineExceeded)
			}

			results := kvsClient.BatchQueryCacheHitKVSWorkers([]string{prefixHash1})
			assert.Empty(t, results)

			// Verify service is marked as down
			assert.Equal(t, kvsMetaServiceDown, kvsClient.kvsMetaServiceStatus.Load().(kvsMetaServiceStatus))
		})

		// 3. Test requests while service is down
		t.Run("requests during service down", func(t *testing.T) {
			results := kvsClient.BatchQueryCacheHitKVSWorkers([]string{prefixHash1, prefixHash2})
			assert.Empty(t, results)
		})

		// 4. Test requests after service recovery
		t.Run("requests after service recovery", func(t *testing.T) {
			// Wait for service recovery duration
			time.Sleep(kvsClient.kvsMetaServiceDownDuration + time.Millisecond*100)

			// Set up successful requests after recovery
			mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetVal([]redis.Z{
				{Score: 0, Member: instance1},
			})
			mock.ExpectZRangeWithScores(prefixHash2, 0, -1).SetVal([]redis.Z{
				{Score: 0, Member: instance2},
			})

			results := kvsClient.BatchQueryCacheHitKVSWorkers([]string{prefixHash1, prefixHash2})
			assert.Equal(t, map[string][]string{
				prefixHash1: {instance1},
				prefixHash2: {instance2},
			}, results)

			// Verify service status has recovered
			assert.Equal(t, kvsMetaServiceHealthy, kvsClient.kvsMetaServiceStatus.Load().(kvsMetaServiceStatus))
		})

		// 5. Test handling of non-timeout errors
		t.Run("non-timeout error handling", func(t *testing.T) {
			mock.ExpectZRangeWithScores(prefixHash1, 0, -1).SetErr(errors.New("non-timeout error"))

			results := kvsClient.BatchQueryCacheHitKVSWorkers([]string{prefixHash1})
			assert.Empty(t, results)

			// Verify service remains healthy (non-timeout errors don't mark service as down)
			assert.Equal(t, kvsMetaServiceHealthy, kvsClient.kvsMetaServiceStatus.Load().(kvsMetaServiceStatus))
		})

		assert.NoError(t, mock.ExpectationsWereMet())
	})

}

func TestKVSClient_CalcInstancesCacheHitLen(t *testing.T) {
	t.Run("basic calculation", func(t *testing.T) {
		kvsClient := &KVSClient{
			chunkSize: 4,
		}

		prefixHashes := []string{"hash1", "hash2", "hash3"}
		prefixHashHitInstances := map[string]sets.String{
			"hash1": sets.NewString("instance1", "instance2"),
			"hash2": sets.NewString("instance1", "instance2"),
			"hash3": sets.NewString("instance1"), // instance2 breaks here
		}

		hitLen := kvsClient.CalcInstancesCacheHitLen(prefixHashes, prefixHashHitInstances)

		// instance1 should have all chunks (3 * chunkSize)
		assert.Equal(t, 12, hitLen["instance1"])
		// instance2 should only have 2 chunks (2 * chunkSize), then breaks
		assert.Equal(t, 8, hitLen["instance2"])
	})

	t.Run("empty inputs", func(t *testing.T) {
		kvsClient := &KVSClient{
			chunkSize: 4,
		}

		// Test with nil inputs
		hitLen := kvsClient.CalcInstancesCacheHitLen(nil, nil)
		assert.Empty(t, hitLen)

		// Test with empty slice and map
		hitLen = kvsClient.CalcInstancesCacheHitLen([]string{}, map[string]sets.String{})
		assert.Empty(t, hitLen)
	})

	t.Run("missing prefix hash", func(t *testing.T) {
		kvsClient := &KVSClient{
			chunkSize: 4,
		}

		prefixHashes := []string{"hash1", "hash2"}
		prefixHashHitInstances := map[string]sets.String{
			"hash1": sets.NewString("instance1"),
			// hash2 missing
		}

		hitLen := kvsClient.CalcInstancesCacheHitLen(prefixHashes, prefixHashHitInstances)
		// instance1 should break at hash2
		assert.Equal(t, 4, hitLen["instance1"])
	})
}

func TestKVSClient_PrefixHash(t *testing.T) {
	t.Run("empty input", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		hashes := kvsClient.PrefixHash(nil)
		assert.Empty(t, hashes)
	})

	t.Run("complete blocks", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		tokens := []int64{1, 2, 3, 4, 5, 6, 7, 8}
		hashes := kvsClient.PrefixHash(tokens)
		assert.Equal(t, 2, len(hashes))
		// Check prefix
		for _, hash := range hashes {
			assert.True(t, strings.HasPrefix(hash, kvsClient.getBlockNamePrefix()))
		}
	})

	t.Run("incomplete block", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		tokens := []int64{1, 2, 3, 4, 5}
		hashes := kvsClient.PrefixHash(tokens)
		assert.Equal(t, 2, len(hashes)) // One complete block + one incomplete block
	})

	t.Run("incomplete block without saving", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      false,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		tokens := []int64{1, 2, 3, 4, 5}
		hashes := kvsClient.PrefixHash(tokens)
		assert.Equal(t, 1, len(hashes)) // Only complete block
	})
}

func TestKVSClient_hashV6d(t *testing.T) {
	t.Run("hash consistency", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		// Same input should produce same hash
		tokens1 := []int64{1, 2, 3, 4}
		tokens2 := []int64{1, 2, 3, 4}

		hashes1, err := kvsClient.hashV6d(tokens1)
		assert.NoError(t, err)
		hashes2, err := kvsClient.hashV6d(tokens2)
		assert.NoError(t, err)

		assert.Equal(t, hashes1, hashes2)
	})

	t.Run("different chunk sizes", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		client1 := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}
		client2 := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            8,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		tokens := []int64{1, 2, 3, 4, 5, 6, 7, 8}

		hashes1, err := client1.hashV6d(tokens)
		assert.NoError(t, err)
		hashes2, err := client2.hashV6d(tokens)
		assert.NoError(t, err)

		assert.Equal(t, 2, len(hashes1))           // Should produce 2 hashes with chunk size 4
		assert.Equal(t, 1, len(hashes2))           // Should produce 1 hash with chunk size 8
		assert.NotEqual(t, hashes1[0], hashes2[0]) // Hashes should be different
	})

	t.Run("zero padding", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            4,
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		// Test that padding with zeros produces expected results
		tokens1 := []int64{1, 2, 3, 4, 5}
		tokens2 := []int64{1, 2, 3, 4, 5, 0, 0, 0}

		hashes1, err := kvsClient.hashV6d(tokens1)
		assert.NoError(t, err)
		hashes2, err := kvsClient.hashV6d(tokens2)
		assert.NoError(t, err)

		assert.Equal(t, hashes1[1], hashes2[1]) // Last blocks should hash to same value
	})

	t.Run("error cases", func(t *testing.T) {
		mockClient, _ := newMockKVSMetaServiceClient()
		kvsClient := &KVSClient{
			kvsMetaServiceClient: mockClient,
			chunkSize:            0, // Invalid chunk size
			saveUnfullChunk:      true,
			irisMetaPrefix:       "iris_meta_",
			vLLMBlockPrefix:      "vllm_block_",
		}

		tokens := []int64{1, 2, 3, 4}
		_, err := kvsClient.hashV6d(tokens)
		assert.Error(t, err) // Should return error
	})
}

type mockKVSMetaServiceClient struct {
	db   *redis.ClusterClient
	mock redismock.ClusterClientMock
}

func newMockKVSMetaServiceClient() (*mockKVSMetaServiceClient, redismock.ClusterClientMock) {
	db, mock := redismock.NewClusterMock()
	return &mockKVSMetaServiceClient{
		db:   db,
		mock: mock,
	}, mock
}

// Implement only the methods used in tests
func (m *mockKVSMetaServiceClient) zReadRange(zsetKey string, topN int) ([]ZMember, error) {
	ctx := context.Background()
	result, err := m.db.ZRangeWithScores(ctx, zsetKey, 0, -1).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return []ZMember{}, nil
		}
		return nil, err
	}

	members := make([]ZMember, len(result))
	for i, z := range result {
		if member, ok := z.Member.(string); ok {
			members[i] = ZMember{
				Member: member,
				Score:  z.Score,
			}
		}
	}
	return members, nil
}

func (m *mockKVSMetaServiceClient) safeBatchZReadRange(zsetKeys []string, topN int) (map[string][]ZMember, error) {
	results := make(map[string][]ZMember)
	for _, key := range zsetKeys {
		members, err := m.zReadRange(key, topN)
		if err != nil {
			if errors.Is(err, redis.Nil) {
				results[key] = []ZMember{}
				continue
			}
			return nil, err
		}
		results[key] = members
	}
	return results, nil
}

func (m *mockKVSMetaServiceClient) zWrite(zsetKey, member string, score float64, withSetName string) error {
	ctx := context.Background()
	return m.db.ZAdd(ctx, zsetKey, redis.Z{
		Score:  score,
		Member: member,
	}).Err()
}

// Empty implementations for unused interface methods
func (m *mockKVSMetaServiceClient) close() error { return nil }

func (m *mockKVSMetaServiceClient) batchZReadRange(zsetKeys []string, topN int) (map[string][]ZMember, error) {
	return nil, nil
}

func (m *mockKVSMetaServiceClient) zWriteWithTTL(zsetKey string, member string, score float64, withSetName string, ttl time.Duration) error {
	return nil
}

func (m *mockKVSMetaServiceClient) safeBatchZWrite(zsetKeys []string, members []string, scores []float64) error {
	return nil
}

func (m *mockKVSMetaServiceClient) safeBatchZWriteWithTTL(zsetKeys []string, members []string, scores []float64, ttl time.Duration) error {
	return nil
}

func (m *mockKVSMetaServiceClient) batchZWriteWithTTL(keys []string, members []string, scores []float64, ttl time.Duration) error {
	return nil
}

func (m *mockKVSMetaServiceClient) batchZWrite(keys []string, members []string, scores []float64) error {
	return nil
}

func (m *mockKVSMetaServiceClient) zDelete(zsetKey, member string) error {
	return nil
}

func (m *mockKVSMetaServiceClient) safeBatchZDelete(zsetKeys []string, members []string) error {
	return nil
}

func (m *mockKVSMetaServiceClient) batchZDelete(zsetKeys []string, members []string) error {
	return nil
}

func (m *mockKVSMetaServiceClient) delete(key string) error {
	return nil
}

func (m *mockKVSMetaServiceClient) safeBatchDelete(keys []string) error {
	return nil
}

func (m *mockKVSMetaServiceClient) batchDelete(keys []string) error {
	return nil
}
