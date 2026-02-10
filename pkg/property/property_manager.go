package property

import (
	"crypto/md5"
	"encoding/hex"
	"os"
	"reflect"
	"strings"
	"sync"
	"time"

	"k8s.io/klog/v2"

	"llm-gateway/pkg/utils/jsquery"
)

const propertiesFile = "/etc/eas/override.properties"

var registerPrefetchKeys = []PrefetchKey{
	// traffic mirror
	{Key: "llm_gateway.traffic_mirror.enable", Type: BoolType},
	{Key: "llm_gateway.traffic_mirror.target", Type: StringType},
	{Key: "llm_gateway.traffic_mirror.ratio", Type: FloatType},
	{Key: "llm_gateway.traffic_mirror.token", Type: StringType},
	{Key: "llm_gateway.traffic_mirror.timeout", Type: FloatType},
	{Key: "llm_gateway.traffic_mirror.enable_log", Type: BoolType},
	// rate limit
	{Key: "llm_gateway.rate_limit.enable", Type: BoolType},
	{Key: "llm_gateway.rate_limit.action", Type: StringType},
	{Key: "llm_gateway.rate_limit.scope", Type: StringType},
	{Key: "llm_gateway.rate_limit.max_requests_per_instance", Type: IntType},
	{Key: "llm_gateway.rate_limit.max_tokens_per_instance", Type: IntType},
	{Key: "llm_gateway.rate_limit.max_prefill_requests_per_instance", Type: IntType},
	{Key: "llm_gateway.rate_limit.max_prefill_tokens_per_instance", Type: IntType},
	{Key: "llm_gateway.rate_limit.max_decode_requests_per_instance", Type: IntType},
	{Key: "llm_gateway.rate_limit.max_decode_tokens_per_instance", Type: IntType},
	{Key: "llm_gateway.rate_limit.max_ratelimit_wait_timeout", Type: IntType},
	{Key: "llm_gateway.rate_limit.ratelimit_retry_interval", Type: IntType},
	// service-router
	{Key: "llm_gateway.route_policy", Type: StringType},
	{Key: "llm_gateway.route_config", Type: StringType},
}

// ConfigPath represents a single configuration file and its metadata.
type ConfigPath struct {
	path            string
	lastContentHash string
}

// PrefetchKeyType defines the expected type for a prefetched key.
type PrefetchKeyType int

const (
	StringType PrefetchKeyType = iota
	IntType
	FloatType
	BoolType
	JSONType
)

// PrefetchKey defines a key that should be parsed and cached on load.
type PrefetchKey struct {
	Key  string
	Type PrefetchKeyType
}

// ConfigChangeListener receives notifications when configuration changes.
// Implementations should handle changes quickly to avoid blocking other listeners.
type ConfigChangeListener interface {
	// OnConfigChanged is called when any of the watched keys change.
	// keys: list of changed configuration keys
	OnConfigChanged(keys []string)
}

// listenerRegistration holds a listener and its key prefix filter.
type listenerRegistration struct {
	listener  ConfigChangeListener
	keyPrefix string // empty means listen to all keys
}

// DynamicConfigManager manages configuration configs with hot reload capability
type DynamicConfigManager struct {
	// configs holds the list of configuration files, later ones have higher priority.
	configs      []ConfigPath
	prefetchKeys map[string]PrefetchKey // Map of keys to prefetch and cache.

	// Cached values for prefetched keys, protected by mutex.
	stringValues map[string]string
	intValues    map[string]int
	floatValues  map[string]float64
	boolValues   map[string]bool
	jsonValues   map[string]interface{}

	// Original JSON Query for non-prefetch keys.
	jq            *jsquery.JQ
	mutex         sync.RWMutex
	reloadChannel chan struct{}

	// Change listeners with optional key prefix filters.
	listeners      []listenerRegistration
	listenersMutex sync.RWMutex
}

var (
	once          sync.Once
	configManager *DynamicConfigManager
)

func GetDynamicConfigManager() *DynamicConfigManager {
	once.Do(func() {
		// Initialize with default config paths and no prefetch keys.
		configManager = newConfigManager([]string{propertiesFile}, registerPrefetchKeys)
	})
	return configManager
}

