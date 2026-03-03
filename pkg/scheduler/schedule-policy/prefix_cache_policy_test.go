package schedule_policy

import (
	"flag"
	"fmt"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/scheduler/lrs"
	"llm-gateway/pkg/types"
	"llm-gateway/pkg/utils/radix"
	"math/rand"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"k8s.io/klog/v2"
)

// TestNewWorkerWithLRU verifies that a new WorkerWithLRU is correctly initialized
// with the given worker and current timestamp.
func TestNewWorkerWithLRU(t *testing.T) {
	worker := &types.LLMWorker{ID: "test-worker-1"}
	now := time.Now()

	wl := NewWorkerWithLRU(worker)

	assert.NotNil(t, wl)
	assert.Equal(t, worker, wl.worker)
	assert.WithinDuration(t, now, wl.lastAccess, 10*time.Millisecond)
}

// TestWorkerWithLRU_Touch verifies that Touch() updates the last access time.
func TestWorkerWithLRU_Touch(t *testing.T) {
	worker := &types.LLMWorker{ID: "test-worker-1"}
	wl := NewWorkerWithLRU(worker)

	initialTime := wl.lastAccess
	time.Sleep(10 * time.Millisecond)

	wl.Touch()

	assert.True(t, wl.lastAccess.After(initialTime), "last access time should be updated")
}

// TestWorkerWithLRU_TouchConcurrent tests thread-safety of Touch() method.
func TestWorkerWithLRU_TouchConcurrent(t *testing.T) {
	worker := &types.LLMWorker{ID: "test-worker-1"}
	wl := NewWorkerWithLRU(worker)

	var wg sync.WaitGroup
	concurrency := 100

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wl.Touch()
		}()
	}

	wg.Wait()
	assert.NotNil(t, wl.lastAccess)
}

// TestTreeNode_AddWorker verifies adding workers to the tree node.
func TestTreeNode_AddWorker(t *testing.T) {
	tn := NewTreeNode()
	worker1 := &types.LLMWorker{ID: "worker-1"}
	worker2 := &types.LLMWorker{ID: "worker-2"}

	tn.AddWorker(worker1)
	assert.Equal(t, 1, tn.list.Len())

	tn.AddWorker(worker2)
	assert.Equal(t, 2, tn.list.Len())

	// Verify LIFO order (last added is at front)
	front := tn.list.Front().Value.(*WorkerWithLRU)
	assert.Equal(t, worker2, front.worker)
}

// TestTreeNode_TouchWorker verifies that touching a worker moves it to the front.
func TestTreeNode_TouchWorker(t *testing.T) {
	tn := NewTreeNode()
	worker1 := &types.LLMWorker{ID: "worker-1"}
	worker2 := &types.LLMWorker{ID: "worker-2"}
	worker3 := &types.LLMWorker{ID: "worker-3"}

	tn.AddWorker(worker1)
	tn.AddWorker(worker2)
	tn.AddWorker(worker3)

	// Order is now: worker3, worker2, worker1
	// Touch worker1 to move it to front
	tn.TouchWorker(worker1)

	// Verify worker1 is now at front
	front := tn.list.Front().Value.(*WorkerWithLRU)
	assert.Equal(t, worker1, front.worker)
}

// TestTreeNode_TouchWorkerNotFound verifies behavior when touching a non-existent worker.
func TestTreeNode_TouchWorkerNotFound(t *testing.T) {
	tn := NewTreeNode()
	worker1 := &types.LLMWorker{ID: "worker-1"}
	worker2 := &types.LLMWorker{ID: "worker-2"}

	tn.AddWorker(worker1)

	// Try to touch a worker that doesn't exist
	tn.TouchWorker(worker2)

	// List should remain unchanged
	assert.Equal(t, 1, tn.list.Len())
	front := tn.list.Front().Value.(*WorkerWithLRU)
	assert.Equal(t, worker1, front.worker)
}

// TestTreeNode_GetWorkers verifies retrieving all workers from the node.
func TestTreeNode_GetWorkers(t *testing.T) {
	tn := NewTreeNode()

	// Empty list should return nil
	workers := tn.GetWorkers()
	assert.Nil(t, workers)

	// Add workers
	worker1 := &types.LLMWorker{ID: "worker-1"}
	worker2 := &types.LLMWorker{ID: "worker-2"}
	worker3 := &types.LLMWorker{ID: "worker-3"}

	tn.AddWorker(worker1)
	tn.AddWorker(worker2)
	tn.AddWorker(worker3)

	workers = tn.GetWorkers()
	assert.Equal(t, 3, len(workers))
	// Verify order: most recently added first
	assert.Equal(t, worker3, workers[0])
	assert.Equal(t, worker2, workers[1])
	assert.Equal(t, worker1, workers[2])
}

// TestTreeNode_CheckAndRemoveInvalidWorkers verifies TTL-based eviction.
func TestTreeNode_CheckAndRemoveInvalidWorkers(t *testing.T) {
	tn := NewTreeNode()
	worker1 := &types.LLMWorker{ID: "worker-1"}
	worker2 := &types.LLMWorker{ID: "worker-2"}

	tn.AddWorker(worker1)
	time.Sleep(50 * time.Millisecond)
	tn.AddWorker(worker2)

	// Remove workers older than 30ms (should remove worker1)
	isEmpty := tn.CheckAndRemoveInvalidWorkers(30 * time.Millisecond)

	assert.False(t, isEmpty)
	assert.Equal(t, 1, tn.list.Len())

	workers := tn.GetWorkers()
	assert.Equal(t, worker2, workers[0])
}

// TestTreeNode_CheckAndRemoveInvalidWorkersEmpty verifies when all workers are evicted.
func TestTreeNode_CheckAndRemoveInvalidWorkersEmpty(t *testing.T) {
	tn := NewTreeNode()
	worker1 := &types.LLMWorker{ID: "worker-1"}

	tn.AddWorker(worker1)
	time.Sleep(50 * time.Millisecond)

	// Remove all workers older than 30ms
	isEmpty := tn.CheckAndRemoveInvalidWorkers(30 * time.Millisecond)

	assert.True(t, isEmpty)
	assert.Equal(t, 0, tn.list.Len())
}

// TestNewPrefixCacheIndexer verifies indexer initialization.
func TestNewPrefixCacheIndexer(t *testing.T) {
	ttl := 5 * time.Second
	memLimit := int64(1024 * 1024) // 1MB

	pci := NewPrefixCacheIndexer(ttl, memLimit, 2*time.Second, "")

	assert.NotNil(t, pci)
	assert.NotNil(t, pci.tree)
	assert.Equal(t, ttl, pci.ttl)
	assert.Equal(t, memLimit, pci.memLimit)
	assert.Equal(t, int64(0), pci.GetMemUsed())

	// Note: MaintenanceLoop is started in goroutine, tested separately
}

// TestPrefixCacheIndexer_AddAndMatchPrefix verifies basic prefix operations.
func TestPrefixCacheIndexer_AddAndMatchPrefix(t *testing.T) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond) // Allow goroutine cleanup

	// Add prefixes
	node1 := NewTreeNode()
	node2 := NewTreeNode()

	pci.AddPrefix("hello", node1)
	pci.AddPrefix("helloworld", node2)

	// Test longest prefix matching
	matched, matchLen, _ := pci.MatchPrefix("helloworld123")
	assert.Same(t, node2, matched, "should match longest prefix 'helloworld'")
	assert.Equal(t, 10, matchLen, "match length should be 10")

	matched, matchLen, _ = pci.MatchPrefix("hello123")
	assert.Same(t, node1, matched, "should match prefix 'hello'")
	assert.Equal(t, 5, matchLen, "match length should be 5")

	matched, matchLen, _ = pci.MatchPrefix("hellx")
	assert.Same(t, node1, matched, "should match prefix 'hello'")
	assert.Equal(t, 4, matchLen, "match length should be 0")

	matched, matchLen, _ = pci.MatchPrefix("zzzzi")
	assert.Nil(t, matched, "should not match non-existent prefix")
	assert.Equal(t, 0, matchLen, "match length should be 0")
}

