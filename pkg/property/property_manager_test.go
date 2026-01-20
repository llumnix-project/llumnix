package property

import (
	"encoding/json"
	"llm-gateway/pkg/utils/jsquery"
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s.io/klog/v2"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Create config file for TEST
func createTestConfigFile(t *testing.T, configPath string, config map[string]interface{}) {
	data, err := json.Marshal(config)
	require.NoError(t, err)
	err = os.WriteFile(configPath, data, 0644)
	require.NoError(t, err)
}

func TestPropertyManager_Get(t *testing.T) {
	// Create temporary config file
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create test configuration
	testConfig := map[string]interface{}{
		"stringKey": "stringValue",
		"intKey":    42,
		"floatKey":  3.14,
		"boolKey":   true,
		"nilKey":    nil,
	}

	// Write to config file
	createTestConfigFile(t, configPath, testConfig)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "stringKey", Type: StringType},
				{Key: "intKey", Type: IntType},
				{Key: "floatKey", Type: FloatType},
				{Key: "boolKey", Type: BoolType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create PropertyManager
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Test getting existing keys
			assert.Equal(t, "stringValue", pm.Get("stringKey"))
			//assert.Equal(t, float64(42), pm.Get("intKey"))
			assert.Equal(t, 3.14, pm.Get("floatKey"))
			assert.Equal(t, true, pm.Get("boolKey"))
			assert.Equal(t, nil, pm.Get("nilKey"))

			// Test getting non-existent key
			assert.Nil(t, pm.Get("nonExistentKey"))
		})
	}
}

func TestPropertyManager_GetStringWithDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	testConfig := map[string]interface{}{
		"stringKey": "stringValue",
		"intKey":    42,
	}

	createTestConfigFile(t, configPath, testConfig)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "stringKey", Type: StringType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Test getting string value
			assert.Equal(t, "stringValue", pm.GetStringWithDefault("stringKey", ""))
			assert.Equal(t, "defaultValue", pm.GetStringWithDefault("nonExistentKey", "defaultValue"))

			// Test getting non-string value (should return default)
			assert.Equal(t, "default", pm.GetStringWithDefault("intKey", "default"))

			// Test getting non-existent key
			assert.Equal(t, "default", pm.GetStringWithDefault("nonExistentKey", "default"))
		})
	}
}

func TestPropertyManager_GetIntWithDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	testConfig := map[string]interface{}{
		"intKey":    42,
		"floatKey":  3.14,
		"stringKey": "stringValue",
	}

	createTestConfigFile(t, configPath, testConfig)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "intKey", Type: IntType},
				{Key: "floatKey", Type: IntType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Test getting integer value
			assert.Equal(t, 42, pm.GetIntWithDefault("intKey", 0))
			assert.Equal(t, 99, pm.GetIntWithDefault("nonExistentKey", 99))

			// Test getting integer from float value
			assert.Equal(t, 3, pm.GetIntWithDefault("floatKey", 0))

			// Test getting non-numeric value (should return default)
			assert.Equal(t, 99, pm.GetIntWithDefault("stringKey", 99))

			// Test getting non-existent key
			assert.Equal(t, 99, pm.GetIntWithDefault("nonExistentKey", 99))
		})
	}
}

func TestPropertyManager_GetFloatWithDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	testConfig := map[string]interface{}{
		"floatKey":  3.14,
		"intKey":    42,
		"stringKey": "stringValue",
	}

	createTestConfigFile(t, configPath, testConfig)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "floatKey", Type: FloatType},
				{Key: "intKey", Type: FloatType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Test getting float value
			assert.Equal(t, 3.14, pm.GetFloatWithDefault("floatKey", 0.0))
			assert.Equal(t, 9.9, pm.GetFloatWithDefault("nonExistentKey", 9.9))

			// Test getting float from integer value
			assert.Equal(t, 42.0, pm.GetFloatWithDefault("intKey", 0.0))

			// Test getting non-numeric value (should return default)
			assert.Equal(t, 9.9, pm.GetFloatWithDefault("stringKey", 9.9))

			// Test getting non-existent key
			assert.Equal(t, 9.9, pm.GetFloatWithDefault("nonExistentKey", 9.9))
		})
	}
}

