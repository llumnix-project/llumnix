package policy

import (
	"fmt"
	"reflect"
	"time"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/klog/v2"

	"llumnix/pkg/consts"
)

func filter(
	instanceViews map[string]*instanceViewScheduling,
	sif []singleInstanceFilter,
	gf []globalFilter,
	fallback bool) map[string]*instanceViewScheduling {

	availableInstanceViews := make(map[string]*instanceViewScheduling)
	for instanceId, instanceView := range instanceViews {
		reject := false
		for _, f := range sif {
			if fallback && f.skipWhenFallback() {
				continue
			}
			// NOTE(sunbiao.sun): single instance filter may have side effects like writing the needsFailover field
			// in instance view, so all single instance filters should be executed once for each instance.
			if f.instanceFilteredOut(instanceView) {
				reject = true
			}
		}
		if !reject {
			availableInstanceViews[instanceId] = instanceView
		}
	}

	klog.V(4).Infof("After single instance filters, available instances: %v", getKeySliceFromMap(availableInstanceViews))

	// NOTE(sunbiao.sun): global filters should be employed after single instance filters,
	// because failover filter (global) requires to read the needsFailover field in instance view written by
	// schedulability&staleness filter.
	for _, f := range gf {
		globalFilteredOutInstanceIds := f.filterOutInstances(instanceViews)
		for instanceId := range globalFilteredOutInstanceIds {
			delete(availableInstanceViews, instanceId)
		}
		if globalFilteredOutInstanceIds.Len() > 0 {
			klog.V(4).Infof("Global filter filtered out instances: %v", globalFilteredOutInstanceIds.List())
		}
	}

	klog.V(4).Infof("Final available instances after all filters: %v", getKeySliceFromMap(availableInstanceViews))
	return availableInstanceViews
}

type singleInstanceFilter interface {
	// instanceFilteredOut returns true if the instance should be filtered out
	// (i.e., not ok for scheduling).
	instanceFilteredOut(*instanceViewScheduling) bool
	skipWhenFallback() bool
}

type globalFilter interface {
	filterOutInstances(map[string]*instanceViewScheduling) sets.String
}

type invertedSingleInstanceFilterWrapper struct {
	innerFilter singleInstanceFilter
}

func (r *invertedSingleInstanceFilterWrapper) instanceFilteredOut(instance *instanceViewScheduling) bool {
	result := !r.innerFilter.instanceFilteredOut(instance)
	if result {
		klog.V(3).Infof("Inverted filter (%T) applied, instance %s filtered out.", r.innerFilter, instance.GetInstanceId())
	}
	return result
}

func (r *invertedSingleInstanceFilterWrapper) skipWhenFallback() bool {
	return r.innerFilter.skipWhenFallback()
}

type metricBasedFilter struct {
	metricName          string
	threshold           float32
	notSkipWhenFallback bool
}

func (f *metricBasedFilter) instanceFilteredOut(
	instance *instanceViewScheduling) bool {
	metric := instance.schedulingCtx.metrics[f.metricName]
	result := !metric.ValueLess(f.threshold)
	if result {
		klog.V(3).Infof(
			"Metric based filter applied, instance %s filtered out due to %s metric (%f not less than %f)",
			instance.GetInstanceId(), f.metricName, metric.GetValue(), f.threshold)
	}
	return result
}

func (f *metricBasedFilter) skipWhenFallback() bool {
	return !f.notSkipWhenFallback
}

type schedulabilityFilter struct{}

func (f *schedulabilityFilter) instanceFilteredOut(instance *instanceViewScheduling) bool {
	instance.schedulingCtx.needsFailover =
		instance.schedulingCtx.needsFailover || !instance.cmsView.Status.Schedulable
	result := !instance.cmsView.Status.Schedulable
	if result {
		klog.V(3).Infof("Schedulability filter applied, instance %s filtered out (not schedulable)",
			instance.GetInstanceId())
	}
	return result
}

func (f *schedulabilityFilter) skipWhenFallback() bool {
	return false
}

type instanceAttributeFilter struct {
	attrKey       string
	rejectedValue interface{}
}

func (f *instanceAttributeFilter) instanceFilteredOut(instance *instanceViewScheduling) bool {
	if instance.cmsView == nil {
		klog.Warningf("cmsView is nil")
		return false
	}

	v := reflect.ValueOf(instance.cmsView)

	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	targetValue := v.FieldByName(f.attrKey)

	if !targetValue.IsValid() {
		klog.Warningf("field %s not found", f.attrKey)
		return false
	}

	targetInterface := targetValue.Interface()
	equal := reflect.DeepEqual(targetInterface, f.rejectedValue)

	if equal {
		klog.V(3).Infof("instanceAttributeFilter applied, instance %s filtered out (attribute %s: %v matches rejected value: %s)",
			instance.cmsView.GetInstanceId(), f.attrKey, targetValue, f.rejectedValue)
	}

	return equal
}

