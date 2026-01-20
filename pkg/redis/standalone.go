package redis

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisStandaloneClient struct {
	client *redis.Client
}

// NewRedisStandaloneClient creates a new Redis standalone client
func NewRedisStandaloneClient(addr string, username string, password string, retryTimes int) RedisClient {
	opts := &redis.Options{
		Addr:       addr,
		Username:   username,
		Password:   password,
		MaxRetries: retryTimes,
		PoolSize:   10,
	}

	client := redis.NewClient(opts)

	return &RedisStandaloneClient{
		client: client,
	}
}

// Set sets the value of a key
func (r *RedisStandaloneClient) Set(ctx context.Context, key, value string, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

// HSet sets the value of one or more fields in a hash
func (r *RedisStandaloneClient) HSet(ctx context.Context, key string, values map[string]string) error {
	return r.client.HSet(ctx, key, values).Err()
}

// HGet gets the value of one or more fields in a hash
func (r *RedisStandaloneClient) HGet(ctx context.Context, key string, fields ...string) (map[string]string, error) {
	if len(fields) == 1 {
		// Single field case - return as a map
		value, err := r.client.HGet(ctx, key, fields[0]).Result()
		if err != nil {
			return nil, err
		}
		return map[string]string{fields[0]: value}, nil
	} else if len(fields) > 1 {
		// Multiple fields case - use HMGet
		values, err := r.client.HMGet(ctx, key, fields...).Result()
		if err != nil {
			return nil, err
		}

		result := make(map[string]string)
		for i, field := range fields {
			if values[i] != nil {
				// Handle different types that can be returned from Redis
				switch v := values[i].(type) {
				case string:
					result[field] = v
				case []byte:
					result[field] = string(v)
				default:
					// Convert other types to string representation
					result[field] = fmt.Sprintf("%v", v)
				}
			}
		}
		return result, nil
	}

	// No fields specified - return empty map
	return make(map[string]string), nil
}

// HGetAll gets all fields and values in a hash
func (r *RedisStandaloneClient) HGetAll(ctx context.Context, key string) (map[string]string, error) {
	return r.client.HGetAll(ctx, key).Result()
}

// Get gets the value of a key
func (r *RedisStandaloneClient) Get(ctx context.Context, key string) (string, error) {
	return r.client.Get(ctx, key).Result()
}

// SAdd adds one or more members to a set
func (r *RedisStandaloneClient) SAdd(ctx context.Context, key string, members ...any) error {
	return r.client.SAdd(ctx, key, members...).Err()
}

// SMembers gets all members of a set
func (r *RedisStandaloneClient) SMembers(ctx context.Context, key string) ([]string, error) {
	return r.client.SMembers(ctx, key).Result()
}

// SRem removes one or more members from a set
func (r *RedisStandaloneClient) SRem(ctx context.Context, key string, members ...any) error {
	return r.client.SRem(ctx, key, members...).Err()
}

// SIsMember checks if a member exists in a set
func (r *RedisStandaloneClient) SIsMember(ctx context.Context, key string, member any) (bool, error) {
	return r.client.SIsMember(ctx, key, member).Result()
}

// SRandMember returns one or more random members from a set
func (r *RedisStandaloneClient) SRandMember(ctx context.Context, key string, count int64) ([]string, error) {
	return r.client.SRandMemberN(ctx, key, count).Result()
}

// SetNX sets the value of a key if it doesn't exist
func (r *RedisStandaloneClient) SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error) {
	return r.client.SetNX(ctx, key, value, expiration).Result()
}

// Del deletes one or more keys
func (r *RedisStandaloneClient) Del(ctx context.Context, keys ...string) error {
	return r.client.Del(ctx, keys...).Err()
}

// Eval executes a Lua script
func (r *RedisStandaloneClient) Eval(ctx context.Context, script string, keys []string, args ...interface{}) (interface{}, error) {
	return r.client.Eval(ctx, script, keys, args...).Result()
}