func TestPropertyManager_GetBoolWithDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	testConfig := map[string]interface{}{
		"boolKey":   true,
		"stringKey": "stringValue",
	}

	createTestConfigFile(t, configPath, testConfig)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "boolKey", Type: BoolType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Test getting boolean value
			assert.Equal(t, true, pm.GetBoolWithDefault("boolKey", false))
			assert.Equal(t, true, pm.GetBoolWithDefault("nonExistentKey", true))

			// Test getting non-boolean value (should return default)
			assert.Equal(t, true, pm.GetBoolWithDefault("stringKey", true))

			// Test getting non-existent key
			assert.Equal(t, true, pm.GetBoolWithDefault("nonExistentKey", true))
		})
	}
}

func TestPropertyManager_NestedKeys(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Create test configuration with nested structure
	testConfig := `
{
	"llm_gateway": {
		"infer_backend": "vllm",
		"max_concurrent_requests": 1000,
		"metric_sync_duration": 2000,
		"traffic_mirror": {
			"enable": true,
			"ratio": 20,
			"target": "https://api.example.com",
			"token": "secret-token-123",
			"timeout": 5000,
			"enable_log": false
		}
	},
	"llm_scheduler": {
		"group": "models_gw",
		"name": "model_gw_llm_scheduler"
	}
}`
	jq, _ := jsquery.NewStringQuery("{}")
	cfg, _ := jsquery.NewStringQuery(testConfig)
	mirrorJQ, err := cfg.QueryToJq("llm-gateway.traffic_mirror")
	if err == nil {
		mirror := struct {
			Enable    bool   `json:"enable"`
			EnableLog bool   `json:"enable_log"`
			Timeout   int    `json:"timeout"`
			Ratio     int    `json:"ratio"`
			Target    string `json:"target"`
			Token     string `json:"token"`
		}{
			EnableLog: true,
		}
		if err = mirrorJQ.As(&mirror); err == nil {
			jq.Set("llm-gateway.traffic_mirror", mirror)
		}
	}
	klog.Infof(jq.String())

	// Write to config file
	err = os.WriteFile(configPath, []byte(testConfig), 0644)
	require.NoError(t, err)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "llm_gateway.infer_backend", Type: StringType},
				{Key: "llm_gateway.max_concurrent_requests", Type: IntType},
				{Key: "llm_gateway.traffic_mirror.enable", Type: BoolType},
				{Key: "llm_gateway.traffic_mirror.timeout", Type: IntType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create PropertyManager
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Retrieve nested values using dot notation
			mirrorTarget := pm.GetStringWithDefault("llm_gateway.traffic_mirror.target", "")
			mirrorRatio := pm.GetIntWithDefault("llm_gateway.traffic_mirror.ratio", 0)
			mirrorToken := pm.GetStringWithDefault("llm_gateway.traffic_mirror.token", "")
			mirrorTimeout := pm.GetIntWithDefault("llm_gateway.traffic_mirror.timeout", 0)

			// Verify retrieved values
			assert.Equal(t, "https://api.example.com", mirrorTarget)
			assert.Equal(t, 20, mirrorRatio)
			assert.Equal(t, "secret-token-123", mirrorToken)
			assert.Equal(t, 5000, mirrorTimeout)

			// Test non-existent nested keys
			assert.Equal(t, "default", pm.GetStringWithDefault("llm_gateway.traffic_mirror.nonExistent", "default"))
			assert.Equal(t, 99, pm.GetIntWithDefault("llm_gateway.traffic_mirror.ratio.nonExistent", 99))

			// 如果使用预取，验证预取值是否正确
			if len(tc.prefetchKeys) > 0 {
				inferBackend := pm.GetStringWithDefault("llm_gateway.infer_backend", "")
				maxConcurrentRequests := pm.GetIntWithDefault("llm_gateway.max_concurrent_requests", 0)
				mirrorEnable := pm.GetBoolWithDefault("llm_gateway.traffic_mirror.enable", false)
				mirrorTimeout := pm.GetIntWithDefault("llm_gateway.traffic_mirror.timeout", 0)

				// Verify retrieved values through prefetch cache
				assert.Equal(t, "vllm", inferBackend)
				assert.Equal(t, 1000, maxConcurrentRequests)
				assert.Equal(t, true, mirrorEnable)
				assert.Equal(t, 5000, mirrorTimeout)
			}
		})
	}
}

func TestPropertyManager_HotReload(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Initial configuration
	initialConfig := map[string]interface{}{
		"key": "initialValue",
	}
	// Update configuration
	updatedConfig := map[string]interface{}{
		"key": "updatedValue",
	}

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "key", Type: StringType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			createTestConfigFile(t, configPath, initialConfig)

			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Verify initial value
			assert.Equal(t, "initialValue", pm.GetStringWithDefault("key", ""))
			assert.Equal(t, "default", pm.GetStringWithDefault("nonExistent", "default"))

			createTestConfigFile(t, configPath, updatedConfig)

			// Wait for file modification time to update
			time.Sleep(1 * time.Second)

			// Force reload
			pm.ForceReload()

			// Wait for reload to complete
			time.Sleep(100 * time.Millisecond)

			// Verify updated value
			assert.Equal(t, "updatedValue", pm.GetStringWithDefault("key", ""))
			// Default value should still work
			assert.Equal(t, "default", pm.GetStringWithDefault("nonExistent", "default"))
		})
	}
}

