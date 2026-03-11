package policy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"llumnix/cmd/config"
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/cms"
	"llumnix/pkg/consts"
	"llumnix/pkg/redis"
	"llumnix/pkg/scheduler/hasher"
	"llumnix/pkg/scheduler/kvs"
	"llumnix/pkg/types"
)

func clearTestDB(client redis.RedisClient) {
	ctx := context.Background()
	// Clear test data with instance metadata prefix
	keys, err := client.GetKeysByPrefix(ctx, cms.LlumnixInstanceMetadataPrefix+"test:")
	if err == nil {
		for _, key := range keys {
			client.Del(ctx, key)
		}
	}

	// Clear test data with instance status prefix
	keys, err = client.GetKeysByPrefix(ctx, cms.LlumnixInstanceStatusPrefix+"test:")
	if err == nil {
		for _, key := range keys {
			client.Del(ctx, key)
		}
	}
}

// getRedisClient attempts to create a Redis client, returns the client if successful, otherwise returns a MockRedis client
func getRedisClient(t *testing.T) redis.RedisClient {
	// Create a channel to receive connection results
	connectChan := make(chan redis.RedisClient, 1)
	errorChan := make(chan error, 1)

	// Try to connect in a goroutine
	go func() {
		redisClient, err := redis.NewRedisStandaloneClientWithRetry("127.0.0.1", "6379", "", "", 1, 1)
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
		return redis.NewMockRedisClient()
	case <-time.After(1 * time.Second):
		// Connection timeout, return MockRedis client
		t.Log("Redis connection timeout, using mock client")
		return redis.NewMockRedisClient()
	}
}

func newConfig() *options.SchedulerConfig {
	return &options.SchedulerConfig{
		SchedulingBaseConfig: config.SchedulingBaseConfig{
			SchedulingPolicy:         consts.SchedulingPolicyLoadBalance,
			EnableFullModeScheduling: true,
		},
		FullModeSchedulingConfig: config.FullModeSchedulingConfig{
			CmsPullStatusIntervalMs:            500,
			CmsPullMetadataIntervalMs:          1000,
			KvsChunkSize:                       1,
			KvsHashAlgo:                        consts.KvsHashAlgoSha256Hex,
			DispatchTopK:                       1,
			DispatchNeutralLoadMetric:          consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchNeutralLoadThreshold:       1.0,
			DispatchPrefillLoadMetric:          consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchPrefillLoadThreshold:       1.0,
			DispatchDecodeLoadMetric:           consts.SchedulingMetricKVCacheUsageRatioProjected,
			DispatchDecodeLoadThreshold:        1.0,
			FailoverDomain:                     consts.FailoverDomainNodeUnit,
			InstanceStalenessSeconds:           60,
			DispatchPrefillCacheLocalityMetric: consts.SchedulingMetricKVCacheHitLen,
			EnableCacheAwareScheduling:         false,
		},
	}
}

func newDispatchPolicy(t *testing.T, config *options.SchedulerConfig, inferType consts.InferType) DispatchPolicy {
	cmsReadClient, _ := cms.NewCMSReadClient(
		getRedisClient(t), config.CmsPullStatusIntervalMs, config.CmsPullMetadataIntervalMs,
		false, config.EnableInstanceStatusLocalAccount, config.EnableCacheAwareScheduling,
		config.RequestLocalAccountStalenessSeconds, -1, false,
		config.NumPredictorWarmupSamples, false)

	var kvsClient kvs.KVSClientInterface
	var tokenHasher *hasher.TokenHasher
	if config.EnableCacheAwareScheduling {
		kvsClient = &MockKVSClient{}
		th, err := hasher.NewTokenHasher(config.KvsHashAlgo)
		if err != nil {
			t.Fatalf("failed to create TokenHasher: %v", err)
		}
		tokenHasher = th
	}

	return DispatchPolicy{
		c:                config,
		schedulingPolicy: config.SchedulingPolicy,
		cmsClient:        cmsReadClient,
		kvsClient:        kvsClient,
		tokenHasher:      tokenHasher,
		policyInternal:   newDispatchPolicyInternal(config),
	}
}

func TestDispatchPolicyName(t *testing.T) {
	c := newConfig()
	policy := newDispatchPolicy(t, c, "")
	assert.Equal(t, c.SchedulingPolicy, policy.Name())
}

