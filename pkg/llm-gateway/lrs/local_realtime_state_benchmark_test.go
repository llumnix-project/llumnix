package lrs

import (
	"easgo/pkg/llm-gateway/types"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"
)

const (
	numInstances = 50
	numGateways  = 2
	numRequests  = 1000
	numUpdates   = 10  // Number of updates per request
	updateAmount = 100 // Token amount increased per update
)

type benchmarkGateway struct {
	id string
}

func (b *benchmarkGateway) Id() string {
	return b.id
}

func createBenchmarkInstance(id int) *types.LLMWorker {
	return &types.LLMWorker{
		Version: 1,
		Endpoint: types.Endpoint{
			Host: fmt.Sprintf("worker-%d.test", id),
			Port: 8080,
		},
	}
}

func BenchmarkLocalRealtimeState(b *testing.B) {
	// Prepare test data
	lrs := NewLocalRealtimeState()

	// Create and register instances
	instances := make([]*types.LLMWorker, numInstances)
	for i := 0; i < numInstances; i++ {
		instances[i] = createBenchmarkInstance(i)
		lrs.AddInstance(instances[i])
	}

	// Create and register gateways
	gateways := make([]*benchmarkGateway, numGateways)
	for i := 0; i < numGateways; i++ {
		lrs.AddGateway(fmt.Sprintf("gateway-%d", i))
	}

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		wg.Add(numRequests)

		// Process requests concurrently
		for i := 0; i < numRequests; i++ {
			go func(reqIndex int) {
				defer wg.Done()

				reqId := fmt.Sprintf("req-%d", reqIndex)
				instanceId := instances[reqIndex%numInstances].Id()
				gatewayId := gateways[reqIndex%numGateways].Id()

				// 1. Allocate request state
				req := &RequestState{
					reqId:      reqId,
					numTokens:  100,
					instanceId: instanceId,
					gatewayId:  gatewayId,
					updateTime: time.Now(),
				}

				err := lrs.AllocateRequestState(req)
				if err != nil {
					b.Logf("Failed to allocate request state: %v", err)
					return
				}

				// 2. Update multiple times randomly
				updateWg := sync.WaitGroup{}
				updateWg.Add(numUpdates)
				for j := 0; j < numUpdates; j++ {
					go func() {
						defer updateWg.Done()

						// Random sleep 0-10ms to simulate real scenario
						time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

						updateReq := &RequestState{
							reqId:      reqId,
							numTokens:  updateAmount,
							instanceId: instanceId,
							gatewayId:  gatewayId,
							updateTime: time.Now(),
						}

						err := lrs.UpdateRequestState(updateReq)
						if err != nil {
							b.Logf("Failed to update request state: %v", err)
						}
					}()
				}
				updateWg.Wait()

				// 3. Release request state
				releaseReq := &RequestState{
					reqId:      reqId,
					numTokens:  100 + updateAmount*numUpdates,
					instanceId: instanceId,
					gatewayId:  gatewayId,
					updateTime: time.Now(),
				}
				lrs.ReleaseRequestState(releaseReq)

			}(i)
		}

		wg.Wait()
	}
}