// NewConfigManager creates a new config manager with multiple config paths and prefetch keys.
func newConfigManager(configPaths []string, prefetchKeys []PrefetchKey) *DynamicConfigManager {
	configs := make([]ConfigPath, len(configPaths))
	for i, path := range configPaths {
		configs[i] = ConfigPath{path: path}
	}

	prefetchKeyMap := make(map[string]PrefetchKey)
	for _, pk := range prefetchKeys {
		prefetchKeyMap[pk.Key] = pk
	}

	cm := &DynamicConfigManager{
		configs:       configs,
		prefetchKeys:  prefetchKeyMap,
		stringValues:  make(map[string]string),
		intValues:     make(map[string]int),
		floatValues:   make(map[string]float64),
		boolValues:    make(map[string]bool),
		jsonValues:    make(map[string]interface{}),
		reloadChannel: make(chan struct{}, 1),
	}

	// Load initial configs (no notification on initial load)
	cm.loadConfigs()

	// Start watching for changes
	go cm.watchConfigs()

	return cm
}

// Get returns the configuration value for the specified key.
// It first checks prefetched keys, then falls back to dynamic lookup.
func (cm *DynamicConfigManager) Get(key string) interface{} {
	// Check if key is prefetched
	if pk, exists := cm.prefetchKeys[key]; exists {
		cm.mutex.RLock()

		// Return from appropriate cache based on declared type
		switch pk.Type {
		case StringType:
			if val, exists := cm.stringValues[key]; exists {
				cm.mutex.RUnlock()
				return val
			}
		case IntType:
			if val, exists := cm.intValues[key]; exists {
				cm.mutex.RUnlock()
				return val
			}
		case FloatType:
			if val, exists := cm.floatValues[key]; exists {
				cm.mutex.RUnlock()
				return val
			}
		case BoolType:
			if val, exists := cm.boolValues[key]; exists {
				cm.mutex.RUnlock()
				return val
			}
		case JSONType:
			if val, exists := cm.jsonValues[key]; exists {
				cm.mutex.RUnlock()
				return val
			}
		}

		cm.mutex.RUnlock()
		return nil
	}

	// Fallback to original dynamic lookup via JQ
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	if cm.jq == nil {
		return nil
	}

	val, err := cm.jq.Query(key)
	if err != nil {
		return nil
	}
	return val
}

// GetStringWithDefault returns a string config value by key, utilizing prefetch cache when available.
// If the key is not found or is of the wrong type, it returns the provided default value.
func (cm *DynamicConfigManager) GetStringWithDefault(key, defaultValue string) string {
	if val := cm.Get(key); val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return defaultValue
}

// GetIntWithDefault returns an integer config value by key, utilizing prefetch cache when available.
// If the key is not found or is of the wrong type, it returns the provided default value.
func (cm *DynamicConfigManager) GetIntWithDefault(key string, defaultValue int) int {
	if val := cm.Get(key); val != nil {
		if i, ok := val.(int); ok {
			return i
		}
		if f, ok := val.(float64); ok {
			return int(f)
		}
	}
	return defaultValue
}

// GetFloatWithDefault returns a float config value by key, utilizing prefetch cache when available.
// If the key is not found or is of the wrong type, it returns the provided default value.
func (cm *DynamicConfigManager) GetFloatWithDefault(key string, defaultValue float64) float64 {
	if val := cm.Get(key); val != nil {
		if f, ok := val.(float64); ok {
			return f
		}
		if i, ok := val.(int); ok {
			return float64(i)
		}
	}
	return defaultValue
}

// GetBoolWithDefault returns a boolean config value by key, utilizing prefetch cache when available.
// If the key is not found or is of the wrong type, it returns the provided default value.
func (cm *DynamicConfigManager) GetBoolWithDefault(key string, defaultValue bool) bool {
	if val := cm.Get(key); val != nil {
		if b, ok := val.(bool); ok {
			return b
		}
	}
	return defaultValue
}

// GetJSONWithDefault returns a JSON config value by key, utilizing prefetch cache when available.
// If the key is not found, it returns the provided default value.
func (cm *DynamicConfigManager) GetJSONWithDefault(key string, defaultValue interface{}) interface{} {
	if val := cm.Get(key); val != nil {
		return val
	}
	return defaultValue
}

// RegisterListener registers a listener to be notified of config changes.
// keyPrefix: if non-empty, only changes to keys with this prefix will trigger notifications.
// Example: RegisterListener(listener, "llm_gateway.rate_limit.") will only notify for rate limit config changes.
//
// IMPORTANT: The listener will be immediately notified with current matching keys to ensure
// it can initialize based on existing configuration.
func (cm *DynamicConfigManager) RegisterListener(listener ConfigChangeListener, keyPrefix string) {
	cm.listenersMutex.Lock()
	defer cm.listenersMutex.Unlock()
	cm.listeners = append(cm.listeners, listenerRegistration{
		listener:  listener,
		keyPrefix: keyPrefix,
	})
	klog.V(3).Infof("Registered config listener with key prefix: %q", keyPrefix)

	// Immediately notify the listener with current matching keys for initialization
	cm.mutex.RLock()
	matchedKeys := cm.collectMatchingKeys(keyPrefix)
	cm.mutex.RUnlock()

	if len(matchedKeys) > 0 {
		klog.V(3).Infof("Notifying newly registered listener with %d initial keys", len(matchedKeys))
		listener.OnConfigChanged(matchedKeys)
	}
}

