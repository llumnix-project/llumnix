package property

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeMirrorJSON(t *testing.T, path string, cfg MirrorConfig) {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0644))
}

func newTestConfigManager(t *testing.T, cfg MirrorConfig) (*ConfigManager, string) {
	t.Helper()
	f, err := os.CreateTemp("", "mirror-prop-test-*.json")
	require.NoError(t, err)
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })
	require.NoError(t, f.Close())

	writeMirrorJSON(t, path, cfg)
	cm := NewConfigManager(map[string]string{"mirror": path})
	return cm, path
}

// --- Initial load tests ---

func TestConfigManager_InitialLoad(t *testing.T) {
	cm, _ := newTestConfigManager(t, MirrorConfig{
		Enable:        true,
		Target:        "http://example.com",
		Ratio:         42.5,
		Timeout:       3000,
		Authorization: "Bearer tok",
		EnableLog:     true,
	})

	cfg := cm.GetMirrorConfig()
	assert.True(t, cfg.Enable)
	assert.Equal(t, "http://example.com", cfg.Target)
	assert.Equal(t, 42.5, cfg.Ratio)
	assert.Equal(t, 3000.0, cfg.Timeout)
	assert.Equal(t, "Bearer tok", cfg.Authorization)
	assert.True(t, cfg.EnableLog)
}

func TestConfigManager_InitialLoad_DefaultsWhenFileMissing(t *testing.T) {
	cm := NewConfigManager(map[string]string{"mirror": "/nonexistent/path/mirror.json"})
	cfg := cm.GetMirrorConfig()

	assert.False(t, cfg.Enable)
	assert.Empty(t, cfg.Target)
	assert.Equal(t, 0.0, cfg.Ratio)
}

func TestConfigManager_InitialLoad_InvalidJSON(t *testing.T) {
	f, err := os.CreateTemp("", "mirror-bad-*.json")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	_, err = f.WriteString(`{invalid json}`)
	require.NoError(t, err)
	require.NoError(t, f.Close())

	cm := NewConfigManager(map[string]string{"mirror": f.Name()})
	cfg := cm.GetMirrorConfig()
	assert.False(t, cfg.Enable, "should retain defaults on parse error")
}

// --- ForceReload tests ---

func TestConfigManager_ForceReload(t *testing.T) {
	cm, path := newTestConfigManager(t, MirrorConfig{
		Enable: false,
		Target: "http://old.com",
		Ratio:  10,
	})

	cfg := cm.GetMirrorConfig()
	assert.False(t, cfg.Enable)
	assert.Equal(t, "http://old.com", cfg.Target)

	writeMirrorJSON(t, path, MirrorConfig{
		Enable: true,
		Target: "http://new.com",
		Ratio:  80,
	})

	cm.ForceReload()
	time.Sleep(200 * time.Millisecond)

	cfg = cm.GetMirrorConfig()
	assert.True(t, cfg.Enable)
	assert.Equal(t, "http://new.com", cfg.Target)
	assert.Equal(t, 80.0, cfg.Ratio)
}

func TestConfigManager_ForceReload_MultipleUpdates(t *testing.T) {
	cm, path := newTestConfigManager(t, MirrorConfig{Ratio: 10})

	for _, ratio := range []float64{20, 50, 90} {
		writeMirrorJSON(t, path, MirrorConfig{Ratio: ratio})
		cm.ForceReload()
		time.Sleep(200 * time.Millisecond)
		assert.Equal(t, ratio, cm.GetMirrorConfig().Ratio)
	}
}

// --- checkForUpdates (hash-based change detection) tests ---

func TestConfigManager_CheckForUpdates_DetectsChange(t *testing.T) {
	cm, path := newTestConfigManager(t, MirrorConfig{Enable: false})

	assert.False(t, cm.GetMirrorConfig().Enable)

	writeMirrorJSON(t, path, MirrorConfig{Enable: true, Target: "http://changed.com"})
	cm.checkForUpdates()

	cfg := cm.GetMirrorConfig()
	assert.True(t, cfg.Enable)
	assert.Equal(t, "http://changed.com", cfg.Target)
}

func TestConfigManager_CheckForUpdates_NoChangeNoReload(t *testing.T) {
	original := MirrorConfig{Enable: true, Target: "http://stable.com", Ratio: 50}
	cm, _ := newTestConfigManager(t, original)

	cm.checkForUpdates()

	cfg := cm.GetMirrorConfig()
	assert.Equal(t, original.Target, cfg.Target)
	assert.Equal(t, original.Ratio, cfg.Ratio)
}

func TestConfigManager_CheckForUpdates_FileDeleted(t *testing.T) {
	cm, path := newTestConfigManager(t, MirrorConfig{Enable: true, Target: "http://example.com"})

	os.Remove(path)
	cm.checkForUpdates()

	cfg := cm.GetMirrorConfig()
	assert.True(t, cfg.Enable, "should retain last known config when file is deleted")
	assert.Equal(t, "http://example.com", cfg.Target)
}

func TestConfigManager_CheckForUpdates_FileCorrupted(t *testing.T) {
	cm, path := newTestConfigManager(t, MirrorConfig{Enable: true, Target: "http://good.com"})

	require.NoError(t, os.WriteFile(path, []byte(`not json at all`), 0644))
	cm.checkForUpdates()

	cfg := cm.GetMirrorConfig()
	assert.True(t, cfg.Enable, "should retain last known config on parse error")
	assert.Equal(t, "http://good.com", cfg.Target)
}

// --- GetMirrorConfig concurrency test ---

func TestConfigManager_ConcurrentAccess(t *testing.T) {
	cm, path := newTestConfigManager(t, MirrorConfig{Enable: true, Ratio: 10})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			writeMirrorJSON(t, path, MirrorConfig{Enable: true, Ratio: float64(i)})
			cm.ForceReload()
			time.Sleep(5 * time.Millisecond)
		}
	}()

	for i := 0; i < 200; i++ {
		cfg := cm.GetMirrorConfig()
		assert.True(t, cfg.Enable)
		assert.GreaterOrEqual(t, cfg.Ratio, 0.0)
		time.Sleep(2 * time.Millisecond)
	}

	<-done
}

// --- Unknown config name ---

func TestConfigManager_UnknownConfigName(t *testing.T) {
	f, err := os.CreateTemp("", "unknown-*.json")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	require.NoError(t, f.Close())

	cm := NewConfigManager(map[string]string{"unknown_name": f.Name()})
	cfg := cm.GetMirrorConfig()
	assert.False(t, cfg.Enable, "unknown config name should not affect mirror config")
}
