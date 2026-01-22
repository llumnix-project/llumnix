package llumnix

import (
	"fmt"
	"log"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/sets"

	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/cms"
	"easgo/pkg/llm-gateway/consts"
	kvs "easgo/pkg/llm-gateway/kvs"
	"easgo/pkg/llm-gateway/structs"
	"easgo/pkg/llm-gateway/types"
)

type MockRedisClient struct {
	data map[string]string
}

func NewMockRedisClient() *MockRedisClient {
	log.Printf("New MockRedisClient")
	return &MockRedisClient{
		data: make(map[string]string),
	}
}

func (m *MockRedisClient) Set(key string, value interface{}) error {
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

func (m *MockRedisClient) Get(key string) (string, error) {
	if val, exists := m.data[key]; exists {
		return val, nil
	}
	return "", nil
}

func (m *MockRedisClient) GetBytes(key string) ([]byte, error) {
	if val, exists := m.data[key]; exists {
		return []byte(val), nil
	}
	return nil, nil
}

func (m *MockRedisClient) Remove(key string) error {
	delete(m.data, key)
	return nil
}

func (m *MockRedisClient) MGetBytes(keys []string) ([][]byte, error) {
	res := make([][]byte, len(keys))
	for i, key := range keys {
		val, err := m.Get(key)
		if err != nil || val == "" {
			res[i] = nil
		} else {
			res[i] = []byte(val)
		}
	}
	return res, nil
}

func (m *MockRedisClient) GetKeysByPrefix(prefix string) ([]string, error) {
	keys := make([]string, 0)
	for key := range m.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}
	return keys, nil
}

func clearTestDB(client cms.RedisClientInterface) {
	// Clear test data with instance metadata prefix
	keys, err := client.GetKeysByPrefix(cms.LlumnixInstanceMetadataPrefix + "test:")
	if err == nil {
		for _, key := range keys {
			client.Remove(key)
		}
	}

	// Clear test data with instance status prefix
	keys, err = client.GetKeysByPrefix(cms.LlumnixInstanceStatusPrefix + "test:")
	if err == nil {
		for _, key := range keys {
			client.Remove(key)
		}
	}
}

// getRedisClient attempts to create a Redis client, returns the client if successful, otherwise returns a MockRedis client
func getRedisClient(t *testing.T) cms.RedisClientInterface {
	// Create a channel to receive connection results
	connectChan := make(chan *cms.RedisClient, 1)
	errorChan := make(chan error, 1)

	// Try to connect in a goroutine
	go func() {
		redisClient, err := cms.NewRedisClient("127.0.0.1", "6379", "", "", 1, 1)
		if err != nil {
			errorChan <- err
		} else {
			connectChan <- redisClient
		}
	}()

	// Set 1 second timeout
	select {
	case client := <-connectChan:
		// Connection successful, return real Redis client
		t.Log("Using real Redis client")
		clearTestDB(client)
		return client
	case err := <-errorChan:
		// Connection failed, return MockRedis client
		t.Logf("Failed to create Redis client: %v, using mock client", err)
		return NewMockRedisClient()
	case <-time.After(1 * time.Second):
		// Connection timeout, return MockRedis client
		t.Log("Redis connection timeout, using mock client")
		return NewMockRedisClient()
	}
}

func newConfig() *options.Config {
	return &options.Config{
		SchedulePolicy: consts.SchedulePolicyLoadBalance,
		LlumnixConfig: options.LlumnixConfig{
			EnableFullModeScheduling: true,

			CmsPullStatusIntervalMs:              500,
			CmsPullMetadataIntervalMs:            1000,
			DispatchTopK:                         1,
			DispatchNeutralLoadMetric:            consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills,
			DispatchNeutralLoadThreshold:         1.0,
			DispatchPrefillLoadMetric:            consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills,
			DispatchPrefillLoadThreshold:         1.0,
			DispatchDecodeLoadMetric:             consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills,
			DispatchDecodeLoadThreshold:          1.0,
			DispatchPrefillAsDecodeLoadMetric:    consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills,
			DispatchPrefillAsDecodeLoadThreshold: 1.0,
			DispatchDecodeAsPrefillLoadMetric:    consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills,
			DispatchDecodeAsPrefillLoadThreshold: 1.0,
			FailoverScope:                        consts.LlumnixFailoverScopeNodeUnit,
			InstanceStalenessSeconds:             60,
			DispatchPrefillCacheLocalityMetric:   consts.LlumnixSchedulingMetricKVCacheHitLen,
			EnableCacheAwareScheduling:           false,
		},
	}
}

