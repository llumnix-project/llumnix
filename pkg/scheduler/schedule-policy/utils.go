package schedule_policy

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/cmd/scheduler/app/options"
	"llumnix/pkg/cms"
	"llumnix/pkg/consts"
	"llumnix/pkg/lrs"
)

func getKeySliceFromMap[M ~map[K]V, K comparable, V any](m M) []K {
	keys := make([]K, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func verifyConfig(c *options.SchedulerConfig) {
	verifySchedulePolicy(c)
	verifySchedulingFeature(c)
	verifyDispatchLoadMetric(c)
}

func verifySchedulePolicy(c *options.SchedulerConfig) {
	liteModeSchedulePolicySet := sets.NewString(consts.SchedulePolicyLoadBalance)
	fullModeSchedulePolicySet := sets.NewString(consts.SchedulePolicyLoadBalance, consts.SchedulePolicyFlood)

	policy := c.SchedulePolicy
	if !c.EnableFullModeScheduling {
		if !liteModeSchedulePolicySet.Has(policy) {
			panic(fmt.Sprintf("The schedule policy %s is not supported when not enable full-mode scheduling.", policy))
		}
	} else {
		if !fullModeSchedulePolicySet.Has(policy) {
			panic(fmt.Sprintf("The schedule policy %s is not supported when enabling full-mode scheduling.", policy))
		}
	}
}

func verifySchedulingFeature(c *options.SchedulerConfig) {
	if !c.EnableFullModeScheduling {
		if c.EnableCacheAwareScheduling == true {
			c.EnableCacheAwareScheduling = false
			klog.Warningf("The scheduling feature cache-aware scheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnablePredictorEnhancedScheduling == true {
			c.EnablePredictorEnhancedScheduling = false
			klog.Warningf("The scheduling feature predictor-enhanced scheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnableAdaptivePD == true {
			c.EnableAdaptivePD = false
			klog.Warningf("The scheduling feature adaptive-pd is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnableRescheduling == true {
			c.EnableRescheduling = false
			klog.Warningf("The scheduling feature rescheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnableInstanceStatusLocalAccount {
			c.EnableInstanceStatusLocalAccount = false
			klog.Warningf("The scheduling feature instance-status-local-account is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
	}
}

func verifyDispatchLoadMetric(c *options.SchedulerConfig) {
	liteModeSchedulingMetricSet := sets.NewString(
		consts.SchedulingMetricNumRequests, consts.SchedulingMetricNumTokens)
	fullModeSchedulingMetricSet := sets.NewString(
		consts.SchedulingMetricKVCacheUsageRatioProjected, consts.SchedulingMetricDecodeBatchSize,
		consts.SchedulingMetricNumWaitingRequests, consts.SchedulingMetricAllPrefillsTokensNum,
		consts.SchedulingMetricKVCacheHitLen, consts.SchedulingMetricCacheAwareAllPrefillsTokensNum,
		consts.SchedulingMetricAdaptiveDecodeBatchSize, consts.SchedulingMetricNumRequests,
		consts.SchedulingMetricAllDecodesTokensNum)

	if !c.EnableFullModeScheduling {
		if !liteModeSchedulingMetricSet.Has(c.DispatchNeutralLoadMetric) {
			klog.Warningf("The neutral dispatch load metric %s is not supported when not enable full-mode scheduling, "+
				"forcefully set metric to %s here.", c.DispatchNeutralLoadMetric, consts.SchedulingMetricNumTokens)
			c.DispatchNeutralLoadMetric = consts.SchedulingMetricNumTokens
		}
		if !liteModeSchedulingMetricSet.Has(c.DispatchPrefillLoadMetric) {
			klog.Warningf("The prefill dispatch load metric %s is not supported when not enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.DispatchPrefillLoadMetric, consts.SchedulingMetricNumTokens)
			c.DispatchPrefillLoadMetric = consts.SchedulingMetricNumTokens
		}
		if !liteModeSchedulingMetricSet.Has(c.DispatchDecodeLoadMetric) {
			klog.Warningf("The decode dispatch load metric %s is not supported when not enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.DispatchDecodeLoadMetric, consts.SchedulingMetricNumTokens)
			c.DispatchDecodeLoadMetric = consts.SchedulingMetricNumTokens
		}
	} else {
		if !fullModeSchedulingMetricSet.Has(c.DispatchNeutralLoadMetric) {
			klog.Warningf("The neutral dispatch load metric %s is not supported when enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.DispatchNeutralLoadMetric, consts.SchedulingMetricKVCacheUsageRatioProjected)
			c.DispatchNeutralLoadMetric = consts.SchedulingMetricKVCacheUsageRatioProjected
		}
		if !fullModeSchedulingMetricSet.Has(c.DispatchPrefillLoadMetric) {
			klog.Warningf("The prefill dispatch load metric %s is not supported when enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.DispatchPrefillLoadMetric, consts.SchedulingMetricAllPrefillsTokensNum)
			c.DispatchPrefillLoadMetric = consts.SchedulingMetricAllPrefillsTokensNum
		}
		if !fullModeSchedulingMetricSet.Has(c.DispatchDecodeLoadMetric) {
			klog.Warningf("The decode dispatch load metric %s is not supported when enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.DispatchDecodeLoadMetric, consts.SchedulingMetricKVCacheUsageRatioProjected)
			c.DispatchDecodeLoadMetric = consts.SchedulingMetricKVCacheUsageRatioProjected
		}
	}
}

func toInstanceViewInterfaceMap[T InstanceViewInterface](
	raw map[string]map[string]T) map[string]map[string]InstanceViewInterface {
	if raw == nil {
		return nil
	}

	res := make(map[string]map[string]InstanceViewInterface, len(raw))
	for mode, views := range raw {
		inner := make(map[string]InstanceViewInterface, len(views))
		for id, v := range views {
			inner[id] = v
		}
		res[mode] = inner
	}
	return res
}

func toClusterViewScheduling(cv clusterView) clusterViewScheduling {
	var groupedInstanceViews map[string]map[string]*instanceViewScheduling
	var instanceViews map[string]*instanceViewScheduling
	groupedInstanceViews = make(map[string]map[string]*instanceViewScheduling, len(cv.groupedInstanceViews))
	instanceViews = make(map[string]*instanceViewScheduling)
	for inferMode, views := range cv.groupedInstanceViews {
		if groupedInstanceViews[inferMode] == nil {
			groupedInstanceViews[inferMode] = make(map[string]*instanceViewScheduling, len(views))
		}
		for instanceID, view := range views {
			groupedInstanceViews[inferMode][instanceID] = &instanceViewScheduling{
				InstanceViewInterface: view,
				schedulingCtx: schedulingCtx{
					metrics:          map[string]instanceSchedulingMetric{},
					needsFailover:    false,
					prefixHitTokens:  0,
					prefixHitRatio:   0.0,
					prefixMissTokens: 0,
				},
			}
			switch v := view.(type) {
			case *cms.InstanceView:
				groupedInstanceViews[inferMode][instanceID].cmsView = v
				groupedInstanceViews[inferMode][instanceID].lrsView = nil
			case *lrs.InstanceView:
				groupedInstanceViews[inferMode][instanceID].lrsView = v
				groupedInstanceViews[inferMode][instanceID].cmsView = nil
			default:
				if v == nil {
					klog.Errorf("Instance view is nil for instance %s", instanceID)
				} else {
					klog.Errorf("Unexpected instance view type of instance %s: %T", instanceID, v)
				}
			}
			instanceViews[instanceID] = groupedInstanceViews[inferMode][instanceID]
		}
	}
	result := clusterViewScheduling{
		groupedInstanceViews: groupedInstanceViews,
		instanceViews:        instanceViews,
		clusterSchedulingCtx: clusterSchedulingCtx{},
	}
	return result
}

func getRemainingInstanceIds(
	instanceViews map[string]*instanceViewScheduling,
	filteredOutInstanceIds sets.String) sets.String {

	allInstanceIds := sets.NewString()
	for id := range instanceViews {
		allInstanceIds.Insert(id)
	}
	return allInstanceIds.Difference(filteredOutInstanceIds)
}

func logSelectedInstance(
	instance *instanceViewScheduling, requestId string, requestInferMode string, enableFullModeScheduling bool) {
	var loadMetric instanceSchedulingMetric
	if enableFullModeScheduling {
		loadMetric = &kvCacheUsageRatioProjected{
			baseMetric: baseMetric{
				name: consts.SchedulingMetricKVCacheUsageRatioProjected,
			},
		}
	} else {
		loadMetric = &numTokens{
			baseMetric: baseMetric{
				name: consts.SchedulingMetricNumTokens,
			},
		}
	}
	loadMetric.Calculate(instance)
	klog.V(3).Infof("[GetToken] dispatch request %s to %s instance %s for %s, %s: %.4f",
		requestId, instance.GetInferMode(), instance.GetInstanceId(), requestInferMode,
		loadMetric.GetName(), loadMetric.GetValue())
}