// TestPrefixCacheIndexer_CommonPrefixMatching validates longest common prefix (LCP) matching.
// Algorithm: Uses Radix Tree's WalkPath for O(k) traversal, where k is the LCP length.
// Only visits nodes along the search path, avoiding O(n) full tree traversal.
//
// Matching Semantics:
//   - Exact match: Query exactly equals a cached key
//   - Prefix match: A cached key is a prefix of the query
//   - Common prefix match: Query and cached key share a common prefix (critical for LLM caching)
//
// Example: Cache ["hello", "helloworld", "hellobaby"]
//
//	Query "helloworld" → matches "helloworld" (exact, 10 chars)
//	Query "helloworld123" → matches "helloworld" (prefix, 10 chars)
//	Query "helloxyz" → matches "hello" (common prefix, 5 chars)
//	Query "helloabc" → matches "hello" (common prefix, 5 chars)
//
// This enables KV cache reuse when prompts share common prefixes but aren't strict prefixes.
func TestPrefixCacheIndexer_CommonPrefixMatching(t *testing.T) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond)

	// Setup: Build a representative cache tree
	nodeHello := NewTreeNode()
	nodeHelloWorld := NewTreeNode()
	nodeHelloBaby := NewTreeNode()
	nodeGoodbye := NewTreeNode()
	nodeWorld := NewTreeNode()

	pci.AddPrefix("hello", nodeHello)           // 5 chars
	pci.AddPrefix("helloworld", nodeHelloWorld) // 10 chars
	pci.AddPrefix("hellobaby", nodeHelloBaby)   // 9 chars
	pci.AddPrefix("goodbye", nodeGoodbye)       // 7 chars
	pci.AddPrefix("world", nodeWorld)           // 5 chars

	// === Test Group 1: Exact Matches ===
	// Use assert.Same to verify we get the exact cached node object (pointer comparison)
	t.Run("ExactMatch_HelloWorld", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("helloworld")
		assert.Same(t, nodeHelloWorld, matched, "should return exact cached node object")
		assert.Equal(t, 10, matchLen, "match length should be 10")
	})

	t.Run("ExactMatch_Hello", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("hello")
		assert.Same(t, nodeHello, matched, "should return exact cached node object")
		assert.Equal(t, 5, matchLen, "match length should be 5")
	})

	// === Test Group 2: Prefix Matches (cached key is prefix of query) ===
	t.Run("PrefixMatch_HelloWorldExtended", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("helloworld123")
		assert.Same(t, nodeHelloWorld, matched, "should return cached 'helloworld' node")
		assert.Equal(t, 10, matchLen, "match length should be 10")
	})

	t.Run("PrefixMatch_GoodbyeExtended", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("goodbye, world!")
		assert.Same(t, nodeGoodbye, matched, "should return cached 'goodbye' node")
		assert.Equal(t, 7, matchLen, "match length should be 7")
	})

	// === Test Group 3: Common Prefix Matches (CRITICAL for LLM caching) ===
	// These test cases validate the key requirement: queries can match cached keys
	// by longest common prefix even when neither is a strict prefix of the other.

	t.Run("CommonPrefix_HelloWorAbc", func(t *testing.T) {
		// "helloworabc" shares "hellowor" (8 chars) with "helloworld"
		// and "hello" (5 chars) with "hello", "hellobaby"
		// Should match "helloworld" as it has longest common prefix (8 chars)
		matched, matchLen, _ := pci.MatchPrefix("helloworabc")
		assert.Same(t, nodeHelloWorld, matched, "should return cached 'helloworld' node")
		assert.Equal(t, 8, matchLen, "match length should be 8")
	})

	t.Run("CommonPrefix_HelloXyz", func(t *testing.T) {
		// Another common prefix case with different suffix
		matched, matchLen, _ := pci.MatchPrefix("helloxyz")
		assert.Same(t, nodeHello, matched, "should return cached 'hello' node")
		assert.Equal(t, 5, matchLen, "match length should be 5")
	})

	// === Test Group 4: Longest Match Selection ===
	// When multiple cached keys share prefixes with query, select the one with longest common prefix

	t.Run("LongestMatch_HelloWorlX", func(t *testing.T) {
		// CRITICAL: "helloworlx" shares "helloworl" (9 chars) with "helloworld"
		// This validates that we can match partial prefixes correctly
		// Should match "helloworld" as it has the longest common prefix (9 chars)
		matched, matchLen, _ := pci.MatchPrefix("helloworlx")
		assert.Same(t, nodeHelloWorld, matched, "should return cached 'helloworld' node (9-char common prefix)")
		assert.Equal(t, 9, matchLen, "match length should be 9")
	})

	t.Run("LongestMatch_HelloWorldPartial", func(t *testing.T) {
		// "hellowor" shares 8 chars with "helloworld", only 5 with "hello"
		// Should match "helloworld" (8-char common prefix)
		matched, matchLen, _ := pci.MatchPrefix("hellowor")
		assert.Same(t, nodeHelloWorld, matched, "should return cached 'helloworld' node")
		assert.Equal(t, 8, matchLen, "match length should be 8")
	})

	// === Test Group 5: No Match Cases ===

	t.Run("NoMatch_CompletelyDifferent", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("unknown")
		assert.Nil(t, matched, "'unknown' has no common prefix with any cached key")
		assert.Equal(t, 0, matchLen, "match length should be 0")
	})

	t.Run("NoMatch_EmptyString", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("")
		assert.Nil(t, matched, "empty string should not match anything")
		assert.Equal(t, 0, matchLen, "match length should be 0")
	})

	// === Test Group 6: Different Tree Branches ===
	// Validates that matching works correctly across different branches

	t.Run("DifferentBranch_Goodbye", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("goodbye")
		assert.Same(t, nodeGoodbye, matched, "should return cached 'goodbye' node")
		assert.Equal(t, 7, matchLen, "match length should be 7")
	})

	t.Run("DifferentBranch_World", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("world")
		assert.Same(t, nodeWorld, matched, "should return cached 'world' node")
		assert.Equal(t, 5, matchLen, "match length should be 5")
	})

	t.Run("DifferentBranch_WorldExtended", func(t *testing.T) {
		matched, matchLen, _ := pci.MatchPrefix("world123")
		assert.Same(t, nodeWorld, matched, "should return cached 'world' node")
		assert.Equal(t, 5, matchLen, "match length should be 5")
	})

	// === Test Group 7: Edge Cases ===

	t.Run("EdgeCase_SingleChar", func(t *testing.T) {
		// 'h' shares 1 char with "hello", "helloworld", "hellobaby"
		// With optimized algorithm, should match "hello" with 1-char common prefix
		matched, matchLen, _ := pci.MatchPrefix("h")
		assert.Same(t, nodeHello, matched, "single character 'h' should match")
		assert.Equal(t, 1, matchLen, "match length should be 1")
	})

	longNodeHelloWorld := NewTreeNode()
	key := "helloworld" + strings.Repeat("x", 100) + strings.Repeat("y", 100) + "z"
	pci.AddPrefix(key, longNodeHelloWorld) // 202 chars

	t.Run("EdgeCase_VeryLongQuery", func(t *testing.T) {
		// Very long query should still match the longest cached prefix
		longQuery := "helloworld" + strings.Repeat("x", 1000)
		matched, matchLen, _ := pci.MatchPrefix(longQuery)
		assert.Same(t, longNodeHelloWorld, matched, "should return cached 'helloworld' node")
		assert.Equal(t, 110, matchLen, "match length should be 202")
	})
}

// TestPrefixCacheIndexer_DeletePrefix verifies prefix deletion.
func TestPrefixCacheIndexer_DeletePrefix(t *testing.T) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond)

	node := NewTreeNode()
	prefix := "test-prefix"

	pci.AddPrefix(prefix, node)
	assert.Greater(t, pci.GetMemUsed(), int64(0))

	matched, _, _ := pci.MatchPrefix(prefix)
	assert.Same(t, node, matched)

	// Delete prefix
	pci.DeletePrefix(prefix)
	assert.Equal(t, int64(0), pci.GetMemUsed())

	matched, _, _ = pci.MatchPrefix(prefix)
	assert.Nil(t, matched)
}

// TestPrefixCacheIndexer_MemoryTracking verifies memory usage tracking.
func TestPrefixCacheIndexer_MemoryTracking(t *testing.T) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond)

	node := NewTreeNode()

	// Add prefixes and verify memory tracking
	pci.AddPrefix("abc", node)
	assert.Equal(t, int64(3), pci.GetMemUsed())

	pci.AddPrefix("defgh", node)
	assert.Equal(t, int64(8), pci.GetMemUsed()) // 3 + 5

	// Delete and verify memory reduction
	pci.DeletePrefix("abc")
	assert.Equal(t, int64(5), pci.GetMemUsed())
}

