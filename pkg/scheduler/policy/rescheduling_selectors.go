package policy

import (
	"llumnix/pkg/consts"
	"math"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
)

type reschedulingSelector interface {
	selectPairs(
		srcCandidates, dstCandidates map[string]*instanceViewScheduling) []*reschedulingPair
}

type aggregateSelector struct {
	srcMetric string
	dstMetric string
}

func (as *aggregateSelector) selectPairs(
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) (pairs []*reschedulingPair) {

	usedInstanceIds := sets.New[string]()
	srcCandidateIds := getKeySliceFromMap(srcCandidates)
	sort.SliceStable(srcCandidateIds, func(i, j int) bool {
		return srcCandidates[srcCandidateIds[i]].metrics[as.srcMetric].Less(
			srcCandidates[srcCandidateIds[j]].metrics[as.srcMetric])
	})
	dstCandidateIds := getKeySliceFromMap(dstCandidates)
	sort.SliceStable(dstCandidateIds, func(i, j int) bool {
		return !dstCandidates[dstCandidateIds[i]].metrics[as.dstMetric].Less(
			dstCandidates[dstCandidateIds[j]].metrics[as.dstMetric])
	})

	srcIndex, dstIndex := 0, 0
	for srcIndex < len(srcCandidateIds) && dstIndex < len(dstCandidateIds) {
		if usedInstanceIds.Has(srcCandidateIds[srcIndex]) {
			srcIndex++
			continue
		}

		if usedInstanceIds.Has(dstCandidateIds[dstIndex]) {
			dstIndex++
			continue
		}

		if srcCandidateIds[srcIndex] == dstCandidateIds[dstIndex] {
			dstIndex++
			continue
		}

		pairs = append(pairs, &reschedulingPair{
			srcView: srcCandidates[srcCandidateIds[srcIndex]],
			dstView: dstCandidates[dstCandidateIds[dstIndex]],
		})

		usedInstanceIds.Insert(srcCandidateIds[srcIndex])
		usedInstanceIds.Insert(dstCandidateIds[dstIndex])
		srcIndex++
		dstIndex++
	}

	return
}

type metricBalanceSelector struct {
	srcMetric          string
	dstMetric          string
	forceHigherToLower bool
	balanceScope       string
}

func (mbs *metricBalanceSelector) selectPairs(
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) (pairs []*reschedulingPair) {

	sortSrcList := func(list []*instanceViewScheduling) {
		sort.SliceStable(list, func(i, j int) bool {
			iLoadMetric := list[i].schedulingCtx.metrics[mbs.srcMetric]
			jLoadMetric := list[j].schedulingCtx.metrics[mbs.srcMetric]
			if iLoadMetric.Less(jLoadMetric) {
				return false
			}
			if jLoadMetric.Less(iLoadMetric) {
				return true
			}
			return list[i].GetInstanceId() < list[j].GetInstanceId()
		})
	}

	sortDstList := func(list []*instanceViewScheduling) {
		sort.SliceStable(list, func(i, j int) bool {
			iLoadMetric := list[i].schedulingCtx.metrics[mbs.dstMetric]
			jLoadMetric := list[j].schedulingCtx.metrics[mbs.dstMetric]
			if iLoadMetric.Less(jLoadMetric) {
				return true
			}
			if jLoadMetric.Less(iLoadMetric) {
				return false
			}
			return list[i].GetInstanceId() < list[j].GetInstanceId()
		})
	}

	pairLists := func(srcList, dstList []*instanceViewScheduling) []*reschedulingPair {
		sortSrcList(srcList)
		sortDstList(dstList)

		var resultPairs []*reschedulingPair
		minLen := int(math.Min(float64(len(srcList)), float64(len(dstList))))
		for i := 0; i < minLen; i++ {
			srcLoadMetric := srcList[i].schedulingCtx.metrics[mbs.srcMetric]
			dstLoadMetric := dstList[i].schedulingCtx.metrics[mbs.dstMetric]
			// Must use less to compare load, because some load metrics might have opposite compare logic.
			if mbs.forceHigherToLower && !dstLoadMetric.Less(srcLoadMetric) {
				break
			}
			resultPairs = append(resultPairs, &reschedulingPair{
				srcView: srcList[i],
				dstView: dstList[i],
			})
		}
		return resultPairs
	}

	switch mbs.balanceScope {
	case consts.ReschedulingLoadBalanceScopeCluster:
		srcList := make([]*instanceViewScheduling, 0, len(srcCandidates))
		for _, instanceView := range srcCandidates {
			srcList = append(srcList, instanceView)
		}
		dstList := make([]*instanceViewScheduling, 0, len(dstCandidates))
		for _, instanceView := range dstCandidates {
			dstList = append(dstList, instanceView)
		}

		return pairLists(srcList, dstList)

	case consts.ReschedulingLoadBalanceScopeUnit:
		srcByUnit := make(map[string][]*instanceViewScheduling)
		for _, instanceView := range srcCandidates {
			metadata := instanceView.cmsView.Metadata
			if metadata == nil || metadata.UnitId == "" {
				continue
			}
			srcByUnit[metadata.UnitId] = append(srcByUnit[metadata.UnitId], instanceView)
		}

		dstByUnit := make(map[string][]*instanceViewScheduling)
		for _, instanceView := range dstCandidates {
			metadata := instanceView.cmsView.Metadata
			if metadata == nil || metadata.UnitId == "" {
				continue
			}
			dstByUnit[metadata.UnitId] = append(dstByUnit[metadata.UnitId], instanceView)
		}

		var allPairs []*reschedulingPair

		for unitId, srcListForUnit := range srcByUnit {
			dstListForUnit, ok := dstByUnit[unitId]
			if !ok {
				continue
			}

			unitPairs := pairLists(srcListForUnit, dstListForUnit)
			allPairs = append(allPairs, unitPairs...)
		}

		return allPairs

	default:

		return nil
	}
}

