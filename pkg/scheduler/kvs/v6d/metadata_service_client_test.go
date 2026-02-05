//go:build exclude
// +build exclude

package v6d

import (
	"context"
	"errors"
	"fmt"
	"io"
	batch_service_redis "llumnix/pkg/redis"
	"testing"
	"time"

	"github.com/go-redis/redismock/v9"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

func TestV6dMetadataServiceClient(t *testing.T) {
	// Create mock client
	db, mock := redismock.NewClusterMock()
	client := &MetadataServiceClient{
		redisClient: db,
		config: &batch_service_redis.Config{
			RedisCluster: batch_service_redis.RedisClusterConfig{
				QueryTimeout:         5,
				MaxBatchSize:         500,
				MaxConcurrentBatches: 10,
			},
		},
	}
	defer client.close()

	t.Run("Basic CRUD Operations", func(t *testing.T) {
		zsetKey := "test_zset"

		// Clean if exists
		mock.ExpectDel(zsetKey).SetVal(0)
		err := client.delete(zsetKey)
		assert.NoError(t, err)

		// Write single member
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member1", Score: 10.5}).SetVal(1)
		err = client.zWrite(zsetKey, "member1", 10.5, "")
		assert.NoError(t, err)

		// Read and verify
		mock.ExpectZRangeWithScores(zsetKey, 0, -1).SetVal([]redis.Z{
			{Member: "member1", Score: 10.5},
		})
		result, err := client.zReadRange(zsetKey, 0)
		assert.NoError(t, err)
		assert.Equal(t, []ZMember{{Member: "member1", Score: 10.5}}, result)

		// Delete member
		mock.ExpectZRem(zsetKey, "member1").SetVal(1)
		err = client.zDelete(zsetKey, "member1")
		assert.NoError(t, err)
	})

	t.Run("TTL Operations", func(t *testing.T) {
		zsetKey := "test_ttl_zset"
		ttl := 60 * time.Second

		// Write with TTL
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member1", Score: 10.5}).SetVal(1)
		mock.ExpectExpire(zsetKey, ttl).SetVal(true)
		err := client.zWriteWithTTL(zsetKey, "member1", 10.5, "", ttl)
		assert.NoError(t, err)

		// Write with TTL and set name
		setName := "test_set"
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member2", Score: 20.5}).SetVal(1)
		mock.ExpectExpire(zsetKey, ttl).SetVal(true)
		mock.ExpectSAdd(setName, zsetKey).SetVal(1)
		err = client.zWriteWithTTL(zsetKey, "member2", 20.5, setName, ttl)
		assert.NoError(t, err)
	})

	t.Run("Error Handling", func(t *testing.T) {
		zsetKey := "test_error_zset"

		// Test Redis connection error
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member1", Score: 10.5}).SetErr(redis.ErrClosed)
		err := client.zWrite(zsetKey, "member1", 10.5, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add member")

		// Test Redis nil response
		mock.ExpectZRangeWithScores(zsetKey, 0, -1).SetVal([]redis.Z{})
		result, err := client.zReadRange(zsetKey, 0)
		assert.NoError(t, err)
		assert.Empty(t, result)

		// Test delete non-existent key
		mock.ExpectDel(zsetKey).SetVal(0)
		err = client.delete(zsetKey)
		assert.NoError(t, err)
	})

	t.Run("Score Ordering", func(t *testing.T) {
		zsetKey := "test_order_zset"

		// Add members with same scores
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "memberA", Score: 15.0}).SetVal(1)
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "memberB", Score: 15.0}).SetVal(1)

		err := client.zWrite(zsetKey, "memberA", 15.0, "")
		assert.NoError(t, err)
		err = client.zWrite(zsetKey, "memberB", 15.0, "")
		assert.NoError(t, err)

		// Verify lexicographical ordering for same scores
		mock.ExpectZRangeWithScores(zsetKey, 0, -1).SetVal([]redis.Z{
			{Member: "memberA", Score: 15.0},
			{Member: "memberB", Score: 15.0},
		})
		result, err := client.zReadRange(zsetKey, 0)
		assert.NoError(t, err)
		assert.Equal(t, []ZMember{
			{Member: "memberA", Score: 15.0},
			{Member: "memberB", Score: 15.0},
		}, result)
	})

	t.Run("TopN Queries", func(t *testing.T) {
		zsetKey := "test_topn_zset"

		// Setup data
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member1", Score: 10.0}).SetVal(1)
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member2", Score: 20.0}).SetVal(1)
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member3", Score: 30.0}).SetVal(1)

		err := client.zWrite(zsetKey, "member1", 10.0, "")
		assert.NoError(t, err)
		err = client.zWrite(zsetKey, "member2", 20.0, "")
		assert.NoError(t, err)
		err = client.zWrite(zsetKey, "member3", 30.0, "")
		assert.NoError(t, err)

		// Test top 2 members
		mock.ExpectZRevRangeWithScores(zsetKey, 0, 1).SetVal([]redis.Z{
			{Member: "member3", Score: 30.0},
			{Member: "member2", Score: 20.0},
		})
		result, err := client.zReadRange(zsetKey, 2)
		assert.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, 30.0, result[0].Score)
		assert.Equal(t, 20.0, result[1].Score)

		// Test invalid topN
		mock.ExpectZRangeWithScores(zsetKey, 0, -1).SetVal([]redis.Z{
			{Member: "member1", Score: 10.0},
			{Member: "member2", Score: 20.0},
			{Member: "member3", Score: 30.0},
		})
		result, err = client.zReadRange(zsetKey, -1)
		assert.NoError(t, err)
		assert.Len(t, result, 3)
	})

	// Test withSetName parameter and SAdd failures
	t.Run("WithSetNameOperations", func(t *testing.T) {
		// Test successful write with set name
		zsetKey := "test_zset"
		setName := "test_set"

		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member1", Score: 1.0}).SetVal(1)
		mock.ExpectSAdd(setName, zsetKey).SetVal(1)

		err := client.zWrite(zsetKey, "member1", 1.0, setName)
		assert.NoError(t, err)

		// Test SAdd failure
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member2", Score: 2.0}).SetVal(1)
		mock.ExpectSAdd(setName, zsetKey).SetErr(errors.New("SAdd failed"))

		err = client.zWrite(zsetKey, "member2", 2.0, setName)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add key")
		assert.Contains(t, err.Error(), "SAdd failed")

		// Test write with TTL and set name
		ttl := 60 * time.Second
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member3", Score: 3.0}).SetVal(1)
		mock.ExpectExpire(zsetKey, ttl).SetVal(true)
		mock.ExpectSAdd(setName, zsetKey).SetVal(1)

		err = client.zWriteWithTTL(zsetKey, "member3", 3.0, setName, ttl)
		assert.NoError(t, err)

		// Test TTL set failure
		mock.ExpectZAdd(zsetKey, redis.Z{Member: "member4", Score: 4.0}).SetVal(1)
		mock.ExpectExpire(zsetKey, ttl).SetErr(errors.New("Expire failed"))

		err = client.zWriteWithTTL(zsetKey, "member4", 4.0, setName, ttl)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to set TTL")
	})

	// Test Redis connection failures for single operations
	t.Run("ConnectionFailures", func(t *testing.T) {
		// Test connection failure during read
		mock.ExpectZRevRangeWithScores("key1", 0, 4).
			SetErr(redis.ErrClosed)

		res, err := client.zReadRange("key1", 5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "redis: client is closed")
		assert.Nil(t, res)

		// Test connection failure during write
		mock.ExpectZAdd("key1", redis.Z{Member: "member1", Score: 1.0}).
			SetErr(io.EOF)

		err = client.zWrite("key1", "member1", 1.0, "")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "EOF")
	})

	// Verify all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestV6dMetadataServiceClientBatch(t *testing.T) {
	// Helper function to generate test data
	gen := func(n int) ([]string, []string, []float64) {
		keys := make([]string, n)
		members := make([]string, n)
		scores := make([]float64, n)
		for i := 0; i < n; i++ {
			keys[i] = fmt.Sprintf("zset-%03d", i)
			members[i] = fmt.Sprintf("member-%03d", i)
			scores[i] = float64(i)
		}
		return keys, members, scores
	}

	// Basic successful operations with small batch
	t.Run("SmallBatch_NoSplit", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		keys, members, scores := gen(2)
		topN := 5

		// Setup write expectations
		for i := range keys {
			mock.ExpectZAdd(keys[i], redis.Z{
				Member: members[i],
				Score:  scores[i],
			}).SetVal(1)
		}

		// Test write operation
		err := client.safeBatchZWrite(keys, members, scores)
		assert.NoError(t, err)

		// Setup read expectations with specific results
		expectedMembers := []redis.Z{
			{Member: "dummy1", Score: 1.5},
			{Member: "dummy2", Score: 2.5},
		}
		for _, k := range keys {
			mock.ExpectZRevRangeWithScores(k, 0, int64(topN-1)).SetVal(expectedMembers)
		}

		// Test read operation
		res, err := client.safeBatchZReadRange(keys, topN)
		assert.NoError(t, err)
		assert.Len(t, res, 2)

		// Verify read results
		expectedResult := map[string][]ZMember{
			keys[0]: {
				{Member: "dummy1", Score: 1.5},
				{Member: "dummy2", Score: 2.5},
			},
			keys[1]: {
				{Member: "dummy1", Score: 1.5},
				{Member: "dummy2", Score: 2.5},
			},
		}
		assert.Equal(t, expectedResult, res)

		// Setup delete member expectations
		for i := range keys {
			mock.ExpectZRem(keys[i], members[i]).SetVal(1)
		}

		// Test member deletion
		err = client.safeBatchZDelete(keys, members)
		assert.NoError(t, err)

		// Setup key deletion expectations
		for _, k := range keys {
			mock.ExpectDel(k).SetVal(1)
		}

		// Test key deletion
		err = client.safeBatchDelete(keys)
		assert.NoError(t, err)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test with larger batch that requires splitting but doesn't fill all batches
	t.Run("LargeBatch_SplitNotFull", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		keys, members, scores := gen(7)
		topN := 1

		// Setup write expectations
		for i := range keys {
			mock.ExpectZAdd(keys[i], redis.Z{
				Member: members[i],
				Score:  scores[i],
			}).SetVal(1)
		}

		// Test write operation
		err := client.safeBatchZWrite(keys, members, scores)
		assert.NoError(t, err)

		// Setup read expectations
		expectedMember := []redis.Z{{Member: "dummy", Score: 1.0}}
		for _, k := range keys {
			mock.ExpectZRevRangeWithScores(k, 0, int64(topN-1)).SetVal(expectedMember)
		}

		// Test read operation
		res, err := client.safeBatchZReadRange(keys, topN)
		assert.NoError(t, err)

		// Verify read results
		assert.Len(t, res, 7)
		for _, k := range keys {
			assert.Len(t, res[k], 1)
			assert.Equal(t, "dummy", res[k][0].Member)
			assert.Equal(t, 1.0, res[k][0].Score)
		}

		// Setup delete member expectations
		for i := range keys {
			mock.ExpectZRem(keys[i], members[i]).SetVal(1)
		}

		// Test member deletion
		err = client.safeBatchZDelete(keys, members)
		assert.NoError(t, err)

		// Setup key deletion expectations
		for _, k := range keys {
			mock.ExpectDel(k).SetVal(1)
		}

		// Test key deletion
		err = client.safeBatchDelete(keys)
		assert.NoError(t, err)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test with huge batch that completely fills multiple batches
	t.Run("HugeBatch_SplitFull", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		keys, members, scores := gen(20)
		topN := 2

		// Setup write expectations
		for i := range keys {
			mock.ExpectZAdd(keys[i], redis.Z{
				Member: members[i],
				Score:  scores[i],
			}).SetVal(1)
		}

		// Test write operation
		err := client.safeBatchZWrite(keys, members, scores)
		assert.NoError(t, err)

		// Setup read expectations with specific results
		expectedMembers := []redis.Z{
			{Member: "dummy1", Score: 10.5},
			{Member: "dummy2", Score: 9.5},
		}
		expectedResult := make(map[string][]ZMember)
		for _, k := range keys {
			mock.ExpectZRevRangeWithScores(k, 0, int64(topN-1)).SetVal(expectedMembers)
			expectedResult[k] = []ZMember{
				{Member: "dummy1", Score: 10.5},
				{Member: "dummy2", Score: 9.5},
			}
		}

		// Test read operation
		res, err := client.safeBatchZReadRange(keys, topN)
		assert.NoError(t, err)
		assert.Equal(t, expectedResult, res)

		// Setup delete member expectations
		for i := range keys {
			mock.ExpectZRem(keys[i], members[i]).SetVal(1)
		}

		// Test member deletion
		err = client.safeBatchZDelete(keys, members)
		assert.NoError(t, err)

		// Setup key deletion expectations
		for _, k := range keys {
			mock.ExpectDel(k).SetVal(1)
		}

		// Test key deletion
		err = client.safeBatchDelete(keys)
		assert.NoError(t, err)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test empty input handling
	t.Run("EmptyInputs", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Test empty read
		res1, err1 := client.safeBatchZReadRange([]string{}, 1)
		assert.NoError(t, err1)
		assert.Empty(t, res1)
		assert.Nil(t, res1)

		// Test empty write
		err2 := client.safeBatchZWrite([]string{}, []string{}, []float64{})
		assert.NoError(t, err2)

		// Test empty delete members
		err3 := client.safeBatchZDelete([]string{}, []string{})
		assert.NoError(t, err3)

		// Test empty delete keys
		err4 := client.safeBatchDelete([]string{})
		assert.NoError(t, err4)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test input validation
	t.Run("InputValidation", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Test mismatched input lengths for write
		err1 := client.safeBatchZWrite(
			[]string{"key1"},
			[]string{"member1", "member2"},
			[]float64{1.0},
		)
		assert.Error(t, err1)
		assert.Equal(t, "zsetKeys, members and scores length mismatch: 1 != 2 != 1", err1.Error())

		// Test mismatched input lengths for delete
		err2 := client.safeBatchZDelete(
			[]string{"key1", "key2"},
			[]string{"member1"},
		)
		assert.Error(t, err2)
		assert.Equal(t, "length mismatch: got 2 keys and 1 members", err2.Error())

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test Redis operation failures
	t.Run("RedisFailures", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Test ZADD failure
		mock.ExpectZAdd("key1", redis.Z{
			Member: "member1",
			Score:  1.0,
		}).SetErr(errors.New("network error"))

		err := client.safeBatchZWrite(
			[]string{"key1"},
			[]string{"member1"},
			[]float64{1.0},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "network error")
		assert.Contains(t, err.Error(), "batch 0 failed")

		// Test ZREVRANGE failure
		mock.ExpectZRevRangeWithScores("key1", 0, 4).
			SetErr(errors.New("connection refused"))

		res, err := client.safeBatchZReadRange([]string{"key1"}, 5)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "connection refused")

		// Test ZREM failure
		mock.ExpectZRem("key1", "member1").
			SetErr(errors.New("operation timeout"))

		err = client.safeBatchZDelete([]string{"key1"}, []string{"member1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "operation timeout")

		// Test DEL failure
		mock.ExpectDel("key1").
			SetErr(errors.New("server error"))

		err = client.safeBatchDelete([]string{"key1"})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "server error")

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test timeout handling
	t.Run("TimeoutHandling", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         1, // Short timeout
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Test read timeout
		mock.ExpectZRevRangeWithScores("key1", 0, 4).
			SetErr(context.DeadlineExceeded)

		res, err := client.safeBatchZReadRange([]string{"key1"}, 5)
		assert.Error(t, err)
		assert.Nil(t, res)
		assert.Contains(t, err.Error(), "context deadline exceeded")

		// Test write timeout
		mock.ExpectZAdd("key1", redis.Z{
			Member: "member1",
			Score:  1.0,
		}).SetErr(context.DeadlineExceeded)

		err = client.safeBatchZWrite(
			[]string{"key1"},
			[]string{"member1"},
			[]float64{1.0},
		)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "context deadline exceeded")

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test concurrent batch handling
	t.Run("ConcurrentBatches", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         2, // Small batch size to force multiple batches
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Create test data that will require multiple concurrent batches
		keys, members, scores := gen(6)

		// Setup write expectations
		for i := range keys {
			mock.ExpectZAdd(keys[i], redis.Z{
				Member: members[i],
				Score:  scores[i],
			}).SetVal(1)
		}

		// Test concurrent batch write
		err := client.safeBatchZWrite(keys, members, scores)
		assert.NoError(t, err)

		// Setup read expectations with specific results
		expectedMembers := []redis.Z{{Member: "test", Score: 1.0}}
		expectedResult := make(map[string][]ZMember)
		for _, k := range keys {
			mock.ExpectZRevRangeWithScores(k, 0, 0).SetVal(expectedMembers)
			expectedResult[k] = []ZMember{{Member: "test", Score: 1.0}}
		}

		// Test concurrent batch read
		res, err := client.safeBatchZReadRange(keys, 1)
		assert.NoError(t, err)
		assert.Equal(t, expectedResult, res)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test error propagation in concurrent operations
	t.Run("ErrorPropagation", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         2,
					MaxConcurrentBatches: 1,
				},
			},
		}
		defer client.close()

		// Set up test data
		keys := []string{"key1", "key2", "key3", "key4"}
		members := []string{"member1", "member2", "member3", "member4"}
		scores := []float64{1.0, 2.0, 3.0, 4.0}

		// Test write with error in second batch
		mock.ExpectZAdd(keys[0], redis.Z{Member: members[0], Score: scores[0]}).SetVal(1)
		mock.ExpectZAdd(keys[1], redis.Z{Member: members[1], Score: scores[1]}).SetVal(1)
		mock.ExpectZAdd(keys[2], redis.Z{Member: members[2], Score: scores[2]}).
			SetErr(errors.New("batch error"))

		err := client.safeBatchZWrite(keys, members, scores)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "batch error")

		mock.ExpectZRevRangeWithScores(keys[0], 0, 0).
			SetVal([]redis.Z{{Member: members[0], Score: scores[0]}})
		mock.ExpectZRevRangeWithScores(keys[1], 0, 0).
			SetVal([]redis.Z{{Member: members[1], Score: scores[1]}})

		res, err := client.safeBatchZReadRange(keys[:2], 1)
		assert.NoError(t, err)
		assert.Len(t, res, 2)

		expectedResult := map[string][]ZMember{
			keys[0]: {{Member: members[0], Score: scores[0]}},
			keys[1]: {{Member: members[1], Score: scores[1]}},
		}
		assert.Equal(t, expectedResult, res)

		keys = []string{"key5", "key6", "key7", "key8"}
		mock.ExpectZRevRangeWithScores(keys[0], 0, 0).
			SetVal([]redis.Z{{Member: "test1", Score: 1.0}})
		mock.ExpectZRevRangeWithScores(keys[1], 0, 0).
			SetVal([]redis.Z{{Member: "test2", Score: 2.0}})
		mock.ExpectZRevRangeWithScores(keys[2], 0, 0).
			SetErr(errors.New("read error"))

		res, err = client.safeBatchZReadRange(keys, 1)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "read error")
		assert.Nil(t, res)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test edge cases and special scenarios
	t.Run("EdgeCases", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Test reading with negative topN (should return all members)
		mock.ExpectZRangeWithScores("key1", 0, -1).
			SetVal([]redis.Z{
				{Member: "member1", Score: 1.0},
				{Member: "member2", Score: 2.0},
				{Member: "member3", Score: 3.0},
			})

		res, err := client.safeBatchZReadRange([]string{"key1"}, -1)
		assert.NoError(t, err)
		assert.Len(t, res["key1"], 3)
		assert.Equal(t, float64(1.0), res["key1"][0].Score)
		assert.Equal(t, float64(2.0), res["key1"][1].Score)
		assert.Equal(t, float64(3.0), res["key1"][2].Score)

		// Test reading with zero topN (should return all members)
		mock.ExpectZRangeWithScores("key1", 0, -1).
			SetVal([]redis.Z{
				{Member: "member1", Score: 1.0},
				{Member: "member2", Score: 2.0},
			})

		res, err = client.safeBatchZReadRange([]string{"key1"}, 0)
		assert.NoError(t, err)
		assert.Len(t, res["key1"], 2)

		// Test reading empty sorted set
		mock.ExpectZRevRangeWithScores("empty-key", 0, 4).
			SetVal([]redis.Z{})

		res, err = client.safeBatchZReadRange([]string{"empty-key"}, 5)
		assert.NoError(t, err)
		assert.Empty(t, res["empty-key"])

		// Test reading non-existent key
		mock.ExpectZRevRangeWithScores("non-existent", 0, 4).
			SetErr(redis.Nil)

		res, err = client.safeBatchZReadRange([]string{"non-existent"}, 5)
		assert.NoError(t, err)
		assert.Empty(t, res["non-existent"])

		// Test with invalid member type in Redis response
		mock.ExpectZRevRangeWithScores("key1", 0, 0).
			SetVal([]redis.Z{{Member: 123, Score: 1.0}}) // Invalid member type

		res, err = client.safeBatchZReadRange([]string{"key1"}, 1)
		assert.NoError(t, err)
		assert.Empty(t, res["key1"])

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test maximum batch size and concurrent limits
	t.Run("BatchLimits", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         2,
					MaxConcurrentBatches: 1, // Test with single concurrent batch
				},
			},
		}
		defer client.close()

		// Create test data that exceeds batch size
		keys, members, scores := gen(5)

		// Setup expectations for sequential processing
		for i := range keys {
			mock.ExpectZAdd(keys[i], redis.Z{
				Member: members[i],
				Score:  scores[i],
			}).SetVal(1)
		}

		// Test write with batch size and concurrency limits
		err := client.safeBatchZWrite(keys, members, scores)
		assert.NoError(t, err)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	// Test Pipeline execution failures in batch operations
	t.Run("PipelineExecFailures", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         3,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		// Test pipeline exec failure in batch read
		keys := []string{"key1", "key2"}
		mock.ExpectZRevRangeWithScores("key1", 0, 4).SetVal([]redis.Z{{Member: "m1", Score: 1.0}})
		mock.ExpectZRevRangeWithScores("key2", 0, 4).SetErr(errors.New("pipeline exec failed"))

		res, err := client.safeBatchZReadRange(keys, 5)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "pipeline exec failed")
		assert.Nil(t, res)

		// Test pipeline exec failure in batch write
		members := []string{"member1", "member2"}
		scores := []float64{1.0, 2.0}

		mock.ExpectZAdd("key1", redis.Z{Member: members[0], Score: scores[0]}).SetVal(1)
		mock.ExpectZAdd("key2", redis.Z{Member: members[1], Score: scores[1]}).
			SetErr(errors.New("pipeline exec failed"))

		err = client.safeBatchZWrite(keys, members, scores)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "pipeline exec failed")
	})

	// Test partial success/failure in concurrent batch operations
	t.Run("BatchPartialFailures", func(t *testing.T) {
		db, mock := redismock.NewClusterMock()
		mock.MatchExpectationsInOrder(false)

		client := &MetadataServiceClient{
			redisClient: db,
			config: &batch_service_redis.Config{
				RedisCluster: batch_service_redis.RedisClusterConfig{
					QueryTimeout:         5,
					MaxBatchSize:         2,
					MaxConcurrentBatches: 2,
				},
			},
		}
		defer client.close()

		keys := []string{"key1", "key2", "key3", "key4"}
		members := []string{"m1", "m2", "m3", "m4"}
		scores := []float64{1.0, 2.0, 3.0, 4.0}

		// First batch succeeds, second batch fails
		mock.ExpectZAdd(keys[0], redis.Z{Member: members[0], Score: scores[0]}).SetVal(1)
		mock.ExpectZAdd(keys[1], redis.Z{Member: members[1], Score: scores[1]}).SetVal(1)
		mock.ExpectZAdd(keys[2], redis.Z{Member: members[2], Score: scores[2]}).
			SetErr(errors.New("network error"))
		// key4 should not be processed due to error in key3

		err := client.safeBatchZWrite(keys, members, scores)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "network error")

		// Verify the first batch was processed by checking both keys
		mock.ExpectZRevRangeWithScores(keys[0], 0, 0).
			SetVal([]redis.Z{{Member: members[0], Score: scores[0]}})
		mock.ExpectZRevRangeWithScores(keys[1], 0, 0).
			SetVal([]redis.Z{{Member: members[1], Score: scores[1]}})

		res, err := client.safeBatchZReadRange(keys[:2], 1)
		assert.NoError(t, err)
		assert.Len(t, res, 2)

		expectedResult := map[string][]ZMember{
			keys[0]: {{Member: members[0], Score: scores[0]}},
			keys[1]: {{Member: members[1], Score: scores[1]}},
		}
		assert.Equal(t, expectedResult, res)

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestMetadataServiceClient_HashTokens(t *testing.T) {
	c := &MetadataServiceClient{}

	t.Run("invalid input", func(t *testing.T) {
		_, err := c.HashTokens(nil, 4, true, "iris_", "vllm_")
		assert.Error(t, err)

		_, err = c.HashTokens([]int64{1, 2, 3}, 0, true, "iris_", "vllm_")
		assert.Error(t, err)
	})

	t.Run("chunking and saveUnfullChunk", func(t *testing.T) {
		tokens := []int64{1, 2, 3, 4, 5} // chunkSize=4 => 1 full + 1 remainder

		hs1, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
		assert.NoError(t, err)
		assert.Len(t, hs1, 2)

		hs2, err := c.HashTokens(tokens, 4, false, "iris_", "vllm_")
		assert.NoError(t, err)
		assert.Len(t, hs2, 1)

		// same input should be deterministic
		hs1Again, err := c.HashTokens(tokens, 4, true, "iris_", "vllm_")
		assert.NoError(t, err)
		assert.Equal(t, hs1, hs1Again)
	})

	t.Run("prefix formatting", func(t *testing.T) {
		hs, err := c.HashTokens([]int64{1, 2, 3, 4}, 4, true, "iris_meta_", "vllm_block_")
		assert.NoError(t, err)
		assert.Len(t, hs, 1)
		assert.Contains(t, hs[0], "iris_meta_"+"vllm_block_")
	})
}

func TestMetadataServiceClient_QueryPrefixHashHitKVSInstances_ParseIP(t *testing.T) {
	db, mock := redismock.NewClusterMock()
	client := &MetadataServiceClient{
		redisClient: db,
		config: &batch_service_redis.Config{
			RedisCluster: batch_service_redis.RedisClusterConfig{
				QueryTimeout:         5,
				MaxBatchSize:         500,
				MaxConcurrentBatches: 10,
			},
		},
	}
	defer client.close()

	hashKey := "hash_1"

	// QueryPrefixHashHitKVSInstances -> zReadRange(hashKey, 0) -> ZRangeWithScores
	mock.ExpectZRangeWithScores(hashKey, 0, -1).SetVal([]redis.Z{
		{Member: "192.168.1.100:8080", Score: 0},
		{Member: "10.0.0.2:9999", Score: 0},
	})

	ips, err := client.QueryPrefixHashHitKVSInstances(hashKey)
	assert.NoError(t, err)
	assert.Equal(t, []string{"192.168.1.100", "10.0.0.2"}, ips)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMetadataServiceClient_BatchQueryPrefixHashHitKVSInstances_ParseIPAndMissingKey(t *testing.T) {
	db, mock := redismock.NewClusterMock()
	mock.MatchExpectationsInOrder(false)

	client := &MetadataServiceClient{
		redisClient: db,
		config: &batch_service_redis.Config{
			RedisCluster: batch_service_redis.RedisClusterConfig{
				QueryTimeout:         5,
				MaxBatchSize:         500,
				MaxConcurrentBatches: 10,
			},
		},
	}
	defer client.close()

	keys := []string{"hash_1", "hash_2"}

	// BatchQueryPrefixHashHitKVSInstances -> safeBatchZReadRange(keys, 0)
	mock.ExpectZRangeWithScores("hash_1", 0, -1).SetVal([]redis.Z{
		{Member: "127.0.0.1:1234", Score: 0},
	})
	// hash_2 not exists => redis.Nil
	mock.ExpectZRangeWithScores("hash_2", 0, -1).SetErr(redis.Nil)

	got, err := client.BatchQueryPrefixHashHitKVSInstances(keys)
	assert.NoError(t, err)

	// hash_1 -> ip:port => ip
	assert.Equal(t, []string{"127.0.0.1"}, got["hash_1"])
	// hash_2 missing -> should be empty slice (not nil is also ok; here check len==0)
	assert.True(t, got["hash_2"] == nil || len(got["hash_2"]) == 0)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMetadataServiceClient_BatchQueryPrefixHashHitKVSInstances_TopN(t *testing.T) {
	db, mock := redismock.NewClusterMock()
	mock.MatchExpectationsInOrder(false)

	client := &MetadataServiceClient{
		redisClient: db,
		config: &batch_service_redis.Config{
			RedisCluster: batch_service_redis.RedisClusterConfig{
				QueryTimeout:         5,
				MaxBatchSize:         500,
				MaxConcurrentBatches: 10,
			},
		},
	}
	defer client.close()

	hashKey := "hash_topn"
	mock.ExpectZRangeWithScores(hashKey, 0, -1).SetVal([]redis.Z{
		{Member: "1.1.1.1:1111", Score: 3},
		{Member: "2.2.2.2:2222", Score: 2},
	})

	ips, err := client.QueryPrefixHashHitKVSInstances(hashKey)
	assert.NoError(t, err)
	assert.Equal(t, []string{"1.1.1.1", "2.2.2.2"}, ips)

	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestMetadataServiceClient_QueryPrefixHashHitKVSInstances_MissingKey(t *testing.T) {
	db, mock := redismock.NewClusterMock()
	client := &MetadataServiceClient{
		redisClient: db,
		config: &batch_service_redis.Config{
			RedisCluster: batch_service_redis.RedisClusterConfig{
				QueryTimeout:         5,
				MaxBatchSize:         500,
				MaxConcurrentBatches: 10,
			},
		},
	}
	defer client.close()

	hashKey := "missing_hash"
	mock.ExpectZRangeWithScores(hashKey, 0, -1).SetErr(redis.Nil)

	ips, err := client.QueryPrefixHashHitKVSInstances(hashKey)
	assert.NoError(t, err)
	assert.Len(t, ips, 0)

	assert.NoError(t, mock.ExpectationsWereMet())
}
