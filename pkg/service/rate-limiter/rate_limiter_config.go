package ratelimiter

import (
	"fmt"
	"llm-gateway/pkg/consts"
	"llm-gateway/pkg/property"
	"sync/atomic"

	"k8s.io/klog/v2"
)

type LimitAction string

const (
	LimitActionReject LimitAction = "reject"
	LimitActionQueue  LimitAction = "queue"
)

func (la LimitAction) String() string {
	return string(la)
}

type LimitScope string

const (
	LimitScopeInstance LimitScope = "instance"
	LimitScopeService  LimitScope = "service"
)

func (ls LimitScope) String() string {
	return string(ls)
}

// RateLimiterConfigInterface defines all configuration methods for rate limiting.
// RateLimiterConfig is the production implementation that periodically reads from
// DynamicConfigManager and caches values in a local atomic snapshot for lock-free reads;
// tests can substitute a lightweight mock to avoid external dependencies.
type RateLimiterConfigInterface interface {
	Enabled() bool
	LimitScope() LimitScope
	LimitAction() LimitAction
	MaxWaitTimeoutMs() int64
	RetryIntervalMs() int64
	MaxRequestsPerInstance() int64
	MaxTokensPerInstance() int64
	MaxPrefillRequestsPerInstance() int64
	MaxPrefillTokensPerInstance() int64
	MaxDecodeRequestsPerInstance() int64
	MaxDecodeTokensPerInstance() int64
}

type RateLimiterConfig struct {
	dyConfigMgr *property.DynamicConfigManager

	oldLimitScope  LimitScope
	oldLimitAction LimitAction
	oldEnabled     bool

	currentSnapshot atomic.Value // stores *configSnapshot; all getters read from here
}

// configSnapshot holds a snapshot of all config values for comparison
type configSnapshot struct {
	enabled                       bool
	limitScope                    LimitScope
	limitAction                   LimitAction
	maxWaitTimeoutMs              int64
	retryIntervalMs               int64
	maxRequestsPerInstance        int64
	maxTokensPerInstance          int64
	maxPrefillRequestsPerInstance int64
	maxPrefillTokensPerInstance   int64
	maxDecodeRequestsPerInstance  int64
	maxDecodeTokensPerInstance    int64
}

func NewRateLimiterConfig() *RateLimiterConfig {
	rc := &RateLimiterConfig{
		dyConfigMgr: property.GetDynamicConfigManager(),
	}
	// Initialize with empty snapshot to avoid nil panic
	rc.currentSnapshot.Store(&configSnapshot{})
	// Register as listener for rate limit config changes.
	rc.dyConfigMgr.RegisterListener(rc, consts.ConfigKeyRateLimitPrefix)
	return rc
}

func (rc *RateLimiterConfig) SaveOldConfig() {
	rc.oldEnabled = rc.Enabled()
	rc.oldLimitScope = rc.LimitScope()
	rc.oldLimitAction = rc.LimitAction()
}

func (rc *RateLimiterConfig) NeedReLoadRateLimiter() bool {
	return rc.oldEnabled != rc.Enabled() ||
		rc.oldLimitScope != rc.LimitScope() ||
		rc.oldLimitAction != rc.LimitAction()
}

// readFromDyConfig reads all config values directly from DynamicConfigManager.
// This is the ONLY method that accesses dyConfigMgr; all getters read from the local snapshot.
// NOTE: Limit fields use 0 to mean "unlimited"
func (rc *RateLimiterConfig) readFromDyConfig() *configSnapshot {
	return &configSnapshot{
		enabled:                       rc.dyConfigMgr.GetBoolWithDefault(consts.ConfigKeyRateLimitEnable, false),
		limitScope:                    LimitScope(rc.dyConfigMgr.GetStringWithDefault(consts.ConfigKeyRateLimitScope, LimitScopeInstance.String())),
		limitAction:                   LimitAction(rc.dyConfigMgr.GetStringWithDefault(consts.ConfigKeyRateLimitAction, LimitActionReject.String())),
		maxWaitTimeoutMs:              int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxWaitTimeout, 5000)),
		retryIntervalMs:               int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitRetryInterval, 100)),
		maxRequestsPerInstance:        int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxRequestsPerInstance, 0)),
		maxTokensPerInstance:          int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxTokensPerInstance, 0)),
		maxPrefillRequestsPerInstance: int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxPrefillRequestsPerInstance, 0)),
		maxPrefillTokensPerInstance:   int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxPrefillTokensPerInstance, 0)),
		maxDecodeRequestsPerInstance:  int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxDecodeRequestsPerInstance, 0)),
		maxDecodeTokensPerInstance:    int64(rc.dyConfigMgr.GetIntWithDefault(consts.ConfigKeyRateLimitMaxDecodeTokensPerInstance, 0)),
	}
}

// getSnapshot returns the current config snapshot atomically.
func (rc *RateLimiterConfig) getSnapshot() *configSnapshot {
	return rc.currentSnapshot.Load().(*configSnapshot)
}

