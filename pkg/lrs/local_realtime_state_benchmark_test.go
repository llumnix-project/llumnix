package lrs

import (
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/types"
)

const (
	numInstances = 50
	numGateways  = 2
	numRequests  = 1000
	numUpdates   = 10  // Number of updates per request
	updateAmount = 100 // Token amount increased per update
)

func createBenchmarkInstance(id int) *types.LLMWorker {
	return &types.LLMWorker{
		Version: 1,
		Role:    types.InferRoleNormal,
		Endpoint: types.Endpoint{
			Host: fmt.Sprintf("worker-%d.test", id),
			Port: 8080,
		},
	}
}

func BenchmarkLocalRealtimeStateClient(b *testing.B) {
	// Prepare test data
	lrsClient := NewLocalRealtimeStateClient(&options.Config{})

	// Create and register instances
	instances := make([]*types.LLMWorker, numInstances)
	for i := 0; i < numInstances; i++ {
		instances[i] = createBenchmarkInstance(i)
		lrsClient.AddInstance(instances[i])
	}

	// Create and register gateways
	gateways := make([]string, numGateways)
	for i := 0; i < numGateways; i++ {
		gateways[i] = fmt.Sprintf("gateway-%d", i)
		lrsClient.AddGateway(gateways[i])
	}

	// Timing statistics
	var allocateTime, updateTime, releaseTime int64

	b.ResetTimer()

	for n := 0; n < b.N; n++ {
		var wg sync.WaitGroup
		wg.Add(numRequests)

		// Process requests concurrently
		for i := 0; i < numRequests; i++ {
			go func(reqIndex int) {
				defer wg.Done()
				time.Sleep(time.Duration(rand.Intn(10)) * time.Millisecond)

				reqId := fmt.Sprintf("req-%d", reqIndex)
				instanceId := instances[reqIndex%numInstances].Id()
				gatewayId := gateways[reqIndex%numGateways]

				// 1. Allocate request state
				req := &RequestState{
					reqId:      reqId,
					numTokens:  100,
					instanceId: instanceId,
					gatewayId:  gatewayId,
					updateTime: time.Now(),
				}

				start := time.Now()
				err := lrsClient.AllocateRequestState(consts.NormalInferMode, req)
				atomic.AddInt64(&allocateTime, time.Since(start).Nanoseconds())
				if err != nil {
					return
				}

				// 2. Update multiple times concurrently
				updateWg := sync.WaitGroup{}
				updateWg.Add(numUpdates)
				for j := 0; j < numUpdates; j++ {
					go func() {
						defer updateWg.Done()

						time.Sleep(time.Duration(rand.Intn(20)) * time.Millisecond)

						updateReq := &RequestState{
							reqId:      reqId,
							numTokens:  updateAmount,
							instanceId: instanceId,
							gatewayId:  gatewayId,
							updateTime: time.Now(),
						}

						start := time.Now()
						lrsClient.UpdateRequestState(consts.NormalInferMode, updateReq)
						atomic.AddInt64(&updateTime, time.Since(start).Nanoseconds())
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
				start = time.Now()
				lrsClient.ReleaseRequestState(consts.NormalInferMode, releaseReq)
				atomic.AddInt64(&releaseTime, time.Since(start).Nanoseconds())

			}(i)
		}

		wg.Wait()
	}

	b.StopTimer()

	// Print timing statistics
	totalOps := int64(b.N * numRequests)
	totalUpdates := int64(b.N * numRequests * numUpdates)
	b.Logf("Allocate: total=%v, avg=%v/op",
		time.Duration(allocateTime), time.Duration(allocateTime/totalOps))
	b.Logf("Update:   total=%v, avg=%v/op",
		time.Duration(updateTime), time.Duration(updateTime/totalUpdates))
	b.Logf("Release:  total=%v, avg=%v/op",
		time.Duration(releaseTime), time.Duration(releaseTime/totalOps))
}
