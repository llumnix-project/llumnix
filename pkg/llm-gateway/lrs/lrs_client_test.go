package lrs

import (
	"fmt"
	"llumnix/cmd/scheduler/app/options"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
)

func createTestInstanceWithInferMode(id string, inferMode string) *types.LLMInstance {
	return &types.LLMInstance{
		Version: 1,
		ID:      id,
		Endpoint: types.Endpoint{
			Host: "test-host",
			Port: 8080,
		},
		Role: types.InferRole(inferMode),
	}
}

func TestLocalRealtimeStateClientConcurrency(t *testing.T) {
	testCases := []struct {
		name      string
		inferMode string
	}{
		{
			name:      "NormalInferMode",
			inferMode: consts.NormalInferMode,
		},
		{
			name:      "PrefillInferMode",
			inferMode: consts.PrefillInferMode,
		},
		{
			name:      "DecodeInferMode",
			inferMode: consts.DecodeInferMode,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{})
			instance := createTestInstanceWithInferMode("instance-1", tc.inferMode)
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
					scsClient.AllocateRequestState(tc.inferMode, allocState)

					// 2. update
					for j := 0; j < rand.Intn(5)+1; j++ {
						updateState := &RequestState{
							reqId:      reqId,
							numTokens:  int64(100 + (j+1)*50),
							instanceId: instance.Id(),
							gatewayId:  gateway,
							updateTime: time.Now(),
						}
						scsClient.UpdateRequestState(tc.inferMode, updateState)
						time.Sleep(time.Millisecond * time.Duration(rand.Intn(5)))
					}

					// 3. Release
					releaseState := &RequestState{
						reqId:      reqId,
						numTokens:  0,
						instanceId: instance.Id(),
						gatewayId:  gateway,
					}
					scsClient.ReleaseRequestState(tc.inferMode, releaseState)
				}(i)
			}

			wg.Wait()

			instanceState := scsClient.GetInstanceView(tc.inferMode, instance.Id())
			assert.NotNil(t, instanceState)
			assert.Equal(t, int64(0), instanceState.NumRequests())
			assert.Equal(t, int64(0), instanceState.NumTokens())
		})
	}
}

