package resolver

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"llumnix/pkg/consts"
)

func TestRedisResolverBuilder_Schema(t *testing.T) {
	builder := &RedisResolverBuilder{}
	assert.Equal(t, "redis", builder.Schema())
}

func TestRedisResolverBuilder_Build_InvalidURI(t *testing.T) {
	builder := &RedisResolverBuilder{}

	// Test empty URI
	_, err := builder.Build("", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing or invalid")

	// Test wrong prefix
	_, err = builder.Build("etcd://localhost:6379", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must start with")
}

func TestRedisResolverBuilder_Build_MissingInstanceType(t *testing.T) {
	builder := &RedisResolverBuilder{}
	args := BuildArgs{
		"redis_username": "user",
		"redis_password": "pass",
	}

	_, err := builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance_type")
}

func TestRedisResolverBuilder_Build_MissingRequiredArgs(t *testing.T) {
	builder := &RedisResolverBuilder{}
	args := BuildArgs{
		"instance_type": string(consts.InferTypePrefill),
	}

	// Missing redis_username
	_, err := builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "username")

	// Missing redis_password
	args["redis_username"] = "user"
	_, err = builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "password")

	// Missing redis_socketTimeout
	args["redis_password"] = "pass"
	_, err = builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "socketTimeout")

	// Missing redis_retryTimes
	args["redis_socketTimeout"] = 1.0
	_, err = builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "retryTimes")

	// Missing redis_discovery_refresh_interval_ms
	args["redis_retryTimes"] = 3
	_, err = builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "refreshIntervalMs")

	// Missing redis_discovery_status_ttl
	args["redis_discovery_refresh_interval_ms"] = 1000
	_, err = builder.Build("redis://localhost:6379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "statusTTLMs")
}

func TestRedisUriPrefix(t *testing.T) {
	assert.Equal(t, "redis://", RedisUriPrefix)
}

func TestRedisDiscoveryPrefix(t *testing.T) {
	assert.Equal(t, "llumnix:discovery:", LlumnixDiscovery)
}
