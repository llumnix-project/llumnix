package schedule_policy

import (
	"container/list"
	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/lrs"
	"llm-gateway/pkg/metrics"
	ratelimiter "llm-gateway/pkg/service/rate-limiter"
	"llm-gateway/pkg/types"
	"llm-gateway/pkg/utils/radix"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/klog/v2"
)

// WorkerWithLRU wraps a worker with LRU tracking metadata.
// It maintains the last access timestamp for cache eviction policies.
type WorkerWithLRU struct {
	mu         sync.Mutex
	worker     *types.LLMWorker
	lastAccess time.Time
}

// NewWorkerWithLRU creates a new LRU-tracked worker wrapper.
func NewWorkerWithLRU(w *types.LLMWorker) *WorkerWithLRU {
	return &WorkerWithLRU{
		worker:     w,
		lastAccess: time.Now(),
	}
}

// Touch updates the last access time to current timestamp.
// This marks the worker as recently used in the LRU cache.
func (w *WorkerWithLRU) Touch() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lastAccess = time.Now()
}

type TreeNode struct {
	// valid worker list, sorted by lastAccess
	list *list.List

	mu sync.RWMutex
}

func NewTreeNode() *TreeNode {
	return &TreeNode{
		list: list.New(),
	}
}

func (tn *TreeNode) AddWorker(w *types.LLMWorker) {
	tn.mu.Lock()
	defer tn.mu.Unlock()

	wl := NewWorkerWithLRU(w)
	tn.list.PushFront(wl)
}

func (tn *TreeNode) TouchWorker(w *types.LLMWorker) {
	tn.mu.Lock()
	defer tn.mu.Unlock()

	var eleToMove *list.Element
	for ele := tn.list.Front(); ele != nil; ele = ele.Next() {
		wl := ele.Value.(*WorkerWithLRU)
		if wl.worker == w {
			eleToMove = ele
			wl.Touch()
			break
		}
	}
	if eleToMove != nil {
		tn.list.MoveToFront(eleToMove)
	}
}

func (tn *TreeNode) GetWorkers() []*types.LLMWorker {
	tn.mu.RLock()
	defer tn.mu.RUnlock()

	if tn.list.Len() == 0 {
		return nil
	}
	workers := make([]*types.LLMWorker, 0, tn.list.Len())
	for ele := tn.list.Front(); ele != nil; ele = ele.Next() {
		workers = append(workers, ele.Value.(*WorkerWithLRU).worker)
	}
	return workers
}

// CheckAndRemoveInvalidWorkers removes stale workers and returns true if list becomes empty.
func (tn *TreeNode) CheckAndRemoveInvalidWorkers(ttl time.Duration) bool {
	tn.mu.Lock()
	defer tn.mu.Unlock()

	for ele := tn.list.Back(); ele != nil; {
		pre := ele.Prev()
		wl := ele.Value.(*WorkerWithLRU)
		dur := time.Since(wl.lastAccess)
		if dur < ttl {
			break
		}
		tn.list.Remove(ele)
		ele = pre
	}
	return tn.list.Len() == 0
}

// PrefixCacheIndexer implements a radix tree-based cache indexer with LRU eviction.
// It uses longest prefix matching to locate cached workers and maintains memory limits.
type PrefixCacheIndexer struct {
	tLock               sync.RWMutex
	tree                *radix.Tree
	ttl                 time.Duration
	memLimit            int64
	memUsed             atomic.Int64
	inferMode           string
	maintenanceInterval time.Duration // interval for maintenance loop (default: 60s production, 2s testing)

	// Cache hit rate metrics (cumulative counters)
	hitChars   atomic.Int64 // Total matched characters
	totalChars atomic.Int64 // Total prompt characters processed
	hitReqs    atomic.Int64 // Requests with cache hit (matchLen > 0)
	totalReqs  atomic.Int64 // Total requests processed
}

// NewPrefixCacheIndexer creates a new prefix cache indexer with specified TTL, memory limit, and maintenance interval.
func NewPrefixCacheIndexer(ttl time.Duration, memLimit int64, maintenanceInterval time.Duration, inferMode string) *PrefixCacheIndexer {
	pci := &PrefixCacheIndexer{
		tree:                radix.New(),
		ttl:                 ttl,
		memLimit:            memLimit,
		maintenanceInterval: maintenanceInterval,
		inferMode:           inferMode,
	}
	// Set limit metric once at construction time, as it never changes.
	metrics.StatusValue("prefix_cache_mem_limit_bytes", metrics.Labels{
		{Name: "infer_mode", Value: inferMode},
	}).Set(float32(memLimit))
	go pci.MaintenanceLoop()
	return pci
}

