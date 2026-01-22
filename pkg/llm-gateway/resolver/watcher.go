package resolver

import (
	"context"
	"easgo/pkg/llm-gateway/types"
	"sync"

	"k8s.io/klog/v2"
)

// observerPair stores a pair of channels for added and removed workers
type observerPair struct {
	added   chan<- types.LLMWorkerSlice
	removed chan<- types.LLMWorkerSlice
}

// Watcher provides common observer management functionality for resolvers
// that implement the Watch method. It handles observer registration, notification,
// and context cancellation.
type Watcher struct {
	// observers stores observer pairs for Watch method
	observers map[*observerPair]struct{}
	obsMu     sync.RWMutex
}

// NewWatcher creates a new Watcher with initialized fields
func NewWatcher() *Watcher {
	return &Watcher{
		observers: make(map[*observerPair]struct{}),
	}
}

// Watch implements the common Watch method for resolvers.
// It returns two channels: one for added workers and one for removed workers.
// The first value sent on the added channel is the current state (all workers considered as "added").
// Both channels will be closed when the context is cancelled or the resolver stops.
// This method is thread-safe and supports multiple concurrent observers.
func (w *Watcher) Watch(ctx context.Context, getCurrentWorkers func() (types.LLMWorkerSlice, error)) (<-chan types.LLMWorkerSlice, <-chan types.LLMWorkerSlice, error) {
	// Create buffered channels to avoid blocking
	addedCh := make(chan types.LLMWorkerSlice, 50)
	removedCh := make(chan types.LLMWorkerSlice, 50)

	// Create observer pair
	pair := &observerPair{
		added:   addedCh,
		removed: removedCh,
	}

	// Register the observer
	w.obsMu.Lock()
	defer w.obsMu.Unlock()
	w.observers[pair] = struct{}{}

	// Get current state using the provided function
	currentSlice, err := getCurrentWorkers()

	// Send initial state as added (all current workers are effectively "added" initially)
	if len(currentSlice) > 0 && err == nil {
		go func() {
			select {
			case addedCh <- currentSlice:
				// Successfully sent initial state
			case <-ctx.Done():
				// Context cancelled before we could send initial state
				// Clean up the observer
				w.removeObserver(pair)
			}
		}()
	}

	// Start a goroutine to handle context cancellation
	go func() {
		<-ctx.Done()
		w.removeObserver(pair)
	}()

	return addedCh, removedCh, nil
}

// removeObserver removes an observer pair
func (w *Watcher) removeObserver(pair *observerPair) {
	w.obsMu.Lock()
	defer w.obsMu.Unlock()
	delete(w.observers, pair)
	close(pair.added)
	close(pair.removed)
}

// notifyObservers sends added and removed workers to all registered observers
func (w *Watcher) notifyObservers(added, removed types.LLMWorkerSlice) {
	w.obsMu.RLock()
	defer w.obsMu.RUnlock()

	for pair := range w.observers {
		// Send added workers
		if len(added) > 0 {
			select {
			case pair.added <- added:
				// Successfully sent
			default:
				// Channel is full, skip this observer
				klog.Warningf("added channel is full, may miss update")
			}
		}

		// Send removed workers
		if len(removed) > 0 {
			select {
			case pair.removed <- removed:
				// Successfully sent
			default:
				// Channel is full, skip this observer
				klog.Warningf("removed channel is full, skipping update")
			}
		}
	}
}