func TestDispatchPolicyScheduleNeutral(t *testing.T) {
	c := newConfig()
	policy := newDispatchPolicy(t, c, "neutral")

	// Test neutral mode scheduling

	instanceViews := map[string]*instanceViewScheduling{
		"instance-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8000},
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypeNeutral] = instanceViews
	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}
	result := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModeNeutral}, clusterViewScheduling)
	assert.Len(t, result, 1)
	assert.Len(t, result[0], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8000, result[0][0].GetInstance().Endpoint.Port)
}

func TestDispatchPolicySchedulePD(t *testing.T) {
	c := newConfig()
	policy := newDispatchPolicy(t, c, "prefill")

	// Test prefill/decode mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypePrefill] = map[string]*instanceViewScheduling{
		"instance-prefill": instanceViews["instance-prefill"],
	}
	groupedInstanceViews[consts.InferTypeDecode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews["instance-decode"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	result := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch}, clusterViewScheduling)
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8001, result[0][0].GetInstance().Endpoint.Port)
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8002, result[1][0].GetInstance().Endpoint.Port)
}

func TestDispatchPolicySchedulePDMissingInstance(t *testing.T) {
	c := newConfig()
	policy := newDispatchPolicy(t, c, "prefill")

	// Test with only prefill instance (should return nil)
	instanceViews1 := map[string]*instanceViewScheduling{
		"instance-prefill": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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

	groupedInstanceViews1 := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews1[consts.InferTypePrefill] = instanceViews1

	clusterViewScheduling1 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews1,
		instanceViews:        instanceViews1,
	}

	result := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch}, clusterViewScheduling1)
	assert.Empty(t, result)

	// Test with only decode instance (should return nil)
	instanceViews2 := map[string]*instanceViewScheduling{
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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

	groupedInstanceViews2 := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews2[consts.InferTypeDecode] = instanceViews2

	clusterViewScheduling2 := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews2,
		instanceViews:        instanceViews2,
	}

	result2 := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch}, clusterViewScheduling2)
	assert.Empty(t, result2)
}

func TestCacheAwareSchedulingSchedulePD(t *testing.T) {
	c := newConfig()
	c.EnableCacheAwareScheduling = true
	policy := newDispatchPolicy(t, c, "prefill")

	// Test prefill/decode mode scheduling
	// schedule() enters cache-aware branch (tokenHasher is real), calls HashTokens and
	// getInstancesPrefixCacheHitLen. MockKVSClient returns empty data, so pre-set
	// prefixHitTokens below are preserved and drive the selector logic.
	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-1",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  10,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  1,
				prefixMissTokens: 1,
			},
		},
		"instance-prefill-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-2",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
		"instance-prefill-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8003},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-3",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8004},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypePrefill] = map[string]*instanceViewScheduling{
		"instance-prefill-1": instanceViews["instance-prefill-1"],
		"instance-prefill-2": instanceViews["instance-prefill-2"],
		"instance-prefill-3": instanceViews["instance-prefill-3"],
	}
	groupedInstanceViews[consts.InferTypeDecode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews["instance-decode"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	policy.cmsClient.Lock()
	result := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch, PromptTokenIds: []uint32{0, 1}}, clusterViewScheduling)
	policy.cmsClient.Unlock()
	assert.Len(t, result, 2)
	assert.Len(t, result[0], 1)
	assert.Len(t, result[1], 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8003, result[0][0].GetInstance().Endpoint.Port)
	assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8004, result[1][0].GetInstance().Endpoint.Port)
}

