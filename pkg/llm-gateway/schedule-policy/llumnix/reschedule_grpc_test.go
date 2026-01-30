package llumnix

import (
	"context"
	"fmt"
	"llumnix/cmd/config"
	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/llm-gateway/cms"
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/llm-gateway/llumlet"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog/v2"
)

type MockLlumletServer struct {
	llumlet.UnimplementedLlumletServer
	grpcServer grpc.Server
	canMigrate bool
}

func (s *MockLlumletServer) Migrate(ctx context.Context, req *llumlet.MigrateRequest) (*llumlet.MigrateResponse, error) {
	var response *llumlet.MigrateResponse
	if s.canMigrate {
		response = &llumlet.MigrateResponse{
			Success: true,
			Message: "Migrated successfully",
		}
	} else {
		response = &llumlet.MigrateResponse{
			Success: false,
			Message: "Migration failed",
		}
	}
	return response, nil
}

func (s *MockLlumletServer) Stop() {
	s.grpcServer.Stop()
}

func startMockServer(port int, canMigrate bool) (*grpc.Server, string, error) {
	address := fmt.Sprintf("localhost:%d", port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, "", fmt.Errorf("failed to listen on %s: %v", address, err)
	}

	s := grpc.NewServer()
	mockServer := &MockLlumletServer{
		canMigrate: canMigrate,
	}

	llumlet.RegisterLlumletServer(s, mockServer)

	go func() {
		if err := s.Serve(lis); err != nil {
			klog.Errorf("Server exited with error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	return s, address, nil
}

var (
	basePort             = 50052
	sharedServers        []*grpc.Server
	sharedConns          []*grpc.ClientConn
	llumletClientManager *llumlet.ClientManager
)

func TestMain(m *testing.M) {
	llumletClientManager = llumlet.NewClientManager(-1)
	sharedServers := make([]*grpc.Server, 5)
	sharedConns := make([]*grpc.ClientConn, 5)
	for i := 0; i < 5; i++ {
		canMigrate := i < 4
		instanceId := "instanceId-" + strconv.Itoa(i)
		sharedServer, sharedAddress, err := startMockServer(basePort+i, canMigrate)
		if err != nil {
			klog.Fatalf("Failed to start mock server: %v", err)
		}
		var client *llumlet.ClientConnection
		client, err = llumletClientManager.GetOrCreateClient(instanceId, sharedAddress)
		if err != nil {
			klog.Fatalf("Failed to get client: %v", err)
		}
		sharedServers[i] = sharedServer
		sharedConns[i] = client.Conn
	}

	code := m.Run()

	for i := 0; i < 5; i++ {
		sharedConns[i].Close()
		sharedServers[i].Stop()
	}
	// waiting for connection close
	time.Sleep(1 * time.Second)
	os.Exit(code)
}

func genInstanceViewScheduling(instanceId string, llumletPort int32, kvtPort int32) *instanceViewScheduling {
	result := &instanceViewScheduling{
		cmsView: &cms.InstanceView{
			Status: &cms.InstanceStatus{
				InstanceId:  instanceId,
				Schedulable: true,
			},
			Metadata: &cms.InstanceMetadata{
				InstanceId:  instanceId,
				Ip:          "localhost",
				LlumletPort: llumletPort,
				KvtPort:     kvtPort,
			},
		},
	}
	result.InstanceViewInterface = result.cmsView
	return result
}

func TestExecuteMigrationsSuccess(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			RescheduleDecodeLoadMetric:  consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			ReschedulePrefillLoadMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			RescheduleNeutralLoadMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			ReschedulePolicies:          "decode_load,prefill_failover,decode_failover,neutral_failover",
			RescheduleLoadBalanceScope:  consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)
	rp.llumletClientManager = llumletClientManager
	reschedulePairs := []*reschedulePair{}
	for i := 0; i < 5; i++ {
		reschedulePairs = append(reschedulePairs, &reschedulePair{
			srcView:        genInstanceViewScheduling("instanceId-"+strconv.Itoa(i), int32(basePort+i), 0),
			dstView:        genInstanceViewScheduling("instanceId-"+strconv.Itoa((i+1)%5), int32(basePort+i), 0),
			reqSelectRule:  consts.MigrationReqSelectRuleNumReq,
			reqSelectOrder: consts.MigrationReqSelectOrderLR,
			reqSelectValue: 1.0,
		})
	}
	results := rp.executeMigrations(reschedulePairs)
	for _, res := range results {
		assert.Nil(t, res.err)
	}
}

func TestExecuteMigrationsGrpcConnectFailed(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			RescheduleDecodeLoadMetric:  consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			ReschedulePrefillLoadMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			RescheduleNeutralLoadMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			ReschedulePolicies:          "decode_load,prefill_failover,decode_failover,neutral_failover",
			RescheduleLoadBalanceScope:  consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)
	rp.llumletClientManager = llumletClientManager
	reschedulePairs := []*reschedulePair{
		{
			srcView:        genInstanceViewScheduling("instanceId-100", int32(basePort+1000), 0),
			dstView:        genInstanceViewScheduling("instanceId-1", int32(basePort+1000), 0),
			reqSelectRule:  consts.MigrationReqSelectRuleNumReq,
			reqSelectOrder: consts.MigrationReqSelectOrderLR,
			reqSelectValue: 1.0,
		},
	}
	results := rp.executeMigrations(reschedulePairs)
	assert.Len(t, results, 1)
	for _, res := range results {
		assert.NotNil(t, res.err)
		assert.True(t, status.Code(res.err) == codes.Unavailable)
	}
}

func TestExecuteMigrationsLlumletMigrateFailed(t *testing.T) {
	config := &options.SchedulerConfig{
		FullModeScheduleConfig: config.FullModeScheduleConfig{
			RescheduleDecodeLoadMetric:  consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			ReschedulePrefillLoadMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			RescheduleNeutralLoadMetric: consts.SchedulingMetricKVBlocksRatioWithAllPrefills,
			ReschedulePolicies:          "decode_load,prefill_failover,decode_failover,neutral_failover",
			RescheduleLoadBalanceScope:  consts.RescheduleLoadBalanceScopeCluster,
		},
	}
	rp := NewReschedulePolicyPartial(config)
	rp.llumletClientManager = llumletClientManager
	reschedulePairs := []*reschedulePair{
		{
			srcView:        genInstanceViewScheduling("instanceId-4", int32(basePort+4), 0),
			dstView:        genInstanceViewScheduling("instanceId-0", int32(basePort+4), 0),
			reqSelectRule:  consts.MigrationReqSelectRuleNumReq,
			reqSelectOrder: consts.MigrationReqSelectOrderLR,
			reqSelectValue: 1.0,
		},
	}
	results := rp.executeMigrations(reschedulePairs)
	assert.Len(t, results, 1)
	for _, res := range results {
		assert.Nil(t, res.err)
		assert.False(t, res.migrateResponse.Success)
	}
}
