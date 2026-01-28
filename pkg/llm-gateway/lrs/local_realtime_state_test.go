package lrs

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/types"
)

func createTestToken(id string) *types.LLMWorker {
	return createTestInstanceWithModel(id, "gpt-3.5-turbo")
}

func createTestInstanceWithModel(id string, model string) *types.LLMWorker {
	return &types.LLMWorker{
		Version: 1,
		Model:   model,
		Endpoint: types.Endpoint{
			Host: "test-host-" + id,
			Port: 8080,
		},
	}
}

func TestRequestState(t *testing.T) {
	reqId := "test-req-1"
	instanceId := "worker-1"
	gatewayId := "gateway-1"
	numTokens := int64(100)

	reqState := NewRequestState(reqId, numTokens, instanceId, gatewayId)
	assert.Equal(t, reqId, reqState.reqId)
	assert.Equal(t, numTokens, reqState.numTokens)
	assert.Equal(t, instanceId, reqState.instanceId)
	assert.Equal(t, gatewayId, reqState.gatewayId)
	assert.NotZero(t, reqState.updateTime)
}

func TestInstanceView(t *testing.T) {
	instance := createTestToken("worker-1")
	wr := NewInstanceView(instance)

	t.Run("Initial State", func(t *testing.T) {
		assert.Equal(t, int64(0), wr.NumTokens())
		assert.Equal(t, int64(0), wr.NumRequests())
	})

	t.Run("Update Request State", func(t *testing.T) {
		req := NewRequestState("req-1", 1, instance.Id(), "gateway-1")
		wr.AllocateRequestState(req)
		req2 := NewRequestState("req-1", 100, instance.Id(), "gateway-1")
		wr.UpdateRequestState(req2)

		assert.Equal(t, int64(100), wr.NumTokens())
		assert.Equal(t, int64(1), wr.NumRequests())
	})

	t.Run("Remove Request State", func(t *testing.T) {
		wr.ReleaseRequestState("req-1")

		assert.Equal(t, int64(0), wr.NumTokens())
		assert.Equal(t, int64(0), wr.NumRequests())
	})
}

func TestLocalRealtimeState(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	t.Run("Add Instance", func(t *testing.T) {
		lrs.AddInstance(instance)
		assert.True(t, lrs.instanceExists(instance.Id()))
	})

	t.Run("Add Gateway", func(t *testing.T) {
		lrs.AddGateway(gateway)
		assert.True(t, lrs.gatewayExists(gateway))
	})

	t.Run("Allocate Request State", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}

		err := lrs.AllocateRequestState(reqState)
		assert.NoError(t, err)
		assert.True(t, lrs.requestExists(reqState.reqId))
	})

	t.Run("Update Request State", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  150,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}

		err := lrs.UpdateRequestState(reqState)
		assert.NoError(t, err)

		iv := lrs.GetInstanceView(instance.Id())
		assert.Equal(t, int64(150), iv.NumTokens())
	})

	t.Run("Release Request State", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  150,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}

		lrs.ReleaseRequestState(reqState)
		assert.False(t, lrs.requestExists(reqState.reqId))

		iv := lrs.GetInstanceView(instance.Id())
		assert.Equal(t, int64(0), iv.NumTokens())
	})

	t.Run("Remove Gateway", func(t *testing.T) {
		lrs.RemoveGateway(gateway)
		assert.False(t, lrs.gatewayExists(gateway))
	})
}

func TestErrorCases(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	t.Run("Allocate Without Instance", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: "non-existent-worker",
			gatewayId:  gateway,
		}

		err := lrs.AllocateRequestState(reqState)
		assert.Equal(t, consts.ErrorEndpointNotFound, err)
	})

	t.Run("Allocate Without Gateway", func(t *testing.T) {
		lrs.AddInstance(instance)

		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  "non-existent-gateway",
		}

		err := lrs.AllocateRequestState(reqState)
		assert.Equal(t, consts.ErrorGatewayNotFound, err)
	})

	t.Run("Duplicate Request", func(t *testing.T) {
		lrs.AddGateway(gateway)

		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
		}

		err1 := lrs.AllocateRequestState(reqState)
		assert.NoError(t, err1)

		err2 := lrs.AllocateRequestState(reqState)
		assert.Equal(t, consts.ErrorRequestExits, err2)
	})
}