func newDispatchPolicy(t *testing.T, config *options.Config, inferMode string) DispatchPolicy {
	cmsReadClient, _ := cms.NewCMSReadClient(
		getRedisClient(t), config.LlumnixConfig.CmsPullStatusIntervalMs, config.LlumnixConfig.CmsPullMetadataIntervalMs,
		false, config.LlumnixConfig.EnableInstanceStatusLocalAccount, config.LlumnixConfig.EnableCacheAwareScheduling,
		config.LlumnixConfig.RequestLocalAccountStalenessSeconds, -1, false, config.LlumnixConfig.KvCacheBlockSize, config.LlumnixConfig.NumPredictorWarmupSamples)

	var kvsClient kvs.KVSClientInterface
	if config.LlumnixConfig.EnableCacheAwareScheduling {
		allInstances := map[string][]string{
			"hash1": {"instance-prefill-1", "instance-prefill-2", "instance-prefill-3", "instance-prefill-4",
				"instance-neutral-1", "instance-neutral-2", "instance-neutral-3"},
			"hash2": {"instance-prefill-2", "instance-prefill-3", "instance-prefill-4",
				"instance-neutral-2", "instance-neutral-3"},
		}
		allHitLens := map[string]int{
			"instance-prefill-1": 1,
			"instance-prefill-2": 2,
			"instance-prefill-3": 2,
			"instance-prefill-4": 2,
			"instance-neutral-1": 1,
			"instance-neutral-2": 2,
			"instance-neutral-3": 2,
		}

		prefixHashHitInstances := make(map[string]sets.String)
		instancesCacheHitLenResp := make(map[string]int)

		for hash, instances := range allInstances {
			matchedInstances := sets.NewString()
			for _, instance := range instances {
				if strings.Contains(instance, inferMode) {
					matchedInstances.Insert(instance)
					if hitLen, exists := allHitLens[instance]; exists {
						instancesCacheHitLenResp[instance] = hitLen
					}
				}
			}
			if matchedInstances.Len() > 0 {
				prefixHashHitInstances[hash] = matchedInstances
			}
		}

		client := &MockKVSClient{
			prefixHashes:             []string{"hash1", "hash2"},
			prefixHashHitInstances:   prefixHashHitInstances,
			instancesCacheHitLenResp: instancesCacheHitLenResp,
		}
		kvsClient = client
	}

	return DispatchPolicy{
		c:                 config,
		schedulePolicy:    config.SchedulePolicy,
		cmsClient:         cmsReadClient,
		kvsClient:         kvsClient,
		policyInternal:    newDispatchPolicyInternal(config),
		schedulePipelines: newSchedulerPipeline(&config.LlumnixConfig),
	}
}

func TestDispatchPolicyName(t *testing.T) {
	config := newConfig()
	policy := newDispatchPolicy(t, config, "")
	assert.Equal(t, config.SchedulePolicy, policy.Name())
}

func TestDispatchPolicyScheduleNeutral(t *testing.T) {
	config := newConfig()
	policy := newDispatchPolicy(t, config, "neutral")

	// Test neutral mode scheduling

	instanceViews := map[string]*instanceViewScheduling{
		"instance-1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8000},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      50,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-1",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:       map[string]instanceSchedulingMetric{},
				needsFailover: false,
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.NormalInferMode] = instanceViews
	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}
	result := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling)
	assert.Len(t, result, 1)
	assert.Len(t, result[0], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8000, result[0][0].GetInstance().Endpoint.Port)
}

func TestDispatchPolicySchedulePD(t *testing.T) {
	config := newConfig()
	policy := newDispatchPolicy(t, config, "prefill")

	// Test prefill/decode mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill": instanceViews["instance-prefill"],
	}
	groupedInstanceViews[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews["instance-decode"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	result := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling)
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8001, result[0][0].GetInstance().Endpoint.Port)
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8002, result[1][0].GetInstance().Endpoint.Port)
}

func TestDispatchPolicySchedulePDMissingInstance(t *testing.T) {
	config := newConfig()
	policy := newDispatchPolicy(t, config, "prefill")

	// Test with only prefill instance (should return nil)
	instanceViews1 := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		// Missing decode instance
	}
	for _, instance := range instanceViews1 {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews1 := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews1[consts.PrefillInferMode] = instanceViews1

	clusterViewScheduling1 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews1,
		instanceViews:        instanceViews1,
	}

	result := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling1)
	assert.Empty(t, result)

	// Test with only decode instance (should return nil)
	instanceViews2 := map[string]*instanceViewScheduling{
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		// Missing prefill instance
	}
	for _, instance := range instanceViews2 {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews2 := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews2[consts.DecodeInferMode] = instanceViews2

	clusterViewScheduling2 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews2,
		instanceViews:        instanceViews2,
	}

	result2 := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling2)
	assert.Empty(t, result2)
}

