package cms

import (
	"llumnix/pkg/llm-gateway/consts"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"
)

type RequestLocalAccount struct {
	ScheduleTimestampMs int64
	InferMode           string
	NumBlocks           int32
	NumUncomputedBlocks int32
	FoundInCMS          bool
}

type InstanceStatusLocalAccount struct {
	RequestLocalAccount                                map[string]*RequestLocalAccount
	NumInflightDispatchPrefillRequests                 int32
	NumInflightDispatchDecodeRequests                  int32
	NumInflightDispatchRequests                        int32
	NumUncomputedBlocksInflightDispatchPrefillRequests int32
	NumBlocksInflightDispatchDecodeRequests            int32
}

type InstanceStatusLocalAccountEditor struct {
	instanceStatusRequestLocalAccountStalenessSeconds int32
	enableCacheAwareScheduling                        bool
}

func newInstanceStatusLocalAccountEditor(
	instanceStatusRequestLocalAccountStalenessSeconds int32,
	enableCacheAwareScheduling bool) *InstanceStatusLocalAccountEditor {
	klog.Infof("[newInstanceStatusLocalAccountEditor] enableCacheAwareScheduling: %v", enableCacheAwareScheduling)
	return &InstanceStatusLocalAccountEditor{
		instanceStatusRequestLocalAccountStalenessSeconds: instanceStatusRequestLocalAccountStalenessSeconds,
		enableCacheAwareScheduling:                        enableCacheAwareScheduling,
	}
}

func (e *InstanceStatusLocalAccountEditor) updateInstanceStatusLocalAccount(
	instanceView *InstanceView, instanceID string) {
	// let the request id reported by cms aligned with the request id used in scheduler
	recentWaitingReqs := trimRequestIDsToSets(instanceView.Status.RecentWaitingRequests)
	waitingReqs := trimRequestIDsToSets(instanceView.Status.WaitingRequests)
	for reqID, requestLocalAccount := range instanceView.InstanceStatusLocalAccount.RequestLocalAccount {
		if !requestLocalAccount.FoundInCMS {
			// If request has found in recent waiting requests, it means that request has been added to engine,
			// so we can update the metrics of inflight requests in instance status local account.
			if recentWaitingReqs.Has(reqID) {
				klog.V(4).Infof(
					"[updateInstanceStatusLocalAccount] [%v] request %v has been found in recent waiting requests, "+
						"update instance status local account",
					instanceID, reqID)
				requestLocalAccount.FoundInCMS = true
				e.deleteRequestLocalAccount(instanceView, instanceID, reqID, false)
			}
		}
		if requestLocalAccount.FoundInCMS && !waitingReqs.Has(reqID) {
			// Delete request local account when the request has found in cms
			// (means that request has been added to engine)
			// and not exists in waiting requests
			// (request local account is required to update num blocks of waiting requests).
			delete(instanceView.InstanceStatusLocalAccount.RequestLocalAccount, reqID)
		}
		if !requestLocalAccount.FoundInCMS && !waitingReqs.Has(reqID) &&
			(time.Now().UnixMilli()-requestLocalAccount.ScheduleTimestampMs)/1000 >=
				int64(e.instanceStatusRequestLocalAccountStalenessSeconds) {
			klog.Warningf(
				"[updateInstanceStatusLocalAccount] [%v] request %v local account has not been deleted "+
					"for %v seconds, delete it",
				instanceID, reqID, e.instanceStatusRequestLocalAccountStalenessSeconds)
			e.deleteRequestLocalAccount(instanceView, instanceID, reqID, true)
		}
	}
	// Update num blocks of unallocated waiting prefill requests in instance status using instance status local account.
	// Because the instance status local account is cache-aware,
	// while num blocks of unallocated waiting prefill requests in instance status is not.
	waitingReqList := trimRequestIDsToList(instanceView.Status.WaitingRequests)
	if e.enableCacheAwareScheduling {
		// NOTE(sunbiao.sun): If a request is not found in instance status local account, it should be preempted or migrated-in,
		// and we suppose that the number of uncomputed blocks of preempted or migrated-in request is 0,
		// so we do not increase num blocks when request id is not found in instance status local account.
		if len(waitingReqList) > 0 {
			klog.V(4).Infof(
				"[updateInstanceStatusLocalAccount] NumUncomputedBlocksAllWaitingPrefills: %v",
				instanceView.Status.NumUncomputedBlocksAllWaitingPrefills)
			numUncomputedBlocksAllWaitingPrefills := int32(0)
			for _, reqID := range waitingReqList {
				requestLocalAccount :=
					instanceView.InstanceStatusLocalAccount.RequestLocalAccount
				if _, exists := requestLocalAccount[reqID]; exists {
					numUncomputedBlocksAllWaitingPrefills += requestLocalAccount[reqID].NumUncomputedBlocks
				}
			}
			klog.V(4).Infof(
				"[updateInstanceStatusLocalAccount] [%v] original num uncomputed blocks all waiting prefills: %v, "+
					"updated num uncomputed blocks all waiting prefills: %v",
				instanceID, instanceView.Status.NumUncomputedBlocksAllWaitingPrefills,
				numUncomputedBlocksAllWaitingPrefills)
			instanceView.Status.NumUncomputedBlocksAllWaitingPrefills = numUncomputedBlocksAllWaitingPrefills
		}
	}
}

