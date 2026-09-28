//go:build integration

package repository

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPrioritySchedulingSignalsSeparateModelsAndCompletedRounds(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "priority@test.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-priority-test", Name: "test"})
	account := mustCreateAccount(t, client, &service.Account{Name: "priority", Platform: service.PlatformOpenAI})
	now := time.Now().Add(-time.Minute)
	add := func(model, requested string, ttft *int, cost float64, tokens int, stream bool, at time.Time) {
		t.Helper()
		_, err := repo.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: fmt.Sprintf("p-%d-%s", time.Now().UnixNano(), model), Model: model, RequestedModel: requested, FirstTokenMs: ttft, ActualCost: cost, InputTokens: tokens, Stream: stream, CreatedAt: at})
		require.NoError(t, err)
	}
	fast, slow := 100, 1000
	add("upstream", "gpt-test", &fast, 0, 10, true, now) // free usage with real tokens is valid
	add("gpt-test", "", &slow, 1, 10, true, now)         // legacy model fallback
	add("other", "other", &slow, 1, 10, true, now)
	add("gpt-test", "", nil, 1, 10, true, now)
	add("gpt-test", "", &slow, 0, 0, true, now)   // failed placeholder
	add("gpt-test", "", &slow, 1, 10, false, now) // nonstreaming duration is not TTFT
	add("gpt-test", "", &slow, 1, 10, true, now.Add(-2*time.Hour))
	var plan int64
	rows, err := tx.QueryContext(ctx, `INSERT INTO scheduled_test_plans(account_id,model_id,cron_expression,enabled,pelican_config,updated_at) VALUES($1,'gpt-test','*/30 * * * *',true,'{"quality":{},"parallel_count":2}',NOW()-INTERVAL '1 hour') RETURNING id`, account.ID)
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&plan))
	require.NoError(t, rows.Close())
	probe := func(round, status, verdict, message, action string, at time.Time) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `INSERT INTO scheduled_test_results(plan_id,status,started_at,finished_at,created_at,pelican_config,quality_round_id,quality_action,quality_judgment,error_message) VALUES($1,$2,$3,$3,$3,'{"parallel_count":2}',$4,$5,jsonb_build_object('verdict',$6::text),$7)`, plan, status, at, round, action, verdict, message)
		require.NoError(t, err)
	}
	probe("passed", "success", "correct", "", "passed", now)
	probe("passed", "success", "correct", "", "passed", now)
	probe("failed", "success", "correct", "", "failure_counted:1/2", now)
	probe("failed", "failed", "incorrect", "state_degraded", "failure_counted:1/2", now)
	probe("partial", "success", "correct", "", "passed", now) // incomplete save cannot count as a passed round
	probe("unknown", "success", "correct", "", "inconclusive", now)
	probe("unknown", "failed", "inconclusive", "timeout", "inconclusive", now)
	probe("stale", "success", "correct", "", "stale_run", now)
	probe("stale", "success", "correct", "", "stale_run", now)
	probe("old", "success", "correct", "", "passed", now.Add(-2*time.Hour))
	probe("old", "success", "correct", "", "passed", now.Add(-2*time.Hour))
	_, err = tx.ExecContext(ctx, `UPDATE usage_logs SET total_cost=3, account_stats_cost=2, account_rate_multiplier=0.5 WHERE account_id=$1 AND actual_cost>0`, account.ID)
	require.NoError(t, err)
	got, err := repo.ReadPrioritySchedulingSignals(ctx, service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "gpt-test", UsageSince: time.Now().Add(-time.Hour), QualitySince: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	require.Equal(t, 2, got[account.ID].Samples)
	require.Equal(t, 3, got[account.ID].ProfitSamples, "nonstreaming and missing TTFT still contribute financial records")
	require.InDelta(t, 3, got[account.ID].Revenue, 0.0001)
	require.InDelta(t, 3, got[account.ID].TheoreticalCost, 0.0001, "stored cost override multiplied once by historical rate")
	groupID := int64(999999)
	differentGroup, err := repo.ReadPrioritySchedulingSignals(ctx, service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "gpt-test", GroupID: &groupID, UsageSince: time.Now().Add(-time.Hour), QualitySince: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	require.Zero(t, differentGroup[account.ID].Revenue)
	require.Zero(t, differentGroup[account.ID].ProfitSamples)
	require.InDelta(t, 910, got[account.ID].P90TTFTMs, 0.01)
	require.Equal(t, 2, got[account.ID].QualitySamples)
	require.Equal(t, 1, got[account.ID].QualityPassed)
	windowStart, windowEnd := time.Now().Add(-time.Hour), time.Now().Add(3*time.Hour)
	recovery, err := repo.ReadPrioritySchedulingSignals(ctx, service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "gpt-test", GroupID: &groupID, UsageSince: windowStart, QualitySince: windowStart, TeamsWindows: []service.PriorityTeamsWindow{{AccountID: account.ID, Start: &windowStart, End: &windowEnd}}, TeamsWindowHours: 4})
	require.NoError(t, err)
	require.InDelta(t, 4, recovery[account.ID].TeamsRevenue, 0.0001, "Teams paid-window income crosses model/group boundaries")
	require.WithinDuration(t, windowStart, *recovery[account.ID].TeamsWindowStart, time.Millisecond)
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET enabled=false WHERE id=$1`, plan)
	require.NoError(t, err)
	got, err = repo.ReadPrioritySchedulingSignals(ctx, service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "gpt-test", UsageSince: time.Now().Add(-time.Hour), QualitySince: time.Now().Add(-time.Hour)})
	require.NoError(t, err)
	require.Zero(t, got[account.ID].QualitySamples)
}

func TestPriorityTeamsFirstUseAnchorSurvivesUsageRetention(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "teams-window@test.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-teams-window-test", Name: "test"})
	account := mustCreateAccount(t, client, &service.Account{Name: "Teams first-use", Platform: service.PlatformOpenAI})
	first := time.Now().Add(-3 * time.Hour).Truncate(time.Microsecond)
	add := func(at time.Time, charge float64) *service.UsageLog {
		t.Helper()
		log := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: fmt.Sprintf("teams-%d", time.Now().UnixNano()), Model: "any-model", ActualCost: charge, CreatedAt: at}
		_, err := repo.Create(ctx, log)
		require.NoError(t, err)
		return log
	}
	oldest := add(first, 20)
	add(first.Add(time.Hour), 15)
	query := service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "other-model", UsageSince: time.Now().Add(-time.Hour), QualitySince: time.Now().Add(-time.Hour), TeamsWindows: []service.PriorityTeamsWindow{{AccountID: account.ID}}, TeamsWindowHours: 4}
	signals, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.WithinDuration(t, first, *signals[account.ID].TeamsWindowStart, time.Millisecond)
	require.InDelta(t, 35, signals[account.ID].TeamsRevenue, 0.001)
	_, err = tx.ExecContext(ctx, `DELETE FROM usage_logs WHERE id=$1`, oldest.ID)
	require.NoError(t, err)
	signals, err = repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.WithinDuration(t, first, *signals[account.ID].TeamsWindowStart, time.Millisecond, "retention must not start a new four-hour purchase")
	require.WithinDuration(t, first.Add(4*time.Hour), *signals[account.ID].TeamsWindowEnd, time.Millisecond)
}