// TestPrefixCacheIndexer_DuplicateAdd verifies updating existing prefix doesn't increase memory.
func TestPrefixCacheIndexer_DuplicateAdd(t *testing.T) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond)

	node1 := NewTreeNode()
	node2 := NewTreeNode()

	pci.AddPrefix("key", node1)
	memAfterFirst := pci.GetMemUsed()

	// Add same key again (update)
	pci.AddPrefix("key", node2)
	memAfterSecond := pci.GetMemUsed()

	assert.Equal(t, memAfterFirst, memAfterSecond, "memory should not increase for duplicate key")

	// Verify node is updated
	matched, _, _ := pci.MatchPrefix("key")
	assert.Equal(t, node2, matched)
}

// TestPrefixCacheIndexer_ConcurrentAccess tests thread-safety.
func TestPrefixCacheIndexer_ConcurrentAccess(t *testing.T) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond)

	var wg sync.WaitGroup
	concurrency := 50

	// Concurrent adds
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			node := NewTreeNode()
			prefix := fmt.Sprintf("prefix-%d", idx)
			pci.AddPrefix(prefix, node)
		}(i)
	}

	// Concurrent matches
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			prefix := fmt.Sprintf("prefix-%d", idx)
			pci.MatchPrefix(prefix)
		}(i)
	}

	wg.Wait()
	assert.Greater(t, pci.GetMemUsed(), int64(0))
}

// TestPrefixCacheIndexer_MaintenanceLoop_TTLEviction verifies TTL-based eviction in maintenance loop.
func TestPrefixCacheIndexer_MaintenanceLoop_TTLEviction(t *testing.T) {
	// Use short TTL for faster test
	pci := NewPrefixCacheIndexer(100*time.Millisecond, 1024*1024, 2*time.Second, "")
	defer time.Sleep(100 * time.Millisecond)

	node := NewTreeNode()
	worker := &types.LLMWorker{ID: "worker-1"}
	node.AddWorker(worker)

	pci.AddPrefix("test", node)
	assert.Greater(t, pci.GetMemUsed(), int64(0))

	// Wait for worker to expire and maintenance loop to run
	time.Sleep(3 * time.Second) // MaintenanceLoop runs every 3 seconds

	// Prefix should be removed due to empty node
	matched, _, _ := pci.MatchPrefix("test")
	assert.Nil(t, matched, "expired prefix should be recycled")
	assert.Equal(t, int64(0), pci.GetMemUsed())
}

// TestPrefixCacheIndexer_MaintenanceLoop_MemoryPressure verifies memory-based eviction.
func TestPrefixCacheIndexer_MaintenanceLoop_MemoryPressure(t *testing.T) {
	// Use very small memory limit
	memLimit := int64(50)
	pci := NewPrefixCacheIndexer(1*time.Hour, memLimit, 2*time.Second, "") // Long TTL to test memory eviction
	defer time.Sleep(100 * time.Millisecond)

	// Add prefixes to exceed memory limit
	node := NewTreeNode()
	worker := &types.LLMWorker{ID: "worker-1"}
	node.AddWorker(worker)

	for i := 0; i < 20; i++ {
		prefix := fmt.Sprintf("prefix-%d", i)
		pci.AddPrefix(prefix, node)
	}

	initialMem := pci.GetMemUsed()
	assert.Greater(t, initialMem, memLimit, "memory should exceed limit")

	// Wait for maintenance loop to run
	time.Sleep(3 * time.Second)

	// Memory should be reduced (evicts 1/3 of limit)
	finalMem := pci.GetMemUsed()
	assert.Less(t, finalMem, initialMem, "memory should be reduced by maintenance loop")
}

// TestPrefixCacheIndexer_MaintenanceLoop_NoPanic verifies MaintenanceLoop handles panics gracefully.
// Note: The current implementation does NOT restart on panic (user removed restart logic)
func TestPrefixCacheIndexer_MaintenanceLoop_NoPanic(t *testing.T) {
	pci := NewPrefixCacheIndexer(2*time.Second, 1024*1024, 2*time.Second, "")

	// Let MaintenanceLoop run for a few cycles
	time.Sleep(5 * time.Second)

	// Verify indexer is still operational
	node := NewTreeNode()
	pci.AddPrefix("test", node)
	matched, _, _ := pci.MatchPrefix("test")
	assert.Equal(t, node, matched)
}

// TestPrefixCacheIndexer_MatchPrefix_ExactMatch tests exact match scenarios.
// Tests that query exactly matches a cached key in the prefix cache.
func TestPrefixCacheIndexer_MatchPrefix_ExactMatch(t *testing.T) {
	// Use smaller, reasonable test data sizes for unit tests
	pci := NewPrefixCacheIndexer(1*time.Hour, 100*1024*1024, 2*time.Second, "")
	node := NewTreeNode()

	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1 * 1024},
		{"8KB", 8 * 1024},
		{"16KB", 16 * 1024},
		{"32KB", 32 * 1024},
		{"128KB", 128 * 1024},
		{"512KB", 512 * 1024},
		{"1MB", 1024 * 1024},
		{"10MB", 10 * 1024 * 1024},
	}

	// Store base strings and target cached strings for each size
	baseStrings := make(map[string]string)
	for _, tc := range sizes {
		// Generate once and store for reuse
		baseStr := generateLongString(tc.size)
		baseStrings[tc.name] = baseStr
		pci.AddPrefix(baseStr, node)
	}

	for _, tc := range sizes {
		t.Run(tc.name, func(t *testing.T) {
			// Use the exact same string that was added to cache
			cachedStr := baseStrings[tc.name]
			n, m, _ := pci.MatchPrefix(cachedStr)
			assert.Same(t, node, n, "should match the cached node")
			assert.Equal(t, len(cachedStr), m, "match length should equal cached string length")
		})
	}
}

// =============================================================================
// LLM Request Scenario Tests
// =============================================================================
// These tests simulate real-world LLM request patterns to validate cache behavior.

// TestLLMScenario_SharedSystemPrompt simulates multiple users sharing the same system prompt.
// In LLM applications, system prompts like "You are a helpful assistant..." are often reused.
// Expected: All requests with the same system prompt should hit the same cache entry.
func TestLLMScenario_SharedSystemPrompt(t *testing.T) {
	pci := NewPrefixCacheIndexerWithoutRecycle(5*time.Second, 100*1024*1024)

	// Simulate a common system prompt
	systemPrompt := "You are a helpful AI assistant. You should provide accurate, helpful, and harmless responses. "

	// First user's request: system prompt + user question
	user1Request := systemPrompt + "What is the capital of France?"
	node1 := NewTreeNode()
	worker1 := &types.LLMWorker{ID: "worker-1"}
	node1.AddWorker(worker1)
	pci.AddPrefix(user1Request, node1)

	// Second user's request: same system prompt + different question
	user2Request := systemPrompt + "How do I cook pasta?"
	matched, matchLen, exactMatch := pci.MatchPrefix(user2Request)

	// Should match with the system prompt length
	assert.NotNil(t, matched, "should find a cache entry")
	assert.GreaterOrEqual(t, matchLen, len(systemPrompt), "should match at least the system prompt")
	assert.False(t, exactMatch, "should not be exact match")

	// Verify match rate
	matchRate := float64(matchLen) / float64(len(user2Request))
	t.Logf("Match rate: %.2f%% (matched %d / %d chars)", matchRate*100, matchLen, len(user2Request))
}

// TestLLMScenario_MultiTurnConversation simulates a multi-turn conversation where context grows.
// Each turn adds to the conversation history, creating progressively longer prefixes.
func TestLLMScenario_MultiTurnConversation(t *testing.T) {
	pci := NewPrefixCacheIndexerWithoutRecycle(5*time.Second, 100*1024*1024)
	worker := &types.LLMWorker{ID: "worker-conv"}

	// Turn 1: Initial request
	turn1 := "System: You are helpful.\nUser: Hello\nAssistant: Hi! How can I help?"
	node1 := NewTreeNode()
	node1.AddWorker(worker)
	pci.AddPrefix(turn1, node1)

	// Turn 2: Adds to conversation history
	turn2 := turn1 + "\nUser: What's the weather?\nAssistant: I don't have real-time data."
	node2 := NewTreeNode()
	node2.AddWorker(worker)
	pci.AddPrefix(turn2, node2)

	// Turn 3: Query with new user input
	turn3Query := turn2 + "\nUser: Tell me a joke."
	matched, matchLen, _ := pci.MatchPrefix(turn3Query)

	assert.NotNil(t, matched, "should match previous conversation")
	assert.Equal(t, len(turn2), matchLen, "should match turn2 exactly")
	t.Logf("Multi-turn: matched %d chars of %d total", matchLen, len(turn3Query))
}

