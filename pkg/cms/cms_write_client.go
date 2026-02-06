package cms

import (
	"context"
	"fmt"

	"llumnix/pkg/redis"

	"google.golang.org/protobuf/proto"
	"k8s.io/klog/v2"
)

const (
	LlumnixInstanceMetadataPrefix = "llumnix:instance_metadata:"
	LlumnixInstanceStatusPrefix   = "llumnix:instance_status:"
)

// CMSWriteClient provides CMS write operation interfaces
type CMSWriteClient struct {
	redisClient redis.RedisClient
	ctx         context.Context
}

// NewCMSWriteClient creates a new CMS write client instance
func NewCMSWriteClient(redisClient redis.RedisClient) (*CMSWriteClient, error) {
	if redisClient == nil {
		return nil, fmt.Errorf("CMS redis client cannot be nil")
	}

	klog.Info("CMSWriteClient initialized")
	return &CMSWriteClient{
		redisClient: redisClient,
		ctx:         context.Background(),
	}, nil
}

// AddInstance adds a new instance with its metadata
func (c *CMSWriteClient) AddInstance(instanceID string, instanceMetadata *InstanceMetadata) error {
	klog.Infof("Adding instance: %s", instanceID)
	key := LlumnixInstanceMetadataPrefix + instanceID
	value, err := proto.Marshal(instanceMetadata)
	if err != nil {
		return err
	}
	return c.redisClient.Set(c.ctx, key, value, 0)
}

// UpdateInstanceMetadata updates instance metadata
func (c *CMSWriteClient) UpdateInstanceMetadata(instanceID string, instanceMetadata *InstanceMetadata) error {
	key := LlumnixInstanceMetadataPrefix + instanceID
	value, err := proto.Marshal(instanceMetadata)
	if err != nil {
		return err
	}
	return c.redisClient.Set(c.ctx, key, value, 0)
}

// UpdateInstanceStatus updates instance status
func (c *CMSWriteClient) UpdateInstanceStatus(instanceID string, instanceStatus *InstanceStatus) error {
	key := LlumnixInstanceStatusPrefix + instanceID
	value, err := proto.Marshal(instanceStatus)
	if err != nil {
		return err
	}
	return c.redisClient.Set(c.ctx, key, value, 0)
}

// RemoveInstance removes an instance and its metadata/status
func (c *CMSWriteClient) RemoveInstance(instanceID string) error {
	klog.Infof("Removing instance: %s", instanceID)
	// remove metadata
	err1 := c.redisClient.Del(c.ctx, LlumnixInstanceMetadataPrefix+instanceID)
	// remove status
	err2 := c.redisClient.Del(c.ctx, LlumnixInstanceStatusPrefix+instanceID)

	if err1 != nil {
		return err1
	}
	return err2
}
