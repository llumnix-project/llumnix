package policy

import (
	"llumnix/pkg/consts"
	"math/rand"
	"sort"

	"golang.org/x/exp/maps"
	"k8s.io/klog/v2"
)

type dispatchSelector interface {
	selectInstance(instances map[string]*instanceViewScheduling, fallback bool) *instanceViewScheduling
}

type metricBasedSelector struct {
	topK        int
	metricNames []string
}

func (s *metricBasedSelector) selectInstance(instances map[string]*instanceViewScheduling, fallback bool) *instanceViewScheduling {
	klog.V(4).Infof(
		"Selecting instance with metricBasedSelector, topK: %d, metricNames: %v, instances count: %d, instance IDs: %v",
		s.topK, s.metricNames, len(instances), maps.Keys(instances))

	if s.topK == 1 {
		selected := s.best(instances)
		if selected != nil {
			klog.V(4).Infof("Best instance selected: %s", selected.GetInstanceId())
		} else {
			klog.V(4).Info("No best instance selected")
		}
		return selected
	}

	selected := s.randomChoiceFromTopK(instances)
	if selected != nil {
		klog.V(4).Infof("Random instance from top K selected: %s", selected.GetInstanceId())
	} else {
		klog.V(4).Info("No instance selected from top K")
	}
	return selected
}