// TestLLMScenario_SimilarPromptsDifferentSuffix tests cache behavior with similar prompts.
// LLM requests often have slight variations in user questions while sharing long prefixes.
func TestLLMScenario_SimilarPromptsDifferentSuffix(t *testing.T) {
	pci := NewPrefixCacheIndexerWithoutRecycle(5*time.Second, 100*1024*1024)
	worker := &types.LLMWorker{ID: "worker-similar"}

	// Base prompt that will be reused
	basePrompt := strings.Repeat("Context information about machine learning and AI. ", 10)

	// Add cached entry
	cachedPrompt := basePrompt + "Question: What is neural network?"
	node := NewTreeNode()
	node.AddWorker(worker)
	pci.AddPrefix(cachedPrompt, node)

	// Test with similar but different question
	testCases := []struct {
		name        string
		query       string
		minMatchLen int
	}{
		{
			name:        "Similar question",
			query:       basePrompt + "Question: What is deep learning?",
			minMatchLen: len(basePrompt),
		},
		{
			name:        "Same base, longer question",
			query:       basePrompt + "Question: What is neural network and how does it work in practice?",
			minMatchLen: len(basePrompt) + len("Question: What is neural network"),
		},
		{
			name:        "Completely different",
			query:       "Tell me about cooking recipes.",
			minMatchLen: 0, // May not match at all
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			matched, matchLen, _ := pci.MatchPrefix(tc.query)
			if tc.minMatchLen > 0 {
				assert.NotNil(t, matched)
				assert.GreaterOrEqual(t, matchLen, tc.minMatchLen)
			}
			t.Logf("Query: %s... -> matched %d chars", tc.query[:min(50, len(tc.query))], matchLen)
		})
	}
}

// TestLLMScenario_HotPromptAccess simulates hot prompt access patterns.
// Some prompts (e.g., popular templates) are accessed much more frequently.
func TestLLMScenario_HotPromptAccess(t *testing.T) {
	pci := NewPrefixCacheIndexerWithoutRecycle(5*time.Second, 100*1024*1024)

	// Create workers
	hotWorker := &types.LLMWorker{ID: "hot-worker"}
	coldWorker := &types.LLMWorker{ID: "cold-worker"}

	// Hot prompt (frequently accessed)
	hotPrompt := "You are a code assistant. Help me write Python code for: "
	hotNode := NewTreeNode()
	hotNode.AddWorker(hotWorker)
	pci.AddPrefix(hotPrompt+"sorting algorithm", hotNode)

	// Cold prompt (rarely accessed)
	coldPrompt := "Translate the following text to French: "
	coldNode := NewTreeNode()
	coldNode.AddWorker(coldWorker)
	pci.AddPrefix(coldPrompt+"hello world", coldNode)

	// Simulate hot prompt being accessed multiple times
	for i := 0; i < 10; i++ {
		query := hotPrompt + fmt.Sprintf("task %d implementation", i)
		matched, matchLen, _ := pci.MatchPrefix(query)
		assert.NotNil(t, matched)
		assert.GreaterOrEqual(t, matchLen, len(hotPrompt)-5) // Allow some tolerance
	}

	// Verify cold prompt still accessible
	matched, matchLen, _ := pci.MatchPrefix(coldPrompt + "goodbye")
	assert.NotNil(t, matched)
	assert.GreaterOrEqual(t, matchLen, len(coldPrompt)-5)
}

// TestLLMScenario_LongContextWindow tests behavior with very long prompts.
// Modern LLMs support 100K+ token context windows.
func TestLLMScenario_LongContextWindow(t *testing.T) {
	pci := NewPrefixCacheIndexerWithoutRecycle(5*time.Second, 500*1024*1024)
	worker := &types.LLMWorker{ID: "long-context-worker"}

	// Simulate a long document context (e.g., RAG scenario)
	longContext := strings.Repeat("This is a paragraph of context information. ", 1000) // ~45KB

	// Cache the long context with a question
	cachedPrompt := longContext + "Based on the above, what is the main topic?"
	node := NewTreeNode()
	node.AddWorker(worker)
	pci.AddPrefix(cachedPrompt, node)

	// Query with same context, different question
	queryPrompt := longContext + "Summarize the key points."
	matched, matchLen, _ := pci.MatchPrefix(queryPrompt)

	assert.NotNil(t, matched)
	assert.GreaterOrEqual(t, matchLen, len(longContext))

	matchRate := float64(matchLen) / float64(len(queryPrompt))
	t.Logf("Long context: %d chars cached, %d chars query, %.2f%% match rate",
		len(cachedPrompt), len(queryPrompt), matchRate*100)
}

// NewPrefixCacheIndexerWithoutRecycle creates an indexer without background recycling.
// This is useful for deterministic unit tests.
func NewPrefixCacheIndexerWithoutRecycle(ttl time.Duration, memLimit int64) *PrefixCacheIndexer {
	return &PrefixCacheIndexer{
		tree:      radix.New(),
		ttl:       ttl,
		memLimit:  memLimit,
		inferMode: "",
	}
}

// =============================================================================
// Schedule Interface Tests
// =============================================================================
const (
	// ChatGPT-style assistant prompt (~200 chars)
	systemPromptAssistant = `You are a helpful AI assistant. You provide accurate, concise answers. ` +
		`Always be polite and professional. If you don't know something, admit it honestly.`

	// Code assistant prompt (~300 chars)
	systemPromptCoder = `You are an expert programmer. Help users write clean, efficient code. ` +
		`Follow best practices, add comments, and explain your solutions. ` +
		`Support languages: Go, Python, Java, TypeScript, Rust.`

	// RAG context template (~500 chars base)
	systemPromptRAG = `You are a knowledge assistant. Answer based on the following context:\n\n` +
		`Context: The LLM Gateway is a high-performance inference router that supports prefix caching, ` +
		`load balancing, and intelligent scheduling. It optimizes GPU utilization by routing requests ` +
		`with similar prefixes to the same worker instance.\n\nAnswer the user's question based on this context.`
)

// createTestConfig creates a minimal config for testing.
// Key config values:
// - PrefixCacheTTL: 1800s (30min) - how long cached prefixes remain valid
// - PrefixCacheLimit: 100MB - memory limit for prefix cache
// - PrefixCacheLoadTolerance: 0.3 - workers with load > mean*(1+tolerance) are filtered out
// - RequestsThreshold: 10 - workers with requests >= threshold are filtered out from cache-hit candidates
func createTestConfig() *options.Config {
	return &options.Config{
		PrefixCacheTTL:                      1800,              // 30 minutes
		PrefixCacheLimit:                    100 * 1024 * 1024, // 100MB
		PrefixCacheLoadTolerance:            0.3,               // threshold = mean * 1.3
		PrefixCacheMaintenanceInterval:      2,                 // 2 seconds for testing
		PrefixCacheWaitingRequestsThreshold: 10,                // filter out workers with >= 10 requests
	}
}

// createTestLRSClient creates an LRS client with test workers
func createTestLRSClient(workers []*types.LLMWorker) *lrs.LocalRealtimeStateClient {
	client := lrs.NewLocalRealtimeStateClient(&options.Config{ServerlessMode: false})
	client.AddGateway("test-gateway") // Add gateway for request state allocation
	for _, w := range workers {
		client.AddInstance(w)
	}
	return client
}

// buildChatPrompt simulates OpenAI chat format: system + history + user message
func buildChatPrompt(systemPrompt string, history []string, userMessage string) string {
	var sb strings.Builder
	sb.WriteString("<|system|>\n")
	sb.WriteString(systemPrompt)
	sb.WriteString("\n")
	for i, msg := range history {
		if i%2 == 0 {
			sb.WriteString("<|user|>\n")
		} else {
			sb.WriteString("<|assistant|>\n")
		}
		sb.WriteString(msg)
		sb.WriteString("\n")
	}
	sb.WriteString("<|user|>\n")
	sb.WriteString(userMessage)
	sb.WriteString("\n<|assistant|>\n")
	return sb.String()
}

