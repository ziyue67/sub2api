package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

type slotCleanupCache struct {
	ConcurrencyCache
	calls atomic.Int64
}

func (c *slotCleanupCache) CleanupExpiredAccountSlotKeys(context.Context) error {
	c.calls.Add(1)
	return nil
}

func TestStartSlotCleanupWorker_UsesCacheWideCleanupWithoutAccountRepo(t *testing.T) {
	cache := &slotCleanupCache{}
	svc := NewConcurrencyService(cache)
	defer svc.StopAPIKeyQueue()

	svc.StartSlotCleanupWorker(nil, time.Hour)

	deadline := time.After(time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if cache.calls.Load() > 0 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("cleanup worker did not call cache-wide account slot cleanup")
		case <-ticker.C:
		}
	}
}

type blockingSlotCleanupCache struct {
	ConcurrencyCache
	started chan struct{}
	stopped chan struct{}
	calls   atomic.Int64
}

func (c *blockingSlotCleanupCache) CleanupExpiredAccountSlotKeys(ctx context.Context) error {
	if c.calls.Add(1) == 1 {
		close(c.started)
	}
	<-ctx.Done()
	close(c.stopped)
	return ctx.Err()
}

func TestStartSlotCleanupWorker_StopCancelsScanAndPreventsLaterPasses(t *testing.T) {
	cache := &blockingSlotCleanupCache{started: make(chan struct{}), stopped: make(chan struct{})}
	svc := NewConcurrencyService(cache)
	defer svc.StopAPIKeyQueue()
	svc.StartSlotCleanupWorker(nil, time.Millisecond)
	select {
	case <-cache.started:
	case <-time.After(time.Second):
		t.Fatal("cleanup did not start")
	}
	svc.StopAPIKeyQueue()
	select {
	case <-cache.stopped:
	case <-time.After(time.Second):
		t.Fatal("stopping the service did not cancel in-flight cleanup")
	}
	time.Sleep(20 * time.Millisecond)
	if got := cache.calls.Load(); got != 1 {
		t.Fatalf("cleanup ran after stop: %d calls", got)
	}
}