func (f *instanceAttributeFilter) skipWhenFallback() bool {
	return false
}

// NOTE(sunbiao.sun): staleness filter could be dynamically employed to handle redis server restart in the future.
type stalenessFilter struct {
	instanceStalenessSeconds int64
}

func (f *stalenessFilter) instanceFilteredOut(instance *instanceViewScheduling) bool {
	// If instance view timestamp is not updated for a long time,
	// we consider instance stale and filter it out.
	instanceStale := (time.Now().UnixMilli()-instance.cmsView.Status.TimestampMs)/1e3 > f.instanceStalenessSeconds
	instance.schedulingCtx.needsFailover =
		instance.schedulingCtx.needsFailover || instanceStale
	if instanceStale {
		klog.V(3).Infof(
			"Staleness filter applied, instance %s filtered out, "+
				"timestamp: %v, now: %v, instanceStalenessSeconds: %v, interval: %vs",
			instance.GetInstanceId(), instance.cmsView.Status.TimestampMs, time.Now().UnixMilli(), f.instanceStalenessSeconds,
			(time.Now().UnixMilli()-instance.cmsView.Status.TimestampMs)/1e3)
	}
	return instanceStale
}

func (f *stalenessFilter) skipWhenFallback() bool {
	return false
}

type failoverFilter struct {
	failoverDomain string
}

func isDataParallelEnabled(instanceViews map[string]*instanceViewScheduling) bool {
	var firstView *instanceViewScheduling
	// Assume that all the instances have the same parallel config.
	for _, m := range instanceViews {
		firstView = m
		break
	}
	result := firstView != nil && firstView.cmsView.Metadata.DataParallelSize > 1
	if result {
		klog.V(3).Infof("Data parallel enabled: %t, first instance metadata: %+v", result, firstView)
	}
	return result
}

func getNeedsFailoverInstances(
	instanceViews map[string]*instanceViewScheduling) sets.String {

	needsFailoverInstances := sets.NewString()
	for id, view := range instanceViews {
		if view.schedulingCtx.needsFailover {
			needsFailoverInstances.Insert(id)
		}
	}
	if needsFailoverInstances.Len() > 0 {
		klog.V(3).Infof("Needs failover instances: %v", needsFailoverInstances.List())
	}
	return needsFailoverInstances
}

func getFailoverNodes(
	needsFailoverInstances sets.String,
	instanceViews map[string]*instanceViewScheduling) sets.String {

	failoverNodes := sets.NewString()
	for id := range needsFailoverInstances {
		view := instanceViews[id]
		if view != nil && view.cmsView.Metadata.NodeId != "" {
			failoverNodes.Insert(view.cmsView.Metadata.NodeId)
		}
	}
	if failoverNodes.Len() > 0 {
		klog.V(3).Infof("Failover nodes %v based on needs failover instances: %v",
			failoverNodes.List(), needsFailoverInstances.List())
	}
	return failoverNodes
}

func getNodeFailoverInstances(
	failoverNodes sets.String,
	instanceViews map[string]*instanceViewScheduling) sets.String {

	nodeFailoverInstances := sets.NewString()
	for id, view := range instanceViews {
		if failoverNodes.Has(view.cmsView.Metadata.NodeId) {
			nodeFailoverInstances.Insert(id)
		}
	}
	if nodeFailoverInstances.Len() > 0 {
		klog.V(3).Infof("Failover instances %v based on failover nodes %v",
			nodeFailoverInstances.List(), failoverNodes.List())
	}
	return nodeFailoverInstances
}

func getFailoverUnits(
	nodeFailoverInstances sets.String,
	instanceViews map[string]*instanceViewScheduling) sets.String {

	failoverUnits := sets.NewString()
	for id := range nodeFailoverInstances {
		view := instanceViews[id]
		failoverUnits.Insert(view.cmsView.Metadata.UnitId)
	}
	if failoverUnits.Len() > 0 {
		klog.V(3).Infof("Failover units %v based on node failover instances %v",
			failoverUnits.List(), nodeFailoverInstances.List())
	}
	return failoverUnits
}