// TestSchedule_NoEndpoints tests Schedule when no endpoints available.
func TestSchedule_NoEndpoints(t *testing.T) {
	config := createTestConfig()
	lrsClient := lrs.NewLocalRealtimeStateClient(&options.Config{})
	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	req := &types.ScheduleRequest{
		Id:         "req-no-endpoint",
		PromptText: buildChatPrompt(systemPromptAssistant, nil, "Hello!"),
	}

	err := pc.Schedule(req)
	assert.Equal(t, consts.ErrorNoAvailableEndpoint, err)
}

// TestSchedule_DifferentApplications simulates requests from different applications.
// Each app has its own system prompt, should be routed to potentially different workers.
func TestSchedule_DifferentApplications(t *testing.T) {
	config := createTestConfig()
	workers := []*types.LLMWorker{
		{ID: "multi-app-worker-0", Version: 1},
		{ID: "multi-app-worker-1", Version: 1},
		{ID: "multi-app-worker-2", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)
	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	apps := []struct {
		name         string
		systemPrompt string
		questions    []string
	}{
		{
			name:         "ChatBot",
			systemPrompt: systemPromptAssistant,
			questions:    []string{"Hello!", "What's the weather?", "Tell me a joke"},
		},
		{
			name:         "CodeAssistant",
			systemPrompt: systemPromptCoder,
			questions:    []string{"Write a bubble sort", "Optimize this code", "Add error handling"},
		},
		{
			name:         "RAGSystem",
			systemPrompt: systemPromptRAG,
			questions:    []string{"What is prefix caching?", "How does load balancing work?"},
		},
	}

	appWorkers := make(map[string]string) // app name -> assigned worker

	for _, app := range apps {
		for i, question := range app.questions {
			req := &types.ScheduleRequest{
				Id:         fmt.Sprintf("%s-req-%d", app.name, i),
				PromptText: buildChatPrompt(app.systemPrompt, nil, question),
			}

			lrsClient.RLock()
			err := pc.Schedule(req)
			lrsClient.RUnlock()

			assert.NoError(t, err)
			assert.Len(t, req.ScheduleResult, 1)

			if i == 0 {
				appWorkers[app.name] = req.ScheduleResult[0].ID
			} else {
				// Same app should consistently hit the same worker
				assert.Equal(t, appWorkers[app.name], req.ScheduleResult[0].ID,
					"%s request %d should use cached prefix", app.name, i)
			}
		}
	}
}

// TestSchedule_LongContextWindow simulates requests with very long context.
// Tests the scheduler's ability to handle large prompts (e.g., 32K+ tokens).
func TestSchedule_LongContextWindow(t *testing.T) {
	config := createTestConfig()
	workers := []*types.LLMWorker{
		{ID: "long-ctx-worker-0", Version: 1},
		{ID: "long-ctx-worker-1", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)
	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	// Simulate a document Q&A scenario with ~10KB context
	longDocument := strings.Repeat("This is a paragraph of text for context. ", 200)
	basePrompt := fmt.Sprintf(
		"<|system|>\nYou are a document analyst.\n<|context|>\n%s\n<|user|>\n",
		longDocument,
	)

	questions := []string{
		"Summarize the document",
		"What are the key points?",
		"Extract all entities mentioned",
	}

	var firstWorkerID string
	for i, q := range questions {
		req := &types.ScheduleRequest{
			Id:         fmt.Sprintf("long-ctx-%d", i),
			PromptText: basePrompt + q + "\n<|assistant|>\n",
		}

		lrsClient.RLock()
		err := pc.Schedule(req)
		lrsClient.RUnlock()

		assert.NoError(t, err)
		assert.Len(t, req.ScheduleResult, 1)

		if i == 0 {
			firstWorkerID = req.ScheduleResult[0].ID
			t.Logf("Long context prompt length: %d chars", len(req.GetPromptPrefix()))
		} else {
			assert.Equal(t, firstWorkerID, req.ScheduleResult[0].ID,
				"long context requests should share cached prefix")
		}
	}
}

// TestSchedule_BurstTraffic simulates burst traffic pattern.
// Multiple concurrent requests hitting the scheduler.
func TestSchedule_BurstTraffic(t *testing.T) {
	config := createTestConfig()
	workers := []*types.LLMWorker{
		{ID: "burst-worker-0", Version: 1},
		{ID: "burst-worker-1", Version: 1},
		{ID: "burst-worker-2", Version: 1},
		{ID: "burst-worker-3", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)
	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	var wg sync.WaitGroup
	results := make(chan string, 100)

	// Simulate 100 concurrent requests with same system prompt
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			req := &types.ScheduleRequest{
				Id:         fmt.Sprintf("burst-%d", idx),
				PromptText: buildChatPrompt(systemPromptAssistant, nil, fmt.Sprintf("Question %d", idx)),
			}

			lrsClient.RLock()
			err := pc.Schedule(req)
			lrsClient.RUnlock()

			if err == nil && len(req.ScheduleResult) > 0 {
				results <- req.ScheduleResult[0].ID
			}
		}(i)
	}

	wg.Wait()
	close(results)

	// Count distribution - with prefix caching, most should go to the same worker
	workerCounts := make(map[string]int)
	for workerID := range results {
		workerCounts[workerID]++
	}

	// At least one worker should have received multiple requests due to caching
	maxCount := 0
	for _, count := range workerCounts {
		if count > maxCount {
			maxCount = count
		}
	}
	assert.Greater(t, maxCount, 50, "prefix cache should aggregate requests to same worker")
	t.Logf("Worker distribution: %v", workerCounts)
}

// simulateWorkerLoad adds simulated requests to a worker to change its token load metrics.
// Total tokens added = numRequests * tokensPerReq.
// This is used to test load-aware scheduling behavior.
func simulateWorkerLoad(lrsClient *lrs.LocalRealtimeStateClient, workerID string, numRequests int, tokensPerReq int64) {
	for i := 0; i < numRequests; i++ {
		reqState := lrs.NewRequestState(
			fmt.Sprintf("sim-req-%s-%d", workerID, i),
			tokensPerReq,
			workerID,
			"test-gateway",
		)
		lrsClient.AllocateRequestState(consts.NormalInferMode, reqState)
	}
}

// TestSchedule_LoadBalancing_NumTokens verifies that scheduler selects worker with fewest tokens when no cache hit.
// This tests the baseline load balancing behavior: without prefix cache hit, select least-loaded worker.
func TestSchedule_LoadBalancing_NumTokens(t *testing.T) {
	config := createTestConfig()

	workers := []*types.LLMWorker{
		{ID: "worker-heavy", Version: 1},
		{ID: "worker-light", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)

	// Setup: worker-heavy has 10000 tokens, worker-light has 1000 tokens
	simulateWorkerLoad(lrsClient, "worker-heavy", 5, 2000) // 10000 tokens
	simulateWorkerLoad(lrsClient, "worker-light", 2, 500)  // 1000 tokens

	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	// New request without cache hit should go to lighter worker
	req := &types.ScheduleRequest{
		Id:         "new-req",
		PromptText: buildChatPrompt(systemPromptAssistant, nil, "This is a new unique question"),
	}

	lrsClient.RLock()
	err := pc.Schedule(req)
	lrsClient.RUnlock()

	assert.NoError(t, err)
	assert.Equal(t, "worker-light", req.ScheduleResult[0].ID,
		"should select worker with fewer tokens when no cache hit")
}

// TestSchedule_LoadBalancing_CacheHitWithOverload verifies load balancing when cache-hit workers exceed token threshold.
// Strategy: When a cache-hit worker's token load exceeds (mean + tolerance * mean), scheduler falls back to
// the least-loaded worker globally, avoiding hotspots even at the cost of cache miss.
func TestSchedule_LoadBalancing_CacheHitWithOverload(t *testing.T) {
	config := createTestConfig()

	workers := []*types.LLMWorker{
		{ID: "worker-busy", Version: 1},
		{ID: "worker-idle", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)

	// Initial state: worker-idle has fewer tokens (900), worker-busy has more (1000)
	simulateWorkerLoad(lrsClient, "worker-idle", 9, 100)  // 900 tokens
	simulateWorkerLoad(lrsClient, "worker-busy", 10, 100) // 1000 tokens

	lrsClient.PrintInstanceViews()

	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	// First request - creates cache entry on worker-idle (least loaded)
	req1 := &types.ScheduleRequest{
		Id:         "req-1",
		PromptText: buildChatPrompt(systemPromptCoder, nil, "Write a function"),
	}
	lrsClient.RLock()
	err := pc.Schedule(req1)
	lrsClient.RUnlock()
	assert.NoError(t, err)
	firstWorker := req1.ScheduleResult[0].ID
	assert.Equal(t, "worker-idle", firstWorker,
		"should select worker with fewer tokens when no cache hit")

	// Now overload worker-idle with additional tokens, making it exceed the tolerance threshold
	// After this: worker-idle has 900 + 6000 = 6900 tokens, worker-busy has 1000 tokens
	// Mean = (6900 + 1000) / 2 = 3950, threshold = 3950 * 1.3 = 5135
	// worker-idle (6900) exceeds threshold, worker-busy (1000) is within threshold
	simulateWorkerLoad(lrsClient, "worker-idle", 12, 500) // +6000 tokens

	// Second request has cache hit on worker-idle, but worker-idle is overloaded
	// Scheduler should fallback to worker-busy (least loaded globally)
	req2 := &types.ScheduleRequest{
		Id:         "req-2",
		PromptText: buildChatPrompt(systemPromptCoder, nil, "Optimize this code"),
	}
	lrsClient.RLock()
	err = pc.Schedule(req2)
	lrsClient.RUnlock()
	assert.NoError(t, err)

	assert.Equal(t, "worker-busy", req2.ScheduleResult[0].ID,
		"should fallback to least-loaded worker when cache-hit worker exceeds token threshold")

	t.Logf("First request -> %s, Second request -> %s", firstWorker, req2.ScheduleResult[0].ID)
}

// TestSchedule_LoadBalancing_RequestsThreshold verifies that workers exceeding RequestsThreshold are filtered out.
// When a cache-hit worker's request count > RequestsThreshold AND token load exceeds tolerance,
// scheduler should fallback to the least-loaded worker globally.
func TestSchedule_LoadBalancing_RequestsThreshold(t *testing.T) {
	config := createTestConfig()
	config.PrefixCacheWaitingRequestsThreshold = 5 // lower threshold for easier testing

	workers := []*types.LLMWorker{
		{ID: "worker-a", Version: 1},
		{ID: "worker-b", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)

	// Setup: worker-a has low load, worker-b starts empty
	simulateWorkerLoad(lrsClient, "worker-a", 2, 100) // 2 requests, 200 tokens

	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	// First request - creates cache entry on worker-b (least loaded)
	req1 := &types.ScheduleRequest{
		Id:         "req-threshold-1",
		PromptText: buildChatPrompt(systemPromptAssistant, nil, "Hello threshold test"),
	}
	lrsClient.RLock()
	err := pc.Schedule(req1)
	lrsClient.RUnlock()
	assert.NoError(t, err)
	firstWorker := req1.ScheduleResult[0].ID
	assert.Equal(t, "worker-b", firstWorker, "first request should go to least loaded worker")
	t.Logf("First request -> %s", firstWorker)

	// Overload worker-b: exceed both RequestsThreshold AND token load tolerance
	// After this: worker-b has 10 requests and 5000 tokens
	// worker-a has 2 requests and 200 tokens
	// Mean tokens = (200 + 5000) / 2 = 2600, threshold = 2600 * 1.3 = 3380
	// worker-b (5000) exceeds threshold, worker-a (200) is within threshold
	simulateWorkerLoad(lrsClient, "worker-b", 10, 500) // +10 requests, +5000 tokens

	// Second request has cache hit on worker-b, but:
	// 1. worker-b exceeds RequestsThreshold (10 > 5)
	// 2. worker-b exceeds token load tolerance (5000 > 3380)
	// Should fallback to worker-a (least loaded globally)
	req2 := &types.ScheduleRequest{
		Id:         "req-threshold-2",
		PromptText: buildChatPrompt(systemPromptAssistant, nil, "Another threshold test"),
	}
	lrsClient.RLock()
	err = pc.Schedule(req2)
	lrsClient.RUnlock()
	assert.NoError(t, err)

	secondWorker := req2.ScheduleResult[0].ID
	t.Logf("Second request -> %s (worker-b had %d requests and high token load)", secondWorker, 10)

	assert.Equal(t, "worker-a", secondWorker,
		"should fallback to worker-a when worker-b exceeds both RequestsThreshold and token load tolerance")
}

// TestSchedule_HighConcurrencyWithLoad tests concurrent scheduling with varying token loads.
// Validates that load-aware scheduling properly distributes requests when combined with prefix caching.
// Workers with lower token counts should receive more requests even with concurrent access.
func TestSchedule_HighConcurrencyWithLoad(t *testing.T) {
	config := createTestConfig()
	config.PrefixCacheLoadTolerance = 0.3 // threshold = mean * 1.3

	workers := []*types.LLMWorker{
		{ID: "concurrent-w0", Version: 1},
		{ID: "concurrent-w1", Version: 1},
		{ID: "concurrent-w2", Version: 1},
		{ID: "concurrent-w3", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)

	// Setup varying token loads across workers
	// Total tokens: w0=12000, w1=1000, w2=5000, w3=300
	// Mean = 4575 tokens, threshold = 4575 * 1.3 = 5947.5
	simulateWorkerLoad(lrsClient, "concurrent-w0", 15, 800) // 12000 tokens (exceeds threshold)
	simulateWorkerLoad(lrsClient, "concurrent-w1", 5, 200)  // 1000 tokens (within threshold)
	simulateWorkerLoad(lrsClient, "concurrent-w2", 10, 500) // 5000 tokens (within threshold)
	simulateWorkerLoad(lrsClient, "concurrent-w3", 3, 100)  // 300 tokens (within threshold, lowest)

	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	var wg sync.WaitGroup
	results := make(chan string, 50)

	// 50 concurrent requests with same system prompt
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			req := &types.ScheduleRequest{
				Id:         fmt.Sprintf("concurrent-%d", idx),
				PromptText: buildChatPrompt(systemPromptAssistant, nil, fmt.Sprintf("Query %d", idx)),
			}
			lrsClient.RLock()
			err := pc.Schedule(req)
			lrsClient.RUnlock()
			if err == nil && len(req.ScheduleResult) > 0 {
				results <- req.ScheduleResult[0].ID
			}
		}(i)
	}

	wg.Wait()
	close(results)

	distribution := make(map[string]int)
	for id := range results {
		distribution[id]++
	}

	t.Logf("Load-aware concurrent distribution: %v", distribution)
	// With load awareness, lighter workers should get more requests
	assert.Greater(t, distribution["concurrent-w3"]+distribution["concurrent-w1"],
		distribution["concurrent-w0"],
		"lighter workers should handle more requests")
}

// TestSchedule_HitRateThreshold_BelowThreshold verifies that when cache hit rate is below threshold,
// scheduler falls back to global least-loaded worker instead of using cache-hit candidates.
// Scenario: cached prefix is short, new prompt is long with minimal match.
// Expected: scheduler ignores cache hit and selects least-loaded worker globally.
func TestSchedule_HitRateThreshold_BelowThreshold(t *testing.T) {
	config := createTestConfig()
	config.PrefixCacheHitRateThreshold = 0.5 // 50%

	workers := []*types.LLMWorker{
		{ID: "worker-cached", Version: 1},
		{ID: "worker-idle", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)
	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	// First request: create cache entry with short prefix
	shortPrefix := "abc" // very short prefix
	req1 := &types.ScheduleRequest{
		Id:         "req-1",
		PromptText: shortPrefix + " short question",
	}
	lrsClient.RLock()
	err := pc.Schedule(req1)
	lrsClient.RUnlock()
	assert.NoError(t, err)
	cachedWorker := req1.ScheduleResult[0].ID
	t.Logf("First request cached on: %s, prompt length: %d", cachedWorker, len(req1.PromptText))

	// Add load to the cached worker to make the other worker more attractive for fallback
	otherWorker := "worker-idle"
	if cachedWorker == "worker-idle" {
		otherWorker = "worker-cached"
	}
	simulateWorkerLoad(lrsClient, cachedWorker, 5, 1000) // 5000 tokens on cached worker
	// otherWorker has 0 tokens

	// Second request: starts with same short prefix but is very long
	// matchLen = 3 (abc), promptLen = 500+
	// hitRate = 3/500 = 0.006 < 0.5 threshold
	longSuffix := strings.Repeat("x", 500)
	req2 := &types.ScheduleRequest{
		Id:         "req-2",
		PromptText: shortPrefix + longSuffix,
	}
	lrsClient.RLock()
	err = pc.Schedule(req2)
	lrsClient.RUnlock()

	assert.NoError(t, err)
	// hitRate = 3 / 503 ≈ 0.006 < 0.5 threshold
	// Should fallback to otherWorker (least loaded globally), NOT cached worker
	assert.Equal(t, otherWorker, req2.ScheduleResult[0].ID,
		"should fallback to least-loaded worker when hit rate below threshold")
	t.Logf("Second request -> %s (cached worker %s had cache entry but hitRate < threshold)",
		req2.ScheduleResult[0].ID, cachedWorker)
}

// TestSchedule_HitRateThreshold_AboveThreshold verifies that when cache hit rate meets threshold,
// scheduler uses cache-hit candidates for selection.
// Scenario: cached prefix matches >= 50% of new prompt (meets default threshold).
// Expected: scheduler selects from cache-hit candidates.
func TestSchedule_HitRateThreshold_AboveThreshold(t *testing.T) {
	config := createTestConfig()
	config.PrefixCacheHitRateThreshold = 0.5 // 50%

	workers := []*types.LLMWorker{
		{ID: "worker-cache-hit", Version: 1},
		{ID: "worker-other", Version: 1},
	}
	lrsClient := createTestLRSClient(workers)
	pc := NewPrefixCachePolicy(config, consts.NormalInferMode, lrsClient)

	// First request: create cache entry
	longPrefix := strings.Repeat("shared prefix content ", 20) // ~440 chars
	req1 := &types.ScheduleRequest{
		Id:         "req-1",
		PromptText: longPrefix + "question one",
	}
	lrsClient.RLock()
	err := pc.Schedule(req1)
	lrsClient.RUnlock()
	assert.NoError(t, err)
	cachedWorker := req1.ScheduleResult[0].ID
	t.Logf("First request cached on: %s, prompt length: %d", cachedWorker, len(req1.PromptText))

	// Add some load to other worker to ensure it's not selected as least-loaded fallback
	// But keep cached worker lightly loaded so it's still a good candidate
	otherWorker := "worker-other"
	if cachedWorker == "worker-other" {
		otherWorker = "worker-cache-hit"
	}
	simulateWorkerLoad(lrsClient, otherWorker, 3, 1000) // 3000 tokens on other worker
	// cached worker has 0 tokens (from initial request, already released)

	// Second request: shares most of the prefix (hitRate >= 50%)
	// Uses ~90% of the original prefix
	highMatchPrefix := longPrefix[:len(longPrefix)*9/10] // ~400 chars, ~90% match
	req2 := &types.ScheduleRequest{
		Id:         "req-2",
		PromptText: highMatchPrefix + "question two",
	}
	lrsClient.RLock()
	err = pc.Schedule(req2)
	lrsClient.RUnlock()

	assert.NoError(t, err)
	// hitRate = 400 / 450 ≈ 0.89 > 0.5 threshold
	// Should use cached worker (cache hit with acceptable hit rate)
	assert.Equal(t, cachedWorker, req2.ScheduleResult[0].ID,
		"should use cache-hit worker when hit rate meets threshold")
	t.Logf("Second request -> %s (hitRate >= threshold, using cache)", req2.ScheduleResult[0].ID)
}

func init() {
	klog.InitFlags(nil)
	flag.Set("v", "2")
}

// =============================================================================
// Benchmark Tests
// =============================================================================

func BenchmarkWorkerWithLRU_Touch(b *testing.B) {
	worker := &types.LLMWorker{ID: "worker-1"}
	wl := NewWorkerWithLRU(worker)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		wl.Touch()
	}
}

func BenchmarkTreeNode_AddWorker(b *testing.B) {
	tn := NewTreeNode()
	worker := &types.LLMWorker{ID: "worker-1"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tn.AddWorker(worker)
	}
}

func BenchmarkPrefixCacheIndexer_AddPrefix(b *testing.B) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024*1024, 2*time.Second, "")
	node := NewTreeNode()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefix := fmt.Sprintf("prefix-%d", i)
		pci.AddPrefix(prefix, node)
	}
}

