package llumnix

import (
	"llumnix/pkg/llm-gateway/consts"
	"llumnix/pkg/util/maps"
	"math"
	"sort"

	"k8s.io/apimachinery/pkg/util/sets"
)

type rescheduleSelector interface {
	selectPairs(
		srcCandidates, dstCandidates map[string]*instanceViewScheduling) []*reschedulePair
}

type aggregateSelector struct {
	srcMetric string
	dstMetric string
}

func (as *aggregateSelector) selectPairs(
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) (pairs []*reschedulePair) {

	usedInstanceIds := sets.NewString()
	srcCandidateIds := maps.Keys(srcCandidates)
	sort.SliceStable(srcCandidateIds, func(i, j int) bool {
		return srcCandidates[srcCandidateIds[i]].metrics[as.srcMetric].Less(
			srcCandidates[srcCandidateIds[j]].metrics[as.srcMetric])
	})
	dstCandidateIds := maps.Keys(dstCandidates)
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

		pairs = append(pairs, &reschedulePair{
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
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) (pairs []*reschedulePair) {

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

	pairLists := func(srcList, dstList []*instanceViewScheduling) []*reschedulePair {
		sortSrcList(srcList)
		sortDstList(dstList)

		var resultPairs []*reschedulePair
		minLen := int(math.Min(float64(len(srcList)), float64(len(dstList))))
		for i := 0; i < minLen; i++ {
			srcLoadMetric := srcList[i].schedulingCtx.metrics[mbs.srcMetric]
			dstLoadMetric := dstList[i].schedulingCtx.metrics[mbs.dstMetric]
			// Must use less to compare load, because some load metrics might have opposite compare logic.
			if mbs.forceHigherToLower && !dstLoadMetric.Less(srcLoadMetric) {
				break
			}
			resultPairs = append(resultPairs, &reschedulePair{
				srcView: srcList[i],
				dstView: dstList[i],
			})
		}
		return resultPairs
	}

	switch mbs.balanceScope {
	case consts.RescheduleLoadBalanceScopeCluster:
		srcList := make([]*instanceViewScheduling, 0, len(srcCandidates))
		for _, instanceView := range srcCandidates {
			srcList = append(srcList, instanceView)
		}
		dstList := make([]*instanceViewScheduling, 0, len(dstCandidates))
		for _, instanceView := range dstCandidates {
			dstList = append(dstList, instanceView)
		}

		return pairLists(srcList, dstList)

	case consts.RescheduleLoadBalanceScopeUnit:
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

		var allPairs []*reschedulePair

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
	srcCandidates, dstCandidates map[string]*instanceViewScheduling) []*reschedulePair {

	var pairs []*reschedulePair

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

		pairs = append(pairs, &reschedulePair{
			srcView: src,
			dstView: dst,
		})
	}

	return pairs
}