func TestCacheAwareSchedulingSchedulePDTopK(t *testing.T) {
	c := newConfig()
	c.EnableCacheAwareScheduling = true
	c.DispatchTopK = 2
	policy := newDispatchPolicy(t, c, "prefill")

	// Test prefill/decode mode scheduling with TopK
	// Same pattern as TestCacheAwareSchedulingSchedulePD: real tokenHasher + empty mock data.
	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-1",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  10,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-1",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  1,
				prefixMissTokens: 1,
			},
		},
		"instance-prefill-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-2",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-2",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
		"instance-prefill-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8003},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-3",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-3",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
		"instance-prefill-4": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8004},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-prefill-4",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-prefill-4",
					InstanceType: "prefill",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
		"instance-decode": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8005},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypePrefill] = map[string]*instanceViewScheduling{
		"instance-prefill-1": instanceViews["instance-prefill-1"],
		"instance-prefill-2": instanceViews["instance-prefill-2"],
		"instance-prefill-3": instanceViews["instance-prefill-3"],
		"instance-prefill-4": instanceViews["instance-prefill-4"],
	}
	groupedInstanceViews[consts.InferTypeDecode] = map[string]*instanceViewScheduling{
		"instance-decode": instanceViews["instance-decode"],
	}

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	req := &types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch, PromptTokenIds: []uint32{0, 1}}
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

		assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.Host)
		assert.Equal(t, "127.0.0.1", result[1][0].GetInstance().Endpoint.Host)
		assert.Equal(t, 8005, result[1][0].GetInstance().Endpoint.Port)
	}

	assert.True(t, selections[8003] > 0, "instance-prefill-3 should be selected")
	assert.True(t, selections[8004] > 0, "instance-prefill-4 should be selected")

	ratio := float64(selections[8003]) / float64(selections[8004])
	assert.True(t, ratio > 0.5 && ratio < 2.0, "selection should be roughly uniform")
}

func TestCacheAwareSchedulingScheduleNeutral(t *testing.T) {
	c := newConfig()
	c.EnableCacheAwareScheduling = true
	policy := newDispatchPolicy(t, c, "neutral")

	// Test neutral mode scheduling
	// Same pattern as TestCacheAwareSchedulingSchedulePD: real tokenHasher + empty mock data.
	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-1",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  10,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-1",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  1,
				prefixMissTokens: 1,
			},
		},
		"instance-neutral-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-2",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  30,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-2",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
		"instance-neutral-3": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8003},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-3",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  20,
					Schedulable:       true,
					TimestampMs:       time.Now().UnixMilli(),
				},
				Metadata: &cms.InstanceMetadata{
					InstanceId:   "instance-neutral-3",
					InstanceType: "neutral",
				},
			},
			schedulingCtx: schedulingCtx{
				metrics:          map[string]instanceSchedulingMetric{},
				prefixHitTokens:  2,
				prefixMissTokens: 0,
			},
		},
	}
	for _, instance := range instanceViews {
		instance.InstanceViewInterface = instance.cmsView
	}

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypeNeutral] = instanceViews

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	req := &types.SchedulingRequest{SchedulingMode: types.SchedulingModeNeutral, PromptTokenIds: []uint32{0, 1}}
	policy.cmsClient.Lock()
	result := policy.schedule(req, clusterViewScheduling)
	policy.cmsClient.Unlock()
	assert.Len(t, result, 1)
	assert.Equal(t, "127.0.0.1", result[0][0].GetInstance().Endpoint.Host)
	assert.Equal(t, 8003, result[0][0].GetInstance().Endpoint.Port)
}

func TestFloodDispatchPolicyScheduleNeutral(t *testing.T) {
	c := newConfig()
	c.SchedulingPolicy = consts.SchedulingPolicyFlood
	policy := newDispatchPolicy(t, c, "neutral")

	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8003},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8004},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8005},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-neutral5",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypeNeutral] = instanceViews

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	result1 := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModeNeutral}, clusterViewScheduling)
	assert.Len(t, result1, 1)
	assert.Len(t, result1[0], 1)

	result2 := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModeNeutral}, clusterViewScheduling)
	assert.Len(t, result2, 1)
	assert.Len(t, result2[0], 1)

	assert.Equal(t, result2[0][0].GetInstanceId(), result1[0][0].GetInstanceId())
	assert.True(t, result2[0][0].GetInstanceId() == "instance-neutral2" || result2[0][0].GetInstanceId() == "instance-neutral4")
}

