package llumlet

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/klog/v2"
)

type MockLlumletServer struct {
	UnimplementedLlumletServer
	grpcServer grpc.Server
}

func (s *MockLlumletServer) Migrate(ctx context.Context, req *MigrateRequest) (*MigrateResponse, error) {
	response := &MigrateResponse{
		Message: "Migrated successfully",
	}
	return response, nil
}

func (s *MockLlumletServer) Stop() {
	s.grpcServer.Stop()

}

func startMockServer(port int) (*grpc.Server, string, error) {
	address := fmt.Sprintf("localhost:%d", port)
	lis, err := net.Listen("tcp", address)
	if err != nil {
		return nil, "", fmt.Errorf("failed to listen on %s: %v", address, err)
	}

	s := grpc.NewServer()
	mockServer := &MockLlumletServer{}

	RegisterLlumletServer(s, mockServer)

	go func() {
		if err := s.Serve(lis); err != nil {
			klog.Errorf("Server exited with error: %v", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)

	return s, address, nil
}

var (
	sharedServer   *grpc.Server
	sharedAddress  string
	sharedConn     *grpc.ClientConn
	sharedServer2  *grpc.Server
	sharedAddress2 string
	sharedConn2    *grpc.ClientConn
)

func TestMain(m *testing.M) {
	var err error
	sharedServer, sharedAddress, err = startMockServer(50152)
	if err != nil {
		klog.Fatalf("Failed to start mock server: %v", err)
	}

	sharedConn, err = grpc.Dial(sharedAddress, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		klog.Fatalf("Failed to dial real server: %v", err)
	}

	sharedServer2, sharedAddress2, err = startMockServer(50153)
	if err != nil {
		klog.Fatalf("Failed to start mock server: %v", err)
	}

	sharedConn2, err = grpc.Dial(sharedAddress2, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		klog.Fatalf("Failed to dial real server: %v", err)
	}

	code := m.Run()

	sharedConn.Close()
	sharedServer.Stop()
	sharedConn2.Close()
	sharedServer2.Stop()

	os.Exit(code)
}

func TestGetClient(t *testing.T) {
	cm := NewClientManager(-1)

	client := cm.GetClient("non-existent")
	if client != nil {
		t.Error("GetClient() should return nil for non-existent llumletClient")
	}
}

func TestGetOrCreateClient(t *testing.T) {
	cm := NewClientManager(-1)

	client, err := cm.GetOrCreateClient("test-instance", sharedAddress)
	if err != nil {
		t.Fatalf("GetOrCreateClient() should not return error for valid address: %v", err)
	}
	if client == nil {
		t.Fatalf("GetOrCreateClient() should return valid llumletClient for valid address")
	}
	if cm.clientConnectionRequestCnt["test-instance"] != 1 {
		t.Error("CreateClient() should increase clientConnectionRequestCnt")
	}
	client2, err2 := cm.GetOrCreateClient("test-instance", sharedAddress)
	if err2 != nil {
		t.Errorf("GetOrCreateClient() should not return error for existing llumletClient: %v", err)
	}
	if client != client2 {
		t.Error("GetOrCreateClient() should return existed llumletClient, not new llumletClient.")
	}
	if cm.clientConnectionRequestCnt["test-instance"] != 2 {
		t.Error("GetClient() should increase clientConnectionRequestCnt")
	}
}

func TestDecrClientConnectionRequestCnt(t *testing.T) {
	cm := NewClientManager(-1)
	cm.GetOrCreateClient("test-instance", sharedAddress)
	cm.GetOrCreateClient("test-instance", sharedAddress)
	assert.Equal(t, cm.clientConnectionRequestCnt["test-instance"], 2)
	cm.DecrClientConnectionRequestCnt("test-instance")
	assert.Equal(t, cm.clientConnectionRequestCnt["test-instance"], 1)
	cm.DecrClientConnectionRequestCnt("test-instance")
	assert.Equal(t, cm.clientConnectionRequestCnt["test-instance"], 0)
}

func TestPoolMaxSize(t *testing.T) {
	cm := NewClientManager(2)
	c, err := cm.GetOrCreateClient("test-instance", sharedAddress)
	if err != nil {
		t.Error("GetOrCreateClient() should not return error")
	}
	_, err = cm.GetOrCreateClient("test-instance2", sharedAddress)
	if err != nil {
		t.Error("GetOrCreateClient() should not return error")
	}

	c, err = cm.GetOrCreateClient("test-instance3", sharedAddress)
	if err == nil {
		t.Error("GetOrCreateClient() should return error for exceeding max size")
	}
	if c != nil {
		t.Error("GetOrCreateClient() should return nil for exceeding max size")
	}

	cm.DecrClientConnectionRequestCnt("test-instance")
	c, err = cm.GetOrCreateClient("test-instance3", sharedAddress)
	assert.Nil(t, err)
	assert.NotNil(t, c)

	cm2 := NewClientManager(-1)
	assert.Equal(t, -1, cm2.maxSize)
	for i := 0; i < 1000; i++ {
		_, err := cm2.GetOrCreateClient("test-instance"+fmt.Sprint(i), sharedAddress)
		if err != nil {
			t.Error("GetOrCreateClient() should not return error")
		}
	}
	assert.Equal(t, 1000, len(cm2.clientConnections))

}

func TestCloseAll(t *testing.T) {
	cm := NewClientManager(-1)
	_, err := cm.GetOrCreateClient("test-instance", sharedAddress)
	if err != nil {
		t.Error("GetOrCreateClient() should not return error")
	}
	_, err = cm.GetOrCreateClient("test-instance2", sharedAddress2)
	if err != nil {
		t.Error("GetOrCreateClient() should not return error")
	}
	if len(cm.clientConnections) != 2 {
		t.Error("GetOrCreateClient() should add a new llumletClient to clientConnections map")
	}
	cm.CloseAll()

	if len(cm.clientConnections) != 0 {
		t.Error("CloseAll() should result in empty clientConnections map")
	}
}
