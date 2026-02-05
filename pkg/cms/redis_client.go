package cms

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"k8s.io/klog/v2"
)

// for mockRedisClient
type RedisClientInterface interface {
	Set(key string, value interface{}) error
	Get(key string) (string, error)
	GetBytes(key string) ([]byte, error)
	MGetBytes(keys []string) ([][]byte, error)
	Remove(key string) error
	GetKeysByPrefix(prefix string) ([]string, error)
}

// RedisClient provides Redis connection and read/write operation interfaces
type RedisClient struct {
	client *redis.Client
	ctx    context.Context
}

// NewRedisClient creates a new Redis client instance
func NewRedisClient(host string,
	port string,
	username string,
	password string,
	socketTimeout float64,
	retryTimes int) (*RedisClient, error) {
	addr := host + ":" + port

	// Create Redis client
	options := &redis.Options{
		Addr:         addr,
		ReadTimeout:  time.Duration(socketTimeout * float64(time.Second)),
		WriteTimeout: time.Duration(socketTimeout * float64(time.Second)),
		MaxRetries:   retryTimes,
		PoolSize:     10,
		MinIdleConns: 5,
	}

	if username != "" {
		options.Username = username
	}

	if password != "" {
		options.Password = password
	}

	rdb := redis.NewClient(options)

	// Test connection immediately after creation
	connectRetryTimes := 3

	for i := 0; i < connectRetryTimes; i++ {
		klog.V(2).Infof("connecting to Redis at %s, %d/%d attempts", addr, i+1, connectRetryTimes)
		client := &RedisClient{
			client: rdb,
			ctx:    context.Background(),
		}
		if err := client.Connect(); err != nil {
			klog.V(2).Infof("failed to connect to Redis: %v", err)
			if i < connectRetryTimes-1 {
				time.Sleep(time.Duration(3) * time.Second)
			}
			continue
		}
		return client, nil
	}

	return nil, fmt.Errorf("failed to connect to Redis after %d attempts", connectRetryTimes)
}

// Connect establishes Redis connection and tests connection status
func (rc *RedisClient) Connect() error {
	_, err := rc.client.Ping(rc.ctx).Result()
	return err
}

// Close closes Redis connection
func (rc *RedisClient) Close() error {
	return rc.client.Close()
}

// encodeToBytes encodes string to bytes if needed
func (rc *RedisClient) encodeToBytes(key interface{}) []byte {
	switch k := key.(type) {
	case string:
		return []byte(k)
	case []byte:
		return k
	default:
		return []byte(fmt.Sprintf("%v", k))
	}
}

// decodeToStr decodes bytes to string if needed
func (rc *RedisClient) decodeToStr(key interface{}) string {
	switch k := key.(type) {
	case string:
		return k
	case []byte:
		return string(k)
	default:
		return fmt.Sprintf("%v", k)
	}
}

// Set sets a value
func (rc *RedisClient) Set(key string, value interface{}) error {
	return rc.client.Set(rc.ctx, key, value, 0).Err()
}

// Get gets a value
func (rc *RedisClient) Get(key string) (string, error) {
	return rc.client.Get(rc.ctx, key).Result()
}

func (rc *RedisClient) GetBytes(key string) ([]byte, error) {
	return rc.client.Get(rc.ctx, key).Bytes()
}

// batch get from redis and return as bytes array
func (rc *RedisClient) MGetBytes(keys []string) ([][]byte, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	results, err := rc.client.MGet(rc.ctx, keys...).Result()
	if err != nil {
		return nil, err
	}

	byteResults := make([][]byte, len(results))

	for i, result := range results {
		if result == nil {
			byteResults[i] = nil
			continue
		}

		if str, ok := result.(string); ok {
			byteResults[i] = []byte(str)
		} else {
			byteResults[i] = []byte(fmt.Sprintf("%v", result))
		}
	}

	return byteResults, nil
}

// Remove removes a key
func (rc *RedisClient) Remove(key string) error {
	return rc.client.Del(rc.ctx, key).Err()
}

// GetKeysByPrefix gets all keys matching a prefix
func (rc *RedisClient) GetKeysByPrefix(prefix string) ([]string, error) {
	var keys []string
	iter := rc.client.Scan(rc.ctx, 0, prefix+"*", 0).Iterator()
	for iter.Next(rc.ctx) {
		keys = append(keys, iter.Val())
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return keys, nil
}