// OnConfigChanged implements property.ConfigChangeListener.
// Called by DynamicConfigManager when rate limit configuration changes.
func (rc *RateLimiterConfig) OnConfigChanged(keys []string) {
	oldSnapshot := rc.getSnapshot()
	newSnapshot := rc.readFromDyConfig()

	// Log changes (only logs fields that actually changed)
	rc.logConfigChanges(oldSnapshot, newSnapshot)

	// Atomically swap in new snapshot so all getters see the updated values
	rc.currentSnapshot.Store(newSnapshot)
}

// logConfigChanges logs the specific config changes.
// Only logs fields that have actually changed.
func (rc *RateLimiterConfig) logConfigChanges(old, new *configSnapshot) {
	if !new.enabled {
		klog.Infof("[RateLimiter] disabled")
	}

	klog.Infof("[RateLimiter] enabled:")

	if old.limitScope != new.limitScope {
		klog.Infof("  - limit_scope: %v -> %v", old.limitScope, new.limitScope)
	}
	if old.limitAction != new.limitAction {
		klog.Infof("  - limit_action: %v -> %v", old.limitAction, new.limitAction)
	}
	if old.maxWaitTimeoutMs != new.maxWaitTimeoutMs {
		klog.Infof("  - max_wait_timeout_ms: %v -> %v", old.maxWaitTimeoutMs, new.maxWaitTimeoutMs)
	}
	if old.retryIntervalMs != new.retryIntervalMs {
		klog.Infof("  - retry_interval_ms: %v -> %v", old.retryIntervalMs, new.retryIntervalMs)
	}
	if old.maxRequestsPerInstance != new.maxRequestsPerInstance {
		klog.Infof("  - max_requests_per_instance: %v -> %v", formatLimitValue(old.maxRequestsPerInstance), formatLimitValue(new.maxRequestsPerInstance))
	}
	if old.maxTokensPerInstance != new.maxTokensPerInstance {
		klog.Infof("  - max_tokens_per_instance: %v -> %v", formatLimitValue(old.maxTokensPerInstance), formatLimitValue(new.maxTokensPerInstance))
	}
	if old.maxPrefillRequestsPerInstance != new.maxPrefillRequestsPerInstance {
		klog.Infof("  - max_prefill_requests_per_instance: %v -> %v", formatLimitValue(old.maxPrefillRequestsPerInstance), formatLimitValue(new.maxPrefillRequestsPerInstance))
	}
	if old.maxPrefillTokensPerInstance != new.maxPrefillTokensPerInstance {
		klog.Infof("  - max_prefill_tokens_per_instance: %v -> %v", formatLimitValue(old.maxPrefillTokensPerInstance), formatLimitValue(new.maxPrefillTokensPerInstance))
	}
	if old.maxDecodeRequestsPerInstance != new.maxDecodeRequestsPerInstance {
		klog.Infof("  - max_decode_requests_per_instance: %v -> %v", formatLimitValue(old.maxDecodeRequestsPerInstance), formatLimitValue(new.maxDecodeRequestsPerInstance))
	}
	if old.maxDecodeTokensPerInstance != new.maxDecodeTokensPerInstance {
		klog.Infof("  - max_decode_tokens_per_instance: %v -> %v", formatLimitValue(old.maxDecodeTokensPerInstance), formatLimitValue(new.maxDecodeTokensPerInstance))
	}
}

// formatLimitValue formats 0 as "unlimited" for better readability.
// By convention, 0 means "no limit" for all max_*_per_instance fields.
func formatLimitValue(val int64) string {
	if val == 0 {
		return "unlimited"
	}
	return fmt.Sprintf("%d", val)
}

func (rc *RateLimiterConfig) NeedRateLimitWait() bool {
	return rc.LimitAction() == LimitActionQueue
}

func (rc *RateLimiterConfig) Enabled() bool {
	return rc.getSnapshot().enabled
}

func (rc *RateLimiterConfig) LimitScope() LimitScope {
	return rc.getSnapshot().limitScope
}

func (rc *RateLimiterConfig) LimitAction() LimitAction {
	return rc.getSnapshot().limitAction
}

func (rc *RateLimiterConfig) MaxWaitTimeoutMs() int64 {
	return rc.getSnapshot().maxWaitTimeoutMs
}

func (rc *RateLimiterConfig) RetryIntervalMs() int64 {
	return rc.getSnapshot().retryIntervalMs
}

func (rc *RateLimiterConfig) MaxRequestsPerInstance() int64 {
	return rc.getSnapshot().maxRequestsPerInstance
}

func (rc *RateLimiterConfig) MaxTokensPerInstance() int64 {
	return rc.getSnapshot().maxTokensPerInstance
}

func (rc *RateLimiterConfig) MaxPrefillRequestsPerInstance() int64 {
	return rc.getSnapshot().maxPrefillRequestsPerInstance
}

func (rc *RateLimiterConfig) MaxPrefillTokensPerInstance() int64 {
	return rc.getSnapshot().maxPrefillTokensPerInstance
}

func (rc *RateLimiterConfig) MaxDecodeRequestsPerInstance() int64 {
	return rc.getSnapshot().maxDecodeRequestsPerInstance
}

func (rc *RateLimiterConfig) MaxDecodeTokensPerInstance() int64 {
	return rc.getSnapshot().maxDecodeTokensPerInstance
}
