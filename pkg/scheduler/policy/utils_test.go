package policy

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"llumnix/pkg/cms"
	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func TestCalculateMetrics(t *testing.T) {
	// Helper function to create test instance
	createInstance := func() *instanceViewScheduling {
		result := &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					NumTotalGpuTokens: 0, // make kvCacheUsageRatioProjected metric value equal to math.MaxFloat32
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId: "0",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		}
		result.InstanceViewInterface = result.cmsView
		return result
	}
	instance := createInstance()
	instances := map[string]*instanceViewScheduling{
		"0": instance,
	}
	config := newConfig()
	metrics := map[string]func() instanceSchedulingMetric{
		consts.SchedulingMetricKVCacheUsageRatioProjected: getSchedulingMetric(
			config, consts.SchedulingMetricKVCacheUsageRatioProjected),
	}
	calculateMetrics(nil, instances, metrics)
	assert.Equal(t, float32(math.MaxFloat32), instances["0"].metrics[consts.SchedulingMetricKVCacheUsageRatioProjected].GetValue())
}

func TestFilter(t *testing.T) {
	// Helper function to create test instances
	createInstance := func(instanceId, nodeId, unitId string, needsFailover bool) *instanceViewScheduling {
		result := &instanceViewScheduling{
			cmsView: &cms.InstanceView{
				Status: &cms.InstanceStatus{
					InstanceId:  instanceId,
					Schedulable: !needsFailover,
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:       instanceId,
					NodeId:           nodeId,
					UnitId:           unitId,
					DataParallelSize: 2,
				},
			},
			schedulingCtx: schedulingCtx{needsFailover: false},
		}
		result.InstanceViewInterface = result.cmsView
		return result
	}

	instanceView0 := createInstance("0", "0", "0", true)
	instanceView1 := createInstance("1", "0", "0", false)
	instanceView2 := createInstance("2", "0", "1", false)
	instanceView3 := createInstance("3", "1", "0", false)
	instanceView4 := createInstance("4", "1", "1", false)

	instanceViews := map[string]*instanceViewScheduling{
		"0": instanceView0,
		"1": instanceView1,
		"2": instanceView2,
		"3": instanceView3,
		"4": instanceView4,
	}

	sif := []singleInstanceFilter{
		&schedulabilityFilter{},
	}

	gf := []globalFilter{
		&failoverFilter{failoverDomain: consts.FailoverDomainNodeUnit},
	}

	remainingInstances := filter(instanceViews, sif, gf, false)
	assert.Equal(t, len(remainingInstances), 0)
	assert.True(t, instanceViews["0"].needsFailover)
	assert.False(t, instanceViews["1"].needsFailover)
	assert.False(t, instanceViews["2"].needsFailover)
	assert.False(t, instanceViews["3"].needsFailover)
	assert.False(t, instanceViews["4"].needsFailover)
}

