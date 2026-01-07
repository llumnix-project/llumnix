package llumnix

import (
	"easgo/pkg/llm-gateway/cms"
	"easgo/pkg/llm-gateway/kvs"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

// calcInstancesPrefixCacheHitLen calculates the prefix hit length of prompt tokens in instance cache.
// It uses the kvs meta service cache client to:
// 1. Chunk the prompt token IDs into smaller pieces
// 2. Calculate prefix hashes for these chunks
// 3. Query which kvs workers have cached content matching these prefix hashes
// 4. Convert kvs workers to instance id set
// 5. Calculate how many tokens each instance has cached (prefix hit length)
// The result is stored in each instance's scheduling context for later use in scheduling decisions.
// If KVS meta service is down (marked after multiple query failures), returns immediately.
// KVS meta service status will automatically recover to healthy after a configured duration.
// NOTE(sunbiao.sun): This function writes to instance view, so it is not placed in the kvs meta service client.
func calcInstancesPrefixCacheHitLen(
	kvsClient kvs.KVSClientInterface,
	cmsClient cms.CMSReadClientInterface,
	promptTokenIds []int64,
	instanceViews map[string]*instanceViewScheduling,
	blockSize int32) {

	totalStart := time.Now()
	defer func() {
		klog.V(3).Infof(
			"calcInstancesPrefixCacheHitLen took: %.2fms, promptTokenIds len: %v",
			time.Since(totalStart).Seconds()*1000, len(promptTokenIds))
	}()

	if kvsClient.IsKVSMetaServiceDown() {
		klog.Warning("KVS meta service is down")
		return
	}

	klog.V(5).Infof("promptTokenIds: %v", promptTokenIds)
	klog.V(3).Infof("promptTokenIds len: %v", len(promptTokenIds))

	start := time.Now()
	prefixHashes := kvsClient.PrefixHash(promptTokenIds)
	klog.V(3).Infof("PrefixHash took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(5).Infof("PrefixHash result: %v", prefixHashes)

	start = time.Now()
	prefixHashHitKVSWorkers := kvsClient.BatchQueryCacheHitKVSWorkers(prefixHashes)
	klog.V(3).Infof("BatchQueryCacheHitKVSWorkers took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(5).Infof("BatchQueryCacheHitKVSWorkers result: %+v", prefixHashHitKVSWorkers)

	start = time.Now()
	prefixHashHitInstances := convertToPrefixHashHitInstances(cmsClient, kvsClient, prefixHashHitKVSWorkers)
	klog.V(3).Infof("convertToPrefixHashHitInstances took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(5).Infof("convertToPrefixHashHitInstances result: %+v", prefixHashHitInstances)

	start = time.Now()
	instancePrefixHitLen := kvsClient.CalcInstancesCacheHitLen(prefixHashes, prefixHashHitInstances)
	klog.V(3).Infof("CalcInstancesCacheHitLen took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(3).Infof("CalcInstancesCacheHitLen result: %+v", instancePrefixHitLen)

	start = time.Now()
	updateCount := 0
	for instanceId, prefixHitLen := range instancePrefixHitLen {
		if instanceView, ok := instanceViews[instanceId]; ok {
			instanceView.schedulingCtx.prefixHitLen = prefixHitLen
			instanceView.schedulingCtx.prefixHitNumBlocks = prefixHitLen / int(blockSize)
			if len(promptTokenIds) > 0 {
				instanceView.schedulingCtx.prefixHitRatio = float32(prefixHitLen) / float32(len(promptTokenIds))
			} else {
				instanceView.schedulingCtx.prefixHitRatio = 0.0
			}
			instanceView.schedulingCtx.prefixMissLen = len(promptTokenIds) - prefixHitLen
			instanceView.schedulingCtx.prefixMissNumBlocks =
				(instanceView.schedulingCtx.prefixMissLen + int(blockSize) - 1) / int(blockSize)
			updateCount++
			klog.V(4).Infof("Updated instance %s with prefixHitLen: %d", instanceId, prefixHitLen)
		} else {
			klog.Warningf("Instance %s not found in instanceViews", instanceId)
		}
	}
	klog.V(3).Infof("Update instanceViews took: %.2fms", time.Since(start).Seconds()*1e3)
}

func convertToPrefixHashHitInstances(
	cmsClient cms.CMSReadClientInterface,
	kvsClient kvs.KVSClientInterface,
	prefixHashHitKVSWorkers map[string][]string) map[string]sets.String {
	if len(prefixHashHitKVSWorkers) == 0 {
		return nil
	}

	prefixHashHitInstances := make(map[string]sets.String, len(prefixHashHitKVSWorkers))
	for prefixHash, hitKVSWorkers := range prefixHashHitKVSWorkers {
		ips := make([]string, len(hitKVSWorkers))
		for i, hitKVSWorker := range hitKVSWorkers {
			ips[i] = kvsClient.ConvertKVSWorkerToIp(hitKVSWorker)
		}
		klog.V(5).Infof("Convert kvs workers to ips: %v", ips)
		prefixHashHitInstances[prefixHash] = cmsClient.GetInstanceIDsByIPs(ips)
	}
	return prefixHashHitInstances
}