func TestDispatchPolicyScheduleAdaptivePD(t *testing.T) {
	config := newConfig()
	config.LlumnixConfig.EnableAdaptivePD = true
	policy := newDispatchPolicy(t, config, "prefill")

	// No available P instances, choose D for prefill
	instanceViews1 := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews1 {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews1 := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews1[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill": instanceViews1["instance-prefill"],
	}
	groupedInstanceViews1[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews1["instance-decode"],
	}

	clusterViewScheduling1 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews1,
		instanceViews:        instanceViews1,
	}

	result := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling1)
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8002, result[0][0].GetInstance().Endpoint.Port) // Choose D for prefill
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8002, result[1][0].GetInstance().Endpoint.Port) // Choose D for decode

	// No available P instances, No available D instances, fallback to P for prefill
	// No available P instances, No available D instances, fallback to D for decode
	instanceViews2 := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews2 {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews2 := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews2[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill": instanceViews2["instance-prefill"],
	}
	groupedInstanceViews2[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews2["instance-decode"],
	}

	clusterViewScheduling2 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews2,
		instanceViews:        instanceViews2,
	}

	result = policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling2)
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8001, result[0][0].GetInstance().Endpoint.Port) // Fallback to P for prefill
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8002, result[1][0].GetInstance().Endpoint.Port) // Fallback to D for decode

	// No available D instances, choose P for decode
	instanceViews3 := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 100,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews3 {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews3 := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews3[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill": instanceViews3["instance-prefill"],
	}
	groupedInstanceViews3[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews3["instance-decode"],
	}

	clusterViewScheduling3 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews3,
		instanceViews:        instanceViews3,
	}

	result = policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling3)
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8001, result[0][0].GetInstance().Endpoint.Port) // Choose P for prefill
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8001, result[1][0].GetInstance().Endpoint.Port) // Choose P for decode
}

func TestCacheAwareSchedulingSchedulePD(t *testing.T) {
	config := newConfig()
	config.LlumnixConfig.EnableCacheAwareScheduling = true
	config.LlumnixConfig.KvCacheBlockSize = 1
	policy := newDispatchPolicy(t, config, "prefill")

	// Test prefill/decode mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-1",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  10,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-2",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-3": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8003},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-3",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8004},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill-1": instanceViews["instance-prefill-1"],
		"instance-prefill-2": instanceViews["instance-prefill-2"],
		"instance-prefill-3": instanceViews["instance-prefill-3"],
	}
	groupedInstanceViews[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews["instance-decode"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	policy.cmsClient.Lock()
	result := policy.schedule(&types.ScheduleRequest{PromptTokenIds: []uint32{0, 1}}, clusterViewScheduling)
	policy.cmsClient.Unlock()
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8003, result[0][0].GetInstance().Endpoint.Port)
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8004, result[1][0].GetInstance().Endpoint.Port)
}

func TestCacheAwareSchedulingSchedulePDTopK(t *testing.T) {
	config := newConfig()
	config.LlumnixConfig.EnableCacheAwareScheduling = true
	config.LlumnixConfig.KvCacheBlockSize = 1
	config.LlumnixConfig.DispatchTopK = 2
	policy := newDispatchPolicy(t, config, "prefill")

	// Test prefill/decode mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-1",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  10,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-2",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-3": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8003},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-3",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill-4": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8004},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-4",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-4",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8005},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode",
					InstanceType: "decode",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill-1": instanceViews["instance-prefill-1"],
		"instance-prefill-2": instanceViews["instance-prefill-2"],
		"instance-prefill-3": instanceViews["instance-prefill-3"],
		"instance-prefill-4": instanceViews["instance-prefill-4"],
	}
	groupedInstanceViews[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews["instance-decode"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	req := &types.ScheduleRequest{PromptTokenIds: []uint32{0, 1}}
	selections := make(map[int]int)
	numIterations := 100

	for i := 0; i < numIterations; i++ {
		policy.cmsClient.Lock()
		result := policy.schedule(req, clusterViewScheduling)
		policy.cmsClient.Unlock()
		assert.Len(t, result, 2)
		assert.Len(t, result[0], 1)
		assert.Len(t, result[1], 1)

		port := result[0][0].GetInstance().Endpoint.Port
		selections[port]++

		assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
		assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.IP)
		assert.Equal(t, 8005, result[1][0].GetInstance().Endpoint.Port)
	}

	assert.True(t, selections[8003] > 0, "instance-prefill-3 should be selected")
	assert.True(t, selections[8004] > 0, "instance-prefill-4 should be selected")

	ratio := float64(selections[8003]) / float64(selections[8004])
	assert.True(t, ratio > 0.5 && ratio < 2.0, "selection should be roughly uniform")
}