func TestMetrics(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	// Prepare test data
	lrs.AddInstance(instance)
	lrs.AddGateway(gateway)

	// Create multiple requests to verify counting
	reqs := []*RequestState{
		{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		},
		{
			reqId:      "req-2",
			numTokens:  200,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		},
	}

	// Add requests
	for _, req := range reqs {
		err := lrs.AllocateRequestState(req)
		assert.NoError(t, err)
	}

	// Verify instance request states
	instanceView := lrs.GetInstanceView(instance.Id())
	assert.NotNil(t, instanceView)
	assert.Equal(t, int64(300), instanceView.NumTokens()) // 100 + 200
	assert.Equal(t, int64(2), instanceView.NumRequests())

	// Submit and verify metrics
	go lrs.SubmitMetric()
	time.Sleep(100 * time.Millisecond)

	// Verify instances request states
	instanceViews := lrs.GetInstanceViews()
	assert.Equal(t, 1, len(instanceViews))
	instanceView = lrs.GetInstanceView(instance.Id())
	assert.Equal(t, int64(300), instanceView.NumTokens())
	assert.Equal(t, int64(2), instanceView.NumRequests())

	// Release request states
	for _, req := range reqs {
		lrs.ReleaseRequestState(req)
	}

	// Verify request states after release
	instanceView = lrs.GetInstanceView(instance.Id())
	assert.NotNil(t, instanceView)
	assert.Equal(t, int64(0), instanceView.NumTokens())
	assert.Equal(t, int64(0), instanceView.NumRequests())
}

func TestEdgeCases(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	t.Run("Release Zero State", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  0,
			instanceId: instance.Id(),
			gatewayId:  gateway,
		}
		lrs.ReleaseRequestState(reqState)
	})

	t.Run("Instance Version Change", func(t *testing.T) {
		lrs.AddInstance(instance)
		lrs.AddGateway(gateway)

		// First request
		reqState1 := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.AllocateRequestState(reqState1)
		assert.NoError(t, err)

		// Create new version of worker
		newInstance := createTestToken("worker-1")
		newInstance.Version = 2
		lrs.AddInstance(newInstance)

		// Try to update with old version
		reqState2 := &RequestState{
			reqId:      "req-1",
			numTokens:  50,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err = lrs.UpdateRequestState(reqState2)
		assert.Error(t, err)
	})

	t.Run("Duplicate Instance Creation", func(t *testing.T) {
		lrs.AddInstance(instance)
		lrs.AddInstance(instance) // Should log warning
	})

	t.Run("Remove Non-existent Instance", func(t *testing.T) {
		lrs.RemoveInstance("non-existent-worker")
	})
}

func TestPrintInstanceViews(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	lrs.AddInstance(instance)
	lrs.AddGateway(gateway)

	reqState := &RequestState{
		reqId:      "req-1",
		numTokens:  100,
		instanceId: instance.Id(),
		gatewayId:  gateway,
		updateTime: time.Now(),
	}
	lrs.AllocateRequestState(reqState)

	// Test PrintInstanceViews
	lrs.PrintInstanceViews()
}

func TestStateRelease(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	lrs.AddInstance(instance)
	lrs.AddGateway(gateway)

	// Add some requests
	reqStates := []*RequestState{
		{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		},
		{
			reqId:      "req-2",
			numTokens:  200,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		},
	}

	for _, reqState := range reqStates {
		err := lrs.AllocateRequestState(reqState)
		assert.NoError(t, err)
	}

	// Test cleanup through instance removal
	lrs.RemoveInstance(instance.Id())
	assert.Nil(t, lrs.GetInstanceView(instance.Id()))

	// Test cleanup through gateway removal
	lrs.AddInstance(instance)
	for _, req := range reqStates {
		err := lrs.AllocateRequestState(req)
		assert.NoError(t, err)
	}
	lrs.RemoveGateway(gateway)
	assert.False(t, lrs.gatewayExists(gateway))

	t.Run("Cleanup Non-existent States", func(t *testing.T) {
		// Cleaning up non-existent states should not cause errors
		lrs.RemoveInstance("non-existent")
		lrs.RemoveGateway("non-existent")
	})

	t.Run("Clear Empty Instance", func(t *testing.T) {
		instance := createTestToken("empty-worker")
		lrs.AddInstance(instance)
		lrs.RemoveInstance(instance.Id())
		assert.Nil(t, lrs.GetInstanceView(instance.Id()))
	})
}

