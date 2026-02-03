package cms

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
)

func TestCMSReadClient(t *testing.T) {
	const WaitingTimeS = 1 * time.Second

	redisClient := getRedisClient(t)
	cmsWriteClient, _ := NewCMSWriteClient(redisClient)
	cmsReadClient, _ := NewCMSReadClient(redisClient, 100, 100, false, false, false, 0, -1, false, -1)

	// Give some time for the read client to initialize
	time.Sleep(WaitingTimeS)

	instanceID1 := genInstanceId(1)
	instanceMetadata1 := genInstanceMetadata(instanceID1, "neutral")
	instanceID2 := genInstanceId(2)
	instanceMetadata2 := genInstanceMetadata(instanceID2, "prefill")

	// test add instance
	err := cmsWriteClient.AddInstance(instanceID1, instanceMetadata1)
	if err != nil {
		t.Fatalf("Failed to add instance: %v", err)
	}

	time.Sleep(WaitingTimeS)
	instanceIDs := cmsReadClient.GetInstanceIDs()
	assert.Equal(t, []string{instanceID1}, instanceIDs, "Instance IDs should match")
	if !reflect.DeepEqual(instanceIDs, []string{instanceID1}) {
		t.Errorf("Expected instance IDs [%s], got %v", instanceID1, instanceIDs)
	}

	instanceMetadatas := cmsReadClient.GetInstanceMetadatas()
	if len(instanceMetadatas) != 1 {
		t.Errorf("Expected 1 instance metadata, got %d", len(instanceMetadatas))
	}

	metadata := cmsReadClient.GetInstanceMetadataByID(instanceID1)
	if metadata == nil {
		t.Error("Expected instance metadata, got nil")
	}

	instanceStatus := cmsReadClient.GetInstanceStatuses()
	expectedStatus := map[string]*InstanceStatus{}
	if !reflect.DeepEqual(instanceStatus, expectedStatus) {
		t.Errorf("Expected  %v, got %v", expectedStatus, instanceStatus)
	}

	// test update instance metadata
	newInstanceMetadata1 := genInstanceMetadata(instanceID1, "decode")
	err = cmsWriteClient.UpdateInstanceMetadata(instanceID1, newInstanceMetadata1)
	if err != nil {
		t.Fatalf("Failed to update instance metadata: %v", err)
	}

	time.Sleep(WaitingTimeS)
	instanceIDs = cmsReadClient.GetInstanceIDs()
	if len(instanceIDs) != 1 {
		t.Errorf("Expected 1 instance ID, got %d", len(instanceIDs))
	}

	instanceMetadatas = cmsReadClient.GetInstanceMetadatas()
	if len(instanceMetadatas) != 1 {
		t.Errorf("Expected 1 instance metadata, got %d", len(instanceMetadatas))
	}

	metadata = cmsReadClient.GetInstanceMetadataByID(instanceID1)
	if metadata == nil {
		t.Error("Expected instance metadata, got nil")
	}

	instanceStatus = cmsReadClient.GetInstanceStatuses()
	expectedStatus = map[string]*InstanceStatus{}
	if !reflect.DeepEqual(instanceStatus, expectedStatus) {
		t.Errorf("Expected 1 instance status, got %d, %v", len(instanceStatus), instanceStatus)
	}
	instanceMetadata1 = newInstanceMetadata1

	// test update instance status
	instanceStatus1 := genInstanceStatus(instanceID1)
	err = cmsWriteClient.UpdateInstanceStatus(instanceID1, instanceStatus1)
	if err != nil {
		t.Fatalf("Failed to update instance status: %v", err)
	}

	time.Sleep(WaitingTimeS)
	instanceIDs = cmsReadClient.GetInstanceIDs()
	if len(instanceIDs) != 1 {
		t.Errorf("Expected 1 instance ID, got %d", len(instanceIDs))
	}

	instanceMetadatas = cmsReadClient.GetInstanceMetadatas()
	if len(instanceMetadatas) != 1 {
		t.Errorf("Expected 1 instance metadata, got %d", len(instanceMetadatas))
	}

	metadata = cmsReadClient.GetInstanceMetadataByID(instanceID1)
	if metadata == nil {
		t.Error("Expected instance metadata, got nil")
	}

	status := cmsReadClient.GetInstanceStatuses()
	if len(status) != 1 {
		t.Errorf("Expected 1 instance status, got %d", len(status))
	}

	statusByID := cmsReadClient.GetInstanceStatusByID(instanceID1)
	if statusByID == nil {
		t.Error("Expected instance status, got nil")
	}

	// test add instance2
	err = cmsWriteClient.AddInstance(instanceID2, instanceMetadata2)
	if err != nil {
		t.Fatalf("Failed to add instance: %v", err)
	}

	instanceStatus2 := genInstanceStatus(instanceID2)
	err = cmsWriteClient.UpdateInstanceStatus(instanceID2, instanceStatus2)
	if err != nil {
		t.Fatalf("Failed to update instance status: %v", err)
	}

	time.Sleep(WaitingTimeS)
	instanceIDs = cmsReadClient.GetInstanceIDs()
	if len(instanceIDs) != 2 {
		t.Errorf("Expected 2 instance IDs, got %d", len(instanceIDs))
	}

	foundID1 := false
	foundID2 := false
	for _, id := range instanceIDs {
		if id == instanceID1 {
			foundID1 = true
		}
		if id == instanceID2 {
			foundID2 = true
		}
	}

	if !foundID1 || !foundID2 {
		t.Error("Not all instance IDs found")
	}

	instanceMetadatas = cmsReadClient.GetInstanceMetadatas()
	if len(instanceMetadatas) != 2 {
		t.Errorf("Expected 2 instance metadatas, got %d", len(instanceMetadatas))
	}

	metadata1 := cmsReadClient.GetInstanceMetadataByID(instanceID1)
	metadata2 := cmsReadClient.GetInstanceMetadataByID(instanceID2)
	if metadata1 == nil || metadata2 == nil {
		t.Error("Expected both instance metadatas, got nil")
	}

	status = cmsReadClient.GetInstanceStatuses()
	if len(status) != 2 {
		t.Errorf("Expected 2 instance statuses, got %d", len(status))
	}

	status1 := cmsReadClient.GetInstanceStatusByID(instanceID1)
	status2 := cmsReadClient.GetInstanceStatusByID(instanceID2)
	if status1 == nil || status2 == nil {
		t.Error("Expected both instance statuses, got nil")
	}

	// test remove instance
	err = cmsWriteClient.RemoveInstance(instanceID1)
	if err != nil {
		t.Fatalf("Failed to remove instance: %v", err)
	}

	time.Sleep(WaitingTimeS)
	instanceIDs = cmsReadClient.GetInstanceIDs()
	if len(instanceIDs) != 1 {
		t.Errorf("Expected 1 instance ID, got %d", len(instanceIDs))
	}

	if instanceIDs[0] != instanceID2 {
		t.Error("Expected instance ID 2")
	}

	instanceMetadatas = cmsReadClient.GetInstanceMetadatas()
	expectedInstanceMetadatas := map[string]*InstanceMetadata{instanceID2: instanceMetadata2}
	if len(instanceMetadatas) != len(expectedInstanceMetadatas) || !proto.Equal(instanceMetadata2, instanceMetadatas[instanceID2]) {
		t.Errorf("Expected instance metadata %v, got %v", expectedInstanceMetadatas, instanceMetadatas)
	}

	metadata = cmsReadClient.GetInstanceMetadataByID(instanceID1)
	if metadata != nil {
		t.Error("Expected nil metadata for removed instance")
	}

	status = cmsReadClient.GetInstanceStatuses()
	expectedInstanceStatus := map[string]*InstanceStatus{instanceID2: instanceStatus2}
	if len(status) != len(expectedInstanceStatus) || !proto.Equal(instanceStatus2, status[instanceID2]) {
		t.Errorf("Expected instance status %v, got %v", expectedInstanceStatus, status)
	}

	statusByID = cmsReadClient.GetInstanceStatusByID(instanceID1)
	if statusByID != nil {
		t.Error("Expected nil status for removed instance")
	}

	// Clean up
	cmsReadClient.Close()
}

