//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3RepositoryIdleProbes(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewChannelMonitorV3Repository(integrationDB)
	group := mustCreateGroup(t, client, &service.Group{Name: "v3-probe-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	other := mustCreateGroup(t, client, &service.Group{Name: "v3-probe-other-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	original, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, repo.UpdateIdleProbe(ctx, original.IdleProbe))
		// Groups cascade to their probe results.
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id IN ($1, $2)`, group.ID, other.ID)
		require.NoError(t, err)
	})

	// Saved apart from the versioned page config, and kept by its saves.
	probe := service.ChannelMonitorV3IdleProbe{Enabled: true, Groups: []service.ChannelMonitorV3IdleProbeGroup{{GroupID: group.ID, Model: "gpt-probe", ReasoningEffort: "low"}}}
	require.NoError(t, repo.UpdateIdleProbe(ctx, probe))
	cfg, err := repo.GetConfig(ctx)
	require.NoError(t, err)
	require.Equal(t, probe, cfg.IdleProbe)
	require.Equal(t, original.Version, cfg.Version)
	saved, err := repo.UpdateConfig(ctx, *cfg, cfg.Version, 0)
	require.NoError(t, err)
	require.Equal(t, probe, saved.IdleProbe)

	slot := time.Now().UTC().Truncate(5 * time.Minute).Add(-time.Hour)
	first, err := repo.ClaimProbe(ctx, group.ID, "gpt-probe", slot, 5)
	require.NoError(t, err)
	require.Positive(t, first)
	again, err := repo.ClaimProbe(ctx, group.ID, "gpt-probe", slot, 5)
	require.NoError(t, err)
	require.Zero(t, again, "a slot is probed once per group and model")
	second, err := repo.ClaimProbe(ctx, group.ID, "gpt-other", slot, 5)
	require.NoError(t, err)
	running, err := repo.ClaimProbe(ctx, group.ID, "gpt-probe", slot.Add(5*time.Minute), 5)
	require.NoError(t, err)
	hourly, err := repo.ClaimProbe(ctx, other.ID, "gpt-probe", slot.Truncate(time.Hour), 60)
	require.NoError(t, err)
	cost := 0.0012
	require.NoError(t, repo.FinishProbe(ctx, service.ChannelMonitorV3ProbeResult{ID: first, Outcome: service.ChannelMonitorV3ProbeSuccess, LatencyMs: 1800, AccountID: 77, CostUSD: &cost}))
	require.NoError(t, repo.FinishProbe(ctx, service.ChannelMonitorV3ProbeResult{ID: second, Outcome: service.ChannelMonitorV3ProbeFailure, LatencyMs: 90000,
		Category: "timeout", ErrorMessage: "timeout: no answer within 90 seconds"}))
	require.NoError(t, repo.FinishProbe(ctx, service.ChannelMonitorV3ProbeResult{ID: hourly, Outcome: service.ChannelMonitorV3ProbeSuccess, LatencyMs: 900}))
	require.Positive(t, running, "an unfinished claim is never read")

	facts, err := repo.ProbeFacts(ctx, []int64{group.ID}, slot.Add(-time.Hour), slot.Add(time.Hour), 5*time.Minute)
	require.NoError(t, err)
	require.ElementsMatch(t, []service.ChannelMonitorV3ProbeFact{
		{GroupID: group.ID, Model: "gpt-probe", Slot: slot, Count: 1, LatencyP50Ms: 1800},
		{GroupID: group.ID, Model: "gpt-other", Slot: slot, Category: "timeout", Count: 1, LatencyP50Ms: 90000},
	}, facts)

	// A probe lands in the slot holding the moment it was taken for, whatever the slot length is now.
	tenMinute, err := repo.ProbeFacts(ctx, []int64{group.ID, other.ID}, slot.Add(-2*time.Hour), slot.Add(2*time.Hour), 10*time.Minute)
	require.NoError(t, err)
	for _, f := range tenMinute {
		if f.GroupID == other.ID {
			hourlyAt := slot.Truncate(time.Hour).Add(time.Hour - 30*time.Second).Truncate(10 * time.Minute)
			require.Equal(t, hourlyAt, f.Slot, "an hourly probe was taken just before the hour ended")
			continue
		}
		require.Equal(t, slot.Add(5*time.Minute-30*time.Second).Truncate(10*time.Minute), f.Slot)
	}
	require.Len(t, tenMinute, 3)

	totals, err := repo.ProbeFacts(ctx, []int64{group.ID}, slot.Add(-time.Hour), time.Now(), 0)
	require.NoError(t, err)
	require.Len(t, totals, 2)
	for _, f := range totals {
		require.True(t, f.Slot.IsZero(), "totals have no slot")
	}
	outside, err := repo.ProbeFacts(ctx, []int64{group.ID}, slot.Add(10*time.Minute), slot.Add(time.Hour), 5*time.Minute)
	require.NoError(t, err)
	require.Empty(t, outside)

	stats, err := repo.ProbeStats(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	var mine *service.ChannelMonitorV3IdleProbeStat
	for i := range stats {
		if stats[i].GroupID == group.ID {
			mine = &stats[i]
		}
	}
	require.NotNil(t, mine)
	require.Equal(t, int64(2), mine.Probes)
	require.Equal(t, int64(1), mine.Failures)
	require.InDelta(t, cost, mine.CostUSD, 1e-12)
	require.NotNil(t, mine.LastAt)
	require.Contains(t, []string{"gpt-probe", "gpt-other"}, mine.LastModel)

	require.NoError(t, repo.PruneProbes(ctx, slot.Add(time.Minute)))
	left, err := repo.ProbeFacts(ctx, []int64{group.ID}, slot.Add(-time.Hour), slot.Add(time.Hour), 5*time.Minute)
	require.NoError(t, err)
	require.Empty(t, left, "pruned by slot")
}

func TestChannelMonitorV3RepositoryCountsRealSuccesses(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := NewChannelMonitorV3Repository(integrationDB)
	group := mustCreateGroup(t, client, &service.Group{Name: "v3-traffic-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	other := mustCreateGroup(t, client, &service.Group{Name: "v3-traffic-other-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	user := mustCreateUser(t, client, &service.User{Email: "v3-traffic-" + uuid.NewString() + "@example.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-v3-traffic-" + uuid.NewString()})
	account := mustCreateAccount(t, client, &service.Account{Name: "v3-traffic-" + uuid.NewString(), Platform: service.PlatformOpenAI})
	t.Cleanup(func() {
		for _, query := range []string{
			`DELETE FROM usage_logs WHERE user_id = $1`,
			`DELETE FROM api_keys WHERE user_id = $1`,
			`DELETE FROM users WHERE id = $1`,
		} {
			_, err := integrationDB.ExecContext(ctx, query, user.ID)
			require.NoError(t, err)
		}
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id = $1`, account.ID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id IN ($1, $2)`, group.ID, other.ID)
		require.NoError(t, err)
	})

	slot := time.Now().UTC().Truncate(5 * time.Minute).Add(-time.Hour)
	logUsage := func(groupID int64, requestID, model string, cost float64, at time.Time) {
		t.Helper()
		_, err := client.UsageLog.Create().SetUserID(user.ID).SetAPIKeyID(key.ID).SetAccountID(account.ID).SetGroupID(groupID).
			SetRequestID(requestID).SetModel("upstream-" + model).SetRequestedModel(model).SetActualCost(cost).SetCreatedAt(at).Save(ctx)
		require.NoError(t, err)
	}
	logUsage(group.ID, "req-a", "gpt-a", 0.1, slot.Add(time.Minute))
	logUsage(group.ID, "req-a2", "gpt-a", 0.2, slot.Add(2*time.Minute))
	logUsage(group.ID, "req-b", "gpt-b", 0.1, slot.Add(3*time.Minute))
	logUsage(group.ID, "req-free", "gpt-a", 0, slot.Add(3*time.Minute))   // nothing charged: not a success
	logUsage(group.ID, "req-late", "gpt-a", 0.1, slot.Add(5*time.Minute)) // next slot
	logUsage(other.ID, "req-other", "gpt-a", 0.1, slot.Add(time.Minute))

	counts, err := repo.SuccessCounts(ctx, []int64{group.ID}, slot, slot.Add(5*time.Minute))
	require.NoError(t, err)
	require.ElementsMatch(t, []service.ChannelMonitorV3Fact{
		{GroupID: group.ID, Model: "gpt-a", Success: 2},
		{GroupID: group.ID, Model: "gpt-b", Success: 1},
	}, counts)
}