func TestUpdateRequestStateEdgeCases(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	t.Run("Update Non-existent Request", func(t *testing.T) {
		lrs.AddInstance(instance)
		lrs.AddGateway(gateway)

		reqState := &RequestState{
			reqId:      "non-existent-req",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.UpdateRequestState(reqState)
		assert.Error(t, err)
	})

	t.Run("Update Request With Invalid Instance", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: "invalid-worker",
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.UpdateRequestState(reqState)
		assert.Error(t, err)
	})
}

func TestInstanceVersionManagement(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	t.Run("Version Update Cleanup", func(t *testing.T) {
		lrs.AddInstance(instance)
		lrs.AddGateway(gateway)

		// Add request to old version
		reqState1 := &RequestState{
			reqId:      "req-1",
			numTokens:  100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.AllocateRequestState(reqState1)
		assert.NoError(t, err)

		// Update instance version
		newInstance := createTestToken("worker-1")
		newInstance.Version = 2
		lrs.AddInstance(newInstance)

		// Verify old requests are cleaned up
		assert.False(t, lrs.requestExists(reqState1.reqId))

		wr := lrs.GetInstanceView(instance.Id())
		assert.Equal(t, int64(0), wr.NumTokens())
	})
}

func TestRequestStateValidation(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instance := createTestToken("worker-1")
	gateway := "gateway-1"

	lrs.AddInstance(instance)
	lrs.AddGateway(gateway)

	t.Run("Zero Request State", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-1",
			numTokens:  0,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.AllocateRequestState(reqState)
		assert.NoError(t, err)
	})

	t.Run("Negative Request State", func(t *testing.T) {
		reqState := &RequestState{
			reqId:      "req-2",
			numTokens:  -100,
			instanceId: instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.AllocateRequestState(reqState)
		assert.NoError(t, err)
	})
}

func TestGetInstanceViewsEmpty(t *testing.T) {
	lrs := NewLocalRealtimeState()
	instanceViews := lrs.GetInstanceViews()
	assert.Empty(t, instanceViews)
}

func TestGatewayOperations(t *testing.T) {
	lrs := NewLocalRealtimeState()
	gateway := "gateway-1"

	t.Run("Remove Non-existent Gateway", func(t *testing.T) {
		lrs.RemoveGateway("non-existent-gateway")
	})

	t.Run("Add And Remove Multiple Times", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			lrs.AddGateway(gateway)
			assert.True(t, lrs.gatewayExists(gateway))
			lrs.RemoveGateway(gateway)
			assert.False(t, lrs.gatewayExists(gateway))
		}
	})
}