type roundRobinSelector struct{}

func (rrs *roundRobinSelector) selectPairs(
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) []*reschedulingPair {

	var pairs []*reschedulingPair

	// Convert src and dst candidates to slices
	srcList := make([]*instanceViewScheduling, 0, len(srcCandidates))
	dstList := make([]*instanceViewScheduling, 0, len(dstCandidates))

	for _, instanceView := range srcCandidates {
		srcList = append(srcList, instanceView)
	}

	for _, instanceView := range dstCandidates {
		dstList = append(dstList, instanceView)
	}

	// No destinations available
	if len(dstList) == 0 {
		return pairs
	}

	// Sort by InstanceId to ensure deterministic behavior
	sort.SliceStable(srcList, func(i, j int) bool {
		return srcList[i].GetInstanceId() < srcList[j].GetInstanceId()
	})

	sort.SliceStable(dstList, func(i, j int) bool {
		return dstList[i].GetInstanceId() < dstList[j].GetInstanceId()
	})

	// For each source, select a destination in round-robin fashion
	for i, src := range srcList {
		// Select destination using round-robin (simple modulo)
		dstIndex := i % len(dstList)
		dst := dstList[dstIndex]

		// Skip if source and destination are the same
		if src.GetInstanceId() == dst.GetInstanceId() {
			continue
		}

		pairs = append(pairs, &reschedulingPair{
			srcView: src,
			dstView: dst,
		})
	}

	return pairs
}

/*
binPackingMitigationSelector implements a selector for overload rescheduling that
pairs the most overloaded source instance with the most loaded (but still available)
destination instance. This strategy attempts to relieve the highest pressure first
while utilizing destination capacity efficiently. Keep in mind that a slice containing
at most one reschedule pair (highest load source to highest load destination).
*/
type binPackingMitigationSelector struct {
}

func (bpms *binPackingMitigationSelector) selectPairs(
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) []*reschedulingPair {

	var maxLoadSrcInstance *instanceViewScheduling
	for _, instanceView := range srcCandidates {
		if maxLoadSrcInstance == nil {
			maxLoadSrcInstance = instanceView
			continue
		}

		if maxLoadSrcInstance.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot].Less(
			instanceView.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot]) {
			maxLoadSrcInstance = instanceView
		}
	}

	var maxLoadDstInstance *instanceViewScheduling
	for _, instanceView := range dstCandidates {
		if maxLoadDstInstance == nil {
			maxLoadDstInstance = instanceView
			continue
		}

		if maxLoadDstInstance.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot].Less(
			instanceView.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot]) {
			maxLoadDstInstance = instanceView
		}
	}

	var resultPairs []*reschedulingPair

	if maxLoadSrcInstance != nil && maxLoadDstInstance != nil {
		resultPairs = append(resultPairs, &reschedulingPair{
			srcView: maxLoadSrcInstance,
			dstView: maxLoadDstInstance,
		})
	}

	return resultPairs
}

/*
binPackingConsolidationSelector implements a selector for underload rescheduling that
pairs the least loaded source instance with the most loaded destination instance. This
strategy consolidates workload by emptying the lightest instances first and packing
requests into fuller instances, improving overall resource utilization. Keep in mind that
a slice containing at most one reschedule pair (lowest load source to highest load
destination) will be returned.
*/
type binPackingConsolidationSelector struct {
}

func (bpcs *binPackingConsolidationSelector) selectPairs(
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) []*reschedulingPair {

	var resultPairs []*reschedulingPair

	var maxLoadDstInstance *instanceViewScheduling
	for _, instanceView := range dstCandidates {
		if maxLoadDstInstance == nil {
			maxLoadDstInstance = instanceView
			continue
		}

		if instanceView.cmsView.ReservedInferType == consts.InferTypeDecode {
			maxLoadDstInstance = instanceView
			break
		}

		if !instanceView.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot].Less(
			maxLoadDstInstance.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot]) {
			maxLoadDstInstance = instanceView
		}
	}

	if maxLoadDstInstance == nil {
		return resultPairs
	}

	var lowestLoadSrcInstance *instanceViewScheduling
	for _, instanceView := range srcCandidates {
		if instanceView.GetInstanceId() == maxLoadDstInstance.GetInstanceId() {
			continue
		}

		if lowestLoadSrcInstance == nil {
			lowestLoadSrcInstance = instanceView
			continue
		}

		if instanceView.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot].Less(
			lowestLoadSrcInstance.schedulingCtx.metrics[consts.SchedulingMetricPredictedTpot]) {
			lowestLoadSrcInstance = instanceView
		}
	}

	if lowestLoadSrcInstance != nil {
		resultPairs = append(resultPairs, &reschedulingPair{
			srcView: lowestLoadSrcInstance,
			dstView: maxLoadDstInstance,
		})
	}

	return resultPairs
}
