package ratelimiter

import (
	"fmt"
	"llm-gateway/pkg/property"
	"sync/atomic"
	"time"

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
	GetConfigVersion() uint64
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

	configVersion   uint64       // Atomic counter for config version
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
	// Initialize local snapshot from dyConfigMgr and start periodic refresh
	rc.currentSnapshot.Store(rc.readFromDyConfig())
	rc.startConfigMonitor()
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
// NOTE: Limit fields use 0 to mean "unlimited"; callers must treat 0 as no-limit
// rather than converting to math.MaxInt64 (which overflows on multiplication).
func (rc *RateLimiterConfig) readFromDyConfig() *configSnapshot {
	return &configSnapshot{
		enabled:                       rc.dyConfigMgr.GetBoolWithDefault("llm_gateway.rate_limit.enable", false),
		limitScope:                    LimitScope(rc.dyConfigMgr.GetStringWithDefault("llm_gateway.rate_limit.scope", LimitScopeInstance.String())),
		limitAction:                   LimitAction(rc.dyConfigMgr.GetStringWithDefault("llm_gateway.rate_limit.action", LimitActionReject.String())),
		maxWaitTimeoutMs:              int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_ratelimit_wait_timeout", 5000)),
		retryIntervalMs:               int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.ratelimit_retry_interval", 100)),
		maxRequestsPerInstance:        int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_requests_per_instance", 0)),
		maxTokensPerInstance:          int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_tokens_per_instance", 0)),
		maxPrefillRequestsPerInstance: int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_prefill_requests_per_instance", 0)),
		maxPrefillTokensPerInstance:   int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_prefill_tokens_per_instance", 0)),
		maxDecodeRequestsPerInstance:  int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_decode_requests_per_instance", 0)),
		maxDecodeTokensPerInstance:    int64(rc.dyConfigMgr.GetIntWithDefault("llm_gateway.rate_limit.max_decode_tokens_per_instance", 0)),
	}
}

// getSnapshot returns the current config snapshot atomically.
func (rc *RateLimiterConfig) getSnapshot() *configSnapshot {
	return rc.currentSnapshot.Load().(*configSnapshot)
}

// startConfigMonitor starts a goroutine to periodically check config changes
func (rc *RateLimiterConfig) startConfigMonitor() {
	go func() {
		// Poll every 10 seconds to check for config changes
		// This aligns with DynamicConfigManager's watch interval
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			rc.checkAndLogConfigChanges()
		}
	}()
}

// checkAndLogConfigChanges reads fresh values from dyConfigMgr, compares with
// the current local snapshot, and atomically swaps in the new snapshot if changed.
func (rc *RateLimiterConfig) checkAndLogConfigChanges() {
	newSnapshot := rc.readFromDyConfig()
	oldSnapshot := rc.getSnapshot()

	if !rc.hasConfigChanged(oldSnapshot, newSnapshot) {
		return
	}

	// Increment version atomically
	atomic.AddUint64(&rc.configVersion, 1)

	// Log changes
	rc.logConfigChanges(oldSnapshot, newSnapshot)

	// Atomically swap in new snapshot so all getters see the updated values
	rc.currentSnapshot.Store(newSnapshot)
}

// hasConfigChanged checks if any config value has changed
func (rc *RateLimiterConfig) hasConfigChanged(old, new *configSnapshot) bool {
	return old.enabled != new.enabled ||
		old.limitScope != new.limitScope ||
		old.limitAction != new.limitAction ||
		old.maxWaitTimeoutMs != new.maxWaitTimeoutMs ||
		old.retryIntervalMs != new.retryIntervalMs ||
		old.maxRequestsPerInstance != new.maxRequestsPerInstance ||
		old.maxTokensPerInstance != new.maxTokensPerInstance ||
		old.maxPrefillRequestsPerInstance != new.maxPrefillRequestsPerInstance ||
		old.maxPrefillTokensPerInstance != new.maxPrefillTokensPerInstance ||
		old.maxDecodeRequestsPerInstance != new.maxDecodeRequestsPerInstance ||
		old.maxDecodeTokensPerInstance != new.maxDecodeTokensPerInstance
}

// logConfigChanges logs the specific config changes
func (rc *RateLimiterConfig) logConfigChanges(old, new *configSnapshot) {
	klog.Infof("[RateLimiter] Config change detected:")

	if old.enabled != new.enabled {
		klog.Infof("  - enabled: %v -> %v", old.enabled, new.enabled)
	}
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

// GetConfigVersion returns the current config version (incremented on each change)
func (rc *RateLimiterConfig) GetConfigVersion() uint64 {
	return atomic.LoadUint64(&rc.configVersion)
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
