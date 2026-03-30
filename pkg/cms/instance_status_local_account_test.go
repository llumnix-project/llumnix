package cms

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"llumnix/pkg/consts"
	"llumnix/pkg/types"
)

func newTestEditor() *InstanceStatusLocalAccountEditor {
	return &InstanceStatusLocalAccountEditor{
		instanceStatusRequestLocalAccountStalenessSeconds: 60,
		enableCacheAwareScheduling:                        false,
		requestToInstanceIds:                              make(map[string][]string),
	}
}

func newTestInstanceView(instanceId string, inferType consts.InferType) *InstanceView {
	return &InstanceView{
		Instance: &types.LLMInstance{InferType: inferType},
		Status:   &InstanceStatus{InstanceId: instanceId},
		Metadata: &InstanceMetadata{InstanceId: instanceId},
		InstanceStatusLocalAccount: InstanceStatusLocalAccount{
			RequestLocalAccount: make(map[string]*RequestLocalAccount),
		},
	}
}

// TestAddRequestLocalAccount_ReverseIndex verifies that addRequestLocalAccount
// populates the reverse index correctly.
func TestAddRequestLocalAccount_ReverseIndex(t *testing.T) {
	editor := newTestEditor()
	view := newTestInstanceView("inst-1", consts.InferTypeNeutral)

	editor.addRequestLocalAccount(view, consts.InferTypeNeutral, 100, 0, "req-1")

	// Reverse index should map req-1 -> [inst-1]
	assert.Equal(t, []string{"inst-1"}, editor.requestToInstanceIds["req-1"])
	// Account should exist on the instance
	assert.Contains(t, view.RequestLocalAccount, "req-1")
	assert.Equal(t, int32(1), view.NumInflightDispatchRequests)
}

// TestAddRequestLocalAccount_PDMode verifies that a request scheduled to both
// prefill and decode instances has both entries in the reverse index.
func TestAddRequestLocalAccount_PDMode(t *testing.T) {
	editor := newTestEditor()
	prefillView := newTestInstanceView("inst-prefill", consts.InferTypePrefill)
	decodeView := newTestInstanceView("inst-decode", consts.InferTypeDecode)

	// First add: prefill instance
	editor.addRequestLocalAccount(prefillView, consts.InferTypePrefill, 100, 0, "req-1")
	// Second add: decode instance
	editor.addRequestLocalAccount(decodeView, consts.InferTypeDecode, 100, 0, "req-1")

	// Reverse index should have both instances
	assert.ElementsMatch(t, []string{"inst-prefill", "inst-decode"}, editor.requestToInstanceIds["req-1"])
	assert.Contains(t, prefillView.RequestLocalAccount, "req-1")
	assert.Contains(t, decodeView.RequestLocalAccount, "req-1")
}

// TestRemoveRequestLocalAccount_SingleInstance verifies removal of a request
// that was scheduled to a single instance (neutral mode).
func TestRemoveRequestLocalAccount_SingleInstance(t *testing.T) {
	editor := newTestEditor()
	view := newTestInstanceView("inst-1", consts.InferTypeNeutral)
	instanceViews := map[string]*InstanceView{"inst-1": view}

	editor.addRequestLocalAccount(view, consts.InferTypeNeutral, 100, 0, "req-1")
	assert.Equal(t, int32(1), view.NumInflightDispatchRequests)
	assert.Equal(t, int32(1), view.NumInflightDispatchPrefillRequests)

	editor.removeRequestLocalAccount("req-1", instanceViews)

	// Account and reverse index should both be cleaned
	assert.NotContains(t, view.RequestLocalAccount, "req-1")
	assert.NotContains(t, editor.requestToInstanceIds, "req-1")
	assert.Equal(t, int32(0), view.NumInflightDispatchRequests)
	assert.Equal(t, int32(0), view.NumInflightDispatchPrefillRequests)
}

// TestRemoveRequestLocalAccount_PDMode verifies removal of a request that was
// scheduled to two different instances (PD disaggregation mode).
func TestRemoveRequestLocalAccount_PDMode(t *testing.T) {
	editor := newTestEditor()
	prefillView := newTestInstanceView("inst-prefill", consts.InferTypePrefill)
	decodeView := newTestInstanceView("inst-decode", consts.InferTypeDecode)
	instanceViews := map[string]*InstanceView{
		"inst-prefill": prefillView,
		"inst-decode":  decodeView,
	}

	editor.addRequestLocalAccount(prefillView, consts.InferTypePrefill, 100, 0, "req-1")
	editor.addRequestLocalAccount(decodeView, consts.InferTypeDecode, 100, 0, "req-1")

	editor.removeRequestLocalAccount("req-1", instanceViews)

	// Both instances should be cleaned
	assert.NotContains(t, prefillView.RequestLocalAccount, "req-1")
	assert.NotContains(t, decodeView.RequestLocalAccount, "req-1")
	assert.NotContains(t, editor.requestToInstanceIds, "req-1")
	assert.Equal(t, int32(0), prefillView.NumInflightDispatchPrefillRequests)
	assert.Equal(t, int32(0), decodeView.NumInflightDispatchDecodeRequests)
}