// TestRefreshLoopPerformance tests the performance of refreshLoop with 1k instances
func TestRefreshLoopPerformance(t *testing.T) {
	// Automatically select client based on connection result
	redisClient := getRedisClient(t)

	// Skip test if using mock Redis client
	if _, isMock := redisClient.(*MockRedisClient); isMock {
		t.Skip("Skipping performance test with mock Redis client")
	}

	// Number of instances to test
	numInstances := 1000

	// Clean up any existing test data
	clearTestDB(redisClient)

	// Generate test data: instance IDs, metadata and status
	instanceIDs := make([]string, numInstances)
	instanceMetadataMap := make(map[string]*InstanceMetadata)
	instanceStatusMap := make(map[string]*InstanceStatus)

	// Prepare test data
	for i := 0; i < numInstances; i++ {
		instanceID := genInstanceId(i)
		instanceIDs[i] = instanceID
		metadataKeys := LlumnixInstanceMetadataPrefix + instanceID
		statusKeys := LlumnixInstanceStatusPrefix + instanceID

		// Create metadata
		metadata := genInstanceMetadata(instanceID, "prefill")
		instanceMetadataMap[instanceID] = metadata

		// Marshal metadata to bytes
		metadataBytes, err := proto.Marshal(metadata)
		if err != nil {
			t.Fatalf("Failed to marshal metadata: %v", err)
		}

		// Create status
		status := genInstanceStatus(instanceID)
		instanceStatusMap[instanceID] = status

		// Marshal status to bytes
		statusBytes, err := proto.Marshal(status)
		if err != nil {
			t.Fatalf("Failed to marshal status: %v", err)
		}

		// Store in Redis
		err = redisClient.Set(metadataKeys, metadataBytes)
		if err != nil {
			t.Fatalf("Failed to set metadata: %v", err)
		}

		err = redisClient.Set(statusKeys, statusBytes)
		if err != nil {
			t.Fatalf("Failed to set status: %v", err)
		}
	}

	cmsReadClient, err := NewCMSReadClient(redisClient, 0, 0, false, false, false, 0, -1, false, -1)
	if err != nil {
		t.Fatalf("Failed to create CMSReadClient: %v", err)
	}

	// Measure refresh metadata time
	startTime := time.Now()
	cmsReadClient.refreshInstanceMetadata()
	elapsedTime := time.Since(startTime)
	t.Logf("Time to refresh %d instances metadata: %v", numInstances, elapsedTime)

	// Measure refresh status1 time
	startTime = time.Now()
	cmsReadClient.refreshInstanceStatus(false)
	elapsedTime = time.Since(startTime)
	t.Logf("Time to refresh %d instances status: %v", numInstances, elapsedTime)

	// Verify that data was loaded correctly
	loadedInstanceIDs := cmsReadClient.GetInstanceIDs()
	if len(loadedInstanceIDs) != numInstances {
		t.Errorf("Expected %d instance IDs, got %d", numInstances, len(loadedInstanceIDs))
	}

	// Check a few instances to verify data integrity
	for i := 0; i < 10; i++ { // Check first 10 instances
		instanceID := genInstanceId(i)

		metadata := cmsReadClient.GetInstanceMetadataByID(instanceID)
		if !proto.Equal(metadata, instanceMetadataMap[instanceID]) {
			t.Errorf("Expected metadata except %v, got %v", instanceMetadataMap[instanceID], metadata)
		}

		status := cmsReadClient.GetInstanceStatusByID(instanceID)
		if !proto.Equal(status, instanceStatusMap[instanceID]) {
			t.Errorf("Expected metadata except %v, got %v", instanceStatusMap[instanceID], status)
		}
	}

	// Clean up
	cmsReadClient.Close()
}