func TestCalcInstancesPromptCacheHitLen(t *testing.T) {
	tests := []struct {
		name            string
		prefixHashes    []string
		chunkSize       int
		numPromptTokens int
		mockKVS         *MockKVSClient
		mockCMS         *MockCMSReadClient
		instanceViews   map[string]*instanceViewScheduling
		expectedHitLen  map[string]int
	}{
		{
			name:            "normal case with multiple instances",
			prefixHashes:    []string{"hash1", "hash2"},
			chunkSize:       50,
			numPromptTokens: 4,
			mockKVS: &MockKVSClient{
				prefixHashHitIps: map[string][]string{
					"hash1": {"1.1.1.1", "2.2.2.2"},
					"hash2": {"1.1.1.1"},
				},
			},
			mockCMS: &MockCMSReadClient{
				ipToInstanceIDsMap: map[string][]string{
					"1.1.1.1": {"instance1"},
					"2.2.2.2": {"instance2"},
				},
			},
			instanceViews: map[string]*instanceViewScheduling{
				"instance1": {
					cmsView: &cms.InstanceView{
						Instance: &types.LLMInstance{
							Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
							InferType: consts.InferTypeNeutral,
						},
						Status: &cms.InstanceStatus{
							NumTotalGpuTokens:                     100,
							NumUsedGpuTokens:                      50,
							NumUncomputedTokensAllWaitingPrefills: 10,
							Schedulable:                           true,
							TimestampMs:                           time.Now().UnixMilli(),
						},
						Metadata: &cms.InstanceMetadata{
							InstanceId: "instance1",
						},
					},
					schedulingCtx: schedulingCtx{},
				},
				"instance2": {
					cmsView: &cms.InstanceView{
						Instance: &types.LLMInstance{
							Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
							InferType: consts.InferTypeNeutral,
						},
						Status: &cms.InstanceStatus{
							NumTotalGpuTokens:                     100,
							NumUsedGpuTokens:                      50,
							NumUncomputedTokensAllWaitingPrefills: 10,
							Schedulable:                           true,
							TimestampMs:                           time.Now().UnixMilli(),
						},
						Metadata: &cms.InstanceMetadata{
							InstanceId: "instance2",
						},
					},
					schedulingCtx: schedulingCtx{},
				},
			},
			expectedHitLen: map[string]int{
				"instance1": 100,
				"instance2": 50,
			},
		},
		{
			name:            "empty prefix hashes",
			prefixHashes:    []string{},
			chunkSize:       50,
			numPromptTokens: 0,
			mockKVS: &MockKVSClient{
				prefixHashHitIps: map[string][]string{},
			},
			mockCMS: &MockCMSReadClient{},
			instanceViews: map[string]*instanceViewScheduling{
				"instance1": {
					cmsView: &cms.InstanceView{
						Instance: &types.LLMInstance{
							Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
							InferType: consts.InferTypeNeutral,
						},
						Status: &cms.InstanceStatus{
							NumTotalGpuTokens:                     100,
							NumUsedGpuTokens:                      50,
							NumUncomputedTokensAllWaitingPrefills: 10,
							Schedulable:                           true,
							TimestampMs:                           time.Now().UnixMilli(),
						},
						Metadata: &cms.InstanceMetadata{
							InstanceId: "instance1",
						},
					},
					schedulingCtx: schedulingCtx{},
				},
			},
			expectedHitLen: map[string]int{},
		},
		{
			name:            "instance not in view map",
			prefixHashes:    []string{"hash1"},
			chunkSize:       100,
			numPromptTokens: 3,
			mockKVS: &MockKVSClient{
				prefixHashHitIps: map[string][]string{
					"hash1": {"1.1.1.1", "2.2.2.2"},
				},
			},
			mockCMS: &MockCMSReadClient{
				ipToInstanceIDsMap: map[string][]string{
					"1.1.1.1": {"instance1"},
					"2.2.2.2": {"instance2"},
				},
			},
			instanceViews: map[string]*instanceViewScheduling{
				"instance1": {
					cmsView: &cms.InstanceView{
						Instance: &types.LLMInstance{
							Endpoint: types.Endpoint{Host: "127.0.0.1", Port: 8000},
							InferType: consts.InferTypeNeutral,
						},
						Status: &cms.InstanceStatus{
							NumTotalGpuTokens:                     100,
							NumUsedGpuTokens:                      50,
							NumUncomputedTokensAllWaitingPrefills: 10,
							Schedulable:                           true,
							TimestampMs:                           time.Now().UnixMilli(),
						},
						Metadata: &cms.InstanceMetadata{
							InstanceId: "instance1",
						},
					},
					schedulingCtx: schedulingCtx{},
				},
			},
			expectedHitLen: map[string]int{
				"instance1": 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, instance := range tt.instanceViews {
				instance.InstanceViewInterface = instance.cmsView
			}
			getInstancesPrefixCacheHitLen(
				tt.mockKVS, tt.mockCMS, tt.prefixHashes, tt.chunkSize,
				tt.numPromptTokens, tt.instanceViews)

			// Verify the prefixHitTokens is set correctly in instance view
			for instanceID, expectedLen := range tt.expectedHitLen {
				if view, exists := tt.instanceViews[instanceID]; exists {
					assert.Equal(t, expectedLen, view.schedulingCtx.prefixHitTokens)
					assert.Equal(t, expectedLen/tt.chunkSize, view.schedulingCtx.prefixHitTokens/tt.chunkSize)
					assert.Equal(t, float32(expectedLen)/float32(tt.numPromptTokens), view.schedulingCtx.prefixHitRatio)
				}
			}
		})
	}
}

