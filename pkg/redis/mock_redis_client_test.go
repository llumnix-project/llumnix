package redis

import (
	"context"
	"testing"
)

func TestMockRedisClient_SetAndGet(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Test Set and Get with string value
	key := "test_key"
	value := "test_value"
	err := client.Set(ctx, key, value, 0)
	if err != nil {
		t.Fatalf("Failed to set key: %v", err)
	}

	result, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get key: %v", err)
	}
	if result != value {
		t.Errorf("Expected %s, got %s", value, result)
	}
}

func TestMockRedisClient_SetWithBytes(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Test Set with []byte value
	key := "test_bytes_key"
	value := []byte("test_bytes_value")
	err := client.Set(ctx, key, value, 0)
	if err != nil {
		t.Fatalf("Failed to set key with bytes: %v", err)
	}

	// Get as string
	result, err := client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get key: %v", err)
	}
	if result != string(value) {
		t.Errorf("Expected %s, got %s", string(value), result)
	}

	// Get as bytes
	bytesResult, err := client.GetBytes(ctx, key)
	if err != nil {
		t.Fatalf("Failed to get bytes: %v", err)
	}
	if string(bytesResult) != string(value) {
		t.Errorf("Expected %s, got %s", string(value), string(bytesResult))
	}
}

func TestMockRedisClient_GetNonExistent(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Test Get with non-existent key
	result, err := client.Get(ctx, "non_existent_key")
	if err != nil {
		t.Fatalf("Get should not return error for non-existent key: %v", err)
	}
	if result != "" {
		t.Errorf("Expected empty result for non-existent key, got %s", result)
	}

	// Test GetBytes with non-existent key
	bytesResult, err := client.GetBytes(ctx, "non_existent_key")
	if err != nil {
		t.Fatalf("GetBytes should not return error for non-existent key: %v", err)
	}
	if bytesResult != nil {
		t.Errorf("Expected nil for non-existent key, got %v", bytesResult)
	}
}

func TestMockRedisClient_Del(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Set a key
	key := "test_key"
	value := "test_value"
	client.Set(ctx, key, value, 0)

	// Verify key exists
	result, _ := client.Get(ctx, key)
	if result != value {
		t.Errorf("Key should exist before deletion")
	}

	// Delete the key
	err := client.Del(ctx, key)
	if err != nil {
		t.Fatalf("Failed to delete key: %v", err)
	}

	// Verify key was removed
	result, err = client.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get should not return error: %v", err)
	}
	if result != "" {
		t.Errorf("Expected empty result after deletion, got %s", result)
	}
}

func TestMockRedisClient_DelMultiple(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Set multiple keys
	keys := []string{"key1", "key2", "key3"}
	for _, key := range keys {
		client.Set(ctx, key, "value", 0)
	}

	// Delete multiple keys
	err := client.Del(ctx, keys...)
	if err != nil {
		t.Fatalf("Failed to delete keys: %v", err)
	}

	// Verify all keys were removed
	for _, key := range keys {
		result, _ := client.Get(ctx, key)
		if result != "" {
			t.Errorf("Key %s should be deleted", key)
		}
	}
}

func TestMockRedisClient_DelNonExistent(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Try to delete non-existent key
	err := client.Del(ctx, "non_existent_key")
	if err != nil {
		t.Fatalf("Del should not return error for non-existent key: %v", err)
	}
}

func TestMockRedisClient_GetKeysByPrefix(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Set keys with different prefixes
	prefix := "test_prefix:"
	keys := []string{prefix + "key1", prefix + "key2", "other_key"}
	for _, k := range keys {
		client.Set(ctx, k, "value", 0)
	}

	// Get keys by prefix
	resultKeys, err := client.GetKeysByPrefix(ctx, prefix)
	if err != nil {
		t.Fatalf("Failed to get keys by prefix: %v", err)
	}

	if len(resultKeys) != 2 {
		t.Errorf("Expected 2 keys with prefix, got %d", len(resultKeys))
	}

	// Verify correct keys are returned
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
}

func TestMockRedisClient_GetKeysByPrefixEmpty(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Test GetKeysByPrefix with no matching keys
	resultKeys, err := client.GetKeysByPrefix(ctx, "non_existent_prefix:")
	if err != nil {
		t.Fatalf("Failed to get keys by prefix: %v", err)
	}
	if len(resultKeys) != 0 {
		t.Errorf("Expected 0 keys with non-existent prefix, got %d", len(resultKeys))
	}
}

func TestMockRedisClient_MGetBytes(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Set multiple keys
	keys := []string{"key1", "key2", "key3"}
	values := []string{"value1", "value2", "value3"}
	for i, key := range keys {
		client.Set(ctx, key, values[i], 0)
	}

	// Get multiple values
	results, err := client.MGetBytes(ctx, keys)
	if err != nil {
		t.Fatalf("Failed to get multiple values: %v", err)
	}

	if len(results) != len(keys) {
		t.Errorf("Expected %d results, got %d", len(keys), len(results))
	}

	// Verify values
	for i, result := range results {
		if string(result) != values[i] {
			t.Errorf("Expected %s at index %d, got %s", values[i], i, string(result))
		}
	}
}

func TestMockRedisClient_MGetBytesWithNonExistent(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Set only some keys
	client.Set(ctx, "key1", "value1", 0)
	client.Set(ctx, "key3", "value3", 0)

	// Get multiple values including non-existent key
	keys := []string{"key1", "key2", "key3"}
	results, err := client.MGetBytes(ctx, keys)
	if err != nil {
		t.Fatalf("Failed to get multiple values: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Verify values
	if string(results[0]) != "value1" {
		t.Errorf("Expected value1, got %s", string(results[0]))
	}
	if results[1] != nil {
		t.Errorf("Expected nil for non-existent key, got %v", results[1])
	}
	if string(results[2]) != "value3" {
		t.Errorf("Expected value3, got %s", string(results[2]))
	}
}

func TestMockRedisClient_ConcurrentAccess(t *testing.T) {
	client := NewMockRedisClient()
	ctx := context.Background()

	// Test concurrent writes and reads
	done := make(chan bool)
	for i := 0; i < 10; i++ {
		go func(n int) {
			key := "concurrent_key"
			for j := 0; j < 100; j++ {
				client.Set(ctx, key, "value", 0)
				client.Get(ctx, key)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to finish
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify final state
	result, err := client.Get(ctx, "concurrent_key")
	if err != nil {
		t.Fatalf("Failed to get key after concurrent access: %v", err)
	}
	if result != "value" {
		t.Errorf("Expected value, got %s", result)
	}
}