// collectMatchingKeys collects all config keys matching the given prefix.
// MUST be called with cm.mutex held for reading.
func (cm *DynamicConfigManager) collectMatchingKeys(keyPrefix string) []string {
	matchedKeys := make([]string, 0)

	// Collect from all cache types
	for key := range cm.stringValues {
		if keyPrefix == "" || strings.HasPrefix(key, keyPrefix) {
			matchedKeys = append(matchedKeys, key)
		}
	}
	for key := range cm.intValues {
		if keyPrefix == "" || strings.HasPrefix(key, keyPrefix) {
			if !contains(matchedKeys, key) {
				matchedKeys = append(matchedKeys, key)
			}
		}
	}
	for key := range cm.floatValues {
		if keyPrefix == "" || strings.HasPrefix(key, keyPrefix) {
			if !contains(matchedKeys, key) {
				matchedKeys = append(matchedKeys, key)
			}
		}
	}
	for key := range cm.boolValues {
		if keyPrefix == "" || strings.HasPrefix(key, keyPrefix) {
			if !contains(matchedKeys, key) {
				matchedKeys = append(matchedKeys, key)
			}
		}
	}
	for key := range cm.jsonValues {
		if keyPrefix == "" || strings.HasPrefix(key, keyPrefix) {
			if !contains(matchedKeys, key) {
				matchedKeys = append(matchedKeys, key)
			}
		}
	}

	return matchedKeys
}

// contains checks if a string slice contains a given string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// notifyListeners notifies all registered listeners about config changes.
// Uses synchronous notification to ensure reliable, ordered delivery.
func (cm *DynamicConfigManager) notifyListeners(changedKeys []string) {
	if len(changedKeys) == 0 {
		return
	}

	cm.listenersMutex.RLock()
	defer cm.listenersMutex.RUnlock()

	for _, reg := range cm.listeners {
		// Filter keys by prefix if specified, no filter, send all changes
		if reg.keyPrefix == "" {
			reg.listener.OnConfigChanged(changedKeys)
			continue
		}

		// Filter to only matching keys
		filteredKeys := make([]string, 0, len(changedKeys))
		for _, key := range changedKeys {
			if strings.HasPrefix(key, reg.keyPrefix) {
				filteredKeys = append(filteredKeys, key)
			}
		}

		if len(filteredKeys) > 0 {
			reg.listener.OnConfigChanged(filteredKeys)
		}
	}
}

