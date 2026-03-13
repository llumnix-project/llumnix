package property

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"os"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

type MirrorConfig struct {
	Enable        bool
	Target        string
	Ratio         float64
	Timeout       float64
	Authorization string
	EnableLog     bool
}

// configContext represents a single configuration file and its metadata.
type configContext struct {
	path            string
	lastContentHash string
}

// ConfigManager manages configuration configs with hot reload capability
type ConfigManager struct {
	configs map[string]*configContext

	mirrorConfig *MirrorConfig

	mutex         sync.RWMutex
	reloadChannel chan struct{}
}

// NewConfigManager creates a new config manager with multiple config paths and prefetch keys.
func NewConfigManager(configPaths map[string]string) *ConfigManager {
	configs := make(map[string]*configContext)
	for configName, configPath := range configPaths {
		configs[configName] = &configContext{
			path:            configPath,
			lastContentHash: "",
		}
	}

	cm := &ConfigManager{
		configs:       configs,
		mirrorConfig:  &MirrorConfig{},
		reloadChannel: make(chan struct{}, 1),
	}

	// Load initial configs
	if err := cm.loadConfigs(); err != nil {
		klog.Warningf("Failed to load initial configs: %v", err)
	}

	// Start watching for changes
	go cm.watchConfigs()

	return cm
}

func (cm *ConfigManager) GetMirrorConfig() MirrorConfig {
	cm.mutex.RLock()
	defer cm.mutex.RUnlock()
	return *cm.mirrorConfig
}

// ForceReload forces a reload of the config file
func (cm *ConfigManager) ForceReload() {
	select {
	case cm.reloadChannel <- struct{}{}:
	default:
	}
}

func (cm *ConfigManager) loadConfigs() error {
	for configName, configCtx := range cm.configs {
		if configName != "mirror" {
			klog.Warningf("Unknown config %s, skipping...", configName)
			continue
		}

		data, err := os.ReadFile(configCtx.path)
		if err != nil {
			klog.Warningf("Failed to read config file %s: %v", configCtx.path, err)
			return nil
		}

		// Parse JSON into a temporary config
		var tempConfig MirrorConfig
		if err = json.Unmarshal(data, &tempConfig); err != nil {
			klog.Warningf("Failed to parse JSON from config file %s: %v", configCtx.path, err)
			return err
		}

		// Update content hash
		hash := md5.Sum(data)

		// Atomically update all internal state under lock
		cm.mutex.Lock()
		cm.mirrorConfig = &tempConfig
		cm.configs[configName].lastContentHash = hex.EncodeToString(hash[:])
		cm.mutex.Unlock()
	}
	return nil
}

// watchConfigs watches the config files for changes
func (cm *ConfigManager) watchConfigs() {
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
func (cm *ConfigManager) checkForUpdates() {
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
		if err := cm.loadConfigs(); err == nil {
			klog.Infof("MirrorConfig files updated, reloaded configuration")
		}
	}
}
