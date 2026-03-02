package lrs

import (
	"fmt"
	"llumnix/cmd/scheduler/app/options"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func createTestInstanceWithInferType(id string, inferType consts.InferType) *types.LLMInstance {
	return &types.LLMInstance{
		Version: 1,
		ID:      id,
		Endpoint: types.Endpoint{
			Host: "test-host",
			Port: 8080,
		},
		InferType: inferType,
	}
}

func TestLocalRealtimeStateClientConcurrency(t *testing.T) {
	testCases := []struct {
		name      string
		inferType consts.InferType
	}{
		{
			name:      "NeutralInferType",
			inferType: consts.InferTypeNeutral,
		},
		{
			name:      "PrefillInferType",
			inferType: consts.InferTypePrefill,
		},
		{
			name:      "DecodeInferType",
			inferType: consts.InferTypeDecode,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{})
			instance := createTestInstanceWithInferType("instance-1", tc.inferType)
			gateway := "gateway-1"
			scsClient.AddInstance(instance)
			scsClient.AddGateway(gateway)

			const numRequests = 100
			var wg sync.WaitGroup
			wg.Add(numRequests)

			for i := 0; i < numRequests; i++ {
				go func(reqNum int) {
					defer wg.Done()
					reqId := fmt.Sprintf("req-%d", reqNum)

					// 1. Allocate
					allocState := &RequestState{
						reqId:      reqId,
						numTokens:  100,
						instanceId: instance.Id(),
						gatewayId:  gateway,
						updateTime: time.Now(),
					}
					scsClient.AllocateRequestState(tc.inferType, allocState)

					// 2. update
					for j := 0; j < rand.Intn(5)+1; j++ {
						updateState := &RequestState{
							reqId:      reqId,
							numTokens:  int64(100 + (j+1)*50),
							instanceId: instance.Id(),
							gatewayId:  gateway,
							updateTime: time.Now(),
						}
						scsClient.UpdateRequestState(tc.inferType, updateState)
						time.Sleep(time.Millisecond * time.Duration(rand.Intn(5)))
					}

					// 3. Release
					releaseState := &RequestState{
						reqId:      reqId,
						numTokens:  0,
						instanceId: instance.Id(),
						gatewayId:  gateway,
					}
					scsClient.ReleaseRequestState(tc.inferType, releaseState)
				}(i)
			}

			wg.Wait()

			instanceState := scsClient.GetInstanceView(tc.inferType, instance.Id())
			assert.NotNil(t, instanceState)
			assert.Equal(t, int64(0), instanceState.NumRequests())
			assert.Equal(t, int64(0), instanceState.NumTokens())
		})
	}
}

