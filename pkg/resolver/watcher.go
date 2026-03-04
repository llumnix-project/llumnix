package resolver

import (
	"context"
	"llm-gateway/pkg/types"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// WorkerEventType identifies the type of a worker change event.
type WorkerEventType int

const (
	// WorkerEventAdd indicates one or more workers were added.
	WorkerEventAdd WorkerEventType = iota
	// WorkerEventRemove indicates one or more workers were removed.
	WorkerEventRemove
	// WorkerEventFullSync carries a full snapshot of all current workers.
	// Consumers should replace their local state with this snapshot to recover
	// from any missed incremental events.
	WorkerEventFullSync
)

// WorkerEvent represents a single worker change event emitted by a resolver.
type WorkerEvent struct {
	Type    WorkerEventType
	Workers types.LLMWorkerSlice
}

const (
	// defaultFullSyncInterval is the interval at which a full snapshot is pushed
	// to all observers to recover from any missed incremental events.
	defaultFullSyncInterval = 30 * time.Second

	// defaultEventChannelBuffer is the buffer size for each observer's event channel.
	defaultEventChannelBuffer = 50
)

// observer stores the event channel for a single Watch caller.
type observer struct {
	ch chan<- WorkerEvent
}

// Watcher provides common observer management functionality for resolvers
// that implement the Watch method. It handles observer registration, notification,
// periodic full-sync, and context cancellation.
type Watcher struct {
	// observers stores all active observers
	observers map[*observer]struct{}
	obsMu     sync.RWMutex
}

// NewWatcher creates a new Watcher with initialized fields
func NewWatcher() *Watcher {
	return &Watcher{
		observers: make(map[*observer]struct{}),
	}
}

// Watch implements the common Watch method for resolvers.
// It returns a single ordered event channel carrying WorkerEvent values.
// The first event is always a WorkerEventFullSync with the current state.
// A periodic WorkerEventFullSync is also sent every defaultFullSyncInterval to
// allow consumers to recover from any missed incremental events.
// The channel is closed when ctx is cancelled.
// This method is thread-safe and supports multiple concurrent observers.
func (w *Watcher) Watch(ctx context.Context, getCurrentWorkers func() (types.LLMWorkerSlice, error)) (<-chan WorkerEvent, error) {
	ch := make(chan WorkerEvent, defaultEventChannelBuffer)
	obs := &observer{ch: ch}

	w.obsMu.Lock()
	w.observers[obs] = struct{}{}
	w.obsMu.Unlock()

	// Get current state for the initial full-sync
	currentSlice, err := getCurrentWorkers()
	if err != nil {
		klog.Warningf("watcher: failed to get initial workers: %v", err)
	}

	go func() {
		// Send initial full-sync so the consumer can bootstrap its local state
		initialEvent := WorkerEvent{
			Type:    WorkerEventFullSync,
			Workers: currentSlice,
		}
		select {
		case ch <- initialEvent:
		case <-ctx.Done():
			w.removeObserver(obs)
			return
		}

		// Periodic full-sync ticker to prevent state divergence from missed events
		ticker := time.NewTicker(defaultFullSyncInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				workers, err := getCurrentWorkers()
				if err != nil {
					klog.Warningf("watcher: failed to get workers for full-sync: %v", err)
					continue
				}
				event := WorkerEvent{
					Type:    WorkerEventFullSync,
					Workers: workers,
				}
				select {
				case ch <- event:
				default:
					klog.Warningf("watcher: event channel is full, dropping full-sync event")
				}
			case <-ctx.Done():
				w.removeObserver(obs)
				return
			}
		}
	}()

	return ch, nil
}

// removeObserver unregisters an observer and closes its channel.
func (w *Watcher) removeObserver(obs *observer) {
	w.obsMu.Lock()
	defer w.obsMu.Unlock()
	if _, ok := w.observers[obs]; ok {
		delete(w.observers, obs)
		close(obs.ch)
	}
}

// notifyObservers sends an incremental add or remove event to all registered observers.
func (w *Watcher) notifyObservers(added, removed types.LLMWorkerSlice) {
	w.obsMu.RLock()
	defer w.obsMu.RUnlock()

	for obs := range w.observers {
		if len(added) > 0 {
			event := WorkerEvent{Type: WorkerEventAdd, Workers: added}
			select {
			case obs.ch <- event:
			default:
				klog.Warningf("watcher: event channel is full, dropping add event")
			}
		}
		if len(removed) > 0 {
			event := WorkerEvent{Type: WorkerEventRemove, Workers: removed}
			select {
			case obs.ch <- event:
			default:
				klog.Warningf("watcher: event channel is full, dropping remove event")
			}
		}
	}
}