func TestFloodDispatchPolicySchedulePD(t *testing.T) {
	c := newConfig()
	c.SchedulingPolicy = consts.SchedulingPolicyFlood
	policy := newDispatchPolicy(t, c, "prefill")

	instanceViews := map[string]*instanceViewScheduling{
		"instance-prefill1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypePrefill,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-prefill5",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      30,
					NumUncomputedTokensAllWaitingPrefills: 10,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode1",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode2",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode3",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode4",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeDecode,
				},
				Status: &cms.InstanceStatus{
					InstanceId:                            "instance-decode5",
					NumTotalGpuTokens:                     100,
					NumUsedGpuTokens:                      40,
					NumUncomputedTokensAllWaitingPrefills: 20,
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypePrefill] = map[string]*instanceViewScheduling{
		"instance-prefill1": instanceViews["instance-prefill1"],
		"instance-prefill2": instanceViews["instance-prefill2"],
		"instance-prefill3": instanceViews["instance-prefill3"],
		"instance-prefill4": instanceViews["instance-prefill4"],
		"instance-prefill5": instanceViews["instance-prefill5"],
	}
	groupedInstanceViews[consts.InferTypeDecode] = map[string]*instanceViewScheduling{
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

	result1 := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch}, clusterViewScheduling)
	assert.Len(t, result1, 2)
	assert.Len(t, result1[0], 1)
	assert.Len(t, result1[1], 1)
	assert.Equal(t, consts.InferTypePrefill, result1[0][0].GetInferType())
	assert.Equal(t, consts.InferTypeDecode, result1[1][0].GetInferType())

	result2 := policy.schedule(&types.SchedulingRequest{SchedulingMode: types.SchedulingModePDBatch}, clusterViewScheduling)
	assert.Len(t, result2, 2)
	assert.Len(t, result2[0], 1)
	assert.Len(t, result2[1], 1)
	assert.Equal(t, result2[0][0].GetInstanceId(), result1[0][0].GetInstanceId())
	assert.Equal(t, result2[1][0].GetInstanceId(), result1[1][0].GetInstanceId())
	assert.True(t, result2[0][0].GetInstanceId() == "instance-prefill2" || result2[0][0].GetInstanceId() == "instance-prefill4")
	assert.True(t, result2[1][0].GetInstanceId() == "instance-decode2" || result2[1][0].GetInstanceId() == "instance-decode4")
}

func TestEnableInstanceStatusLocalAccountScheduleNeutral(t *testing.T) {
	c := newConfig()
	c.EnableInstanceStatusLocalAccount = true
	policy := newDispatchPolicy(t, c, "neutral")

	// Test neutral mode scheduling
	instanceViews := map[string]*instanceViewScheduling{
		"instance-neutral-1": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8001},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-1",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  15,
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
					NumUncomputedTokensInflightDispatchPrefillRequests: 0,
				},
			},
			schedulingCtx: schedulingCtx{
				metrics: map[string]instanceSchedulingMetric{},
			},
		},
		"instance-neutral-2": {
			cmsView: &cms.InstanceView{
				Instance: &types.LLMInstance{
					Endpoint:  types.Endpoint{Host: "127.0.0.1", Port: 8002},
					InferType: consts.InferTypeNeutral,
				},
				Status: &cms.InstanceStatus{
					InstanceId:        "instance-neutral-2",
					NumTotalGpuTokens: 100,
					NumUsedGpuTokens:  30,
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
					NumUncomputedTokensInflightDispatchPrefillRequests: 0,
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

	groupedInstanceViews := make(map[consts.InferType]map[string]*instanceViewScheduling)
	groupedInstanceViews[consts.InferTypeNeutral] = instanceViews

	clusterViewScheduling := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
	}

	promptTokenIds := []uint32{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	req := &types.SchedulingRequest{
		SchedulingMode:  types.SchedulingModeNeutral,
		PromptNumTokens: len(promptTokenIds),
		PromptTokenIds:  promptTokenIds,
	}

	result1 := policy.schedule(req, clusterViewScheduling)
	assert.Equal(t, int32(1), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(10), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests)
	assert.Equal(t, 8001, result1[0][0].GetInstance().Endpoint.Port)

	// Clear metrics for next schedule
	instanceViews["instance-neutral-1"].metrics = map[string]instanceSchedulingMetric{}
	instanceViews["instance-neutral-2"].metrics = map[string]instanceSchedulingMetric{}

	result2 := policy.schedule(req, clusterViewScheduling)
	assert.Equal(t, int32(2), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(20), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests)
	assert.Equal(t, 8001, result2[0][0].GetInstance().Endpoint.Port)

	// Clear metrics for next schedule
	instanceViews["instance-neutral-1"].metrics = map[string]instanceSchedulingMetric{}
	instanceViews["instance-neutral-2"].metrics = map[string]instanceSchedulingMetric{}

	result3 := policy.schedule(req, clusterViewScheduling)
	assert.Equal(t, int32(2), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(20), instanceViews["instance-neutral-1"].cmsView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests)
	assert.Equal(t, int32(1), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(10), instanceViews["instance-neutral-2"].cmsView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests)
	assert.Equal(t, 8002, result3[0][0].GetInstance().Endpoint.Port)
}
