package policy

import (
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/pkg/cms"
	"llumnix/pkg/scheduler/kvs"
)

// calcInstancesPrefixCacheHitLen calculates the total length of cache hits for each instance
// based on the provided prefix hashes and their corresponding hit instances.
// It accumulates chunkSize for each consecutive hit; a gap marks the instance as broken.
func calcInstancesPrefixCacheHitLen(
	chunkSize int, prefixHashes []string, prefixHashHitInstances map[string]sets.Set[string]) map[string]int {
	if len(prefixHashes) == 0 || prefixHashHitInstances == nil {
		return nil
	}

	instanceCacheHitLen := make(map[string]int)
	instanceLastHitIdx := make(map[string]int)
	instanceBroken := make(map[string]bool)

	for i, prefixHash := range prefixHashes {
		hitInstances := prefixHashHitInstances[prefixHash]
		if hitInstances == nil {
			continue
		}
		for instance := range hitInstances {
			if instanceBroken[instance] {
				continue
			}
			if lastSeenIdx, ok := instanceLastHitIdx[instance]; !ok {
				instanceLastHitIdx[instance] = i
				instanceCacheHitLen[instance] += chunkSize
			} else if lastSeenIdx == i-1 {
				instanceLastHitIdx[instance] = i
				instanceCacheHitLen[instance] += chunkSize
			} else {
				instanceBroken[instance] = true
			}
		}
	}

	return instanceCacheHitLen
}

// getInstancesPrefixCacheHitLen queries prefix cache hit information from KVS metadata service
// and updates each instance's scheduling context with the result.
//
// Flow:
//  1. Query KVS metadata service for instances that cached content matching the given prefix hashes.
//  2. Convert KVS instance IPs to instance IDs via CMS.
//  3. Calculate prefix cache hit length per instance (via calcInstancesPrefixCacheHitLen).
//  4. Write prefixHitTokens, prefixHitRatio, and prefixMissTokens into each instance's scheduling context.
//
// If KVS metadata service is down (marked after consecutive query failures), returns immediately.
// The service status automatically recovers to healthy after a configured duration.
//
// NOTE(sunbiao.sun): This function writes to instance view, so it is not placed in the kvs metadata service client.
func getInstancesPrefixCacheHitLen(
	kvsClient kvs.KVSClientInterface,
	cmsClient cms.CMSReadClientInterface,
	prefixHashes []string,
	chunkSize int,
	numPromptTokens int,
	instanceViews map[string]*instanceViewScheduling) {

	totalStart := time.Now()
	defer func() {
		klog.V(3).Infof(
			"getInstancesPrefixCacheHitLen took: %.2fms, numPromptTokens: %v",
			time.Since(totalStart).Seconds()*1000, numPromptTokens)
	}()

	if kvsClient.IsKVSMetadataServiceDown() {
		klog.Warning("KVS metadata service is down")
		return
	}

	klog.V(5).Infof("prefixHashes: %v", prefixHashes)
	klog.V(3).Infof("prefixHashes len: %v", len(prefixHashes))

	start := time.Now()
	prefixHashHitKVSInstances := kvsClient.BatchQueryCacheHitKVSInstances(prefixHashes)
	klog.V(3).Infof("BatchQueryCacheHitKVSInstances took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(5).Infof("BatchQueryCacheHitKVSInstances result: %+v", prefixHashHitKVSInstances)

	start = time.Now()
	prefixHashHitInstances := convertToCacheHitInstances(cmsClient, prefixHashHitKVSInstances)
	klog.V(3).Infof("convertToCacheHitInstances took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(5).Infof("convertToCacheHitInstances result: %+v", prefixHashHitInstances)

	start = time.Now()
	instancePrefixHitLen := calcInstancesPrefixCacheHitLen(chunkSize, prefixHashes, prefixHashHitInstances)
	klog.V(3).Infof("CalcInstancesCacheHitLen took: %.2fms", time.Since(start).Seconds()*1e3)
	klog.V(3).Infof("CalcInstancesCacheHitLen result: %+v", instancePrefixHitLen)

	start = time.Now()
	updateCount := 0
	for instanceId, prefixHitLen := range instancePrefixHitLen {
		if instanceView, ok := instanceViews[instanceId]; ok {
			instanceView.schedulingCtx.prefixHitTokens = prefixHitLen
			if numPromptTokens > 0 {
				instanceView.schedulingCtx.prefixHitRatio = float32(prefixHitLen) / float32(numPromptTokens)
			} else {
				instanceView.schedulingCtx.prefixHitRatio = 0.0
			}
			instanceView.schedulingCtx.prefixMissTokens = numPromptTokens - prefixHitLen
			updateCount++
			klog.V(4).Infof("Updated instance %s with prefixHitTokens: %d", instanceId, prefixHitLen)
		} else {
			klog.Warningf("Instance %s not found in instanceViews", instanceId)
		}
	}
	klog.V(3).Infof("Update instanceViews took: %.2fms", time.Since(start).Seconds()*1e3)
}

func convertToCacheHitInstances(
	cmsClient cms.CMSReadClientInterface,
	prefixHashHitKVSInstances map[string][]string) map[string]sets.Set[string] {
	if len(prefixHashHitKVSInstances) == 0 {
		return nil
	}

	prefixHashHitInstances := make(map[string]sets.Set[string], len(prefixHashHitKVSInstances))
	for prefixHash, hitKVSInstances := range prefixHashHitKVSInstances {
		prefixHashHitInstances[prefixHash] = cmsClient.GetInstanceIDsByIPs(hitKVSInstances)
	}
	return prefixHashHitInstances
}
