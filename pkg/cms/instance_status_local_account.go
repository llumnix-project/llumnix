package cms

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
)

type RequestLocalAccount struct {
	ScheduleTimestampMs int64
	InferType           consts.InferType
	NumTokens           int32
	NumUncomputedTokens int32
	FoundInCMS          bool
}

type InstanceStatusLocalAccount struct {
	RequestLocalAccount                                map[string]*RequestLocalAccount
	NumInflightDispatchPrefillRequests                 int32
	NumInflightDispatchDecodeRequests                  int32
	NumInflightDispatchRequests                        int32
	NumUncomputedTokensInflightDispatchPrefillRequests int32
	NumTokensInflightDispatchDecodeRequests            int32
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
			// (request local account is required to update num tokens of waiting requests).
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
	// Update num tokens of unallocated waiting prefill requests in instance status using instance status local account.
	// Because the instance status local account is cache-aware,
	// while num tokens of unallocated waiting prefill requests in instance status is not.
	waitingReqList := trimRequestIDsToList(instanceView.Status.WaitingRequests)
	if e.enableCacheAwareScheduling {
		// NOTE(sunbiao.sun): If a request is not found in instance status local account, it should be preempted or migrated-in,
		// and we suppose that the number of uncomputed tokens of preempted or migrated-in request is 0,
		// so we do not increase num tokens when request id is not found in instance status local account.
		if len(waitingReqList) > 0 {
			klog.V(4).Infof(
				"[updateInstanceStatusLocalAccount] NumUncomputedTokensAllWaitingPrefills: %v",
				instanceView.Status.NumUncomputedTokensAllWaitingPrefills)
			numUncomputedTokensAllWaitingPrefills := int32(0)
			for _, reqID := range waitingReqList {
				requestLocalAccount :=
					instanceView.InstanceStatusLocalAccount.RequestLocalAccount
				if _, exists := requestLocalAccount[reqID]; exists {
					numUncomputedTokensAllWaitingPrefills += requestLocalAccount[reqID].NumUncomputedTokens
				}
			}
			klog.V(4).Infof(
				"[updateInstanceStatusLocalAccount] [%v] original num uncomputed tokens all waiting prefills: %v, "+
					"updated num uncomputed tokens all waiting prefills: %v",
				instanceID, instanceView.Status.NumUncomputedTokensAllWaitingPrefills,
				numUncomputedTokensAllWaitingPrefills)
			instanceView.Status.NumUncomputedTokensAllWaitingPrefills = numUncomputedTokensAllWaitingPrefills
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

	if requestLocalAccount.InferType == consts.InferTypePrefill ||
		requestLocalAccount.InferType == consts.InferTypeNeutral {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests -= 1
		instanceView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests -=
			requestLocalAccount.NumUncomputedTokens
	}

	if requestLocalAccount.InferType == consts.InferTypeDecode ||
		requestLocalAccount.InferType == consts.InferTypeNeutral {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchDecodeRequests -= 1
		instanceView.InstanceStatusLocalAccount.NumTokensInflightDispatchDecodeRequests -=
			requestLocalAccount.NumTokens
	}

	instanceView.InstanceStatusLocalAccount.NumInflightDispatchRequests -= 1

	if deleteRequest {
		delete(instanceView.InstanceStatusLocalAccount.RequestLocalAccount, reqID)
	}
}

func (e *InstanceStatusLocalAccountEditor) addRequestLocalAccount(
	instanceView *InstanceView,
	inferType consts.InferType,
	numTokens int32,
	prefixHitNumTokens int32,
	requestId string,
	firstAdd bool) {
	numUncomputedTokens := numTokens
	if e.enableCacheAwareScheduling {
		numUncomputedTokens -= prefixHitNumTokens
	}

	if firstAdd {
		klog.Infof("InstanceStatusLocalAccount: %+v", instanceView.InstanceStatusLocalAccount)
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchRequests += 1
		instanceView.InstanceStatusLocalAccount.RequestLocalAccount[requestId] =
			&RequestLocalAccount{
				ScheduleTimestampMs: time.Now().UnixMilli(),
				InferType:           inferType,
				NumTokens:           numTokens,
				NumUncomputedTokens: numUncomputedTokens,
				FoundInCMS:          false,
			}
	}

	if inferType == consts.InferTypePrefill || inferType == consts.InferTypeNeutral {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests += 1
		instanceView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests += numUncomputedTokens
	}

	if inferType == consts.InferTypeDecode || inferType == consts.InferTypeNeutral {
		instanceView.InstanceStatusLocalAccount.NumInflightDispatchDecodeRequests += 1
		instanceView.InstanceStatusLocalAccount.NumTokensInflightDispatchDecodeRequests += numTokens
	}

	if !firstAdd {
		// Not first update means that this instance is selected as both prefill and decode,
		// so this instance should be considered as neutral infer type.
		instanceView.InstanceStatusLocalAccount.RequestLocalAccount[requestId].InferType = consts.InferTypeNeutral
	}
}

func (e *InstanceStatusLocalAccountEditor) revertRequestPrefillLocalAccount(
	instanceView *InstanceView, numTokens int32, prefixHitNumTokens int32, requestId string) {
	if e.enableCacheAwareScheduling {
		numTokens -= prefixHitNumTokens
	}

	instanceView.InstanceStatusLocalAccount.NumInflightDispatchPrefillRequests -= 1
	instanceView.InstanceStatusLocalAccount.NumUncomputedTokensInflightDispatchPrefillRequests -= numTokens
	delete(instanceView.InstanceStatusLocalAccount.RequestLocalAccount, requestId)
}

func trimRequestIDsToSets(rawRequestIDs []string) sets.Set[string] {
	result := sets.New[string]()
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
		return trimSuffix(strings.TrimPrefix(requestID, "cmpl-"))
	} else if strings.HasPrefix(requestID, "chatcmpl-") {
		return trimSuffix(strings.TrimPrefix(requestID, "chatcmpl-"))
	} else {
		return trimSuffix(requestID)
	}
}

func trimSuffix(requestID string) string {
	if strings.Contains(requestID, "+") {
		parts := strings.Split(requestID, "+")
		return parts[0]
	}

	if idx := strings.LastIndex(requestID, "-"); idx != -1 {
		return requestID[:idx]
	}

	return requestID
}