// MaintenanceLoop runs periodically to perform background maintenance tasks:
// - Updates monitoring metrics (memory usage, cache hit rates)
// - Removes stale workers based on TTL
// - Manages memory pressure by evicting 1/3 of entries when limit exceeded
// Interval is configurable via PrefixCacheMaintenanceInterval (default: 60s).
func (pci *PrefixCacheIndexer) MaintenanceLoop() {
	for {
		time.Sleep(pci.maintenanceInterval)

		memUsed := pci.memUsed.Load()
		memRecycle := memUsed > pci.memLimit

		// Update metrics for monitoring.
		pci.updateMetrics(memUsed)

		// Recycle stale workers and manage memory pressure.
		tStart := time.Now()
		pci.tLock.RLock()
		var removes []string
		recycleMemSize := int64(0)
		pci.tree.WalkPostOrder(func(s string, v interface{}) bool {
			if memRecycle && recycleMemSize < pci.memLimit/3 {
				recycleMemSize += int64(len(s))
				removes = append(removes, s)
			} else {
				treeNode := v.(*TreeNode)
				needDel := treeNode.CheckAndRemoveInvalidWorkers(pci.ttl)
				if needDel {
					removes = append(removes, s)
				}
			}
			return false
		})
		pci.tLock.RUnlock()
		dur := time.Since(tStart)
		if dur > time.Second {
			klog.Warningf("prefix cache indexer recycle too long: %v", dur)
		}

		for _, r := range removes {
			pci.DeletePrefix(r)
		}
		if len(removes) > 0 {
			klog.Infof("prefix cache indexer recycled %d entries, now using %d MB", len(removes), pci.memUsed.Load()/consts.MB)
		}
	}
}

// MatchPrefix finds the cached entry with the longest common prefix (LCP) and returns
// both the node and the length of the common prefix.
func (pci *PrefixCacheIndexer) MatchPrefix(input string) (*TreeNode, int, bool) {
	pci.tLock.RLock()
	defer pci.tLock.RUnlock()

	if len(input) == 0 {
		return nil, 0, false
	}

	_, node, matchLen, exactMatch, found := pci.tree.LongestCommonPrefix(input)
	if !found {
		return nil, 0, false
	}
	return node.(*TreeNode), matchLen, exactMatch
}

// AddPrefix inserts a new prefix into the cache tree.
func (pci *PrefixCacheIndexer) AddPrefix(input string, n *TreeNode) {
	pci.tLock.Lock()
	defer pci.tLock.Unlock()
	_, update := pci.tree.Insert(input, n)
	if !update {
		pci.memUsed.Add(int64(len(input)))
	}
}

// DeletePrefix removes a prefix from the cache tree.
func (pci *PrefixCacheIndexer) DeletePrefix(input string) {
	pci.tLock.Lock()
	defer pci.tLock.Unlock()
	_, deleted := pci.tree.Delete(input)
	if deleted {
		pci.memUsed.Add(-int64(len(input)))
	}
}

// GetMemUsed returns the current memory usage in bytes.
func (pci *PrefixCacheIndexer) GetMemUsed() int64 {
	return pci.memUsed.Load()
}

// recordStats records cache hit statistics for monitoring.
// matchLen: the length of matched prefix (0 means no hit)
// promptLen: the total length of the prompt
func (pci *PrefixCacheIndexer) recordStats(matchLen int, promptLen int) {
	pci.totalChars.Add(int64(promptLen))
	pci.totalReqs.Add(1)
	if matchLen > 0 {
		pci.hitChars.Add(int64(matchLen))
		pci.hitReqs.Add(1)
	}
}

// updateMetrics exposes monitoring metrics for prefix cache.
// It reports memory usage and cache hit rates (incremental stats for the last interval).
func (pci *PrefixCacheIndexer) updateMetrics(memUsed int64) {
	// Memory usage metric.
	metrics.StatusValue("prefix_cache_mem_used_bytes", metrics.Labels{
		{Name: "infer_mode", Value: pci.inferMode},
	}).Set(float32(memUsed))

	// Cache hit rate metrics (incremental stats, swap-and-reset for next interval).
	// Character hit rate: percentage of prompt characters that matched cached prefixes.
	// Request hit rate: percentage of requests that had any cache hit (matchLen > 0).
	hitChars := pci.hitChars.Swap(0)
	totalChars := pci.totalChars.Swap(0)
	hitReqs := pci.hitReqs.Swap(0)
	totalReqs := pci.totalReqs.Swap(0)

	charHitRate := float32(0)
	reqHitRate := float32(0)
	if totalChars > 0 {
		charHitRate = float32(hitChars) / float32(totalChars)
	}
	if totalReqs > 0 {
		reqHitRate = float32(hitReqs) / float32(totalReqs)
	}
	metrics.StatusValue("prefix_cache_char_hit_rate", metrics.Labels{
		{Name: "infer_mode", Value: pci.inferMode},
	}).Set(charHitRate)
	metrics.StatusValue("prefix_cache_req_hit_rate", metrics.Labels{
		{Name: "infer_mode", Value: pci.inferMode},
	}).Set(reqHitRate)
}