func TestCacheAwareSchedulingScheduleNeutral(t *testing.T) {
	config := newConfig()
	config.LlumnixConfig.EnableCacheAwareScheduling = true
	config.LlumnixConfig.KvCacheBlockSize = 1
	policy := newDispatchPolicy(t, config, "neutral")

	// Test neutral mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral-1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-1",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  10,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-1",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-2",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-2",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-3": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8003},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-3",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-3",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.NormalInferMode] = instanceViews

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	req := &types.ScheduleRequest{PromptTokenIds: []uint32{0, 1}}
	policy.cmsClient.Lock()
	result := policy.schedule(req, clusterViewScheduling)
	policy.cmsClient.Unlock()
	assert.Len(t, result, 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.IP)
	assert.Equal(t, 8003, result[0][0].GetInstance().Endpoint.Port)
}

func TestFloodDispatchPolicyScheduleNeutral(t *testing.T) {
	config := newConfig()
	config.SchedulePolicy = consts.SchedulePolicyFlood
	policy := newDispatchPolicy(t, config, "neutral")

	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral1",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral1",
					InstanceType: "neutral",
					NodeId:       "node-1",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral2",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral2",
					InstanceType: "neutral",
					NodeId:       "node-2",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral3": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8003},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral3",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral3",
					InstanceType: "neutral",
					NodeId:       "node-3",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral4": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8004},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral4",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral4",
					InstanceType: "neutral",
					NodeId:       "node-0", // 4 % 4 = 0
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral5": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8005},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral5",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral5",
					InstanceType: "neutral",
					NodeId:       "node-1", // 5 % 4 = 1
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.NormalInferMode] = instanceViews

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	result1 := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling)
	assert.Len(t, result1, 1)
	assert.Len(t, result1[0], 1)

	result2 := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling)
	assert.Len(t, result2, 1)
	assert.Len(t, result2[0], 1)

	assert.Equal(t, result2[0][0].GetInstanceId(), result1[0][0].GetInstanceId())
	assert.True(t, result2[0][0].GetInstanceId() == "instance-neutral2" || result2[0][0].GetInstanceId() == "instance-neutral4")
}

func TestFloodDispatchPolicySchedulePD(t *testing.T) {
	config := newConfig()
	config.SchedulePolicy = consts.SchedulePolicyFlood
	policy := newDispatchPolicy(t, config, "prefill")

	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill1",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill1",
					InstanceType: "prefill",
					NodeId:       "node-1", // 1 % 4 = 1
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill2",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill2",
					InstanceType: "prefill",
					NodeId:       "node-2", // 2 % 4 = 2
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill3": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill3",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill3",
					InstanceType: "prefill",
					NodeId:       "node-3", // 3 % 4 = 3
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill4": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill4",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill4",
					InstanceType: "prefill",
					NodeId:       "node-0", // 4 % 4 = 0
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-prefill5": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.PrefillInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill5",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      30,
					NumUncomputedBlocksAllWaitingPrefills: 10,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill5",
					InstanceType: "prefill",
					NodeId:       "node-1", // 5 % 4 = 1
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode1",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           false,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode1",
					InstanceType: "decode",
					NodeId:       "node-11", // 10 + 1 % 4 = 10 + 1 = 11
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode2",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode2",
					InstanceType: "decode",
					NodeId:       "node-12", // 10 + 2 % 4 = 10 + 2 = 12
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode3": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode3",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           0, // stale
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode3",
					InstanceType: "decode",
					NodeId:       "node-13", // 10 + 3 % 4 = 10 + 3 = 13
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode4": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode4",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode4",
					InstanceType: "decode",
					NodeId:       "node-10", // 10 + 4 % 4 = 10 + 0 = 10
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-decode5": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.DecodeInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode5",
					NumTotalGpuBlocks:                     100,
					NumUsedGpuBlocks:                      40,
					NumUncomputedBlocksAllWaitingPrefills: 20,
					Schedulable:                           true,
					TimestampMs:                           time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-decode5",
					InstanceType: "decode",
					NodeId:       "node-11", // 10 + 5 % 4 = 10 + 1 = 11
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.PrefillInferMode] = map[string]*instanceViewScheduling{
		"instance-prefill1": instanceViews["instance-prefill1"],
		"instance-prefill2": instanceViews["instance-prefill2"],
		"instance-prefill3": instanceViews["instance-prefill3"],
		"instance-prefill4": instanceViews["instance-prefill4"],
		"instance-prefill5": instanceViews["instance-prefill5"],
	}
	groupedInstanceViews[consts.DecodeInferMode] = map[string]*instanceViewScheduling{
		"instance-decode1": instanceViews["instance-decode1"],
		"instance-decode2": instanceViews["instance-decode2"],
		"instance-decode3": instanceViews["instance-decode3"],
		"instance-decode4": instanceViews["instance-decode4"],
		"instance-decode5": instanceViews["instance-decode5"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	result1 := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling)
	assert.Len(t, result1, 2)
	assert.Len(t, result1[0], 1)
	assert.Len(t, result1[1], 1)
	assert.Equal(t, consts.PrefillInferMode, result1[0][0].GetInferMode())
	assert.Equal(t, consts.DecodeInferMode, result1[1][0].GetInferMode())

	result2 := policy.schedule(&types.ScheduleRequest{}, clusterViewScheduling)
	assert.Len(t, result2, 2)
	assert.Len(t, result2[0], 1)
	assert.Len(t, result2[1], 1)
	assert.Equal(t, result2[0][0].GetInstanceId(), result1[0][0].GetInstanceId())
	assert.Equal(t, result2[1][0].GetInstanceId(), result1[1][0].GetInstanceId())
	assert.True(t, result2[0][0].GetInstanceId() == "instance-prefill2" || result2[0][0].GetInstanceId() == "instance-prefill4")
	assert.True(t, result2[1][0].GetInstanceId() == "instance-decode2" || result2[1][0].GetInstanceId() == "instance-decode4")
}

