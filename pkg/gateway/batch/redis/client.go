package redis

import (
	"context"
	"time"
)

// RedisClient defines the interface for Redis client operations
type RedisClient interface {
	// Set sets the value of a key
	Set(ctx context.Context, key, value string, expiration time.Duration) error

	// HSet sets the value of one or more fields in a hash
	HSet(ctx context.Context, key string, values map[string]string) error

	// HGet gets the value of one or more fields in a hash
	HGet(ctx context.Context, key string, fields ...string) (map[string]string, error)

	// HGetAll gets all fields and values in a hash
	HGetAll(ctx context.Context, key string) (map[string]string, error)

	// Get gets the value of a key
	Get(ctx context.Context, key string) (string, error)

	// SAdd adds one or more members to a set
	SAdd(ctx context.Context, key string, members ...any) error

	// SMembers gets all members of a set
	SMembers(ctx context.Context, key string) ([]string, error)

	// SRem removes one or more members from a set
	SRem(ctx context.Context, key string, members ...any) error

	// SIsMember checks if a member exists in a set
	SIsMember(ctx context.Context, key string, member any) (bool, error)

	// SRandMember returns one or more random members from a set
	SRandMember(ctx context.Context, key string, count int64) ([]string, error)

	// SetNX sets the value of a key if it doesn't exist
	SetNX(ctx context.Context, key, value string, expiration time.Duration) (bool, error)

	// Del deletes one or more keys
	Del(ctx context.Context, keys ...string) error

	// Eval executes a Lua script
	Eval(ctx context.Context, script string, keys []string, args ...any) (any, error)
}
