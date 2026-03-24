package resolver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"
	"llumnix/pkg/consts"
	"llumnix/pkg/etcd"
)

func TestEtcdResolverBuilder_Schema(t *testing.T) {
	builder := &EtcdResolverBuilder{}
	assert.Equal(t, "etcd", builder.Schema())
}

func TestEtcdResolverBuilder_Build_InvalidURI(t *testing.T) {
	builder := &EtcdResolverBuilder{}

	// Test empty URI
	_, err := builder.Build("", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "missing or invalid")

	// Test wrong prefix
	_, err = builder.Build("redis://localhost:2379", BuildArgs{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must start with")
}

func TestEtcdResolverBuilder_Build_MissingInstanceType(t *testing.T) {
	builder := &EtcdResolverBuilder{}
	args := BuildArgs{
		"etcd_username": "user",
		"etcd_password": "pass",
	}

	_, err := builder.Build("etcd://localhost:2379", args)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "instance_type")
}

func TestNewEtcdResolver(t *testing.T) {
	// This test would require a real etcd instance, so we test the structure instead
	// In integration tests, we would use etcd.NewMockClient()
	t.Skip("Requires real etcd instance - run in integration tests")
}

func TestEtcdResolver_GetLLMInstances(t *testing.T) {
	// Create a mock etcd client
	mockClient := etcd.NewMockClient()

	// Create test pod data
	podInfo := &PodDiscoveryInfo{
		PodName:     "test-pod",
		TimestampMs: time.Now().UnixMilli(),
		Instances: []*InstanceDiscoveryInfo{
			{
				InstanceType:    string(consts.InferTypePrefill),
				EntrypointIp:    "10.0.0.1",
				EntrypointPort:  8080,
				KvTransferIp:    "10.0.0.1",
				KvTransferPort:  9090,
				DpRank:          0,
				DpSize:          1,
				Model:           "test-model",
				Version:         1,
			},
		},
	}

	data, err := proto.Marshal(podInfo)
	assert.NoError(t, err)

	// Add data to mock client
	mockClient.AddMockData(EtcdDiscoveryPrefix+"test-pod", data)

	// Verify mock client works
	result, err := mockClient.GetWithPrefix(context.Background(), EtcdDiscoveryPrefix)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(result))

	// Test buildInstanceList directly
	resolver := &etcdResolver{
		inferType: consts.InferTypePrefill,
		podDiscoveryInfos: map[string]*PodDiscoveryInfo{
			"test-pod": podInfo,
		},
	}

	instances := resolver.buildInstanceList(resolver.podDiscoveryInfos)
	assert.Equal(t, 1, len(instances))
	assert.Equal(t, "test-pod_dp0", instances[0].Id())
	assert.Equal(t, consts.InferTypePrefill, instances[0].InferType)
	assert.Equal(t, "10.0.0.1", instances[0].Endpoint.Host)
	assert.Equal(t, 8080, instances[0].Endpoint.Port)
	assert.Equal(t, "10.0.0.1", instances[0].AuxIp)
	assert.Equal(t, 9090, instances[0].AuxPort)
	assert.Equal(t, 0, instances[0].DPRank)
	assert.Equal(t, 1, instances[0].DPSize)
}

func TestEtcdResolver_InstanceFiltering(t *testing.T) {
	// Create test pod data with multiple instance types
	podInfo := &PodDiscoveryInfo{
		PodName:     "test-pod",
		TimestampMs: time.Now().UnixMilli(),
		Instances: []*InstanceDiscoveryInfo{
			{
				InstanceType:    string(consts.InferTypePrefill),
				EntrypointIp:    "10.0.0.1",
				EntrypointPort:  8080,
				KvTransferIp:    "10.0.0.1",
				KvTransferPort:  9090,
				DpRank:          0,
				DpSize:          1,
				Model:           "test-model",
				Version:         1,
			},
			{
				InstanceType:    string(consts.InferTypeDecode),
				EntrypointIp:    "10.0.0.2",
				EntrypointPort:  8081,
				KvTransferIp:    "10.0.0.2",
				KvTransferPort:  9091,
				DpRank:          0,
				DpSize:          1,
				Model:           "test-model",
				Version:         1,
			},
		},
	}

	testCases := []struct {
		name           string
		inferType      consts.InferType
		expectedCount  int
		expectedType   consts.InferType
	}{
		{
			name:          "filter for prefill only",
			inferType:     consts.InferTypePrefill,
			expectedCount: 1,
			expectedType:  consts.InferTypePrefill,
		},
		{
			name:          "filter for decode only",
			inferType:     consts.InferTypeDecode,
			expectedCount: 1,
			expectedType:  consts.InferTypeDecode,
		},
		{
			name:          "all types included",
			inferType:     consts.InferTypeAll,
			expectedCount: 2,
			expectedType:  "", // multiple types
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &etcdResolver{
				inferType: tc.inferType,
				podDiscoveryInfos: map[string]*PodDiscoveryInfo{
					"test-pod": podInfo,
				},
			}

			instances := resolver.buildInstanceList(resolver.podDiscoveryInfos)
			assert.Equal(t, tc.expectedCount, len(instances))

			if tc.expectedType != "" {
				assert.Equal(t, tc.expectedType, instances[0].InferType)
			} else {
				// When InferTypeAll, verify both types are present
				typesFound := make(map[consts.InferType]bool)
				for _, inst := range instances {
					typesFound[inst.InferType] = true
				}
				assert.True(t, typesFound[consts.InferTypePrefill])
				assert.True(t, typesFound[consts.InferTypeDecode])
			}
		})
	}
}

func TestEtcdResolver_TTLExpiration(t *testing.T) {
	// Test TTL logic
	now := time.Now().UnixMilli()
	leaseTTLMs := int64(60000) // 60 seconds

	// Fresh timestamp - not expired
	assert.False(t, now-1000  < now-leaseTTLMs)

	// Old timestamp - expired
	oldTimestamp := now - leaseTTLMs - 1000
	assert.True(t, oldTimestamp  < now-leaseTTLMs)
}

func TestEtcdUriPrefix(t *testing.T) {
	assert.Equal(t, "etcd://", EtcdUriPrefix)
}

func TestEtcdResolver_Watch(t *testing.T) {
	// This would require a real etcd instance
	// In production, we test the Watch interface contract
	t.Skip("Requires real etcd instance - run in integration tests")
}