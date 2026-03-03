package cms

import (
	"fmt"
	"log"
	"sync"
	"testing"
	"time"
)

type MockRedisClient struct {
	mu   sync.RWMutex
	data map[string]string
}

func NewMockRedisClient() *MockRedisClient {
	log.Printf("New MockRedisClient")
	return &MockRedisClient{
		data: make(map[string]string),
	}
}

func (m *MockRedisClient) Set(key string, value interface{}) error {
	mu.Lock()
	defer mu.Unlock()
	// Check the type of value and handle accordingly
	switch v := value.(type) {
	case string:
		m.data[key] = v
	case []byte:
		// Convert bytes to string for storage
		m.data[key] = string(v)
	default:
		// For other types, convert to string representation
		m.data[key] = fmt.Sprintf("%v", v)
	}
	return nil
}

func (m *MockRedisClient) Get(key string) (string, error) {
	mu.RLock()
	defer mu.RUnlock()
	if val, exists := m.data[key]; exists {
		return val, nil
	}
	return "", nil
}

func (m *MockRedisClient) GetBytes(key string) ([]byte, error) {
	mu.RLock()
	defer mu.RUnlock()
	if val, exists := m.data[key]; exists {
		return []byte(val), nil
	}
	return nil, nil
}

func (m *MockRedisClient) Remove(key string) error {
	mu.Lock()
	defer mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *MockRedisClient) MGetBytes(keys []string) ([][]byte, error) {
	mu.RLock()
	defer mu.RUnlock()
	res := make([][]byte, len(keys))
	for i, key := range keys {
		val, err := m.Get(key)
		if err != nil || val == "" {
			res[i] = nil
		} else {
			res[i] = []byte(val)
		}
	}
	return res, nil
}

func (m *MockRedisClient) GetKeysByPrefix(prefix string) ([]string, error) {
	mu.RLock()
	defer mu.RUnlock()
	keys := make([]string, 0)
	for key := range m.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func clearTestDB(client RedisClientInterface) {
	mu.Lock()
	defer mu.Unlock()
	// Clear test data with instance metadata prefix
	keys, err := client.GetKeysByPrefix(LlumnixInstanceMetadataPrefix + "test:")
	if err == nil {
		for _, key := range keys {
			client.Remove(key)
		}
	}

	// Clear test data with instance status prefix
	keys, err = client.GetKeysByPrefix(LlumnixInstanceStatusPrefix + "test:")
	if err == nil {
		for _, key := range keys {
			client.Remove(key)
		}
	}
}

// getRedisClient attempts to create a Redis client, returns the client if successful, otherwise returns a MockRedis client
func getRedisClient(t *testing.T) RedisClientInterface {
	// Create a channel to receive connection results
	connectChan := make(chan *RedisClient, 1)
	errorChan := make(chan error, 1)

	// Try to connect in a goroutine
	go func() {
		redisClient, err := NewRedisClient("127.0.0.1", "6379", "default", "", 1.0, 1)

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
		return NewMockRedisClient()
	case <-time.After(1 * time.Second):
		// Connection timeout, return MockRedis client
		t.Log("Redis connection timeout, using mock client")
		return NewMockRedisClient()
	}
}

func TestRedisClient(t *testing.T) {
	// Automatically select client based on connection result
	client := getRedisClient(t)

	// Test Set and Get
	key := "test_key"
	value := "test_value"
	err := client.Set(key, value)
	if err != nil {
		t.Fatalf("Failed to set key: %v", err)
	}

	result, err := client.Get(key)
	if err != nil {
		t.Fatalf("Failed to get key: %v", err)
	}
	if result != value {
		t.Errorf("Expected %s, got %s", value, result)
	}

	// Test Get with non-existent key
	result, err = client.Get("non_existent_key")
	if err == nil && result != "" {
		t.Errorf("Expected empty result for non-existent key, got %s", result)
	}

	// Test Remove
	err = client.Remove(key)
	if err != nil {
		t.Fatalf("Failed to remove key: %v", err)
	}

	// Verify the key was removed
	result, err = client.Get(key)
	if err == nil && result != "" {
		t.Errorf("Expected empty result after removal, got %s", result)
	}

	// Try to remove non-existent key
	err = client.Remove("non_existent_key")
	if err != nil {
		t.Fatalf("Failed to remove non-existent key: %v", err)
	}

	// Test GetKeysByPrefix
	prefix := "test_prefix:"
	keys := []string{prefix + "key1", prefix + "key2", "other_key"}
	for _, k := range keys {
		client.Set(k, "value")
	}

	// Verify we can get keys by prefix
	resultKeys, err := client.GetKeysByPrefix(prefix)
	if err != nil {
		t.Fatalf("Failed to get keys by prefix: %v", err)
	}
	if len(resultKeys) != 2 {
		t.Errorf("Expected 2 keys with prefix, got %d", len(resultKeys))
	}

	foundKey1 := false
	foundKey2 := false
	for _, k := range resultKeys {
		if k == prefix+"key1" {
			foundKey1 = true
		}
		if k == prefix+"key2" {
			foundKey2 = true
		}
	}

	if !foundKey1 || !foundKey2 {
		t.Error("Not all expected keys found")
	}

	// Test GetKeysByPrefix with no matching keys
	resultKeys, err = client.GetKeysByPrefix("non_existent_prefix:")
	if err != nil {
		t.Fatalf("Failed to get keys by prefix: %v", err)
	}
	if len(resultKeys) != 0 {
		t.Errorf("Expected 0 keys with non-existent prefix, got %d", len(resultKeys))
	}
}
