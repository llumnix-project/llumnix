package cms

import (
	"context"
	"llumnix/pkg/redis"
	"testing"
	"time"
)

// clearTestDB clears test data from Redis
func clearTestDB(client redis.RedisClient) {
	ctx := context.Background()
	// Clear test data with instance metadata prefix
	keys, err := client.GetKeysByPrefix(ctx, LlumnixInstanceMetadataPrefix+"test:")
	if err == nil {
		for _, key := range keys {
			client.Del(ctx, key)
		}
	}

	// Clear test data with instance status prefix
	keys, err = client.GetKeysByPrefix(ctx, LlumnixInstanceStatusPrefix+"test:")
	if err == nil {
		for _, key := range keys {
			client.Del(ctx, key)
		}
	}
}

// getRedisClient attempts to create a Redis client, returns the client if successful, otherwise returns a MockRedis client
func getRedisClient(t *testing.T) redis.RedisClient {
	// Create a channel to receive connection results
	connectChan := make(chan redis.RedisClient, 1)
	errorChan := make(chan error, 1)

	// Try to connect in a goroutine
	go func() {
		redisClient, err := redis.NewRedisStandaloneClientWithRetry("127.0.0.1", "6379", "default", "", 1.0, 1)

		if err != nil {
			errorChan <- err
		} else {
			connectChan <- redisClient
		}
	}()

	// Set 1 second timeout
	select {
	case client := <-connectChan:
		// Connection successful, return real Redis client
		t.Log("Using real Redis client")
		clearTestDB(client)
		return client
	case err := <-errorChan:
		// Connection failed, return MockRedis client
		t.Logf("Failed to create Redis client: %v, using mock client", err)
		return redis.NewMockRedisClient()
	case <-time.After(1 * time.Second):
		// Connection timeout, return MockRedis client
		t.Log("Redis connection timeout, using mock client")
		return redis.NewMockRedisClient()
	}
}
