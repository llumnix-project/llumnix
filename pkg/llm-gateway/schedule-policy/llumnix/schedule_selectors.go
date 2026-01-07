package llumnix

import (
	"math/rand"
	"sort"

	"golang.org/x/exp/maps"
	"k8s.io/klog/v2"
)

type dispatchSelector interface {
	selectInstance(instances map[string]*instanceViewScheduling) *instanceViewScheduling
}

type metricBasedSelector struct {
	topK        int
	metricNames []string
}

func (s *metricBasedSelector) selectInstance(instances map[string]*instanceViewScheduling) *instanceViewScheduling {
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

	klog.V(4).Infof("Best instance selected: %s", selectedInstance.GetInstanceId())

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

func (f *fixedPreferenceSelector) selectInstance(instances map[string]*instanceViewScheduling) *instanceViewScheduling {
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