func TestGetAllWorkStatsByModel(t *testing.T) {
	lrs := NewLocalRealtimeState()
	gateway := "gateway-1"

	// Create instances with different models
	gpt35Instance1 := createTestInstanceWithModel("worker-gpt35-1", "gpt-3.5-turbo")
	gpt35Instance2 := createTestInstanceWithModel("worker-gpt35-2", "gpt-3.5-turbo")
	gpt4Instance := createTestInstanceWithModel("worker-gpt4-1", "gpt-4")
	claudeInstance := createTestInstanceWithModel("worker-claude-1", "claude-2")

	t.Run("Empty Manager", func(t *testing.T) {
		results := lrs.GetInstanceViewsByModel("gpt-3.5-turbo")
		assert.Empty(t, results)
	})

	t.Run("Single Model Match", func(t *testing.T) {
		lrs.AddInstance(gpt35Instance1)
		lrs.AddGateway(gateway)

		results := lrs.GetInstanceViewsByModel("gpt-3.5-turbo")
		view, exists := results[gpt35Instance1.Id()]
		assert.True(t, exists)
		assert.Equal(t, gpt35Instance1.Id(), view.GetInstance().Id())
	})

	t.Run("Multiple Instances Same Model", func(t *testing.T) {
		lrs.AddInstance(gpt35Instance2)

		results := lrs.GetInstanceViewsByModel("gpt-3.5-turbo")
		assert.Equal(t, 2, len(results))

		// Verify both instances are in the results
		instanceIds := make(map[string]bool)
		for _, result := range results {
			instanceIds[result.GetInstance().Id()] = true
		}
		assert.True(t, instanceIds[gpt35Instance1.Id()])
		assert.True(t, instanceIds[gpt35Instance2.Id()])
	})

	t.Run("Different Models", func(t *testing.T) {
		lrs.AddInstance(gpt4Instance)
		lrs.AddInstance(claudeInstance)

		// Test GPT-4 model
		gpt4Results := lrs.GetInstanceViewsByModel("gpt-4")
		assert.Equal(t, 1, len(gpt4Results))
		view, exists := gpt4Results[gpt4Instance.Id()]
		assert.True(t, exists)
		assert.Equal(t, gpt4Instance.Id(), view.GetInstance().Id())

		// Test Claude model
		claudeResults := lrs.GetInstanceViewsByModel("claude-2")
		assert.Equal(t, 1, len(claudeResults))
		view, exists = claudeResults[claudeInstance.Id()]
		assert.True(t, exists)
		assert.Equal(t, claudeInstance.Id(), view.GetInstance().Id())

		// Test non-existent model
		nonexistentResults := lrs.GetInstanceViewsByModel("nonexistent-model")
		assert.Empty(t, nonexistentResults)
	})

	t.Run("Mixed Models With States", func(t *testing.T) {
		// Add request states for instances of different models
		reqState1 := &RequestState{
			reqId:      "req-gpt35-1",
			numTokens:  100,
			instanceId: gpt35Instance1.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err := lrs.AllocateRequestState(reqState1)
		assert.NoError(t, err)

		reqState2 := &RequestState{
			reqId:      "req-gpt4-1",
			numTokens:  200,
			instanceId: gpt4Instance.Id(),
			gatewayId:  gateway,
			updateTime: time.Now(),
		}
		err = lrs.AllocateRequestState(reqState2)
		assert.NoError(t, err)

		// Verify model-based filtering still works correctly
		gpt35Results := lrs.GetInstanceViewsByModel("gpt-3.5-turbo")
		assert.Equal(t, 2, len(gpt35Results))

		gpt4Results := lrs.GetInstanceViewsByModel("gpt-4")
		assert.Equal(t, 1, len(gpt4Results))

		view, exists := gpt4Results[gpt4Instance.Id()]
		assert.True(t, exists)
		assert.Equal(t, gpt4Instance.Id(), view.GetInstance().Id())
		assert.Equal(t, int64(200), view.NumTokens())
	})

	t.Run("Case Sensitivity", func(t *testing.T) {
		// Test case sensitivity of model names
		upperCaseResults := lrs.GetInstanceViewsByModel("GPT-3.5-TURBO")
		assert.Empty(t, upperCaseResults) // Should be empty because model names are case-sensitive

		lowerCaseResults := lrs.GetInstanceViewsByModel("gpt-3.5-turbo")
		assert.Equal(t, 2, len(lowerCaseResults))
	})

	t.Run("Empty Model String", func(t *testing.T) {
		// Test empty string model
		emptyModelInstance := createTestInstanceWithModel("worker-empty", "")
		lrs.AddInstance(emptyModelInstance)

		emptyResults := lrs.GetInstanceViewsByModel("")
		assert.Equal(t, 1, len(emptyResults))
		view, exists := emptyResults[emptyModelInstance.Id()]
		assert.True(t, exists)
		assert.Equal(t, emptyModelInstance.Id(), view.GetInstance().Id())
	})
}