func TestPropertyManager_NonExistentFile(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "nonexistent.json")

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "stringKey", Type: StringType},
				{Key: "intKey", Type: IntType},
				{Key: "floatKey", Type: FloatType},
				{Key: "boolKey", Type: BoolType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create PropertyManager - should work even if file doesn't exist
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Accessing any key should return default values
			assert.Equal(t, "defaultString", pm.GetStringWithDefault("anyKey", "defaultString"))
			assert.Equal(t, 99, pm.GetIntWithDefault("anyKey", 99))
			assert.Equal(t, 9.9, pm.GetFloatWithDefault("anyKey", 9.9))
			assert.Equal(t, true, pm.GetBoolWithDefault("anyKey", true))
			assert.Nil(t, pm.Get("anyKey"))
		})
	}
}

func TestPropertyManager_InvalidJSON(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "invalid.json")

	// Create invalid JSON file
	err := os.WriteFile(configPath, []byte("{ invalid json }"), 0644)
	require.NoError(t, err)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "stringKey", Type: StringType},
				{Key: "intKey", Type: IntType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create PropertyManager - should work even with invalid JSON
			pm := NewPropertyManager([]string{configPath}, tc.prefetchKeys)

			// Accessing any key should return default values
			assert.Equal(t, "defaultString", pm.GetStringWithDefault("anyKey", "defaultString"))
			assert.Equal(t, 99, pm.GetIntWithDefault("anyKey", 99))
			assert.Equal(t, 9.9, pm.GetFloatWithDefault("anyKey", 9.9))
			assert.Equal(t, true, pm.GetBoolWithDefault("anyKey", true))
			assert.Nil(t, pm.Get("anyKey"))
		})
	}
}

func TestPropertyManager_MultipleConfigPaths(t *testing.T) {
	tempDir := t.TempDir()

	// Create two config files
	configPath1 := filepath.Join(tempDir, "config1.json")
	configPath2 := filepath.Join(tempDir, "config2.json")

	// First config file
	testConfig1 := map[string]interface{}{
		"commonKey":  "value1",
		"uniqueKey1": "uniqueValue1",
		"nested": map[string]interface{}{
			"key1": "nestedValue1",
		},
	}

	// Second config file (should override values from first)
	testConfig2 := map[string]interface{}{
		"commonKey":  "value2",
		"uniqueKey2": "uniqueValue2",
		"nested": map[string]interface{}{
			"key2": "nestedValue2",
		},
	}

	createTestConfigFile(t, configPath1, testConfig1)
	createTestConfigFile(t, configPath2, testConfig2)

	// Test the cases without prefetch and with prefetch
	testCases := []struct {
		name         string
		prefetchKeys []PrefetchKey
	}{
		{
			name:         "WithoutPrefetch",
			prefetchKeys: []PrefetchKey{},
		},
		{
			name: "WithPrefetch",
			prefetchKeys: []PrefetchKey{
				{Key: "commonKey", Type: StringType},
				{Key: "uniqueKey1", Type: StringType},
				{Key: "uniqueKey2", Type: StringType},
				{Key: "nested.key1", Type: StringType},
				{Key: "nested.key2", Type: StringType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create PropertyManager with multiple config paths
			pm := NewPropertyManager([]string{configPath1, configPath2}, tc.prefetchKeys)

			// Test that second config overrides first config
			assert.Equal(t, "value2", pm.GetStringWithDefault("commonKey", ""))

			// Test values unique to each config
			assert.Equal(t, "uniqueValue1", pm.GetStringWithDefault("uniqueKey1", ""))
			assert.Equal(t, "uniqueValue2", pm.GetStringWithDefault("uniqueKey2", ""))

			// Test nested values
			assert.Equal(t, "nestedValue1", pm.GetStringWithDefault("nested.key1", ""))
			assert.Equal(t, "nestedValue2", pm.GetStringWithDefault("nested.key2", ""))

			// Test non-existent key
			assert.Equal(t, "default", pm.GetStringWithDefault("nonExistentKey", "default"))
		})
	}
}
