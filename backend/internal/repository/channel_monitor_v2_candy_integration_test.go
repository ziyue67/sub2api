//go:build integration

package repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2CandyPersistenceAndClaims(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewChannelMonitorV2Repository(integrationDB).(*channelMonitorV2Repository)
	original, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		current, loadErr := repo.GetConfig(ctx)
		require.NoError(t, loadErr)
		_, restoreErr := repo.UpdateConfig(ctx, *original, current.Version)
		require.NoError(t, restoreErr)
	})
	group := mustCreateGroup(t, client, &service.Group{Name: "candy-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	other := mustCreateGroup(t, client, &service.Group{Name: "other-candy-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	probe := service.ChannelMonitorV2CandyProbe{GroupID: group.ID, Enabled: true, Model: "model-test", ReasoningEffort: "medium", IntervalMinutes: 1}
	cfg := *original
	cfg.Enabled = true
	cfg.CandyProbes = []service.ChannelMonitorV2CandyProbe{probe}
	updated, err := repo.UpdateConfig(ctx, cfg, cfg.Version)
	require.NoError(t, err)
	require.Equal(t, cfg.CandyProbes, updated.CandyProbes)
	loaded, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, cfg.CandyProbes, loaded.CandyProbes)
	now := time.Now().UTC().Truncate(time.Minute)
	var wg sync.WaitGroup
	ids := make([]int64, 16)
	errs := make([]error, 16)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ids[i], errs[i] = repo.ClaimCandyProbe(ctx, probe, "test-key", now, updated.Version)
		}(i)
	}
	wg.Wait()
	var claimed int64
	count := 0
	for i, id := range ids {
		require.NoError(t, errs[i])
		if id != 0 {
			count++
			claimed = id
		}
	}
	require.Equal(t, 1, count, "replicas may only claim one sample for a group/slot")
	next, err := repo.ClaimCandyProbe(ctx, probe, "test-key", now.Add(time.Minute), updated.Version)
	require.NoError(t, err)
	require.Zero(t, next, "an in-flight group cannot overlap another slot")
	require.NoError(t, repo.FinishCandyProbe(ctx, service.ChannelMonitorV2CandyResult{ID: claimed, Verdict: "correct", AnswerPreview: "21个", LatencyMs: 123}))
	items, err := repo.CandyHistory(ctx, map[int64]string{group.ID: "test-key"}, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "21个", items[0].AnswerPreview)
	items, err = repo.CandyHistory(ctx, map[int64]string{other.ID: "test-key"}, now.Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, items)
	next, err = repo.ClaimCandyProbe(ctx, probe, "test-key", now.Add(time.Minute), updated.Version)
	require.NoError(t, err)
	require.NotZero(t, next)
	_, err = integrationDB.ExecContext(ctx, "UPDATE channel_monitor_v2_candy_results SET checked_at=$2 WHERE id=$1", next, now.Add(-4*time.Minute))
	require.NoError(t, err)
	require.NoError(t, repo.PruneCandyHistory(ctx, now))
	require.NoError(t, repo.FinishCandyProbe(ctx, service.ChannelMonitorV2CandyResult{ID: next, Verdict: "correct"}))
	var verdict, reason string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT verdict,reason FROM channel_monitor_v2_candy_results WHERE id=$1", next).Scan(&verdict, &reason))
	require.Equal(t, "error", verdict)
	require.Equal(t, "interrupted", reason)
	denied, err := repo.ClaimCandyProbe(ctx, probe, "key", now.Add(2*time.Minute), updated.Version-1)
	require.NoError(t, err)
	require.Zero(t, denied, "obsolete config cannot issue probes")
	_, err = integrationDB.ExecContext(ctx, "UPDATE channel_monitor_v2_candy_results SET checked_at=$2 WHERE group_id=$1", group.ID, now.Add(-25*time.Hour))
	require.NoError(t, err)
	require.NoError(t, repo.PruneCandyHistory(ctx, now))
	items, err = repo.CandyHistory(ctx, map[int64]string{group.ID: "test-key"}, now.Add(-48*time.Hour))
	require.NoError(t, err)
	require.Empty(t, items)
	updated.Enabled = false
	disabled, err := repo.UpdateConfig(ctx, *updated, updated.Version)
	require.NoError(t, err)
	denied, err = repo.ClaimCandyProbe(ctx, probe, "key", now.Add(2*time.Minute), disabled.Version)
	require.NoError(t, err)
	require.Zero(t, denied)
}