func (e *InstanceStatusLocalAccountEditor) deleteRequestLocalAccount(
	instanceView *InstanceView, instanceID string, reqID string, deleteRequest bool) {
	requestLocalAccount, exists := instanceView.InstanceStatusLocalAccount.RequestLocalAccount[reqID]

	if !exists {
		klog.Warningf(
			"[deleteRequestLocalAccount] [%v] request %v does not exist in instanceStatusLocalAccount, "+
				"skip delete request local account", instanceID, reqID)
		return
	}

	if requestLocalAccount.InferMode == consts.PrefillInferMode ||
		requestLocalAccount.InferMode == consts.NormalInferMode {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests -= 1
		instanceView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests -=
			requestLocalAccount.NumUncomputedBlocks
	}

	if requestLocalAccount.InferMode == consts.DecodeInferMode ||
		requestLocalAccount.InferMode == consts.NormalInferMode {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchDecodeRequests -= 1
		instanceView.InstanceStatusLocalAccount.NumBlocksInflightDispatchDecodeRequests -=
			requestLocalAccount.NumBlocks
	}

	instanceView.InstanceStatusLocalAccount.NumInflightDispatchRequests -= 1

	if deleteRequest {
		delete(instanceView.InstanceStatusLocalAccount.RequestLocalAccount, reqID)
	}
}

func (e *InstanceStatusLocalAccountEditor) addRequestLocalAccount(
	instanceView *InstanceView,
	inferMode string,
	numBlocks int32,
	prefixHitNumBlocks int32,
	requestId string,
	firstAdd bool) {
	numUncomputedBlocks := numBlocks
	if e.enableCacheAwareScheduling {
		numUncomputedBlocks -= prefixHitNumBlocks
	}

	if firstAdd {
		klog.Infof("InstanceStatusLocalAccount: %v", instanceView.InstanceStatusLocalAccount)
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchRequests += 1
		instanceView.InstanceStatusLocalAccount.RequestLocalAccount[requestId] =
			&RequestLocalAccount{
				ScheduleTimestampMs: time.Now().UnixMilli(),
				InferMode:           inferMode,
				NumBlocks:           numBlocks,
				NumUncomputedBlocks: numUncomputedBlocks,
				FoundInCMS:          false,
			}
	}

	if inferMode == consts.PrefillInferMode || inferMode == consts.NormalInferMode {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests += 1
		instanceView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests += numUncomputedBlocks
	}

	if inferMode == consts.DecodeInferMode || inferMode == consts.NormalInferMode {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchDecodeRequests += 1
		instanceView.InstanceStatusLocalAccount.NumBlocksInflightDispatchDecodeRequests += numBlocks
	}

	if !firstAdd {
		// Not first update means that this instance is selected as both prefill and decode,
		// so this instance should be considered as normal infer mode.
		instanceView.InstanceStatusLocalAccount.RequestLocalAccount[requestId].InferMode = consts.NormalInferMode
	}
}

func (e *InstanceStatusLocalAccountEditor) revertRequestPrefillLocalAccount(
	instanceView *InstanceView, numBlocks int32, prefixHitNumBlocks int32, requestId string) {
	if e.enableCacheAwareScheduling {
		numBlocks -= prefixHitNumBlocks
	}

	instanceView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests -= 1
	instanceView.InstanceStatusLocalAccount.NumUncomputedBlocksInflightDispatchPrefillRequests -= numBlocks
	delete(instanceView.InstanceStatusLocalAccount.RequestLocalAccount, requestId)
}

func trimRequestIDsToSets(rawRequestIDs []string) sets.String {
	result := sets.NewString()
	for _, reqID := range rawRequestIDs {
		trimmedID := trimRequestID(reqID)
		result.Insert(trimmedID)
	}
	return result
}

func trimRequestIDsToList(rawRequestIDs []string) []string {
	result := make([]string, 0, len(rawRequestIDs))
	for _, reqID := range rawRequestIDs {
		trimmedID := trimRequestID(reqID)
		result = append(result, trimmedID)
	}
	return result
}

func trimRequestID(requestID string) string {
	// Trim the potential prefix and suffix to get the exact request ID.
	if strings.HasPrefix(requestID, "cmpl-") {
		return trimPlusSuffix(strings.TrimPrefix(requestID, "cmpl-"))
	} else if strings.HasPrefix(requestID, "chatcmpl-") {
		return trimPlusSuffix(strings.TrimPrefix(requestID, "chatcmpl-"))
	} else {
		return trimPlusSuffix(requestID)
	}
}

func trimPlusSuffix(requestID string) string {
	parts := strings.Split(requestID, "+")
	return parts[0]
}
