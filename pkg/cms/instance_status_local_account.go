package cms

import (
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
)

// RequestLocalAccount tracks the local accounting state for a single dispatched request on an instance.
//
// InferType reflects the scheduling role of this instance for the request:
//   - Prefill / Decode: only one stage's counters are incremented.
//   - Neutral: both prefill and decode counters are incremented (either neutral scheduling,
//     or PD scheduling where the same instance is selected for both stages).
//
// deleteRequestLocalAccount uses InferType to determine which stage counters to decrement.
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
	// requestToInstanceIds maps requestId -> list of instanceIds (InstanceMetadata.InstanceId)
	// for O(1) reverse lookup when releasing request local accounts.
	requestToInstanceIds map[string][]string
}

func newInstanceStatusLocalAccountEditor(
	instanceStatusRequestLocalAccountStalenessSeconds int32,
	enableCacheAwareScheduling bool) *InstanceStatusLocalAccountEditor {
	klog.Infof("[newInstanceStatusLocalAccountEditor] enableCacheAwareScheduling: %v", enableCacheAwareScheduling)
	return &InstanceStatusLocalAccountEditor{
		instanceStatusRequestLocalAccountStalenessSeconds: instanceStatusRequestLocalAccountStalenessSeconds,
		enableCacheAwareScheduling:                        enableCacheAwareScheduling,
		requestToInstanceIds:                              make(map[string][]string),
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
			e.removeInstanceFromRequestIndex(reqID, instanceID)
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

// deleteRequestLocalAccount decrements the stage counters (prefill/decode) based on the request's InferType,
// and optionally removes the request entry and reverse index.
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
		e.removeInstanceFromRequestIndex(reqID, instanceID)
	}
}

// addRequestLocalAccount adds or updates a request's local account on an instance.
//
// Scheduling scenarios and their add behavior:
//
// 1. Neutral scheduling:
//   - Single add with InferType=Neutral. Increments both prefill and decode counters.
//
// 2. PD scheduling (different instances):
//   - Two adds: Prefill on inst-A, Decode on inst-B. Each is a fresh add on its instance.
//
// 3. PD scheduling (same instance):
//   - First add: Prefill → creates account with InferType=Prefill.
//   - Second add: Decode → existing.InferType=Prefill ≠ Decode, not Neutral → firstAdd=false
//     → increments decode counters, then sets InferType=Neutral.
//
// Retry ordering guarantee:
// Release is synchronous on the gateway side, so the previous scheduling state is fully
// released before a retry issues a new Schedule. The scheduler never sees a stale entry for
// the same requestId on retry, making duplicate-add impossible by construction.
func (e *InstanceStatusLocalAccountEditor) addRequestLocalAccount(
	instanceView *InstanceView,
	inferType consts.InferType,
	numTokens int32,
	prefixHitNumTokens int32,
	requestId string) {
	numUncomputedTokens := numTokens
	if e.enableCacheAwareScheduling {
		numUncomputedTokens -= prefixHitNumTokens
	}

	firstAdd := true
	if existing, exists := instanceView.InstanceStatusLocalAccount.RequestLocalAccount[requestId]; exists {
		// PD scheduling to the same instance: first add was Prefill, this add is Decode (or vice versa).
		// Fall through as firstAdd=false to add the other stage's counters.
		if existing.InferType == inferType || existing.InferType == consts.InferTypeNeutral {
			// Should not happen: Release is synchronous, so the old entry is always removed
			// before a retry reaches here. Log a warning for defensive monitoring.
			klog.Warningf(
				"[addRequestLocalAccount] [%v] request %v already exists with inferType=%v (incoming=%v), "+
					"unexpected duplicate add, skipping",
				instanceView.GetInstanceId(), requestId, existing.InferType, inferType)
			return
		}
		klog.Infof(
			"[addRequestLocalAccount] [%v] request %v already exists with inferType=%v, "+
				"adding %v stage counters (PD scheduling to same instance)",
			instanceView.GetInstanceId(), requestId, existing.InferType, inferType)
		firstAdd = false
	} else {
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
		e.requestToInstanceIds[requestId] = append(e.requestToInstanceIds[requestId], instanceView.GetInstanceId())
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
		// Both prefill and decode counters have been incremented on this instance.
		// Set InferType to Neutral so that deleteRequestLocalAccount decrements
		// both prefill and decode counters.
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
	delete(e.requestToInstanceIds, requestId)
}

// removeRequestLocalAccount removes the local accounts for a request from all instances
// it was scheduled to, using the reverse index for O(1) lookup.
func (e *InstanceStatusLocalAccountEditor) removeRequestLocalAccount(
	requestId string, instanceViews map[string]*InstanceView) {
	instanceIds, exists := e.requestToInstanceIds[requestId]
	if !exists {
		return
	}
	for _, instanceId := range instanceIds {
		instanceView, ok := instanceViews[instanceId]
		if !ok {
			// instanceView not found, only clean up the reverse index.
			e.removeInstanceFromRequestIndex(requestId, instanceId)
			continue
		}
		if _, exists := instanceView.InstanceStatusLocalAccount.RequestLocalAccount[requestId]; exists {
			// deleteRequestLocalAccount cleans up both the account and the reverse index internally.
			e.deleteRequestLocalAccount(instanceView, instanceId, requestId, true)
			klog.V(3).Infof("[removeRequestLocalAccount] removed request %s from instance %s",
				requestId, instanceId)
		}
	}
}

// removeInstanceFromRequestIndex removes a specific instanceId from the reverse index
// for the given reqID. If the list becomes empty, the entire entry is deleted.
func (e *InstanceStatusLocalAccountEditor) removeInstanceFromRequestIndex(reqID string, instanceID string) {
	ids := e.requestToInstanceIds[reqID]
	for i, id := range ids {
		if id == instanceID {
			e.requestToInstanceIds[reqID] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	if len(e.requestToInstanceIds[reqID]) == 0 {
		delete(e.requestToInstanceIds, reqID)
	}
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