type PrefixCachePolicy struct {
	config       *options.Config
	cacheIndexer *PrefixCacheIndexer
	lrsClient    *lrs.LocalRealtimeStateClient
	inferMode    string
}

func NewPrefixCachePolicy(config *options.Config, inferMode string, lrsClient *lrs.LocalRealtimeStateClient) *PrefixCachePolicy {
	ttl := time.Duration(config.PrefixCacheTTL) * time.Second
	memLimit := config.PrefixCacheLimit
	maintenanceInterval := time.Duration(config.PrefixCacheMaintenanceInterval) * time.Second
	pc := &PrefixCachePolicy{
		config:       config,
		cacheIndexer: NewPrefixCacheIndexer(ttl, memLimit, maintenanceInterval, inferMode),
		lrsClient:    lrsClient,
		inferMode:    inferMode,
	}
	return pc
}

func (pc *PrefixCachePolicy) Name() string {
	return "prefix-cache"
}

// Schedule tries to select the best worker for the given request.
func (pc *PrefixCachePolicy) Schedule(schReq *types.ScheduleRequest) error {
	instanceViews := pc.lrsClient.GetInstanceViews(pc.inferMode)
	if len(instanceViews) == 0 {
		return consts.ErrorNoAvailableEndpoint
	}
	results, err := ratelimiter.Filter(pc.inferMode, schReq, instanceViews)
	if err != nil {
		return err
	}
	selectWorker := pc.trySelectBestOf(schReq.Id, schReq.PromptText, results)
	schReq.ScheduleResult = append(schReq.ScheduleResult, *selectWorker)
	return nil
}

func (pc *PrefixCachePolicy) addNewPrefixWithWorker(msg string, worker *types.LLMWorker) {
	node := NewTreeNode()
	node.AddWorker(worker)
	pc.cacheIndexer.AddPrefix(msg, node)
}

func (pc *PrefixCachePolicy) getPrefixMatchCandidates(n *TreeNode) []*types.LLMWorker {
	if n == nil {
		return nil
	}
	return n.GetWorkers()
}

// filterCandidatesByLoad filters workers by load balance to avoid hotspots.
// Strategy: Calculate mean load across all workers, then keep only candidates
// whose load is below (mean + tolerance_factor * mean).
func (pc *PrefixCachePolicy) filterCandidatesByLoad(
	requestId string,
	candidates []*types.LLMWorker,
	allInstanceViews map[string]*lrs.InstanceView) map[string]*lrs.InstanceView {
	if len(candidates) == 0 {
		return nil
	}

	// select candidate instance views
	instanceViews := make(map[string]*lrs.InstanceView)
	for _, worker := range candidates {
		workerId := worker.Id()
		if _, exists := allInstanceViews[workerId]; exists {
			instanceViews[workerId] = allInstanceViews[workerId]
		}
	}
	if len(instanceViews) == 0 {
		klog.V(2).Infof("[PrefixCache][Req:%s] No valid candidates after filtering", requestId)
		return nil
	}

	// Calculate mean load (NumTokens) across all available workers
	var totalTokens int64
	count := 0
	for _, iv := range allInstanceViews {
		if iv != nil {
			totalTokens += iv.NumTokens()
			count++
		}
	}
	if count == 0 {
		return nil
	}

	meanTokens := float64(totalTokens) / float64(count)
	// Use configurable tolerance factor from config
	// E.g., if tolerance=0.5 and mean=100, threshold=150
	threshold := meanTokens * (1.0 + pc.config.PrefixCacheLoadTolerance)

	klog.V(2).Infof("[PrefixCache][Req:%s] Load filter: mean=%.1f tokens, tolerance=%.2f, threshold=%.1f tokens",
		requestId, meanTokens, pc.config.PrefixCacheLoadTolerance, threshold)

	// Filter candidates: keep only those within acceptable load range
	filtered := make(map[string]*lrs.InstanceView)
	for _, iv := range instanceViews {
		load := float64(iv.NumTokens())
		if load <= threshold {
			filtered[iv.GetInstance().Id()] = iv
			klog.V(2).Infof("[PrefixCache][Req:%s]  ✓ Worker %s: load=%.1f (within threshold)", requestId, iv.GetInstance().Id(), load)
		} else {
			klog.V(2).Infof("[PrefixCache][Req:%s]  ✗ Worker %s: load=%.1f (exceeds threshold %.1f)",
				requestId, iv.GetInstance().Id(), load, threshold)
		}
	}

	klog.V(2).Infof("[PrefixCache][Req:%s] Load filter result: %d/%d candidates passed", requestId, len(filtered), len(candidates))

	return filtered
}