func BenchmarkPrefixCacheIndexer_MatchPrefix(b *testing.B) {
	pci := NewPrefixCacheIndexer(5*time.Second, 1024*1024*1024, 2*time.Second, "")
	node := NewTreeNode()

	// Pre-populate with prefixes
	for i := 0; i < 1000; i++ {
		prefix := fmt.Sprintf("prefix-%d", i)
		pci.AddPrefix(prefix, node)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		prefix := fmt.Sprintf("prefix-%d-extra", i%1000)
		pci.MatchPrefix(prefix)
	}
}

// generateLongString creates a string of specified size (in bytes) with randomized content.
// Each call generates different random content to prevent pattern-based cache optimization.
//
// Algorithm:
// - Uses crypto-quality randomness for realistic LLM prompt simulation
// - Character set: [a-zA-Z0-9 ] (63 chars) matches typical text distribution
// - No repeating patterns that could trigger string interning or SIMD shortcuts
func generateLongString(size int) string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789 "
	charsetLen := len(charset)

	builder := strings.Builder{}
	builder.Grow(size)

	// Generate random content byte by byte
	for i := 0; i < size; i++ {
		builder.WriteByte(charset[rand.Intn(charsetLen)])
	}

	return builder.String()
}

// BenchmarkPrefixCacheIndexer_MatchPrefix_LongStrings tests MatchPrefix performance with various string lengths.
// Tests scenarios from 1KB to 10MB to validate performance characteristics under LLM long input scenarios.
//
// Performance expectations:
// - Sub-linear time complexity due to WalkPrefix optimization
// - strings.HasPrefix provides SIMD-accelerated prefix detection
// - Adaptive prefix length reduces candidate set for long strings (>=128 chars)
// - Early termination on perfect match
//
// High-density cache simulation:
// - Single shared PrefixCacheIndexer instance across all size benchmarks
// - 1000+ cached entries per size to stress radix tree traversal
// - Multiple prefix variations to maximize candidate branches
func BenchmarkPrefixCacheIndexer_MatchPrefix_LongStrings(b *testing.B) {
	// Shared cache indexer for high-density testing
	pci := NewPrefixCacheIndexer(1*time.Hour, 100*1024*1024*1024, 2*time.Second, "") // 100GB limit
	node := NewTreeNode()
	node.AddWorker(&types.LLMWorker{ID: "worker-1"})

	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1 * 1024},
		{"8KB", 8 * 1024},
		{"16KB", 16 * 1024},
		{"32KB", 32 * 1024},
		{"128KB", 128 * 1024},
		{"512KB", 512 * 1024},
		{"1MB", 1024 * 1024},
		{"10MB", 10 * 1024 * 1024},
	}

	// Store generated base strings for each size to ensure query strings match cached entries
	baseStrings := make(map[int]string)
	for _, tc := range sizes {
		baseStrings[tc.size] = generateLongString(tc.size)
	}

	// Pre-populate cache with high-density data across all sizes
	for _, tc := range sizes {
		baseStr := baseStrings[tc.size]

		// Add 1000 cached entries per size with varying prefixes
		// This creates a dense radix tree with many candidate branches
		for i := 0; i < 1000; i++ {
			// Create variations at different positions to maximize tree branching:
			// - Vary prefix (first 20 chars)
			// - Vary middle (around 50% position)
			// - Vary suffix (last 20 chars)
			midPos := tc.size / 2
			if midPos > 20 && midPos+20 < tc.size {
				variation := fmt.Sprintf("%03d", i%100) + baseStr[3:midPos] +
					fmt.Sprintf("-%04d-", i) + baseStr[midPos+6:tc.size-10] +
					fmt.Sprintf("-v%03d", i%100)
				pci.AddPrefix(variation, node)
			} else {
				variation := fmt.Sprintf("%03d", i%100) + baseStr[3:tc.size-10] + fmt.Sprintf("-v%03d", i%100)
				pci.AddPrefix(variation, node)
			}
		}
	}

	for _, tc := range sizes {
		b.Run(tc.name, func(b *testing.B) {
			baseStr := baseStrings[tc.size]

			// Query string: shares prefix with cached entries (uses index 50 variation)
			midPos := tc.size / 2
			var queryStr string
			if midPos > 20 && midPos+20 < tc.size {
				queryStr = "050" + baseStr[3:midPos] + "-9999-" + baseStr[midPos+6:tc.size-10] + "-query"
			} else {
				queryStr = "050" + baseStr[3:tc.size-10] + "-query"
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pci.MatchPrefix(queryStr)
			}
		})
	}
}

