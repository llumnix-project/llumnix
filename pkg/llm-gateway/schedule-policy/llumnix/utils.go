package llumnix

import (
	"easgo/cmd/llm-gateway/app/options"
	"easgo/pkg/llm-gateway/cms"
	"easgo/pkg/llm-gateway/lrs"
	"fmt"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"easgo/pkg/llm-gateway/consts"
)

func verifyConfig(c *options.Config) {
	verifySchedulePolicy(c)
	verifySchedulingFeature(c)
	verifyDispatchLoadMetric(c)
}

func verifySchedulePolicy(c *options.Config) {
	liteModeSchedulePolicySet := sets.NewString(consts.SchedulePolicyLoadBalance)
	fullModeSchedulePolicySet := sets.NewString(consts.SchedulePolicyLoadBalance, consts.SchedulePolicyFlood)

	policy := c.SchedulePolicy
	if !c.LlumnixConfig.EnableFullModeScheduling {
		if !liteModeSchedulePolicySet.Has(policy) {
			panic(fmt.Sprintf("The schedule policy %s is not supported when not enable full-mode scheduling.", policy))
		}
	} else {
		if !fullModeSchedulePolicySet.Has(policy) {
			panic(fmt.Sprintf("The schedule policy %s is not supported when enabling full-mode scheduling.", policy))
		}
	}
}

func verifySchedulingFeature(c *options.Config) {
	if !c.LlumnixConfig.EnableFullModeScheduling {
		if c.LlumnixConfig.EnableCacheAwareScheduling == true {
			c.LlumnixConfig.EnableCacheAwareScheduling = false
			klog.Warningf("The scheduling feature cache-aware scheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.LlumnixConfig.EnablePredictorEnhancedScheduling == true {
			c.LlumnixConfig.EnablePredictorEnhancedScheduling = false
			klog.Warningf("The scheduling feature predictor-enhanced scheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.LlumnixConfig.EnableAdaptivePD == true {
			c.LlumnixConfig.EnableAdaptivePD = false
			klog.Warningf("The scheduling feature adaptive-pd is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.LlumnixConfig.EnableRescheduling == true {
			c.LlumnixConfig.EnableRescheduling = false
			klog.Warningf("The scheduling feature rescheduling is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
		if c.LlumnixConfig.EnableInstanceStatusLocalAccount {
			c.LlumnixConfig.EnableInstanceStatusLocalAccount = false
			klog.Warningf("The scheduling feature instance-status-local-account is not supported when not enable full-mode scheduling, forcefully disable it here.")
		}
	}
}

func verifyDispatchLoadMetric(c *options.Config) {
	liteModeSchedulingMetricSet := sets.NewString(
		consts.LlumnixSchedulingMetricNumRequests, consts.LlumnixSchedulingMetricNumTokens)
	fullModeSchedulingMetricSet := sets.NewString(
		consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills, consts.LlumnixSchedulingMetricDecodeBatchSize,
		consts.LlumnixSchedulingMetricNumWaitingRequests, consts.LlumnixSchedulingMetricAllPrefillsKVBlocksNum,
		consts.LlumnixSchedulingMetricKVCacheHitLen, consts.LlumnixSchedulingMetricCacheAwareAllPrefillsKVBlocksNum,
		consts.LlumnixSchedulingMetricAdaptiveDecodeBatchSize, consts.LlumnixSchedulingMetricNumRequests,
		consts.LlumnixSchedulingMetricAllDecodesSeqLenWithAllPrefills)

	if !c.LlumnixConfig.EnableFullModeScheduling {
		if !liteModeSchedulingMetricSet.Has(c.LlumnixConfig.DispatchNeutralLoadMetric) {
			klog.Warningf("The neutral dispatch load metric %s is not supported when not enable full-mode scheduling, "+
				"forcefully set metric to %s here.", c.LlumnixConfig.DispatchNeutralLoadMetric, consts.LlumnixSchedulingMetricNumTokens)
			c.LlumnixConfig.DispatchNeutralLoadMetric = consts.LlumnixSchedulingMetricNumTokens
		}
		if !liteModeSchedulingMetricSet.Has(c.LlumnixConfig.DispatchPrefillLoadMetric) {
			klog.Warningf("The prefill dispatch load metric %s is not supported when not enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.LlumnixConfig.DispatchPrefillLoadMetric, consts.LlumnixSchedulingMetricNumTokens)
			c.LlumnixConfig.DispatchPrefillLoadMetric = consts.LlumnixSchedulingMetricNumTokens
		}
		if !liteModeSchedulingMetricSet.Has(c.LlumnixConfig.DispatchDecodeLoadMetric) {
			klog.Warningf("The decode dispatch load metric %s is not supported when not enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.LlumnixConfig.DispatchDecodeLoadMetric, consts.LlumnixSchedulingMetricNumTokens)
			c.LlumnixConfig.DispatchDecodeLoadMetric = consts.LlumnixSchedulingMetricNumTokens
		}
	} else {
		if !fullModeSchedulingMetricSet.Has(c.LlumnixConfig.DispatchNeutralLoadMetric) {
			klog.Warningf("The neutral dispatch load metric %s is not supported when enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.LlumnixConfig.DispatchNeutralLoadMetric, consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills)
			c.LlumnixConfig.DispatchNeutralLoadMetric = consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
		}
		if !fullModeSchedulingMetricSet.Has(c.LlumnixConfig.DispatchPrefillLoadMetric) {
			klog.Warningf("The prefill dispatch load metric %s is not supported when enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.LlumnixConfig.DispatchPrefillLoadMetric, consts.LlumnixSchedulingMetricAllPrefillsKVBlocksNum)
			c.LlumnixConfig.DispatchPrefillLoadMetric = consts.LlumnixSchedulingMetricAllPrefillsKVBlocksNum
		}
		if !fullModeSchedulingMetricSet.Has(c.LlumnixConfig.DispatchDecodeLoadMetric) {
			klog.Warningf("The decode dispatch load metric %s is not supported when enable full-mode scheduling, "+
				"forcefully set metric to %s here", c.LlumnixConfig.DispatchDecodeLoadMetric, consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills)
			c.LlumnixConfig.DispatchDecodeLoadMetric = consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills
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
					metrics:             map[string]instanceSchedulingMetric{},
					needsFailover:       false,
					prefixHitLen:        0,
					prefixHitNumBlocks:  0,
					prefixHitRatio:      0.0,
					prefixMissLen:       0,
					prefixMissNumBlocks: 0,
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
		loadMetric = &kvBlocksRatioWithAllPrefills{
			baseMetric: baseMetric{
				name: consts.LlumnixSchedulingMetricKVBlocksRatioWithAllPrefills,
			},
		}
	} else {
		loadMetric = &numTokens{
			baseMetric: baseMetric{
				name: consts.LlumnixSchedulingMetricNumTokens,
			},
		}
	}
	loadMetric.Calculate(instance)
	klog.V(3).Infof("[GetToken] dispatch request %s to %s instance %s for %s, %s: %.4f",
		requestId, instance.GetInferMode(), instance.GetInstanceId(), requestInferMode,
		loadMetric.GetName(), loadMetric.GetValue())
}