func TestScheduelrStateStore(t *testing.T) {
	scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{})
	gateway := "gateway-1"

	t.Run("Instance Creation for Different Infer Types", func(t *testing.T) {
		// Test neutral type
		neutralInstance := createTestInstanceWithInferType("instance-1", consts.InferTypeNeutral)
		scsClient.AddInstance(neutralInstance)
		stats := scsClient.GetInstanceView(consts.InferTypeNeutral, neutralInstance.Id())
		assert.NotNil(t, stats)

		// Test prefill type
		prefillInstance := createTestInstanceWithInferType("instance-2", consts.InferTypePrefill)
		scsClient.AddInstance(prefillInstance)
		stats = scsClient.GetInstanceView(consts.InferTypePrefill, prefillInstance.Id())
		assert.NotNil(t, stats)

		// Test decode type
		decodeInstance := createTestInstanceWithInferType("instance-3", consts.InferTypeDecode)
		scsClient.AddInstance(decodeInstance)
		stats = scsClient.GetInstanceView(consts.InferTypeDecode, decodeInstance.Id())
		assert.NotNil(t, stats)
	})

	t.Run("Instance Removal for Different Infer Types", func(t *testing.T) {
		// Test neutral type deletion
		scsClient.RemoveInstance(consts.InferTypeNeutral, "instance-1")
		stats := scsClient.GetInstanceView(consts.InferTypeNeutral, "instance-1")
		assert.Nil(t, stats)

		// Test prefill type deletion
		scsClient.RemoveInstance(consts.InferTypePrefill, "instance-2")
		stats = scsClient.GetInstanceView(consts.InferTypePrefill, "instance-2")
		assert.Nil(t, stats)

		// Test decode type deletion
		scsClient.RemoveInstance(consts.InferTypeDecode, "instance-3")
		stats = scsClient.GetInstanceView(consts.InferTypeDecode, "instance-3")
		assert.Nil(t, stats)
	})

	t.Run("Gateway Operations", func(t *testing.T) {
		// Add gateway
		scsClient.AddGateway(gateway)

		inferTypes := []consts.InferType{consts.InferTypeNeutral, consts.InferTypePrefill, consts.InferTypeDecode}
		for _, inferType := range inferTypes {
			// Create test requests for each infer type
			instance := createTestInstanceWithInferType("instance-"+string(inferType), inferType)
			scsClient.AddInstance(instance)

			reqState := &RequestState{
				reqId:      "req-1",
				numTokens:  100,
				instanceId: instance.Id(),
				gatewayId:  gateway,
				updateTime: time.Now(),
			}

			// Test allocate
			err := scsClient.AllocateRequestState(inferType, reqState)
			assert.NoError(t, err)

			// Test update
			reqState.numTokens = 150
			err = scsClient.UpdateRequestState(inferType, reqState)
			assert.NoError(t, err)

			// Test release
			scsClient.ReleaseRequestState(inferType, reqState)
		}

		// Remove gateway
		scsClient.RemoveGateway(gateway)
	})

	t.Run("GetInstanceViews for Different Infer Types", func(t *testing.T) {
		for _, inferType := range []consts.InferType{consts.InferTypeNeutral, consts.InferTypePrefill, consts.InferTypeDecode} {
			instanceViews := scsClient.GetInstanceViews(inferType)
			assert.NotNil(t, instanceViews)
		}
	})

	t.Run("Metrics and Stats Output", func(t *testing.T) {
		// Add some test data
		instance := createTestInstanceWithInferType("instance-5", consts.InferTypeNeutral)
		scsClient.AddInstance(instance)
		scsClient.AddGateway(gateway)

		reqState := &RequestState{
			reqId:      "req-2",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		scsClient.AllocateRequestState(consts.InferTypeNeutral, reqState)

		// Test metrics submission
		// scsClient.SubmitMetric()

		// Test stats printing
		scsClient.PrintInstanceViews()
	})

	t.Run("Error Cases", func(t *testing.T) {
		// Test allocate with non-existent instance
		reqState := &RequestState{
			reqId:      "req-3",
			numTokens:  100,
			instanceId: "non-existent-instance",
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := scsClient.AllocateRequestState(consts.InferTypeNeutral, reqState)
		assert.Error(t, err)

		// Test update with non-existent request
		err = scsClient.UpdateRequestState(consts.InferTypeNeutral, reqState)
		assert.Error(t, err)

		// Test operations with invalid infer type
		err = scsClient.AllocateRequestState("invalid-type", reqState)
		assert.Error(t, err)
	})

	t.Run("Concurrent Infer Type Operations", func(t *testing.T) {
		instance := createTestInstanceWithInferType("instance-6", consts.InferTypeNeutral)
		scsClient.AddInstance(instance)
		scsClient.AddGateway(gateway)

		done := make(chan bool)
		for i := 0; i < 3; i++ {
			go func(idx int) {
				inferType := consts.InferTypeNeutral
				switch idx {
				case 1:
					inferType = consts.InferTypePrefill
				case 2:
					inferType = consts.InferTypeDecode
				}

				reqState := &RequestState{
					reqId:      "concurrent-req",
					numTokens:  100,
					instanceId: instance.Id(),
					gatewayId:  gateway,
					updateTime: time.Now(),
				}

				scsClient.AllocateRequestState(inferType, reqState)
				scsClient.UpdateRequestState(inferType, reqState)
				scsClient.ReleaseRequestState(inferType, reqState)
				done <- true
			}(i)
		}

		for i := 0; i < 3; i++ {
			<-done
		}
	})

	t.Run("GetInstanceViewsByModel with MultiModelSupport", func(t *testing.T) {
		// Create a scsClient that supports multiple models
		scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{MultiModelSupport: true})
		gateway := "gateway-1"

		// Create instances with different models and different modes
		gpt35NeutralInstance := createTestInstanceWithInferTypeAndModel("instance-gpt35-neutral", consts.InferTypeNeutral, "gpt-3.5-turbo")
		gpt35PrefillInstance := createTestInstanceWithInferTypeAndModel("instance-gpt35-prefill", consts.InferTypePrefill, "gpt-3.5-turbo")
		gpt35DecodeInstance := createTestInstanceWithInferTypeAndModel("instance-gpt35-decode", consts.InferTypeDecode, "gpt-3.5-turbo")
		gpt4NeutralInstance := createTestInstanceWithInferTypeAndModel("instance-gpt4-neutral", consts.InferTypeNeutral, "gpt-4")
		claudeNeutralInstance := createTestInstanceWithInferTypeAndModel("instance-claude-neutral", consts.InferTypeNeutral, "claude-2")

		// Add all instances
		instances := []*types.LLMInstance{gpt35NeutralInstance, gpt35PrefillInstance, gpt35DecodeInstance, gpt4NeutralInstance, claudeNeutralInstance}
		for _, instance := range instances {
			scsClient.AddInstance(instance)
		}
		scsClient.AddGateway(gateway)

		t.Run("Filter by Model in Neutral Mode", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.InferTypeNeutral)
			assert.Equal(t, 1, len(results))
			view, exists := results[gpt35NeutralInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt35NeutralInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Filter by Model in Prefill Mode", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.InferTypePrefill)
			assert.Equal(t, 1, len(results))
			view, exists := results[gpt35PrefillInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt35PrefillInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Filter by Model in Decode Mode", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.InferTypeDecode)
			assert.Equal(t, 1, len(results))
			view, exists := results[gpt35DecodeInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt35DecodeInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Filter Different Models", func(t *testing.T) {
			gpt4Results := scsClient.GetInstanceViewsByModel("gpt-4", consts.InferTypeNeutral)
			assert.Equal(t, 1, len(gpt4Results))
			view, exists := gpt4Results[gpt4NeutralInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt4NeutralInstance.Id(), view.GetInstance().Id())

			claudeResults := scsClient.GetInstanceViewsByModel("claude-2", consts.InferTypeNeutral)
			assert.Equal(t, 1, len(claudeResults))
			view, exists = claudeResults[claudeNeutralInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, claudeNeutralInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Non-existent Model", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("nonexistent-model", consts.InferTypeNeutral)
			assert.Empty(t, results)
		})

		t.Run("Empty Model String", func(t *testing.T) {
			emptyModelInstance := createTestInstanceWithInferTypeAndModel("instance-empty", consts.InferTypeNeutral, "")
			scsClient.AddInstance(emptyModelInstance)

			results := scsClient.GetInstanceViewsByModel("", consts.InferTypeNeutral)
			assert.Equal(t, 1, len(results))
			view, exists := results[emptyModelInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, emptyModelInstance.Id(), view.GetInstance().Id())
		})
	})

	t.Run("GetInstanceViewsByModel without MultiModelSupport", func(t *testing.T) {
		// Create a scsClient that does not support multiple models
		scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{MultiModelSupport: false})
		gateway := "gateway-1"

		// Create instances with different models
		gpt35Instance := createTestInstanceWithInferTypeAndModel("instance-gpt35", consts.InferTypeNeutral, "gpt-3.5-turbo")
		gpt4Instance := createTestInstanceWithInferTypeAndModel("instance-gpt4", consts.InferTypeNeutral, "gpt-4")

		// Add instances
		scsClient.AddInstance(gpt35Instance)
		scsClient.AddInstance(gpt4Instance)
		scsClient.AddGateway(gateway)

		// When multi-model is not supported, should return all instances, ignoring the model parameter
		results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.InferTypeNeutral)
		assert.Equal(t, 2, len(results)) // Should return all instances, not filtered by model

		// Verify both instances are in the result
		instanceIds := make(map[string]bool)
		for _, result := range results {
			instanceIds[result.GetInstance().Id()] = true
		}
		assert.True(t, instanceIds[gpt35Instance.Id()])
		assert.True(t, instanceIds[gpt4Instance.Id()])
	})

	t.Run("GetInstanceViewsByModel with States", func(t *testing.T) {
		scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{MultiModelSupport: true})
		gateway := "gateway-1"

		// Create instance and allocate request state
		instance := createTestInstanceWithInferTypeAndModel("instance-with-states", consts.InferTypeNeutral, "gpt-3.5-turbo")
		scsClient.AddInstance(instance)
		scsClient.AddGateway(gateway)

		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  150,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := scsClient.AllocateRequestState(consts.InferTypeNeutral, reqState)
		assert.NoError(t, err)

		// Verify the returned instance contains request state
		results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.InferTypeNeutral)
		assert.Equal(t, 1, len(results))
		view, exists := results[instance.Id()]
		assert.True(t, exists)
		assert.Equal(t, int64(150), view.NumTokens())
		assert.Equal(t, int64(1), view.NumRequests())
	})

	t.Run("GetInstanceViewsByModel Invalid Mode", func(t *testing.T) {
		scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{MultiModelSupport: true})
		instance := createTestInstanceWithInferTypeAndModel("instance-test", consts.InferTypeNeutral, "gpt-3.5-turbo")
		scsClient.AddInstance(instance)

		// Test invalid inferType
		results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", "invalid-type")
		assert.Empty(t, results) // Invalid infer type should return empty result
	})
}

// createTestInstanceWithInferTypeAndModel creates a test instance with specified mode and model
func createTestInstanceWithInferTypeAndModel(id string, inferType consts.InferType, model string) *types.LLMInstance {
	return &types.LLMInstance{
		Version: 1,
		Model:   model,
		InferType: inferType,
		Endpoint: types.Endpoint{
			Host: "test-host-" + id,
			Port: 8080,
		},
	}
}
