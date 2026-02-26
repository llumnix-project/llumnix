package kvs

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --------- Fake client implementing KVSMetadataServiceClientInterface ---------

type FakeKVSMetadataServiceClient struct {
	mu sync.Mutex

	// injectable funcs (if set, they override the static returns below)
	batchQueryFn func(hashKeys []string) (map[string][]string, error)

	batchRet map[string][]string
	batchErr error

	// observations
	lastBatchKeys []string
}

func (f *FakeKVSMetadataServiceClient) BatchQueryPrefixHashHitKVSInstances(hashKeys []string) (map[string][]string, error) {
	f.mu.Lock()
	f.lastBatchKeys = append([]string(nil), hashKeys...)
	fn := f.batchQueryFn
	ret := f.batchRet
	err := f.batchErr
	f.mu.Unlock()

	if fn != nil {
		return fn(hashKeys)
	}
	if err != nil {
		return nil, err
	}

	// deep copy
	out := make(map[string][]string, len(ret))
	for k, v := range ret {
		out[k] = append([]string(nil), v...)
	}
	return out, nil
}

// --------- helpers ---------

func equalMapStringSlice(got, want map[string][]string) bool {
	return reflect.DeepEqual(got, want)
}

// --------- tests (ordered by kvs_client.go method order) ---------

// --- newKVSClient(...) ---

// --- (c *KVSClient) BatchQueryCacheHitKVSInstances(prefixHashes []string) map[string][]string ---

func TestKVSClient_BatchQuery_EmptyInput(t *testing.T) {
	fake := &FakeKVSMetadataServiceClient{}

	kvsClient := &KVSClient{
		kvsMetadataServiceClient: fake,
		kvsMetadataServiceStatus: atomic.Value{},
	}
	kvsClient.kvsMetadataServiceStatus.Store(kvsMetadataServiceHealthy)

	got := kvsClient.BatchQueryCacheHitKVSInstances(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got=%v", got)
	}

	got = kvsClient.BatchQueryCacheHitKVSInstances([]string{})
	if len(got) != 0 {
		t.Fatalf("expected empty, got=%v", got)
	}
}

func TestKVSClient_BatchQuery_ServiceDownShortCircuit(t *testing.T) {
	var called int32
	fake := &FakeKVSMetadataServiceClient{
		batchQueryFn: func(hashKeys []string) (map[string][]string, error) {
			atomic.AddInt32(&called, 1)
			return map[string][]string{"h1": {"i1"}}, nil
		},
	}

	kvsClient := &KVSClient{
		kvsMetadataServiceClient:       fake,
		retryTimes:                     3,
		retryInterval:                  time.Millisecond,
		kvsMetadataServiceDownDuration: 10 * time.Second,
		kvsMetadataServiceStatus:       atomic.Value{},
		kvsMetadataServiceLastDownTime: atomic.Value{},
	}

	// mark down and not yet recovered
	kvsClient.kvsMetadataServiceStatus.Store(kvsMetadataServiceDown)
	kvsClient.kvsMetadataServiceLastDownTime.Store(time.Now())

	got := kvsClient.BatchQueryCacheHitKVSInstances([]string{"h1"})
	if got != nil {
		t.Fatalf("expected nil when service down, got=%v", got)
	}
	if atomic.LoadInt32(&called) != 0 {
		t.Fatalf("expected backend NOT called, got called=%d", called)
	}
}

