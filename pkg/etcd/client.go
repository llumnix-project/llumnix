package etcd

import (
	"context"
	"fmt"
	"time"

	"go.etcd.io/etcd/client/v3"
	"k8s.io/klog/v2"
)

// EtcdClient defines the interface for etcd client operations
type EtcdClient interface {
	// Put stores a key-value pair with optional lease
	Put(ctx context.Context, key string, value []byte, leaseID clientv3.LeaseID) error

	// Get retrieves the value for a key
	Get(ctx context.Context, key string) ([]byte, error)

	// GetWithPrefix retrieves all key-value pairs with the given prefix
	GetWithPrefix(ctx context.Context, prefix string) (map[string][]byte, error)

	// Delete removes a key
	Delete(ctx context.Context, key string) error

	// Watch watches for changes on a prefix
	Watch(ctx context.Context, prefix string) clientv3.WatchChan

	// GrantLease creates a new lease with the given TTL
	GrantLease(ctx context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error)

	// KeepAlive keeps a lease alive
	KeepAlive(ctx context.Context, leaseID clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error)

	// RevokeLease revokes a lease
	RevokeLease(ctx context.Context, leaseID clientv3.LeaseID) error

	// Close closes the client connection
	Close() error
}

// Client implements EtcdClient interface using the official etcd v3 client
type Client struct {
	client *clientv3.Client
}

// NewClient creates a new etcd client
func NewClient(endpoints []string, username, password string, dialTimeout time.Duration) (*Client, error) {
	config := clientv3.Config{
		Endpoints:   endpoints,
		DialTimeout: dialTimeout,
	}

	if username != "" && password != "" {
		config.Username = username
		config.Password = password
	}

	cli, err := clientv3.New(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create etcd client: %w", err)
	}

	// Test connection
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	if _, err := cli.Status(ctx, endpoints[0]); err != nil {
		cli.Close()
		return nil, fmt.Errorf("failed to connect to etcd: %w", err)
	}

	klog.Infof("etcd client connected to %v", endpoints)
	return &Client{client: cli}, nil
}

// Put implements EtcdClient.Put
func (c *Client) Put(ctx context.Context, key string, value []byte, leaseID clientv3.LeaseID) error {
	var opts []clientv3.OpOption
	if leaseID != 0 {
		opts = append(opts, clientv3.WithLease(leaseID))
	}

	_, err := c.client.Put(ctx, key, string(value), opts...)
	return err
}

// Get implements EtcdClient.Get
func (c *Client) Get(ctx context.Context, key string) ([]byte, error) {
	resp, err := c.client.Get(ctx, key)
	if err != nil {
		return nil, err
	}

	if len(resp.Kvs) == 0 {
		return nil, nil
	}

	return resp.Kvs[0].Value, nil
}

// GetWithPrefix implements EtcdClient.GetWithPrefix
func (c *Client) GetWithPrefix(ctx context.Context, prefix string) (map[string][]byte, error) {
	resp, err := c.client.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	result := make(map[string][]byte)
	for _, kv := range resp.Kvs {
		result[string(kv.Key)] = kv.Value
	}

	return result, nil
}

// Delete implements EtcdClient.Delete
func (c *Client) Delete(ctx context.Context, key string) error {
	_, err := c.client.Delete(ctx, key)
	return err
}

// Watch implements EtcdClient.Watch
func (c *Client) Watch(ctx context.Context, prefix string) clientv3.WatchChan {
	return c.client.Watch(ctx, prefix, clientv3.WithPrefix())
}

// GrantLease implements EtcdClient.GrantLease
func (c *Client) GrantLease(ctx context.Context, ttl int64) (*clientv3.LeaseGrantResponse, error) {
	return c.client.Lease.Grant(ctx, ttl)
}

// KeepAlive implements EtcdClient.KeepAlive
func (c *Client) KeepAlive(ctx context.Context, leaseID clientv3.LeaseID) (<-chan *clientv3.LeaseKeepAliveResponse, error) {
	return c.client.KeepAlive(ctx, leaseID)
}

// RevokeLease implements EtcdClient.RevokeLease
func (c *Client) RevokeLease(ctx context.Context, leaseID clientv3.LeaseID) error {
	_, err := c.client.Lease.Revoke(ctx, leaseID)
	return err
}

// Close implements EtcdClient.Close
func (c *Client) Close() error {
	return c.client.Close()
}