func TestKVSClient_ConvertToPrefixHashHitInstances(t *testing.T) {
	mockCMSReadClient := &MockCMSReadClient{
		ipToInstanceIDsMap: map[string][]string{
			"192.168.1.1": {"instance1", "instance2"},
			"192.168.1.2": {"instance2", "instance3"},
			"192.168.1.3": {"instance1"},
		},
	}

	tests := []struct {
		name                      string
		prefixHashHitKVSInstances map[string][]string
		expectedInstances         map[string]sets.Set[string]
	}{
		{
			name: "normal case",
			prefixHashHitKVSInstances: map[string][]string{
				"hash1": {"192.168.1.1", "192.168.1.2"},
				"hash2": {"192.168.1.2", "192.168.1.3"},
			},
			expectedInstances: map[string]sets.Set[string]{
				"hash1": sets.New[string]("instance1", "instance2", "instance3"),
				"hash2": sets.New[string]("instance1", "instance2", "instance3"),
			},
		},
		{
			name: "empty ips",
			prefixHashHitKVSInstances: map[string][]string{
				"hash1": {},
			},
			expectedInstances: map[string]sets.Set[string]{
				"hash1": sets.New[string](),
			},
		},
		{
			name:                      "empty input",
			prefixHashHitKVSInstances: map[string][]string{},
			expectedInstances:         map[string]sets.Set[string]{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToCacheHitInstances(mockCMSReadClient, tt.prefixHashHitKVSInstances)

			if len(result) != len(tt.expectedInstances) {
				t.Errorf("Expected %d entries, got %d", len(tt.expectedInstances), len(result))
			}

			for prefixHash, expectedSet := range tt.expectedInstances {
				resultSet, exists := result[prefixHash]
				if !exists {
					t.Errorf("Expected prefix hash %s not found in result", prefixHash)
					continue
				}

				if !resultSet.Equal(expectedSet) {
					t.Errorf("For prefix hash %s: expected instances %v, got %v",
						prefixHash, expectedSet.UnsortedList(), resultSet.UnsortedList())
				}
			}
		})
	}
}

// MockKVSClient implements a mock version of kvsClient
type MockKVSClient struct {
	prefixHashHitIps map[string][]string
}

func (m *MockKVSClient) BatchQueryCacheHitKVSInstances(prefixHashes []string) map[string][]string {
	return m.prefixHashHitIps
}

func (c *MockKVSClient) IsKVSMetadataServiceDown() bool {
	return false
}

type MockCMSReadClient struct {
	ipToInstanceIDsMap map[string][]string
}

func (m *MockCMSReadClient) Unlock() {
	//TODO implement me
	panic("implement me")
}

func (m *MockCMSReadClient) Lock() {
	//TODO implement me
	panic("implement me")
}

func (m *MockCMSReadClient) RLock() {}

func (m *MockCMSReadClient) RUnlock() {}

func (m *MockCMSReadClient) GetInstanceIDs() []string { return nil }

func (m *MockCMSReadClient) GetInstanceMetadatas() map[string]*cms.InstanceMetadata { return nil }

func (m *MockCMSReadClient) GetInstanceMetadataByID(instanceID string) *cms.InstanceMetadata {
	return nil
}

func (m *MockCMSReadClient) GetInstanceStatuses() map[string]*cms.InstanceStatus { return nil }

func (m *MockCMSReadClient) GetInstanceStatusByID(instanceID string) *cms.InstanceStatus {
	return nil
}

func (m *MockCMSReadClient) GetInstanceIDsByIPs(ips []string) sets.Set[string] {
	result := sets.New[string]()
	for _, ip := range ips {
		if instances, ok := m.ipToInstanceIDsMap[ip]; ok {
			result.Insert(instances...)
		}
	}
	return result
}

func (m *MockCMSReadClient) GetInstanceViews() map[string]*cms.InstanceView {
	return nil
}

func (m *MockCMSReadClient) GetGroupedInstanceViews() map[consts.InferType]map[string]*cms.InstanceView {
	return nil
}

func TestCalcInstancesCacheHitLen_BrokenOnGap(t *testing.T) {
	chunkSize := 4

	prefixHashes := []string{"h1", "h2", "h3", "h4"}
	hit := map[string]sets.Set[string]{
		"h1": sets.New[string]("i1", "i2"),
		"h2": sets.New[string]("i1"),       // i2 gap starts here
		"h3": sets.New[string]("i1", "i2"), // i2 reappears, but should be broken and not counted further
		"h4": sets.New[string]("i1"),
	}

	got := calcInstancesPrefixCacheHitLen(chunkSize, prefixHashes, hit)

	if got["i1"] != 16 {
		t.Fatalf("i1 expected 16, got %d (map=%v)", got["i1"], got)
	}
	if got["i2"] != 4 {
		t.Fatalf("i2 expected 4, got %d (map=%v)", got["i2"], got)
	}
}