func (s *metricBasedSelector) best(
	instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling {
	klog.V(4).Infof("Finding best instance among %d instances, IDs: %v",
		len(instanceViews), maps.Keys(instanceViews))

	var selectedInstance *instanceViewScheduling

	for key, instanceView := range instanceViews {
		klog.V(3).Infof("instance %s schedulingCtx: %+v", key, instanceView.schedulingCtx)
		for name, metric := range instanceView.schedulingCtx.metrics {
			klog.V(3).Infof("instance %s metric %s: %+v", key, name, metric.GetValue())
		}
		if instanceView.cmsView != nil {
			klog.V(3).Infof("instance %s instanceView: %+v", key, *instanceView.cmsView)
		}
		if instanceView.lrsView != nil {
			klog.V(3).Infof("instance %s instanceView: %+v", key, *instanceView.lrsView)
		}
	}

	for _, instanceView := range instanceViews {
		if selectedInstance == nil {
			selectedInstance = instanceView
			continue
		}
		for _, metricName := range s.metricNames {
			currentMetric := instanceView.schedulingCtx.metrics[metricName]
			selectedMetric := selectedInstance.schedulingCtx.metrics[metricName]
			klog.V(3).Infof("instance %s metric %s: %v",
				instanceView.GetInstanceId(), metricName, currentMetric.GetValue())
			klog.V(3).Infof("instance %s metric %s: %v",
				selectedInstance.GetInstanceId(), metricName, selectedMetric.GetValue())
			if currentMetric.Less(selectedMetric) {
				selectedInstance = instanceView
				break
			}
			if selectedMetric.Less(currentMetric) {
				break
			}
		}
	}

	if selectedInstance != nil {
		klog.V(4).Infof("Best instance selected: %s", selectedInstance.GetInstanceId())
	}
	return selectedInstance
}

func (s *metricBasedSelector) randomChoiceFromTopK(
	instanceViews map[string]*instanceViewScheduling) *instanceViewScheduling {
	klog.V(4).Infof("Selecting random instance from top K among %d instances, IDs: %v",
		len(instanceViews), maps.Keys(instanceViews))

	viewList := make([]*instanceViewScheduling, len(instanceViews))
	i := 0
	for _, instanceView := range instanceViews {
		viewList[i] = instanceView
		i++
	}

	klog.V(4).Infof("Sorting instances by metrics: %v", s.metricNames)
	sort.SliceStable(viewList, func(i, j int) bool {
		for _, metricName := range s.metricNames {
			metricI := viewList[i].schedulingCtx.metrics[metricName]
			metricJ := viewList[j].schedulingCtx.metrics[metricName]
			if metricI.Less(metricJ) {
				return true
			}
			if metricJ.Less(metricI) {
				return false
			}
		}
		return false
	})
	topK := s.topK
	if topK > len(viewList) {
		topK = len(viewList)
		klog.V(4).Infof("Adjusted topK from %d to %d (number of available instances)", s.topK, topK)
	}

	instanceIds := make([]string, len(viewList))
	for i, view := range viewList {
		instanceIds[i] = view.GetInstanceId()
	}

	selected := viewList[rand.Intn(topK)]
	klog.V(4).Infof("Randomly selected instance %s from %v", selected.GetInstanceId(), instanceIds)

	return selected
}

type fixedPreferenceSelector struct {
	lastSelectedInstanceId string
}

func (f *fixedPreferenceSelector) selectInstance(instances map[string]*instanceViewScheduling, fallback bool) *instanceViewScheduling {
	klog.V(4).Infof("Selecting instance with fixedPreferenceSelector, last selected: %s, instances count: %d, instance IDs: %v",
		f.lastSelectedInstanceId, len(instances), maps.Keys(instances))

	if view, exists := instances[f.lastSelectedInstanceId]; exists {
		klog.V(4).Infof("Returning previously selected instance: %s", f.lastSelectedInstanceId)
		return view
	}

	klog.V(4).Infof("Previously selected instance %s not found, selecting first available instance",
		f.lastSelectedInstanceId)

	for instanceID, view := range instances {
		klog.V(4).Infof("Selecting instance: %s", instanceID)
		f.lastSelectedInstanceId = instanceID
		return view
	}

	klog.V(4).Info("No instances available for selection")
	return nil
}

type sloPrefillApdSelector struct {
}

func (s *sloPrefillApdSelector) selectInstance(instances map[string]*instanceViewScheduling, fallback bool) *instanceViewScheduling {
	targetMetric := consts.SchedulingMetricPredictedTtft

	var selectedInstance *instanceViewScheduling
	for _, instanceView := range instances {
		if selectedInstance == nil {
			selectedInstance = instanceView
			continue
		}

		if instanceView.schedulingCtx.metrics[targetMetric].Less(
			selectedInstance.schedulingCtx.metrics[targetMetric]) {
			selectedInstance = instanceView
		} else if instanceView.schedulingCtx.metrics[targetMetric].Equal(
			selectedInstance.schedulingCtx.metrics[targetMetric]) &&
			instanceView.cmsView.ReservedInferType == consts.InferTypePrefill {
			selectedInstance = instanceView
		}
	}

	return selectedInstance
}

type sloDecodeApdSelector struct {
	tpotSlo                  float32
	tpotSloDispatchThreshold float32
}

/*
selectInstance selects the most appropriate instance for decode requests based on predicted
TPOT metrics and reservation state, with fallback logic considering TTFT when primary selection
criteria cannot be satisfied.

Selection Strategy:
  - Primary (!fallback): Selects the instance with the highest predicted TPOT to maximize bin-packing
    efficiency. When TPOT values are equal, prefers instances with ReservedInferType="decode" to keep
    decode-reserved instances utilized.
  - Fallback: Prioritizes instances with predicted TPOT under the SLO threshold, selecting the highest
    TPOT for bin-packing efficiency (ties broken by lowest TTFT). If none qualify, selects the instance
    with the lowest predicted TPOT.
*/
func (s *sloDecodeApdSelector) selectInstance(instances map[string]*instanceViewScheduling, fallback bool) *instanceViewScheduling {
	var selectedInstance *instanceViewScheduling

	if !fallback {
		for _, instanceView := range instances {
			metric := instanceView.metrics[consts.SchedulingMetricPredictedTpot]
			klog.V(3).Infof("sloDecodeApdSelector: instance %s metric %s: %v", instanceView.GetInstanceId(), consts.SchedulingMetricPredictedTpot, metric.GetValue())

			if selectedInstance == nil {
				selectedInstance = instanceView
				continue
			}

			if instanceView.cmsView.ReservedInferType == consts.InferTypeDecode {
				selectedInstance = instanceView
				break
			}

			if selectedInstance.metrics[consts.SchedulingMetricPredictedTpot].Less(
				instanceView.metrics[consts.SchedulingMetricPredictedTpot]) {
				selectedInstance = instanceView
			}
		}
	} else {
		for _, instanceView := range instances {
			metric := instanceView.metrics[consts.SchedulingMetricPredictedTtft]
			metric.Calculate(nil, instanceView)
		}

		var lowestTpotOverSloDispatchThreshold *instanceViewScheduling
		var highestTpotUnderSloDispatchThreshold *instanceViewScheduling

		for _, instanceView := range instances {
			predictedTpotMetric := instanceView.metrics[consts.SchedulingMetricPredictedTpot]
			predictedTtftMetric := instanceView.metrics[consts.SchedulingMetricPredictedTtft]
			klog.V(3).Infof("sloDecodeApdSelector fallback: instance %s tpot metric : %v, ttft metric: %v", instanceView.GetInstanceId(), predictedTpotMetric.GetValue(), predictedTtftMetric.GetValue())

			if predictedTpotMetric.ValueLess(s.tpotSlo * s.tpotSloDispatchThreshold) {
				if highestTpotUnderSloDispatchThreshold == nil {
					highestTpotUnderSloDispatchThreshold = instanceView
				} else {
					higherTpot := highestTpotUnderSloDispatchThreshold.metrics[consts.SchedulingMetricPredictedTpot].Less(predictedTpotMetric)

					lowerTtft := highestTpotUnderSloDispatchThreshold.metrics[consts.SchedulingMetricPredictedTpot].Equal(predictedTpotMetric) &&
						predictedTtftMetric.Less(highestTpotUnderSloDispatchThreshold.metrics[consts.SchedulingMetricPredictedTtft])

					if higherTpot || lowerTtft {
						highestTpotUnderSloDispatchThreshold = instanceView
					}
				}
			} else {
				if lowestTpotOverSloDispatchThreshold == nil {
					lowestTpotOverSloDispatchThreshold = instanceView
				} else if predictedTpotMetric.Less(
					lowestTpotOverSloDispatchThreshold.metrics[consts.SchedulingMetricPredictedTpot]) {
					lowestTpotOverSloDispatchThreshold = instanceView
				}
			}
		}

		selectedInstance = highestTpotUnderSloDispatchThreshold
		if selectedInstance == nil {
			selectedInstance = lowestTpotOverSloDispatchThreshold
		}
	}

	return selectedInstance
}
