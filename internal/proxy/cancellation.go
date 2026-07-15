package proxy

import (
	"context"
	"sync"
	"time"
)

type CancellationRegistry struct {
	mutex   sync.Mutex
	entries map[string]context.CancelFunc
}

func NewCancellationRegistry() *CancellationRegistry {
	return &CancellationRegistry{entries: make(map[string]context.CancelFunc)}
}

func (registry *CancellationRegistry) Track(parent context.Context, executionID string) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	registry.mutex.Lock()
	registry.entries[executionID] = cancel
	registry.mutex.Unlock()
	return ctx, func() {
		registry.mutex.Lock()
		delete(registry.entries, executionID)
		registry.mutex.Unlock()
		cancel()
	}
}

func (registry *CancellationRegistry) Cancel(executionID string) bool {
	registry.mutex.Lock()
	cancel := registry.entries[executionID]
	registry.mutex.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

type CancellationSource interface {
	ListCancellationRequested(context.Context) ([]string, error)
}

func (registry *CancellationRegistry) Poll(ctx context.Context, source CancellationSource, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := source.ListCancellationRequested(ctx)
			if err != nil {
				continue
			}
			for _, id := range ids {
				registry.Cancel(id)
			}
		}
	}
}