func selectLeastTokenOne(instanceViews map[string]*lrs.InstanceView) *types.LLMWorker {
	var minTokens int64 = math.MaxInt64
	var target *lrs.InstanceView
	for _, iv := range instanceViews {
		if iv == nil {
			continue
		}
		tokens := iv.NumTokens()
		if tokens < minTokens {
			minTokens = tokens
			target = iv
		}
	}
	if target == nil {
		return nil
	}
	return target.GetInstance()
}

func (pc *PrefixCachePolicy) trySelectBestOf(requestId string, prompt string, instanceViews map[string]*lrs.InstanceView) *types.LLMWorker {
	var candidates []*types.LLMWorker
	cacheNode, matchLen, exactMatch := pc.cacheIndexer.MatchPrefix(prompt)
	if cacheNode != nil {
		candidates = cacheNode.GetWorkers()
	}

	promptLen := len(prompt)

	// No prefix cache hit
	if len(candidates) == 0 {
		selected := selectLeastTokenOne(instanceViews)
		if selected == nil {
			klog.Warningf("[PrefixCache][Req:%s] No valid worker available", requestId)
			return nil
		}
		klog.V(2).Infof("[PrefixCache][Req:%s] Selected worker %s with least tokens and no cache hit", requestId, selected.Id())
		pc.addNewPrefixWithWorker(prompt, selected)
		// No cache hit, record 0 matched chars.
		pc.cacheIndexer.recordStats(0, promptLen)
		return selected
	}

	// Filter candidates by load balance: only keep workers within acceptable load range.
	// Workers with cache hit but excessive load should be avoided to prevent hotspots.
	filtered := pc.filterCandidatesByLoad(requestId, candidates, instanceViews)

	// If all candidates are overloaded, fallback to global least-loaded worker
	if len(filtered) == 0 {
		selected := selectLeastTokenOne(instanceViews)
		if selected == nil {
			klog.Warningf("[PrefixCache][Req:%s] No valid worker available (all overloaded fallback)", requestId)
			return nil
		}
		klog.V(2).Infof("[PrefixCache][Req:%s] Selected worker %s with least load (reason: all cache-hit workers overloaded)",
			requestId, selected.Id())

		// Check if selected is in candidates for touch and hit rate calculation.
		hitLen := 0
		for _, worker := range candidates {
			if worker.Id() == selected.Id() {
				hitLen = matchLen
				if exactMatch {
					cacheNode.TouchWorker(selected)
				}
				break
			}
		}
		if hitLen == 0 {
			// TODO(wingo.zwt) Add a threshold for cache control, e.g., skip adding if match rate exceeds 80%
			pc.addNewPrefixWithWorker(prompt, selected)
		}
		pc.cacheIndexer.recordStats(hitLen, promptLen)
		return selected
	}

	// Select the least loaded worker among cache-hit candidates
	selected := selectLeastTokenOne(filtered)
	if selected == nil {
		klog.Warningf("[PrefixCache][Req:%s] No valid worker in filtered candidates", requestId)
		return nil
	}
	if iv, exists := filtered[selected.Id()]; exists && iv != nil {
		klog.V(2).Infof("[PrefixCache][Req:%s] Selected worker %s with cache hit %.2f%% (reason: cache hit + lowest load within tolerance)",
			requestId, selected.Id(), float64(matchLen)/float64(promptLen)*100)
	}
	if exactMatch {
		cacheNode.TouchWorker(selected)
	} else {
		pc.addNewPrefixWithWorker(prompt, selected)
	}
	// Selected from filtered candidates, this is a real cache hit.
	pc.cacheIndexer.recordStats(matchLen, promptLen)
	return selected
}
