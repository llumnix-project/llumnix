package res

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"llm-gateway/cmd/llm-gateway/app/options"
	"llm-gateway/pkg/metrics"
	"llm-gateway/pkg/resolver"
	"llm-gateway/pkg/types"

	"k8s.io/klog/v2"
)

const (
	// DefaultCollectInterval is the default interval for collecting metrics.
	DefaultCollectInterval = 10 * time.Second
	// MaxConsecutiveFailures is the maximum number of consecutive failures before skipping an instance.
	MaxConsecutiveFailures = 3
)

// RemoteEngineStates manages remote engine state collection and storage.
// It periodically collects metrics from multiple instances and stores them.
type RemoteEngineStates struct {
	collector *Collector
	interval  time.Duration

	// metrics stores the latest metrics for each instance
	metrics map[string]*EngineState
	mu      sync.RWMutex

	// instances stores the instance configurations
	instances map[string]*InstanceConfig

	// stopCh is used to stop the collection loop
	stopOnce sync.Once
	stopCh   chan struct{}
}

// NewRemoteEngineStates creates a new metric manager with default settings.
func NewRemoteEngineStates(config *options.Config) *RemoteEngineStates {
	return NewRemoteEngineStatesWithConfig(DefaultCollectInterval, config)
}

// NewRemoteEngineStatesWithConfig creates a new metric manager with custom collect interval.
func NewRemoteEngineStatesWithConfig(interval time.Duration, config *options.Config) *RemoteEngineStates {
	serviceToken := ""
	if config != nil {
		serviceToken = config.ServiceToken
	}
	return &RemoteEngineStates{
		collector: NewCollectorWithConfig(DefaultTimeout, serviceToken),
		interval:  interval,
		metrics:   make(map[string]*EngineState),
		instances: make(map[string]*InstanceConfig),
		stopCh:    make(chan struct{}),
	}
}

// addInstance adds an instance to the manager for metric collection.
// It automatically detects the backend type on first addition.
func (m *RemoteEngineStates) addInstance(worker *types.LLMWorker) error {
	instanceID := worker.Id()
	endpoint := worker.Endpoint.String()

	m.mu.Lock()
	defer m.mu.Unlock()

	// Detect backend type
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	backend, _, err := m.collector.DetectBackendType(ctx, endpoint)
	if err != nil {
		klog.Warningf("Failed to detect backend type for instance %s: %v", instanceID, err)
		// Still add the instance, will retry detection during collection
		backend = ""
	}

	m.instances[instanceID] = &InstanceConfig{
		InstanceID: instanceID,
		Endpoint:   endpoint,
		Backend:    backend,
		Model:      worker.Model,
	}

	if backend != "" {
		klog.Infof("Added instance %s (endpoint: %s, backend: %s) to metric manager",
			instanceID, endpoint, backend)
	} else {
		klog.Infof("Added instance %s (endpoint: %s, backend: auto-detect) to metric manager",
			instanceID, endpoint)
	}

	return nil
}

// removeInstance removes an instance from the manager.
func (m *RemoteEngineStates) removeInstance(instanceID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	delete(m.instances, instanceID)
	delete(m.metrics, instanceID)
	m.collector.RemoveInstance(instanceID)

	klog.Infof("Removed instance %s from metric manager", instanceID)
}

// GetMetric returns the latest metric for an instance.
// Returns nil if the instance doesn't exist or no metrics have been collected yet.
func (m *RemoteEngineStates) GetMetric(instanceID string) *EngineState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.metrics[instanceID]
}

// GetAllMetrics returns all latest metrics.
func (m *RemoteEngineStates) GetAllMetrics() map[string]*EngineState {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid race conditions
	result := make(map[string]*EngineState, len(m.metrics))
	for k, v := range m.metrics {
		result[k] = v
	}
	return result
}

// Start starts the periodic metric collection loop and metrics submission.
// It is non-blocking and spawns two goroutines: one for collection and one for metric submission.
func (m *RemoteEngineStates) Start(llmResolver resolver.LLMResolver) error {
	ctx := context.Background()
	// Start watching resolver events
	eventChan, err := llmResolver.Watch(ctx)
	if err != nil {
		return fmt.Errorf("failed to watch resolver: %w", err)
	}

	klog.Infof("Remote Engine States started with interval: %v", m.interval)

	// Start collection loop in a goroutine
	go m.run(ctx, eventChan)

	// Start metrics submission in a goroutine
	go m.SubmitMetric()

	return nil
}

// run is the main loop for collecting metrics and handling resolver events.
func (m *RemoteEngineStates) run(ctx context.Context, eventChan <-chan resolver.WorkerEvent) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			klog.Info("Remote Engine States stopped")
			return

		case <-ticker.C:
			m.collectAllMetrics(ctx)

		case event, ok := <-eventChan:
			if !ok {
				klog.Info("Remote Engine States: resolver event channel closed")
				return
			}
			m.handleResolverEvent(event)
		}
	}
}