func getUnitFailoverInstances(
	failoverUnits sets.String,
	instanceViews map[string]*instanceViewScheduling) sets.String {

	unitFailoverInstances := sets.NewString()
	for id, view := range instanceViews {
		if failoverUnits.Has(view.cmsView.Metadata.UnitId) {
			unitFailoverInstances.Insert(id)
		}
	}
	if unitFailoverInstances.Len() > 0 {
		klog.V(3).Infof("Failover instances %v based on failover units %v",
			unitFailoverInstances.List(), failoverUnits.List())
	}
	return unitFailoverInstances
}

func (f *failoverFilter) filterOutInstances(instanceViews map[string]*instanceViewScheduling) sets.String {

	needsFailoverInstances := getNeedsFailoverInstances(instanceViews)
	switch f.failoverDomain {
	case consts.FailoverDomainInstance:
		// When the failover domain is instance, failover instances are identical to needs failover instances.
		klog.V(3).Infof("Instance domain failover, filtered out instances: %v",
			needsFailoverInstances.List())
		return needsFailoverInstances
	case consts.FailoverDomainNode:
		// Failover instances sharing the same node with the needs failover instances.
		failoverNodes := getFailoverNodes(needsFailoverInstances, instanceViews)
		result := getNodeFailoverInstances(failoverNodes, instanceViews)
		klog.V(3).Infof("Node domain failover, filtered out instances: %v", result.List())
		return result
	case consts.FailoverDomainInstanceUnit:
		if !isDataParallelEnabled(instanceViews) {
			klog.V(3).Infof(
				"Data parallel disabled, instance domain failover filtered out instances: %v",
				needsFailoverInstances.List())
			return needsFailoverInstances
		}
		failoverUnits := getFailoverUnits(needsFailoverInstances, instanceViews)
		result := getUnitFailoverInstances(failoverUnits, instanceViews)
		klog.V(3).Infof("Instance domain failover filtered out instances: %v",
			result.List())
		return result
	case consts.FailoverDomainNodeUnit:
		// 1. Find instances on nodes that have needs failover instances
		// 2. Failover instances that share the same unit with instances found in step 1
		failoverNodes := getFailoverNodes(needsFailoverInstances, instanceViews)
		nodeFailoverInstances := getNodeFailoverInstances(failoverNodes, instanceViews)
		if !isDataParallelEnabled(instanceViews) {
			klog.V(3).Infof("Data parallel disabled, Node domain failover filtered out instances: %v",
				nodeFailoverInstances.List())
			return nodeFailoverInstances
		}
		failoverUnits := getFailoverUnits(nodeFailoverInstances, instanceViews)
		result := getUnitFailoverInstances(failoverUnits, instanceViews)
		klog.V(3).Infof("Node domain failover filtered out instances: %v", result.List())
		return result
	default:
		klog.Errorf("Unsupported failover domain: %s", f.failoverDomain)
		panic(fmt.Sprintf("unsupported failover domain: %s", f.failoverDomain))
	}
}

type inferTypeFilter struct {
	targetInferType consts.InferType
}

func (f *inferTypeFilter) instanceFilteredOut(instance *instanceViewScheduling) bool {
	result := instance.GetInferType() != f.targetInferType
	if result {
		klog.V(3).Infof(
			"Infer type filter applied, instance %s filtered out (instance type: %s, target type: %s)",
			instance.GetInstanceId(), instance.GetInferType(), f.targetInferType)
	}
	return result
}

func (f *inferTypeFilter) skipWhenFallback() bool {
	return false
}

type failoverMigrationSrcFilter struct {
	instanceStalenessSeconds int64
	failoverDomain          string
}

func (fmf *failoverMigrationSrcFilter) filterOutInstances(
	instanceViews map[string]*instanceViewScheduling) sets.String {
	failoverSingleInstanceFilters := []singleInstanceFilter{
		&schedulabilityFilter{},
		&stalenessFilter{instanceStalenessSeconds: fmf.instanceStalenessSeconds},
	}
	// Employ schedulability&staleness filter to mark needsFailover field in instance view.
	for _, instanceView := range instanceViews {
		for _, f := range failoverSingleInstanceFilters {
			f.instanceFilteredOut(instanceView)
		}
	}
	temporaryFailoverFilter := failoverFilter{
		failoverDomain: fmf.failoverDomain,
	}
	// Generate failover filtered out instance id set.
	failoverFilteredOutInstanceIds := temporaryFailoverFilter.filterOutInstances(instanceViews)
	// The failover migration src instance id set is identical to the failover instance id set, so the difference set
	// between universal instance id set and failover filtered out instance id set is filtered out.
	result := getRemainingInstanceIds(instanceViews, failoverFilteredOutInstanceIds)
	klog.V(3).Infof(
		"Failover migration src filter, failover filtered out instances: %v, remaining instances: %v",
		failoverFilteredOutInstanceIds.List(), result.List())
	return result
}
