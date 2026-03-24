package etcd

import (
	"context"
	"sync"

	"go.etcd.io/etcd/client/v3"
)

// MockClient is a mock implementation of EtcdClient for testing
type MockClient struct {
	mu      sync.RWMutex
	data    map[string][]byte
	leases  map[clientv3.LeaseID]int64
	watchCh chan clientv3.WatchResponse
}

// NewMockClient creates a new mock etcd client
func NewMockClient() *MockClient {
	return &MockClient{
		data:    make(map[string][]byte),
		leases:  make(map[clientv3.LeaseID]int64),
		watchCh: make(chan clientv3.WatchResponse, 10),
	}
}

// Put implements EtcdClient.Put
func (m *MockClient) Put(ctx context.Context, key string, value []byte, leaseID clientv3.LeaseID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

// Get implements EtcdClient.Get
func (m *MockClient) Get(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.data[key], nil
}

// GetWithPrefix implements EtcdClient.GetWithPrefix
func (m *MockClient) GetWithPrefix(ctx context.Context, prefix string) (map[string][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]byte)
	for key, value := range m.data {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			result[key] = value
		}
	}
	return result, nil
}

// Delete implements EtcdClient.Delete
func (m *MockClient) Delete(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

// Watch implements EtcdClient.Watch
func (m *MockClient) Watch(ctx context.Context, prefix string) clientv3.WatchChan {
	return m.watchCh
}

// GrantLease implements EtcdClient.GrantLease
func (m *MockClient) GrantLease(ctx context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	leaseID := clientv3.LeaseID(len(m.leases) + 1)
	m.leases[leaseID] = ttl
	return &clientv3.LeaseGrantResponse{ID: leaseID, TTL: ttl}, nil
}

// KeepAlive implements EtcdClient.KeepAlive
func (m *MockClient) KeepAlive(ctx context.Context, leaseID clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	ch := make(chan *clientv3.LeaseKeepAliveResponse)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch, nil
}

// RevokeLease implements EtcdClient.RevokeLease
func (m *MockClient) RevokeLease(ctx context.Context, leaseID clientv3.LeaseID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.leases, leaseID)
	return nil
}

// Close implements EtcdClient.Close
func (m *MockClient) Close() error {
	return nil
}

// AddMockData adds data directly for testing
func (m *MockClient) AddMockData(key string, value []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
}

// Clear clears all data
func (m *MockClient) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make(map[string][]byte)
}

// SendWatchResponse sends a watch response for testing
func (m *MockClient) SendWatchResponse(resp clientv3.WatchResponse) {
	m.watchCh <- resp
}
