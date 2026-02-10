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
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

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
				{Key: "extraKey", Type: StringType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

			// Test getting string value
			assert.Equal(t, "stringValue", pm.GetStringWithDefault("stringKey", "stringValue"))
			assert.Equal(t, "defaultValue", pm.GetStringWithDefault("nonExistentKey", "defaultValue"))
			assert.Equal(t, "defaultValue", pm.GetStringWithDefault("extraKey", "defaultValue"))

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
				{Key: "extraKey", Type: IntType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

			// Test getting integer value
			assert.Equal(t, 42, pm.GetIntWithDefault("intKey", 0))
			assert.Equal(t, 99, pm.GetIntWithDefault("nonExistentKey", 99))
			assert.Equal(t, 9, pm.GetIntWithDefault("extraKey", 9))

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
				{Key: "extraKey", Type: FloatType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

			// Test getting float value
			assert.Equal(t, 3.14, pm.GetFloatWithDefault("floatKey", 0.0))
			assert.Equal(t, 9.9, pm.GetFloatWithDefault("nonExistentKey", 9.9))
			assert.Equal(t, 19.9, pm.GetFloatWithDefault("extraKey", 19.9))

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
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

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

func TestPropertyManager_GetJSONWithDefault(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	// Define struct with rich field types
	type SubObject struct {
		SubKey1  string                 `json:"subKey1"`
		SubKey2  int                    `json:"subKey2"`
		SubKey3  bool                   `json:"subKey3"`
		SubKey4  float64                `json:"subKey4"`
		SubKey5  map[string]interface{} `json:"subKey5"`
		SubArray []interface{}          `json:"subArray"`
	}

	// Create test configuration with various JSON types
	// Using JSON string directly to ensure proper structure
	testConfigStr := `{
		"objectKey": {
			"subKey1": "subValue1",
			"subKey2": 42,
			"subKey3": true,
			"subKey4": 3.14,
			"subKey5": {
				"nestedKey1": "nestedValue1",
				"nestedKey2": 100
			},
			"subArray": ["item1", "item2", "item3"]
		},
		"arrayKey": [1, 2, 3],
		"stringKey": "stringValue",
		"intKey": 42,
		"floatKey": 3.14,
		"boolKey": true
	}`

	// Write JSON string to config file
	err := os.WriteFile(configPath, []byte(testConfigStr), 0644)
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
				{Key: "objectKey", Type: JSONType},
				{Key: "arrayKey", Type: JSONType},
				{Key: "stringKey", Type: JSONType},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

			// Test getting object value
			objVal := pm.GetJSONWithDefault("objectKey", "default")
			assert.NotEqual(t, "default", objVal) // Ensure we didn't get the default value
			assert.NotNil(t, objVal)
			objMap, ok := objVal.(map[string]interface{})
			assert.True(t, ok)
			assert.Equal(t, "subValue1", objMap["subKey1"])
			assert.Equal(t, float64(42), objMap["subKey2"])

			// Test unmarshaling JSON to struct with rich field types
			// Convert to bytes and unmarshal to struct
			objBytes, err := json.Marshal(objVal)
			require.NoError(t, err)

			var objStruct SubObject
			err = json.Unmarshal(objBytes, &objStruct)
			require.NoError(t, err)

			assert.Equal(t, "subValue1", objStruct.SubKey1)
			assert.Equal(t, 42, objStruct.SubKey2)
			assert.Equal(t, true, objStruct.SubKey3)
			assert.Equal(t, 3.14, objStruct.SubKey4)
			assert.Equal(t, "nestedValue1", objStruct.SubKey5["nestedKey1"])
			assert.Equal(t, float64(100), objStruct.SubKey5["nestedKey2"])
			assert.Len(t, objStruct.SubArray, 3)
			assert.Equal(t, "item1", objStruct.SubArray[0])
			assert.Equal(t, "item2", objStruct.SubArray[1])
			assert.Equal(t, "item3", objStruct.SubArray[2])

			// Test getting array value
			arrVal := pm.GetJSONWithDefault("arrayKey", "default")
			assert.NotEqual(t, "default", arrVal) // Ensure we didn't get the default value
			assert.NotNil(t, arrVal)
			arrSlice, ok := arrVal.([]interface{})
			assert.True(t, ok)
			assert.Equal(t, 3, len(arrSlice))
			assert.Equal(t, float64(1), arrSlice[0])
			assert.Equal(t, float64(2), arrSlice[1])
			assert.Equal(t, float64(3), arrSlice[2])

			// Test getting primitive values as JSON
			strVal := pm.GetJSONWithDefault("stringKey", "default")
			assert.NotEqual(t, "default", strVal) // Ensure we didn't get the default value
			assert.Equal(t, "stringValue", strVal)

			intVal := pm.GetJSONWithDefault("intKey", "default")
			assert.NotEqual(t, "default", intVal) // Ensure we didn't get the default value
			assert.Equal(t, float64(42), intVal)  // Note: JSON unmarshals integers as float64

			floatVal := pm.GetJSONWithDefault("floatKey", "default")
			assert.NotEqual(t, "default", floatVal) // Ensure we didn't get the default value
			assert.Equal(t, 3.14, floatVal)

			boolVal := pm.GetJSONWithDefault("boolKey", "default")
			assert.NotEqual(t, "default", boolVal) // Ensure we didn't get the default value
			assert.Equal(t, true, boolVal)

			// Test getting non-existent key
			defaultVal := pm.GetJSONWithDefault("nonExistentKey", "default")
			assert.Equal(t, "default", defaultVal)

			// Test nested key access
			nestedVal := pm.GetJSONWithDefault("objectKey.subKey1", "default")
			assert.NotEqual(t, "default", nestedVal) // Ensure we didn't get the default value
			assert.Equal(t, "subValue1", nestedVal)

			nestedVal2 := pm.GetJSONWithDefault("objectKey.subKey2", "default")
			assert.NotEqual(t, "default", nestedVal2) // Ensure we didn't get the default value
			assert.Equal(t, float64(42), nestedVal2)
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
	mirrorJQ, err := cfg.QueryToJq("llm_gateway.traffic_mirror")
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
			jq.Set("llm_gateway.traffic_mirror", mirror)
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
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

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

			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

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
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

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
			pm := newConfigManager([]string{configPath}, tc.prefetchKeys)

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
			pm := newConfigManager([]string{configPath1, configPath2}, tc.prefetchKeys)

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
