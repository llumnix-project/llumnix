package policy

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
	verifySchedulingPolicy(c)
	verifySchedulingFeature(c)
	verifyDispatchLoadMetric(c)
}

func verifySchedulingPolicy(c *options.SchedulerConfig) {
	liteModeSchedulingPolicySet := sets.New[string](consts.SchedulingPolicyLoadBalance)
	fullModeSchedulingPolicySet := sets.New[string](
		consts.SchedulingPolicyLoadBalance,
		consts.SchedulingPolicyFlood,
		consts.SchedulingPolicySlo)

	policy := c.SchedulingPolicy
	if !c.EnableFullModeScheduling {
		if !liteModeSchedulingPolicySet.Has(policy) {
			panic(fmt.Sprintf("The scheduling policy %s is not supported when not enable full-mode scheduling.", policy))
		}
	} else {
		if !fullModeSchedulingPolicySet.Has(policy) {
			panic(fmt.Sprintf("The scheduling policy %s is not supported when enabling full-mode scheduling.", policy))
		}
	}
}

func verifySchedulingFeature(c *options.SchedulerConfig) {
	if !c.EnableFullModeScheduling {
		if c.EnableCacheAwareScheduling {
			c.EnableCacheAwareScheduling = false
			klog.Warningf("The scheduling feature cache-aware scheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnablePredictorEnhancedScheduling {
			c.EnablePredictorEnhancedScheduling = false
			klog.Warningf("The scheduling feature predictor-enhanced scheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnableAdaptivePD {
			c.EnableAdaptivePD = false
			klog.Warningf("The scheduling feature adaptive-pd is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.EnableRescheduling {
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
	liteModeSchedulingMetricSet := sets.New[string](
		consts.SchedulingMetricNumRequests, consts.SchedulingMetricNumTokens)
	fullModeSchedulingMetricSet := sets.New[string](
		consts.SchedulingMetricKVCacheUsageRatioProjected, consts.SchedulingMetricDecodeBatchSize,
		consts.SchedulingMetricNumWaitingRequests, consts.SchedulingMetricAllPrefillsTokensNum,
		consts.SchedulingMetricKVCacheHitLen, consts.SchedulingMetricCacheAwareAllPrefillsTokensNum,
		consts.SchedulingMetricNumRequests, consts.SchedulingMetricAllDecodesTokensNum)

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
	raw map[consts.InferType]map[string]T) map[consts.InferType]map[string]InstanceViewInterface {
	if raw == nil {
		return nil
	}

	res := make(map[consts.InferType]map[string]InstanceViewInterface, len(raw))
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
	var groupedInstanceViews map[consts.InferType]map[string]*instanceViewScheduling
	var instanceViews map[string]*instanceViewScheduling
	groupedInstanceViews = make(map[consts.InferType]map[string]*instanceViewScheduling, len(cv.groupedInstanceViews))
	instanceViews = make(map[string]*instanceViewScheduling)
	for inferType, views := range cv.groupedInstanceViews {
		if groupedInstanceViews[inferType] == nil {
			groupedInstanceViews[inferType] = make(map[string]*instanceViewScheduling, len(views))
		}
		for instanceID, view := range views {
			groupedInstanceViews[inferType][instanceID] = &instanceViewScheduling{
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
				groupedInstanceViews[inferType][instanceID].cmsView = v
				groupedInstanceViews[inferType][instanceID].lrsView = nil
			case *lrs.InstanceView:
				groupedInstanceViews[inferType][instanceID].lrsView = v
				groupedInstanceViews[inferType][instanceID].cmsView = nil
			default:
				if v == nil {
					klog.Errorf("Instance view is nil for instance %s", instanceID)
				} else {
					klog.Errorf("Unexpected instance view type of instance %s: %T", instanceID, v)
				}
			}
			instanceViews[instanceID] = groupedInstanceViews[inferType][instanceID]
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
	filteredOutInstanceIds sets.Set[string]) sets.Set[string] {

	allInstanceIds := sets.New[string]()
	for id := range instanceViews {
		allInstanceIds.Insert(id)
	}
	return allInstanceIds.Difference(filteredOutInstanceIds)
}

func logSelectedInstance(
	instance *instanceViewScheduling, requestId string, requestInferType consts.InferType, enableFullModeScheduling bool) {
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
	loadMetric.Calculate(nil, instance)
	klog.V(3).Infof("[GetToken] dispatch request %s to %s instance %s for %s, %s: %.4f",
		requestId, instance.GetInferType(), instance.GetInstanceId(), requestInferType,
		loadMetric.GetName(), loadMetric.GetValue())
}