func TestEnableInstanceStatusLocalAccountScheduleNeutral(t *testing.T) {
	config := newConfig()
	config.LlumnixConfig.EnableInstanceStatusLocalAccount = true
	config.LlumnixConfig.KvCacheBlockSize = 1
	policy := newDispatchPolicy(t, config, "neutral")

	// Test neutral mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral-1": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8001},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-1",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  15,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-1",
					InstanceType: "neutral",
				},
				InstanceStatusLocalAccount: cms.InstanceStatusLocalAccount{
					RequestLocalAccount:                                map[string]*cms.RequestLocalAccount{},
					NumInflightDispatchRequests:                        0,
					NumInflightDispatchPrefillRequests:                 0,
					NumUncomputedBlocksInflightDispatchPrefillRequests: 0,
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-2": {
			cmsView: &cms.InstanceView{
				Token: &structs.Token{
					Endpoint:  structs.Endpoint{IP: "127.0.0.1", Port: 8002},
					InferMode: consts.NormalInferMode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-2",
					NumTotalGpuBlocks: 100,
					NumUsedGpuBlocks:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-2",
					InstanceType: "neutral",
				},
				InstanceStatusLocalAccount: cms.InstanceStatusLocalAccount{
					RequestLocalAccount:                                map[string]*cms.RequestLocalAccount{},
					NumInflightDispatchRequests:                        0,
					NumInflightDispatchPrefillRequests:                 0,
					NumUncomputedBlocksInflightDispatchPrefillRequests: 0,
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[string]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.NormalInferMode] = instanceViews

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	promptTokenIds := []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	req := &types.ScheduleRequest{PromptTokenIds: promptTokenIds}

	result1 := policy.schedule(req, clusterViewScheduling)
	assert.Equal(t, int32(1), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(10), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests)
	assert.Equal(t, 8001, result1[0][0].GetInstance().Endpoint.Port)

	// Clear metrics for next schedule
	instanceViews["instance-neutral-1"].metrics = map[string]instanceSchedulingMetric{}
	instanceViews["instance-neutral-2"].metrics = map[string]instanceSchedulingMetric{}

	result2 := policy.schedule(req, clusterViewScheduling)
	assert.Equal(t, int32(2), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(20), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests)
	assert.Equal(t, 8001, result2[0][0].GetInstance().Endpoint.Port)

	// Clear metrics for next schedule
	instanceViews["instance-neutral-1"].metrics = map[string]instanceSchedulingMetric{}
	instanceViews["instance-neutral-2"].metrics = map[string]instanceSchedulingMetric{}

	result3 := policy.schedule(req, clusterViewScheduling)
	assert.Equal(t, int32(2), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(20), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests)
	assert.Equal(t, int32(1), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(10), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests)
	assert.Equal(t, 8002, result3[0][0].GetInstance().Endpoint.Port)
}