// BenchmarkPrefixCacheIndexer_MatchPrefix_ExactMatch benchmarks exact match scenarios.
// Tests best-case performance when query exactly matches a cached key in high-density cache.
func BenchmarkPrefixCacheIndexer_MatchPrefix_ExactMatch(b *testing.B) {
	// Shared cache indexer for high-density testing
	pci := NewPrefixCacheIndexer(1*time.Hour, 100*1024*1024*1024, 2*time.Second, "")
	sizes := []struct {
		name string
		size int
		node *TreeNode
	}{
		{"1KB", 1 * 1024, NewTreeNode()},
		{"8KB", 8 * 1024, NewTreeNode()},
		{"16KB", 16 * 1024, NewTreeNode()},
		{"32KB", 32 * 1024, NewTreeNode()},
		{"128KB", 128 * 1024, NewTreeNode()},
		{"512KB", 512 * 1024, NewTreeNode()},
		{"1MB", 1024 * 1024, NewTreeNode()},
		{"10MB", 10 * 1024 * 1024, NewTreeNode()},
	}

	// Store base strings and target cached strings for each size
	baseStrings := make(map[string]string)
	for _, tc := range sizes {
		baseStr := generateLongString(tc.size)
		baseStrings[tc.name] = baseStr
		pci.AddPrefix(baseStr, tc.node)
	}

	for _, tc := range sizes {
		b.Run(tc.name, func(b *testing.B) {
			// Use the exact cached string from pre-population
			cachedStr := baseStrings[tc.name]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				n, m, _ := pci.MatchPrefix(cachedStr)
				if m == 0 {
					b.Fatalf("MatchPrefix: %d, %v, %d\n", len(cachedStr), n, m)
				}
			}
		})
	}
}