// TestRemoveRequestLocalAccount_InstanceNotInViews verifies that when an instance
// is no longer in instanceViews, the reverse index is still cleaned up.
func TestRemoveRequestLocalAccount_InstanceNotInViews(t *testing.T) {
	editor := newTestEditor()
	view := newTestInstanceView("inst-1", consts.InferTypeNeutral)

	editor.addRequestLocalAccount(view, consts.InferTypeNeutral, 100, 0, "req-1")

	// Pass empty instanceViews to simulate instance removal from cluster
	editor.removeRequestLocalAccount("req-1", map[string]*InstanceView{})

	// Reverse index should be cleaned even though instance is gone
	assert.NotContains(t, editor.requestToInstanceIds, "req-1")
}

// TestRemoveRequestLocalAccount_NonExistentRequest verifies that removing a
// non-existent request is a no-op.
func TestRemoveRequestLocalAccount_NonExistentRequest(t *testing.T) {
	editor := newTestEditor()

	// Should not panic or error
	editor.removeRequestLocalAccount("non-existent", map[string]*InstanceView{})
	assert.Empty(t, editor.requestToInstanceIds)
}

// TestRemoveInstanceFromRequestIndex_PDMode verifies that removing one instance
// from the reverse index preserves the other instance's entry.
func TestRemoveInstanceFromRequestIndex_PDMode(t *testing.T) {
	editor := newTestEditor()
	editor.requestToInstanceIds["req-1"] = []string{"inst-prefill", "inst-decode"}

	editor.removeInstanceFromRequestIndex("req-1", "inst-prefill")

	// Only decode instance should remain
	assert.Equal(t, []string{"inst-decode"}, editor.requestToInstanceIds["req-1"])

	// Removing the last one should delete the entry entirely
	editor.removeInstanceFromRequestIndex("req-1", "inst-decode")
	assert.NotContains(t, editor.requestToInstanceIds, "req-1")
}

// TestRevertRequestPrefillLocalAccount_ReverseIndex verifies that reverting a
// prefill local account cleans up the reverse index.
func TestRevertRequestPrefillLocalAccount_ReverseIndex(t *testing.T) {
	editor := newTestEditor()
	view := newTestInstanceView("inst-prefill", consts.InferTypePrefill)

	editor.addRequestLocalAccount(view, consts.InferTypePrefill, 100, 0, "req-1")
	assert.Contains(t, editor.requestToInstanceIds, "req-1")

	editor.revertRequestPrefillLocalAccount(view, 100, 0, "req-1")

	// Both account and reverse index should be cleaned
	assert.NotContains(t, view.RequestLocalAccount, "req-1")
	assert.NotContains(t, editor.requestToInstanceIds, "req-1")
	assert.Equal(t, int32(0), view.NumInflightDispatchPrefillRequests)
}

// TestUpdateInstanceStatusLocalAccount_Reconcile verifies that the reconciliation
// during refresh correctly cleans up local accounts and the reverse index when
// a request has been found in CMS and left the waiting queue.
func TestUpdateInstanceStatusLocalAccount_Reconcile(t *testing.T) {
	editor := newTestEditor()
	view := newTestInstanceView("inst1", consts.InferTypeNeutral)

	editor.addRequestLocalAccount(view, consts.InferTypeNeutral, 100, 0, "reqA")

	// Simulate: request appeared in RecentWaitingRequests (engine accepted it)
	// Engine reports IDs after trim, so use the same bare ID.
	view.Status.RecentWaitingRequests = []string{"reqA"}
	view.Status.WaitingRequests = []string{"reqA"}
	editor.updateInstanceStatusLocalAccount(view, "inst1")

	// FoundInCMS should be set, but account still exists (still in WaitingRequests)
	assert.True(t, view.RequestLocalAccount["reqA"].FoundInCMS)
	assert.Contains(t, editor.requestToInstanceIds, "reqA")

	// Simulate: request left WaitingRequests (completed or moved on)
	view.Status.WaitingRequests = []string{}
	editor.updateInstanceStatusLocalAccount(view, "inst1")

	// Account and reverse index should be fully cleaned
	assert.NotContains(t, view.RequestLocalAccount, "reqA")
	assert.NotContains(t, editor.requestToInstanceIds, "reqA")
}