// detectChangedKeys compares old and new values to determine which keys changed.
func (cm *DynamicConfigManager) detectChangedKeys(
	oldStrings map[string]string,
	oldInts map[string]int,
	oldFloats map[string]float64,
	oldBools map[string]bool,
	oldJsons map[string]interface{},
	newStrings map[string]string,
	newInts map[string]int,
	newFloats map[string]float64,
	newBools map[string]bool,
	newJsons map[string]interface{},
) []string {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()

	changedKeys := make([]string, 0)

	// Check string changes
	for key, newVal := range newStrings {
		if oldVal, exists := oldStrings[key]; !exists || oldVal != newVal {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range oldStrings {
		if _, exists := newStrings[key]; !exists {
			changedKeys = append(changedKeys, key)
		}
	}

	// Check int changes
	for key, newVal := range newInts {
		if oldVal, exists := oldInts[key]; !exists || oldVal != newVal {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range oldInts {
		if _, exists := newInts[key]; !exists {
			changedKeys = append(changedKeys, key)
		}
	}

	// Check float changes
	for key, newVal := range newFloats {
		if oldVal, exists := oldFloats[key]; !exists || oldVal != newVal {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range oldFloats {
		if _, exists := newFloats[key]; !exists {
			changedKeys = append(changedKeys, key)
		}
	}

	// Check bool changes
	for key, newVal := range newBools {
		if oldVal, exists := oldBools[key]; !exists || oldVal != newVal {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range oldBools {
		if _, exists := newBools[key]; !exists {
			changedKeys = append(changedKeys, key)
		}
	}

	// Check JSON changes
	// Use reflect.DeepEqual for deep comparison of complex structures
	for key, newVal := range newJsons {
		oldVal, exists := oldJsons[key]
		if !exists {
			changedKeys = append(changedKeys, key)
			continue
		}
		// Deep comparison handles maps, slices, and nested structures correctly
		if !reflect.DeepEqual(oldVal, newVal) {
			changedKeys = append(changedKeys, key)
		}
	}
	for key := range oldJsons {
		if _, exists := newJsons[key]; !exists {
			changedKeys = append(changedKeys, key)
		}
	}

	return changedKeys
}

// loadConfigs loads configs from all config files, merging them with later files taking precedence.
// Returns the list of changed keys.
func (cm *DynamicConfigManager) loadConfigs() ([]string, error) {
	// Create a merged JSON Query to hold combined config data.
	finalJQ := jsquery.NewQuery(nil)

	// Temporary maps to store parsed values before updating main maps.
	newStringValues := make(map[string]string)
	newIntValues := make(map[string]int)
	newFloatValues := make(map[string]float64)
	newBoolValues := make(map[string]bool)
	newJsonValues := make(map[string]interface{})

	for i := range cm.configs {
		data, err := os.ReadFile(cm.configs[i].path)
		if err != nil {
			if !os.IsNotExist(err) {
				klog.Warningf("Failed to read config file %s: %v", cm.configs[i].path, err)
			}
			continue
		}
		tempJQ, err := jsquery.NewBytesQuery(data)
		if err != nil {
			klog.Warningf("Failed to create JQ query from config file %s: %v", cm.configs[i].path, err)
			continue
		}
		tempJQ.MergeWithoutOverwrite(finalJQ)
		finalJQ = tempJQ

		// Update content hash
		hash := md5.Sum(data)
		cm.configs[i].lastContentHash = hex.EncodeToString(hash[:])
	}

	// Populate prefetch caches using the merged JQ
	for _, pk := range cm.prefetchKeys {
		rawVal, _ := finalJQ.Query(pk.Key)

		switch pk.Type {
		case StringType:
			if str, ok := rawVal.(string); ok {
				newStringValues[pk.Key] = str
			}
		case IntType:
			if i, ok := rawVal.(int); ok {
				newIntValues[pk.Key] = i
			} else if f, ok := rawVal.(float64); ok {
				newIntValues[pk.Key] = int(f)
			}
		case FloatType:
			if f, ok := rawVal.(float64); ok {
				newFloatValues[pk.Key] = f
			} else if i, ok := rawVal.(int); ok {
				newFloatValues[pk.Key] = float64(i)
			}
		case BoolType:
			if b, ok := rawVal.(bool); ok {
				newBoolValues[pk.Key] = b
			}
		case JSONType:
			// For JSON type, store the raw value regardless of its actual type
			newJsonValues[pk.Key] = rawVal
		}
	}

	// Detect changes before updating
	changedKeys := cm.detectChangedKeys(
		cm.stringValues, cm.intValues, cm.floatValues, cm.boolValues, cm.jsonValues,
		newStringValues, newIntValues, newFloatValues, newBoolValues, newJsonValues,
	)

	// Atomically update all internal state under lock
	cm.mutex.Lock()
	cm.jq = finalJQ
	cm.stringValues = newStringValues
	cm.intValues = newIntValues
	cm.floatValues = newFloatValues
	cm.boolValues = newBoolValues
	cm.jsonValues = newJsonValues
	cm.mutex.Unlock()

	return changedKeys, nil
}

// watchConfigs watches the config files for changes
func (cm *DynamicConfigManager) watchConfigs() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cm.checkForUpdates()
		case <-cm.reloadChannel:
			cm.checkForUpdates()
		}
	}
}

// checkForUpdates checks if any of the config files have been updated.
func (cm *DynamicConfigManager) checkForUpdates() {
	needsReload := false

	for i := range cm.configs {
		data, err := os.ReadFile(cm.configs[i].path)
		if err != nil {
			if !os.IsNotExist(err) {
				klog.Warningf("Failed to read config file %s: %v", cm.configs[i].path, err)
			}
			continue
		}

		hash := md5.Sum(data)
		currentContentHash := hex.EncodeToString(hash[:])

		cm.mutex.RLock()
		lastContentHash := cm.configs[i].lastContentHash
		cm.mutex.RUnlock()

		if currentContentHash != lastContentHash {
			needsReload = true
			break // No need to check further once we know one changed
		}
	}

	if needsReload {
		if changedKeys, err := cm.loadConfigs(); err == nil {
			klog.Infof("Config files updated, reloaded configuration")
			// Notify listeners after successful reload
			cm.notifyListeners(changedKeys)
		}
	}
}

// ForceReload forces a reload of the config file
func (cm *DynamicConfigManager) ForceReload() {
	select {
	case cm.reloadChannel <- struct{}{}:
	default:
	}
}
