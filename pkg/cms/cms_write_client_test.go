package cms

import (
	"context"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
)

func genInstanceId(index int) string {
	return "test:instance_id" + strconv.Itoa(index)
}

func genInstanceMetadata(instanceID string, instanceType string) *InstanceMetadata {
	return &InstanceMetadata{
		InstanceId:   instanceID,
		InstanceType: instanceType,
		UtcCreate:    float64(time.Now().Unix()),
	}
}

func genInstanceStatus(instanceID string) *InstanceStatus {
	now := time.Now().UnixMilli()
	return &InstanceStatus{
		InstanceId:  instanceID,
		TimestampMs: now,
	}
}

func TestCMSWriteClient(t *testing.T) {
	redisClient := getRedisClient(t)
	cmsWriteClient, _ := NewCMSWriteClient(redisClient)
	ctx := context.Background()
	instanceID1 := genInstanceId(1)
	instanceID2 := genInstanceId(2)

	// test add instance
	instanceMetadata1 := genInstanceMetadata(instanceID1, "neutral")
	err := cmsWriteClient.AddInstance(instanceID1, instanceMetadata1)
	if err != nil {
		t.Fatalf("Failed to add instance: %v", err)
	}

	val, _ := redisClient.Get(ctx, LlumnixInstanceMetadataPrefix+instanceID1)
	data1, _ := proto.Marshal(instanceMetadata1)
	if val != string(data1) {
		t.Error("Instance metadata not found in redis or mismatch")
	}

	instanceMetadata2 := genInstanceMetadata(instanceID2, "prefill")
	err = cmsWriteClient.AddInstance(instanceID2, instanceMetadata2)
	if err != nil {
		t.Fatalf("Failed to add instance: %v", err)
	}

	val1, _ := redisClient.Get(ctx, LlumnixInstanceMetadataPrefix+instanceID1)
	data1, _ = proto.Marshal(instanceMetadata1)
	if val1 != string(data1) {
		t.Error("Instance metadata 1 not found in redis or mismatch")
	}

	val2, _ := redisClient.Get(ctx, LlumnixInstanceMetadataPrefix+instanceID2)
	data2, _ := proto.Marshal(instanceMetadata2)
	if val2 != string(data2) {
		t.Error("Instance metadata 2 not found in redis or mismatch")
	}

	// test update instance metadata
	newInstanceMetadata1 := genInstanceMetadata(instanceID1, "decode")
	err = cmsWriteClient.UpdateInstanceMetadata(instanceID1, newInstanceMetadata1)
	if err != nil {
		t.Fatalf("Failed to update instance metadata: %v", err)
	}

	val, _ = redisClient.Get(ctx, LlumnixInstanceMetadataPrefix+instanceID1)
	dataNew1, _ := proto.Marshal(newInstanceMetadata1)
	if val != string(dataNew1) {
		t.Error("Updated instance metadata not found in redis or mismatch")
	}
	instanceMetadata1 = newInstanceMetadata1

	// test update instance status
	instanceStatus1 := genInstanceStatus(instanceID1)
	err = cmsWriteClient.UpdateInstanceStatus(instanceID1, instanceStatus1)
	if err != nil {
		t.Fatalf("Failed to update instance status: %v", err)
	}

	val, _ = redisClient.Get(ctx, LlumnixInstanceStatusPrefix+instanceID1)
	statusData1, _ := proto.Marshal(instanceStatus1)
	if val != string(statusData1) {
		t.Error("Instance status not found in redis or mismatch")
	}

	instanceStatus2 := genInstanceStatus(instanceID2)
	err = cmsWriteClient.UpdateInstanceStatus(instanceID2, instanceStatus2)
	if err != nil {
		t.Fatalf("Failed to update instance status: %v", err)
	}

	val1, _ = redisClient.Get(ctx, LlumnixInstanceStatusPrefix+instanceID1)
	statusData1, _ = proto.Marshal(instanceStatus1)
	if val1 != string(statusData1) {
		t.Error("Instance status 1 not found in redis or mismatch")
	}

	val2, _ = redisClient.Get(ctx, LlumnixInstanceStatusPrefix+instanceID2)
	statusData2, _ := proto.Marshal(instanceStatus2)
	if val2 != string(statusData2) {
		t.Error("Instance status 2 not found in redis or mismatch")
	}

	// test remove instance
	err = cmsWriteClient.RemoveInstance(instanceID1)
	if err != nil {
		t.Fatalf("Failed to remove instance: %v", err)
	}

	val, _ = redisClient.Get(ctx, LlumnixInstanceMetadataPrefix+instanceID1)
	if val != "" {
		t.Error("Instance metadata should be removed from redis")
	}

	val, _ = redisClient.Get(ctx, LlumnixInstanceStatusPrefix+instanceID2)
	statusData2, _ = proto.Marshal(instanceStatus2)
	if val != string(statusData2) {
		t.Error("Instance status 2 should still exist in redis")
	}

	err = cmsWriteClient.RemoveInstance(instanceID2)
	if err != nil {
		t.Fatalf("Failed to remove instance: %v", err)
	}

	val, _ = redisClient.Get(ctx, LlumnixInstanceMetadataPrefix+instanceID1)
	if val != "" {
		t.Error("Instance metadata should be removed from redis")
	}

	val, _ = redisClient.Get(ctx, LlumnixInstanceStatusPrefix+instanceID2)
	if val != "" {
		t.Error("Instance status should be removed from redis")
	}

}