func TestCMSReadClientIPMapping(t *testing.T) {
	redisClient := getRedisClient(t)
	cmsWriteClient, _ := NewCMSWriteClient(redisClient)
	cmsReadClient, _ := NewCMSReadClient(redisClient, 100, 100, false, false, false, 0, -1, false, -1)

	const WaitingTimeS = 1 * time.Second
	time.Sleep(WaitingTimeS)

	// Test case for empty IP
	instanceID := genInstanceId(1)
	emptyIPMetadata := &InstanceMetadata{
		InstanceId: instanceID,
		IpKvs:      "",
	}
	err := cmsWriteClient.AddInstance(instanceID, emptyIPMetadata)
	assert.NoError(t, err)
	time.Sleep(WaitingTimeS)

	// Verify empty IP should not be mapped
	instances := cmsReadClient.GetInstanceIDsByIPs([]string{""})
	assert.Equal(t, 0, instances.Len(), "Empty IP should not map to any instance")

	// Force refresh to ensure data is updated
	cmsReadClient.refreshInstanceMetadata()
	time.Sleep(WaitingTimeS)

	// Test IP update
	newIP := "192.168.1.1"
	updatedMetadata := &InstanceMetadata{
		InstanceId: instanceID,
		IpKvs:      newIP,
	}
	err = cmsWriteClient.UpdateInstanceMetadata(instanceID, updatedMetadata)
	assert.NoError(t, err)

	// Force refresh and wait
	cmsReadClient.refreshInstanceMetadata()
	time.Sleep(WaitingTimeS)

	instances = cmsReadClient.GetInstanceIDsByIPs([]string{newIP})
	assert.True(t, instances.Has(instanceID), "Updated IP should map to instance")

	// Test multiple instances mapping to the same IP
	instanceID2 := genInstanceId(2)
	sameIPMetadata := &InstanceMetadata{
		InstanceId: instanceID2,
		IpKvs:      newIP,
	}
	err = cmsWriteClient.AddInstance(instanceID2, sameIPMetadata)
	assert.NoError(t, err)

	// Force refresh and wait
	cmsReadClient.refreshInstanceMetadata()
	time.Sleep(WaitingTimeS)

	instances = cmsReadClient.GetInstanceIDsByIPs([]string{newIP})
	assert.True(t, instances.Has(instanceID), "IP should still map to first instance")
	assert.True(t, instances.Has(instanceID2), "IP should also map to second instance")
	assert.Equal(t, 2, instances.Len(), "IP should map to exactly two instances")

	// Test instance removal and IP mapping cleanup
	err = cmsWriteClient.RemoveInstance(instanceID)
	assert.NoError(t, err)

	// Force refresh and wait
	cmsReadClient.refreshInstanceMetadata()
	time.Sleep(WaitingTimeS)

	instances = cmsReadClient.GetInstanceIDsByIPs([]string{newIP})
	assert.False(t, instances.Has(instanceID), "Removed instance should not be mapped")
	assert.True(t, instances.Has(instanceID2), "Other instance should still be mapped")
	assert.Equal(t, 1, instances.Len(), "Should have exactly one instance mapped")

	// Test multiple IPs query
	instanceID3 := genInstanceId(3)
	anotherIP := "192.168.1.2"
	multiIPMetadata := &InstanceMetadata{
		InstanceId: instanceID3,
		IpKvs:      anotherIP,
	}
	err = cmsWriteClient.AddInstance(instanceID3, multiIPMetadata)
	assert.NoError(t, err)

	// Force refresh and wait
	cmsReadClient.refreshInstanceMetadata()
	time.Sleep(WaitingTimeS)

	// Query multiple IPs at once
	multiInstances := cmsReadClient.GetInstanceIDsByIPs([]string{newIP, anotherIP})
	assert.True(t, multiInstances.Has(instanceID2), "Should find instance from first IP")
	assert.True(t, multiInstances.Has(instanceID3), "Should find instance from second IP")
	assert.Equal(t, 2, multiInstances.Len(), "Should find exactly two instances")

	// Test invalid IP format
	invalidIPs := []string{
		"256.256.256.256", // Invalid IPv4
		"not-an-ip",       // Not an IP at all
		":::1",            // Malformed IPv6
	}

	invalidIPInstances := cmsReadClient.GetInstanceIDsByIPs(invalidIPs)
	assert.Equal(t, 0, invalidIPInstances.Len(), "Invalid IPs should not return any instances")

	// Test mixed valid and invalid IPs
	mixedInstances := cmsReadClient.GetInstanceIDsByIPs(append(invalidIPs, newIP))
	assert.True(t, mixedInstances.Has(instanceID2), "Should find instance from valid IP")
	assert.Equal(t, 1, mixedInstances.Len(), "Should only find instances from valid IP")

	// Clean up
	cmsReadClient.Close()
}