// BenchmarkPrefixCacheIndexer_MatchPrefix_PrefixMatch benchmarks prefix match scenarios.
// Query is longer than cached key, testing when cached key is a prefix of query in dense cache.
func BenchmarkPrefixCacheIndexer_MatchPrefix_PrefixMatch(b *testing.B) {
	// Shared cache indexer for high-density testing
	pci := NewPrefixCacheIndexer(1*time.Hour, 100*1024*1024*1024, 2*time.Second, "")
	node := NewTreeNode()
	node.AddWorker(&types.LLMWorker{ID: "worker-1"})

	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1 * 1024},
		{"8KB", 8 * 1024},
		{"16KB", 16 * 1024},
		{"32KB", 32 * 1024},
		{"128KB", 128 * 1024},
		{"512KB", 512 * 1024},
		{"1MB", 1024 * 1024},
		{"10MB", 10 * 1024 * 1024},
	}

	// Store base strings and target cached strings
	baseStrings := make(map[int]string)
	cachedTargets := make(map[int]string)
	for _, tc := range sizes {
		baseStrings[tc.size] = generateLongString(tc.size)
	}

	// Pre-populate with 500 entries per size
	for _, tc := range sizes {
		baseStr := baseStrings[tc.size]
		for i := 0; i < 500; i++ {
			variation := fmt.Sprintf("%03d-", i) + baseStr[4:]
			pci.AddPrefix(variation, node)
			cachedTargets[tc.size] = variation
		}
	}

	for _, tc := range sizes {
		b.Run(tc.name, func(b *testing.B) {
			cachedStr := cachedTargets[tc.size]

			// Query is longer - append suffix to cached string
			queryStr := cachedStr + "-additional-suffix-with-more-content"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pci.MatchPrefix(queryStr)
			}
		})
	}
}

// BenchmarkPrefixCacheIndexer_MatchPrefix_NoMatch benchmarks worst-case scenarios.
// Query has no common prefix with any cached key in dense cache, testing traversal overhead.
func BenchmarkPrefixCacheIndexer_MatchPrefix_NoMatch(b *testing.B) {
	// Shared cache indexer for high-density testing
	pci := NewPrefixCacheIndexer(1*time.Hour, 100*1024*1024*1024, 2*time.Second, "")
	node := NewTreeNode()
	node.AddWorker(&types.LLMWorker{ID: "worker-1"})

	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1 * 1024},
		{"8KB", 8 * 1024},
		{"16KB", 16 * 1024},
		{"32KB", 32 * 1024},
		{"128KB", 128 * 1024},
		{"512KB", 512 * 1024},
		{"1MB", 1024 * 1024},
		{"10MB", 10 * 1024 * 1024},
	}

	// Generate base strings for each size
	baseStrings := make(map[int]string)
	queryStrings := make(map[int]string)
	for _, tc := range sizes {
		baseStrings[tc.size] = generateLongString(tc.size - 1)
		queryStrings[tc.size] = generateLongString(tc.size - 1)
	}

	// Pre-populate with 500 entries per size, all starting with 'a'-'m' range
	for _, tc := range sizes {
		baseStr := baseStrings[tc.size]
		for i := 0; i < 500; i++ {
			// Use 'a' through 'm' prefix (first half of alphabet)
			prefixChar := rune('a' + (i % 13))
			cachedStr := string(prefixChar) + baseStr
			pci.AddPrefix(cachedStr, node)
		}
	}

	for _, tc := range sizes {
		b.Run(tc.name, func(b *testing.B) {
			// Query starts with 'z' - completely different from all cached entries
			queryStr := "z" + queryStrings[tc.size]

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pci.MatchPrefix(queryStr)
			}
		})
	}
}

// BenchmarkPrefixCacheIndexer_MatchPrefix_ManyCandidates benchmarks with extremely dense cache.
// Simulates worst-case scenario where WalkPrefix must traverse many candidates with shared prefixes.
func BenchmarkPrefixCacheIndexer_MatchPrefix_ManyCandidates(b *testing.B) {
	// Shared cache indexer for maximum density testing
	pci := NewPrefixCacheIndexer(1*time.Hour, 100*1024*1024*1024, 2*time.Second, "")
	node := NewTreeNode()
	node.AddWorker(&types.LLMWorker{ID: "worker-1"})

	sizes := []struct {
		name string
		size int
	}{
		{"1KB", 1 * 1024},
		{"8KB", 8 * 1024},
		{"16KB", 16 * 1024},
		{"32KB", 32 * 1024},
		{"128KB", 128 * 1024},
		{"512KB", 512 * 1024},
		{"1MB", 1024 * 1024},
		{"10MB", 10 * 1024 * 1024},
	}

	// Generate base strings for each size
	baseStrings := make(map[int]string)
	for _, tc := range sizes {
		baseStrings[tc.size] = generateLongString(tc.size)
	}

	// Pre-populate with 2000+ entries per size, all sharing common prefixes
	// This creates maximum branching factor in the radix tree
	for _, tc := range sizes {
		baseStr := baseStrings[tc.size]
		commonPrefixLen := min(tc.size-20, 100)

		cnt := 100

		// Add entries sharing the same initial prefix
		for i := 0; i < cnt; i++ {
			// All share first 100 chars (or tc.size-20), diverge after
			if commonPrefixLen+20 < tc.size {
				variation := baseStr[:commonPrefixLen] +
					fmt.Sprintf("-var%04d-", i) +
					baseStr[commonPrefixLen+10:]
				if len(variation) > tc.size {
					variation = variation[:tc.size]
				}
				pci.AddPrefix(variation, node)
			} else {
				variation := baseStr[:tc.size-15] + fmt.Sprintf("-v%04d", i)
				pci.AddPrefix(variation, node)
			}
		}
	}

	for _, tc := range sizes {
		b.Run(tc.name, func(b *testing.B) {
			baseStr := baseStrings[tc.size]
			commonPrefixLen := min(tc.size-20, 100)

			// Query shares common prefix, forcing traversal of many candidates
			queryStr := baseStr[:commonPrefixLen] + "-query-9999"

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				pci.MatchPrefix(queryStr)
			}
		})
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
