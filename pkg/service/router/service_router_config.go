package router

import (
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/property"
	"sync/atomic"

	"k8s.io/klog/v2"
)

// ServiceRouterConfigInterface defines all configuration methods for service routing.
// ServiceRouterConfig is the production implementation that periodically reads from
// DynamicConfigManager and caches values in a local atomic snapshot for lock-free reads;
// tests can substitute a lightweight mock to avoid external dependencies.
type ServiceRouterConfigInterface interface {
	RoutePolicy() string
	RouteConfigRaw() string
	// NeedRebuild returns true if router instance should be rebuilt
	NeedRebuild() bool
	// MarkRebuilt resets the rebuild flag after router is rebuilt
	MarkRebuilt()
}

type ServiceRouterConfig struct {
	dyConfigMgr *property.DynamicConfigManager

	currentSnapshot atomic.Value // stores *configSnapshot; all getters read from here
	needRebuild     atomic.Bool  // signals when router needs rebuilding
}

// configSnapshot holds a snapshot of all config values for comparison
type configSnapshot struct {
	routePolicy    string
	routeConfigRaw string
}

func NewServiceRouterConfig() *ServiceRouterConfig {
	rc := &ServiceRouterConfig{
		dyConfigMgr: property.GetDynamicConfigManager(),
	}
	// Initialize with empty snapshot to avoid nil panic
	rc.currentSnapshot.Store(&configSnapshot{})
	// Register as listener for route config changes.
	rc.dyConfigMgr.RegisterListener(rc, consts.ConfigKeyRoutePrefix)
	return rc
}

// readFromDyConfig reads all config values directly from DynamicConfigManager.
// This is the ONLY method that accesses dyConfigMgr; all getters read from the local snapshot.
func (rc *ServiceRouterConfig) readFromDyConfig() *configSnapshot {
	return &configSnapshot{
		routePolicy:    rc.dyConfigMgr.GetStringWithDefault(consts.ConfigKeyRoutePolicy, ""),
		routeConfigRaw: rc.dyConfigMgr.GetStringWithDefault(consts.ConfigKeyRouteConfig, ""),
	}
}

// getSnapshot returns the current config snapshot atomically.
func (rc *ServiceRouterConfig) getSnapshot() *configSnapshot {
	return rc.currentSnapshot.Load().(*configSnapshot)
}

// OnConfigChanged implements property.ConfigChangeListener.
// Called by DynamicConfigManager when route configuration changes.
func (rc *ServiceRouterConfig) OnConfigChanged(keys []string) {
	oldSnapshot := rc.getSnapshot()
	newSnapshot := rc.readFromDyConfig()

	// Log changes (only logs fields that actually changed)
	rc.logConfigChanges(oldSnapshot, newSnapshot)

	// Atomically swap in new snapshot so all getters see the updated values
	rc.currentSnapshot.Store(newSnapshot)

	// Signal that router needs rebuilding
	rc.needRebuild.Store(true)
}

// logConfigChanges logs the specific config changes.
// Only logs fields that have actually changed.
func (rc *ServiceRouterConfig) logConfigChanges(old, new *configSnapshot) {
	if new.routeConfigRaw == "" || new.routePolicy == "" {
		klog.Infof("[ServiceRouter] disabled")
		return
	}

	klog.Infof("[ServiceRouter] enabled")
	klog.Infof("  - route_policy: %v", new.routePolicy)
	klog.Infof("  - route_config: %v", new.routeConfigRaw)
}

func (rc *ServiceRouterConfig) RoutePolicy() string {
	return rc.getSnapshot().routePolicy
}

func (rc *ServiceRouterConfig) RouteConfigRaw() string {
	return rc.getSnapshot().routeConfigRaw
}

func (rc *ServiceRouterConfig) NeedRebuild() bool {
	return rc.needRebuild.Load()
}

func (rc *ServiceRouterConfig) MarkRebuilt() {
	rc.needRebuild.Store(false)
}
