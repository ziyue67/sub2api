package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func startContentModerationTestWorkers(t *testing.T, svc *ContentModerationService, ids ...int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	for _, id := range ids {
		workers.Add(1)
		go func() {
			defer workers.Done()
			svc.runWorker(ctx, id)
		}()
	}
	t.Cleanup(func() {
		cancel()
		done := make(chan struct{})
		go func() { workers.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("moderation workers did not stop")
		}
	})
}

func TestContentModerationWorkersDoNotLoadSettingsWhileIdle(t *testing.T) {
	for _, warm := range []bool{false, true} {
		name := "cold"
		if warm {
			name = "expired_snapshot"
		}
		t.Run(name, func(t *testing.T) {
			settings := &contentModerationRuntimeSettingRepo{values: map[string]string{
				SettingKeyContentModerationConfig: runtimeCacheTestConfig(t),
			}}
			svc := runtimeCacheTestService(settings, time.Millisecond)
			svc.asyncQueue = make(chan contentModerationTask, 4)
			if warm {
				_, err := svc.loadRuntimeSnapshot(context.Background())
				require.NoError(t, err)
				current := *svc.runtimeSnapshot.Load()
				current.loadedAt = time.Now().Add(-time.Hour)
				svc.runtimeSnapshot.Store(&current)
			}
			beforeValue, beforeMultiple := settings.calls()
			ids := make([]int, maxContentModerationWorkerCount)
			for i := range ids {
				ids[i] = i
			}
			startContentModerationTestWorkers(t, svc, ids...)
			require.Never(t, func() bool {
				value, multiple := settings.calls()
				return value != beforeValue || multiple != beforeMultiple
			}, 250*time.Millisecond, time.Millisecond)

			// The idle gate must wake for actual work without an external
			// request having to prime or refresh the runtime snapshot first.
			svc.enqueueRecord(ContentModerationCheckInput{}, nil,
				&ContentModerationLog{RequestID: "idle-wakeup"}, "", false, false)
			require.Eventually(t, func() bool { return svc.asyncProcessed.Load() == 1 }, time.Second, time.Millisecond)
			require.Empty(t, svc.asyncQueue)
			logs, ok := svc.repo.(*contentModerationTestRepo)
			require.True(t, ok)
			logs.mu.Lock()
			persisted := append([]ContentModerationLog(nil), logs.logs...)
			logs.mu.Unlock()
			require.Len(t, persisted, 1)
			require.Equal(t, "idle-wakeup", persisted[0].RequestID)
		})
	}
}

func TestContentModerationWorkerRetainsQueueOnConfigFailure(t *testing.T) {
	settings := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: runtimeCacheTestConfig(t),
	}}
	settings.failMultiple(errors.New("settings temporarily unavailable"))
	svc := runtimeCacheTestService(settings, time.Hour)
	svc.asyncQueue = make(chan contentModerationTask, 4)
	svc.enqueueRecord(ContentModerationCheckInput{}, nil, &ContentModerationLog{}, "", false, false)
	startContentModerationTestWorkers(t, svc, 0)
	require.Eventually(t, func() bool { _, calls := settings.calls(); return calls > 0 }, time.Second, time.Millisecond)
	require.Len(t, svc.asyncQueue, 1, "a config failure must not consume or discard the task")
	require.Zero(t, svc.asyncProcessed.Load())
	settings.failMultiple(nil)
	require.Eventually(t, func() bool { return svc.asyncProcessed.Load() == 1 }, 2*time.Second, time.Millisecond)
	require.Empty(t, svc.asyncQueue)
}

func TestContentModerationIdleWorkerHonorsWorkerCountUpdate(t *testing.T) {
	cfg := defaultContentModerationConfig()
	cfg.WorkerCount = 1
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	settings := &contentModerationRuntimeSettingRepo{values: map[string]string{
		SettingKeyContentModerationConfig: string(raw),
	}}
	svc := runtimeCacheTestService(settings, time.Hour)
	svc.asyncQueue = make(chan contentModerationTask, 4)
	svc.enqueueRecord(ContentModerationCheckInput{}, nil, &ContentModerationLog{}, "", false, false)
	startContentModerationTestWorkers(t, svc, 1)
	require.Eventually(t, func() bool { _, calls := settings.calls(); return calls > 0 }, time.Second, time.Millisecond)
	require.Len(t, svc.asyncQueue, 1, "an inactive worker must leave work for eligible workers")
	require.Zero(t, svc.asyncProcessed.Load())
	count := 2
	_, err = svc.UpdateConfig(context.Background(), UpdateContentModerationConfigInput{WorkerCount: &count})
	require.NoError(t, err)
	require.Eventually(t, func() bool { return svc.asyncProcessed.Load() == 1 }, 2*time.Second, time.Millisecond)
	require.Empty(t, svc.asyncQueue)
}