// handleResolverEvent processes resolver events to add/remove instances.
func (m *RemoteEngineStates) handleResolverEvent(event resolver.WorkerEvent) {
	switch event.Type {
	case resolver.WorkerEventAdd:
		for i := range event.Workers {
			worker := &event.Workers[i]
			instanceID := worker.Id()
			endpoint := worker.Endpoint.String()
			klog.Infof("Remote Engine States: adding backend instance %s (endpoint: %s)", instanceID, endpoint)
			if err := m.addInstance(worker); err != nil {
				klog.Warningf("Remote Engine State: Failed to add instance %s: %v", instanceID, err)
			}
		}

	case resolver.WorkerEventRemove:
		for _, worker := range event.Workers {
			instanceID := worker.Id()
			klog.Infof("Remote Engine States: removing backend instance %s", instanceID)
			m.removeInstance(instanceID)
		}

	case resolver.WorkerEventFullSync:
		m.handleFullSync(event.Workers)
	}
}

// handleFullSync syncs instances with the new snapshot.
// It adds missing instances and removes extra ones, preserving existing ones.
func (m *RemoteEngineStates) handleFullSync(workers types.LLMWorkerSlice) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Build a map of worker info from the snapshot
	workerInfo := make(map[string]*types.LLMWorker, len(workers))
	for i := range workers {
		w := &workers[i]
		workerInfo[w.Id()] = w
	}

	// Remove instances that are no longer in the snapshot
	for instanceID := range m.instances {
		if _, exists := workerInfo[instanceID]; !exists {
			delete(m.instances, instanceID)
			delete(m.metrics, instanceID)
			m.collector.RemoveInstance(instanceID)
			klog.Infof("Remote Engine State: removed instance %s (not in snapshot)", instanceID)
		}
	}

	// Add instances that are in the snapshot but not in our list
	for instanceID, worker := range workerInfo {
		if _, exists := m.instances[instanceID]; !exists {
			m.instances[instanceID] = &InstanceConfig{
				InstanceID: instanceID,
				Endpoint:   worker.Endpoint.String(),
				Backend:    "", // Will be detected during collection
				Model:      worker.Model,
			}
			klog.Infof("Remote Engine State: added instance %s (endpoint: %s) from snapshot",
				instanceID, worker.Endpoint.String())
		}
	}
}

// collectAllMetrics collects metrics from all registered instances.
func (m *RemoteEngineStates) collectAllMetrics(ctx context.Context) {
	m.mu.RLock()
	instances := make([]*InstanceConfig, 0, len(m.instances))
	for _, cfg := range m.instances {
		instances = append(instances, cfg)
	}
	m.mu.RUnlock()

	// Collect metrics from each instance
	for _, cfg := range instances {
		// Skip if instance has failed too many times consecutively
		if cfg.ConsecutiveFailures >= MaxConsecutiveFailures {
			klog.V(3).Infof("Skipping metric collection for instance %s due to %d consecutive failures",
				cfg.InstanceID, cfg.ConsecutiveFailures)
			continue
		}

		metric, detectedBackend, err := m.collector.CollectEngineState(
			ctx,
			cfg.InstanceID,
			cfg.Endpoint,
			cfg.Backend, // Use cached backend type
			int64(m.interval.Milliseconds()),
		)

		if err != nil {
			klog.V(2).Infof("Failed to collect metrics from instance %s: %v", cfg.InstanceID, err)
			m.mu.Lock()
			if instance, exists := m.instances[cfg.InstanceID]; exists {
				instance.ConsecutiveFailures++
			}
			m.mu.Unlock()
			continue
		}

		// Set model from config
		metric.Model = cfg.Model

		m.mu.Lock()
		// Reset failure count on success and update backend type if newly detected
		if instance, exists := m.instances[cfg.InstanceID]; exists {
			instance.ConsecutiveFailures = 0
			if cfg.Backend == "" && detectedBackend != "" {
				instance.Backend = detectedBackend
				// klog.Infof("Detected backend type for instance %s: %s", cfg.InstanceID, detectedBackend)
			}
		}

		m.metrics[cfg.InstanceID] = metric
		m.mu.Unlock()
		klog.V(3).Infof("Collected metrics from instance %s: running=%d, waiting=%d, gpu_usage=%.2f%%",
			cfg.InstanceID, metric.RunningRequests, metric.WaitingRequests, metric.GPUCacheUsage)
	}
}

// Stop stops the manager and releases resources.
func (m *RemoteEngineStates) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

// SubmitMetric periodically exposes collected metrics to the metrics system.
// It runs in a separate goroutine and submits metrics every 5 seconds.
func (m *RemoteEngineStates) SubmitMetric() {
	defer func() {
		if e := recover(); e != nil {
			klog.Warningf("res metrics submit loop crashed, err: %s\ntrace:%s",
				e, string(debug.Stack()))
			go m.SubmitMetric()
		}
	}()

	for {
		time.Sleep(5 * time.Second)
		m.submitMetrics()
	}
}

// submitMetrics exposes all collected metrics to the metrics system.
func (m *RemoteEngineStates) submitMetrics() {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, state := range m.metrics {
		labels := metrics.Labels{
			{Name: "model", Value: state.Model},
			{Name: "address", Value: state.Endpoint},
		}

		metrics.StatusValue("llm_waiting_requests", labels).Set(float32(state.WaitingRequests))
		metrics.StatusValue("llm_running_requests", labels).Set(float32(state.RunningRequests))
		metrics.StatusValue("llm_gpu_cache_usage", labels).Set(float32(state.GPUCacheUsage))
		metrics.StatusValue("llm_tps_out", labels).Set(float32(state.TokenPerSecondOut))
		metrics.StatusValue("llm_tps_in", labels).Set(float32(state.TokenPerSecondIn))
	}
}