func TestChannelMonitorV2CandyLatest100PerActiveConfig(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewChannelMonitorV2Repository(integrationDB).(*channelMonitorV2Repository)
	group := mustCreateGroup(t, client, &service.Group{Name: "history-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	other := mustCreateGroup(t, client, &service.Group{Name: "history-other-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	excluded := mustCreateGroup(t, client, &service.Group{Name: "history-excluded-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	now := time.Now().UTC().Truncate(time.Minute)
	seed := func(id int64, key string, count int, at time.Time) {
		t.Helper()
		_, err := integrationDB.ExecContext(ctx,
			"INSERT INTO channel_monitor_v2_candy_results(group_id,config_key,model,reasoning_effort,slot,checked_at,finished_at,verdict) "+
				"SELECT $1,$2,'synthetic-model','medium',$3::timestamptz-i*INTERVAL '1 minute',$3::timestamptz-i*INTERVAL '1 minute',$3::timestamptz, "+
				"CASE WHEN i%3=0 THEN 'error' WHEN i%3=1 THEN 'correct' ELSE 'incorrect' END FROM generate_series(1,$4::integer) AS n(i)", id, key, at, count)
		require.NoError(t, err)
	}
	seed(group.ID, "active", 125, now)
	seed(group.ID, "obsolete", 110, now.Add(time.Minute))
	seed(other.ID, "other-active", 3, now)
	seed(other.ID, "other-active", 1, now.Add(-25*time.Hour))
	seed(excluded.ID, "active", 120, now)
	_, err := integrationDB.ExecContext(ctx,
		"INSERT INTO channel_monitor_v2_candy_results(group_id,config_key,model,reasoning_effort,slot,checked_at) VALUES($1,'active','synthetic-model','medium',$2,$2)", group.ID, now)
	require.NoError(t, err)
	items, err := repo.CandyHistory(ctx, map[int64]string{group.ID: "active", other.ID: "other-active"}, now.Add(-service.ChannelMonitorV2CandyRetention))
	require.NoError(t, err)
	require.Len(t, items, 103)
	byGroup := map[int64][]service.ChannelMonitorV2CandyResult{}
	for index, item := range items {
		require.NotEqual(t, excluded.ID, item.GroupID)
		require.NotEqual(t, "running", item.Verdict)
		byGroup[item.GroupID] = append(byGroup[item.GroupID], item)
		if index > 0 {
			require.False(t, item.CheckedAt.Before(items[index-1].CheckedAt))
			if item.CheckedAt.Equal(items[index-1].CheckedAt) {
				require.Greater(t, item.ID, items[index-1].ID)
			}
		}
	}
	require.Len(t, byGroup[group.ID], 100)
	require.Len(t, byGroup[other.ID], 3)
	require.Equal(t, now.Add(-100*time.Minute), byGroup[group.ID][0].CheckedAt.UTC())
	require.Equal(t, now.Add(-time.Minute), byGroup[group.ID][99].CheckedAt.UTC())
	for _, item := range byGroup[group.ID] {
		require.Equal(t, "active", item.ConfigKey, "old configurations must not displace current samples before limiting")
	}
	for _, item := range byGroup[other.ID] {
		require.Equal(t, "other-active", item.ConfigKey)
	}
	empty, err := repo.CandyHistory(ctx, map[int64]string{}, now.Add(-service.ChannelMonitorV2CandyRetention))
	require.NoError(t, err)
	require.Empty(t, empty)
}