func TestScheduelrStateStore(t *testing.T) {
	scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{})
	gateway := "gateway-1"

	t.Run("Instance Creation for Different Modes", func(t *testing.T) {
		// Test normal mode
		normalInstance := createTestInstanceWithInferMode("instance-1", consts.NormalInferMode)
		scsClient.AddInstance(normalInstance)
		stats := scsClient.GetInstanceView(consts.NormalInferMode, normalInstance.Id())
		assert.NotNil(t, stats)

		// Test prefill mode
		prefillInstance := createTestInstanceWithInferMode("instance-2", consts.PrefillInferMode)
		scsClient.AddInstance(prefillInstance)
		stats = scsClient.GetInstanceView(consts.PrefillInferMode, prefillInstance.Id())
		assert.NotNil(t, stats)

		// Test decode mode
		decodeInstance := createTestInstanceWithInferMode("instance-3", consts.DecodeInferMode)
		scsClient.AddInstance(decodeInstance)
		stats = scsClient.GetInstanceView(consts.DecodeInferMode, decodeInstance.Id())
		assert.NotNil(t, stats)
	})

	t.Run("Instance Removal for Different Modes", func(t *testing.T) {
		// Test normal mode deletion
		scsClient.RemoveInstance(consts.NormalInferMode, "instance-1")
		stats := scsClient.GetInstanceView(consts.NormalInferMode, "instance-1")
		assert.Nil(t, stats)

		// Test prefill mode deletion
		scsClient.RemoveInstance(consts.PrefillInferMode, "instance-2")
		stats = scsClient.GetInstanceView(consts.PrefillInferMode, "instance-2")
		assert.Nil(t, stats)

		// Test decode mode deletion
		scsClient.RemoveInstance(consts.DecodeInferMode, "instance-3")
		stats = scsClient.GetInstanceView(consts.DecodeInferMode, "instance-3")
		assert.Nil(t, stats)
	})

	t.Run("Gateway Operations", func(t *testing.T) {
		// Add gateway
		scsClient.AddGateway(gateway)

		modes := []string{consts.NormalInferMode, consts.PrefillInferMode, consts.DecodeInferMode}
		for _, mode := range modes {
			// Create test requests for each mode
			instance := createTestInstanceWithInferMode("instance-"+mode, mode)
			scsClient.AddInstance(instance)

			reqState := &RequestState{
				reqId:      "req-1",
				numTokens:  100,
				instanceId: instance.Id(),
				gatewayId:  gateway,
				updateTime: time.Now(),
			}

			// Test allocate
			err := scsClient.AllocateRequestState(mode, reqState)
			assert.NoError(t, err)

			// Test update
			reqState.numTokens = 150
			err = scsClient.UpdateRequestState(mode, reqState)
			assert.NoError(t, err)

			// Test release
			scsClient.ReleaseRequestState(mode, reqState)
		}

		// Remove gateway
		scsClient.RemoveGateway(gateway)
	})

	t.Run("GetInstanceViews for Different Modes", func(t *testing.T) {
		for _, mode := range []string{consts.NormalInferMode, consts.PrefillInferMode, consts.DecodeInferMode} {
			instanceViews := scsClient.GetInstanceViews(mode)
			assert.NotNil(t, instanceViews)
		}
	})

	t.Run("Metrics and Stats Output", func(t *testing.T) {
		// Add some test data
		instance := createTestInstanceWithInferMode("instance-5", consts.NormalInferMode)
		scsClient.AddInstance(instance)
		scsClient.AddGateway(gateway)

		reqState := &RequestState{
			reqId:      "req-2",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		scsClient.AllocateRequestState(consts.NormalInferMode, reqState)

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
		err := scsClient.AllocateRequestState(consts.NormalInferMode, reqState)
		assert.Error(t, err)

		// Test update with non-existent request
		err = scsClient.UpdateRequestState(consts.NormalInferMode, reqState)
		assert.Error(t, err)

		// Test operations with invalid mode
		err = scsClient.AllocateRequestState("invalid-mode", reqState)
		assert.Error(t, err)
	})

	t.Run("Concurrent Mode Operations", func(t *testing.T) {
		instance := createTestInstanceWithInferMode("instance-6", consts.NormalInferMode)
		scsClient.AddInstance(instance)
		scsClient.AddGateway(gateway)

		done := make(chan bool)
		for i := 0; i < 3; i++ {
			go func(idx int) {
				mode := consts.NormalInferMode
				switch idx {
				case 1:
					mode = consts.PrefillInferMode
				case 2:
					mode = consts.DecodeInferMode
				}

				reqState := &RequestState{
					reqId:      "concurrent-req",
					numTokens:  100,
					instanceId: instance.Id(),
					gatewayId:  gateway,
					updateTime: time.Now(),
				}

				scsClient.AllocateRequestState(mode, reqState)
				scsClient.UpdateRequestState(mode, reqState)
				scsClient.ReleaseRequestState(mode, reqState)
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
		gpt35NormalInstance := createTestInstanceWithInferModeAndModel("instance-gpt35-normal", consts.NormalInferMode, "gpt-3.5-turbo")
		gpt35PrefillInstance := createTestInstanceWithInferModeAndModel("instance-gpt35-prefill", consts.PrefillInferMode, "gpt-3.5-turbo")
		gpt35DecodeInstance := createTestInstanceWithInferModeAndModel("instance-gpt35-decode", consts.DecodeInferMode, "gpt-3.5-turbo")
		gpt4NormalInstance := createTestInstanceWithInferModeAndModel("instance-gpt4-normal", consts.NormalInferMode, "gpt-4")
		claudeNormalInstance := createTestInstanceWithInferModeAndModel("instance-claude-normal", consts.NormalInferMode, "claude-2")

		// Add all instances
		instances := []*types.LLMInstance{gpt35NormalInstance, gpt35PrefillInstance, gpt35DecodeInstance, gpt4NormalInstance, claudeNormalInstance}
		for _, instance := range instances {
			scsClient.AddInstance(instance)
		}
		scsClient.AddGateway(gateway)

		t.Run("Filter by Model in Normal Mode", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.NormalInferMode)
			assert.Equal(t, 1, len(results))
			view, exists := results[gpt35NormalInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt35NormalInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Filter by Model in Prefill Mode", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.PrefillInferMode)
			assert.Equal(t, 1, len(results))
			view, exists := results[gpt35PrefillInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt35PrefillInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Filter by Model in Decode Mode", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.DecodeInferMode)
			assert.Equal(t, 1, len(results))
			view, exists := results[gpt35DecodeInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt35DecodeInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Filter Different Models", func(t *testing.T) {
			gpt4Results := scsClient.GetInstanceViewsByModel("gpt-4", consts.NormalInferMode)
			assert.Equal(t, 1, len(gpt4Results))
			view, exists := gpt4Results[gpt4NormalInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, gpt4NormalInstance.Id(), view.GetInstance().Id())

			claudeResults := scsClient.GetInstanceViewsByModel("claude-2", consts.NormalInferMode)
			assert.Equal(t, 1, len(claudeResults))
			view, exists = claudeResults[claudeNormalInstance.Id()]
			assert.True(t, exists)
			assert.Equal(t, claudeNormalInstance.Id(), view.GetInstance().Id())
		})

		t.Run("Non-existent Model", func(t *testing.T) {
			results := scsClient.GetInstanceViewsByModel("nonexistent-model", consts.NormalInferMode)
			assert.Empty(t, results)
		})

		t.Run("Empty Model String", func(t *testing.T) {
			emptyModelInstance := createTestInstanceWithInferModeAndModel("instance-empty", consts.NormalInferMode, "")
			scsClient.AddInstance(emptyModelInstance)

			results := scsClient.GetInstanceViewsByModel("", consts.NormalInferMode)
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
		gpt35Instance := createTestInstanceWithInferModeAndModel("instance-gpt35", consts.NormalInferMode, "gpt-3.5-turbo")
		gpt4Instance := createTestInstanceWithInferModeAndModel("instance-gpt4", consts.NormalInferMode, "gpt-4")

		// Add instances
		scsClient.AddInstance(gpt35Instance)
		scsClient.AddInstance(gpt4Instance)
		scsClient.AddGateway(gateway)

		// When multi-model is not supported, should return all instances, ignoring the model parameter
		results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.NormalInferMode)
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
		instance := createTestInstanceWithInferModeAndModel("instance-with-states", consts.NormalInferMode, "gpt-3.5-turbo")
		scsClient.AddInstance(instance)
		scsClient.AddGateway(gateway)

		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  150,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := scsClient.AllocateRequestState(consts.NormalInferMode, reqState)
		assert.NoError(t, err)

		// Verify the returned instance contains request state
		results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", consts.NormalInferMode)
		assert.Equal(t, 1, len(results))
		view, exists := results[instance.Id()]
		assert.True(t, exists)
		assert.Equal(t, int64(150), view.NumTokens())
		assert.Equal(t, int64(1), view.NumRequests())
	})

	t.Run("GetInstanceViewsByModel Invalid Mode", func(t *testing.T) {
		scsClient := NewLocalRealtimeStateClient(&options.SchedulerConfig{MultiModelSupport: true})
		instance := createTestInstanceWithInferModeAndModel("instance-test", consts.NormalInferMode, "gpt-3.5-turbo")
		scsClient.AddInstance(instance)

		// Test invalid inferMode
		results := scsClient.GetInstanceViewsByModel("gpt-3.5-turbo", "invalid-mode")
		assert.Empty(t, results) // Invalid mode should return empty result
	})
}

// createTestInstanceWithInferModeAndModel creates a test instance with specified mode and model
func createTestInstanceWithInferModeAndModel(id string, inferMode string, model string) *types.LLMInstance {
	return &types.LLMInstance{
		Version: 1,
		Model:   model,
		Role:    types.InferRole(inferMode),
		Endpoint: types.Endpoint{
			Host: "test-host-" + id,
			Port: 8080,
		},
	}
}