func TestKVSClient_CacheOperations_Basic(t *testing.T) {
	fake := &FakeKVSMetadataServiceClient{
		batchRet: map[string][]string{
			"prefix_hash_1": {"instance_1", "instance_2"},
			"prefix_hash_2": {"instance_1"},
		},
	}

	kvsClient := &KVSClient{
		kvsMetadataServiceClient: fake,

		retryTimes:    3,
		retryInterval: 1 * time.Millisecond,

		kvsMetadataServiceStatus:       atomic.Value{},
		kvsMetadataServiceLastDownTime: atomic.Value{},
	}
	kvsClient.kvsMetadataServiceStatus.Store(kvsMetadataServiceHealthy)
	kvsClient.kvsMetadataServiceLastDownTime.Store(time.Now())

	got := kvsClient.BatchQueryCacheHitKVSInstances([]string{"prefix_hash_1", "prefix_hash_2"})
	want := map[string][]string{
		"prefix_hash_1": {"instance_1", "instance_2"},
		"prefix_hash_2": {"instance_1"},
	}

	if !equalMapStringSlice(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestKVSClient_BatchQuery_RetrySuccess(t *testing.T) {
	var attempts int32

	fake := &FakeKVSMetadataServiceClient{
		batchQueryFn: func(hashKeys []string) (map[string][]string, error) {
			n := atomic.AddInt32(&attempts, 1)
			if n <= 2 {
				return nil, context.DeadlineExceeded
			}
			return map[string][]string{
				"prefix_hash_1": {"instance_1"},
			}, nil
		},
	}

	kvsClient := &KVSClient{
		kvsMetadataServiceClient: fake,

		retryTimes:    3,
		retryInterval: 1 * time.Millisecond,

		kvsMetadataServiceStatus:       atomic.Value{},
		kvsMetadataServiceLastDownTime: atomic.Value{},
	}
	kvsClient.kvsMetadataServiceStatus.Store(kvsMetadataServiceHealthy)
	kvsClient.kvsMetadataServiceLastDownTime.Store(time.Now())

	got := kvsClient.BatchQueryCacheHitKVSInstances([]string{"prefix_hash_1"})
	want := map[string][]string{"prefix_hash_1": {"instance_1"}}

	if !equalMapStringSlice(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected 3 attempts, got %d", attempts)
	}
}

func TestKVSClient_BatchQuery_NonTimeoutError_RetryAndMarkDown(t *testing.T) {
	var attempts int32
	nonTimeoutErr := errors.New("some non-timeout error")

	fake := &FakeKVSMetadataServiceClient{
		batchQueryFn: func(hashKeys []string) (map[string][]string, error) {
			atomic.AddInt32(&attempts, 1)
			return nil, nonTimeoutErr
		},
	}

	kvsClient := &KVSClient{
		kvsMetadataServiceClient: fake,

		retryTimes:    3,
		retryInterval: 1 * time.Millisecond,

		kvsMetadataServiceDownDuration: 0,

		kvsMetadataServiceStatus:       atomic.Value{},
		kvsMetadataServiceLastDownTime: atomic.Value{},
	}
	kvsClient.kvsMetadataServiceStatus.Store(kvsMetadataServiceHealthy)
	kvsClient.kvsMetadataServiceLastDownTime.Store(time.Now())

	got := kvsClient.BatchQueryCacheHitKVSInstances([]string{"prefix_hash_1"})
	if len(got) != 0 {
		t.Fatalf("expected empty, got=%v", got)
	}

	if atomic.LoadInt32(&attempts) != int32(kvsClient.retryTimes) {
		t.Fatalf("expected %d attempts, got %d", kvsClient.retryTimes, attempts)
	}

	st := kvsClient.kvsMetadataServiceStatus.Load().(kvsMetadataServiceStatus)
	if st != kvsMetadataServiceDown {
		t.Fatalf("expected service down, got %v", st)
	}
}

// --- (c *KVSClient) IsKVSMetadataServiceDown() bool ---

func TestKVSClient_IsKVSMetadataServiceDown_AutoRecover(t *testing.T) {
	kvsClient := &KVSClient{
		kvsMetadataServiceDownDuration: 10 * time.Millisecond,
		kvsMetadataServiceStatus:       atomic.Value{},
		kvsMetadataServiceLastDownTime: atomic.Value{},
	}
	kvsClient.kvsMetadataServiceStatus.Store(kvsMetadataServiceDown)
	kvsClient.kvsMetadataServiceLastDownTime.Store(time.Now().Add(-time.Second)) // long ago

	if down := kvsClient.IsKVSMetadataServiceDown(); down {
		t.Fatalf("expected recovered (false), got true")
	}
	if st := kvsClient.kvsMetadataServiceStatus.Load().(kvsMetadataServiceStatus); st != kvsMetadataServiceHealthy {
		t.Fatalf("expected status healthy after recover, got %v", st)
	}
}

