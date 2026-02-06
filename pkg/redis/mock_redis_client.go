package redis

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// MockRedisClient is a mock implementation of RedisClient for testing
type MockRedisClient struct {
	mu   sync.RWMutex
	data map[string]string
}

// NewMockRedisClient creates a new mock Redis client
func NewMockRedisClient() *MockRedisClient {
	return &MockRedisClient{
		data: make(map[string]string),
	}
}

// Set sets a value
func (m *MockRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
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

// HSet sets hash fields
func (m *MockRedisClient) HSet(ctx context.Context, key string, values map[string]string) error {
	return nil
}

// HGet gets hash fields
func (m *MockRedisClient) HGet(ctx context.Context, key string, fields ...string) (map[string]string, error) {
	return nil, nil
}

// HGetAll gets all hash fields
func (m *MockRedisClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return nil, nil
}

// Get gets a value
func (m *MockRedisClient) Get(ctx context.Context, key string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if val, exists := m.data[key]; exists {
		return val, nil
	}
	return "", nil
}

// GetBytes gets a value as bytes
func (m *MockRedisClient) GetBytes(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if val, exists := m.data[key]; exists {
		return []byte(val), nil
	}
	return nil, nil
}

// MGetBytes gets multiple values as bytes
func (m *MockRedisClient) MGetBytes(ctx context.Context, keys []string) ([][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	res := make([][]byte, len(keys))
	for i, key := range keys {
		val, err := m.Get(ctx, key)
		if err != nil || val == "" {
			res[i] = nil
		} else {
			res[i] = []byte(val)
		}
	}
	return res, nil
}

// SAdd adds members to a set
func (m *MockRedisClient) SAdd(ctx context.Context, key string, members ...any) error {
	return nil
}

// SMembers gets all members of a set
func (m *MockRedisClient) SMembers(ctx context.Context, key string) ([]string, error) {
	return nil, nil
}

// SRem removes members from a set
func (m *MockRedisClient) SRem(ctx context.Context, key string, members ...any) error {
	return nil
}

// SIsMember checks if a member exists in a set
func (m *MockRedisClient) SIsMember(ctx context.Context, key string, member any) (bool, error) {
	return false, nil
}

// SRandMember returns random members from a set
func (m *MockRedisClient) SRandMember(ctx context.Context, key string, count int64) ([]string, error) {
	return nil, nil
}

// SetNX sets a value if it doesn't exist
func (m *MockRedisClient) SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error) {
	return false, nil
}

// Del deletes keys
func (m *MockRedisClient) Del(ctx context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, key := range keys {
		delete(m.data, key)
	}
	return nil
}

// GetKeysByPrefix gets keys by prefix
func (m *MockRedisClient) GetKeysByPrefix(ctx context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	keys := make([]string, 0)
	for key := range m.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

// Eval executes a Lua script
func (m *MockRedisClient) Eval(ctx context.Context, script string, keys []string, args ...any) (any, error) {
	return nil, nil
}
