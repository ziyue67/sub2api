//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

type channelOperationsFixture struct {
	repo                                         *channelRepository
	channel, group, account, outsider, user, key int64
}

func newChannelOperationsFixture(t *testing.T) *channelOperationsFixture {
	t.Helper()
	ctx := context.Background()
	f := &channelOperationsFixture{repo: &channelRepository{db: integrationDB}}
	ch := &service.Channel{Name: fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano()), Status: service.StatusActive, BillingModelSource: service.BillingModelSourceRequested}
	require.NoError(t, f.repo.Create(ctx, ch))
	f.channel = ch.ID
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO groups(name,platform) VALUES($1,'openai') RETURNING id`, fmt.Sprintf("operations-%d", f.channel)).Scan(&f.group))
	require.NoError(t, f.repo.SetGroupIDs(ctx, f.channel, []int64{f.group}))
	a := mustCreateAccount(t, integrationEntClient, &service.Account{Name: "Operations account", Platform: service.PlatformOpenAI, Credentials: map[string]any{"api_key": "synthetic-fixture-only"}})
	f.account = a.ID
	a = mustCreateAccount(t, integrationEntClient, &service.Account{Name: "Outside account", Platform: service.PlatformOpenAI})
	f.outsider = a.ID
	_, err := integrationDB.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES($1,$2)`, f.account, f.group)
	require.NoError(t, err)
	u := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("operations-%d@test.invalid", f.channel)})
	f.user = u.ID
	k := mustCreateApiKey(t, integrationEntClient, &service.APIKey{UserID: u.ID, Key: fmt.Sprintf("operations-fixture-%d", f.channel), Name: "Operations fixture"})
	f.key = k.ID
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM ops_error_logs WHERE account_id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE account_id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE account_id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id IN ($1,$2)`, f.account, f.outsider)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM users WHERE id=$1`, f.user)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM channels WHERE id=$1`, f.channel)
		_, _ = integrationDB.ExecContext(ctx, `DELETE FROM groups WHERE id=$1`, f.group)
	})
	return f
}
func (f *channelOperationsFixture) catalog(t *testing.T) *service.ChannelOperationsCatalog {
	t.Helper()
	out, err := f.repo.ChannelOperationsCatalog(context.Background(), f.channel, service.ChannelOperationsQuery{Page: 1, PageSize: 20, UpstreamPage: 1, Membership: "all"})
	require.NoError(t, err)
	return out
}
func (f *channelOperationsFixture) mutate(t *testing.T, m service.ChannelOperationsMutation) error {
	t.Helper()
	revision := f.catalog(t).Revision
	m.Revision = &revision
	_, err := f.repo.MutateChannelOperations(context.Background(), f.channel, m)
	return err
}
func (f *channelOperationsFixture) upstream(t *testing.T, name string) int64 {
	t.Helper()
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "create_upstream", Name: name}))
	var id int64
	require.NoError(t, integrationDB.QueryRow(`SELECT id FROM channel_operation_upstreams WHERE channel_id=$1 AND name=$2`, f.channel, name).Scan(&id))
	return id
}
func TestChannelOperationsMembershipValidationConflictAndDetach(t *testing.T) {
	f := newChannelOperationsFixture(t)
	ctx := context.Background()
	u1 := f.upstream(t, "First")
	u2 := f.upstream(t, "Second")
	require.ErrorIs(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.outsider, UpstreamID: u1}), service.ErrChannelOperationsValidation)
	// A canonical channel's upstream cannot be attached through another channel.
	other := newChannelOperationsFixture(t)
	require.ErrorIs(t, other.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: other.account, UpstreamID: u1}), service.ErrChannelOperationsValidation)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u1}))
	require.ErrorIs(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u2}), service.ErrChannelOperationsConflict)
	require.ErrorIs(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u2, ExpectedUpstreamID: u1}), service.ErrChannelOperationsValidation)
	revision := f.catalog(t).Revision
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "update_upstream", UpstreamID: u1, Name: "Renamed"}))
	_, err := f.repo.MutateChannelOperations(ctx, f.channel, service.ChannelOperationsMutation{Action: "detach", AccountID: f.account, ExpectedUpstreamID: u1, Confirm: true, Revision: &revision})
	require.ErrorIs(t, err, service.ErrChannelOperationsConflict)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u2, ExpectedUpstreamID: u1, Confirm: true}))
	// Current group edits do not erase stale memberships; they remain detachable.
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1`, f.account)
	require.NoError(t, err)
	got := f.catalog(t)
	require.Len(t, got.Accounts, 1)
	require.False(t, got.Accounts[0].Eligible)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "detach", AccountID: f.account, ExpectedUpstreamID: u2, Confirm: true}))
	require.Empty(t, f.catalog(t).Accounts)
	var credentials string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT credentials->>'api_key' FROM accounts WHERE id=$1`, f.account).Scan(&credentials))
	require.Equal(t, "synthetic-fixture-only", credentials)
}
func TestChannelOperationsConcurrentChangesHaveOneWinner(t *testing.T) {
	f := newChannelOperationsFixture(t)
	revision := f.catalog(t).Revision
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"A", "B"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			_, err := f.repo.MutateChannelOperations(context.Background(), f.channel, service.ChannelOperationsMutation{Action: "create_upstream", Name: name, Revision: &revision})
			results <- err
		}(name)
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, service.ErrChannelOperationsConflict)
			conflict++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, conflict)
	require.Equal(t, int64(1), f.catalog(t).UpstreamTotal)
}
func TestChannelOperationsCatalogPaginationSoftDeleteAndChannelDelete(t *testing.T) {
	f := newChannelOperationsFixture(t)
	u1 := f.upstream(t, "A")
	_ = f.upstream(t, "B")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u1}))
	q := service.ChannelOperationsQuery{Page: 1, PageSize: 1, UpstreamPage: 2, Membership: "assigned", Search: fmt.Sprint(f.account)}
	page, err := f.repo.ChannelOperationsCatalog(context.Background(), f.channel, q)
	require.NoError(t, err)
	require.Len(t, page.Upstreams, 1)
	require.Equal(t, "B", page.Upstreams[0].Name)
	require.Equal(t, int64(2), page.UpstreamTotal)
	require.Equal(t, int64(1), page.Total)
	q.Page = 2
	page, err = f.repo.ChannelOperationsCatalog(context.Background(), f.channel, q)
	require.NoError(t, err)
	require.Empty(t, page.Accounts)
	require.Equal(t, int64(1), page.Total)
	_, err = integrationDB.Exec(`UPDATE accounts SET deleted_at=NOW() WHERE id=$1`, f.account)
	require.NoError(t, err)
	page = f.catalog(t)
	require.Len(t, page.Accounts, 1)
	require.False(t, page.Accounts[0].Eligible)
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "delete_upstream", UpstreamID: u1, Confirm: true}))
	require.Zero(t, f.catalog(t).Total)
	require.NoError(t, f.repo.Delete(context.Background(), f.channel))
	var n int
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM accounts WHERE id=$1`, f.account).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM account_groups WHERE account_id=$1`, f.account).Scan(&n))
	require.Equal(t, 1, n)
	require.NoError(t, integrationDB.QueryRow(`SELECT count(*) FROM channel_operation_upstreams WHERE channel_id=$1`, f.channel).Scan(&n))
	require.Zero(t, n)
}
func TestChannelOperationsMigrationReplayIsAdditive(t *testing.T) {
	f := newChannelOperationsFixture(t)
	u := f.upstream(t, "Kept")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u}))
	raw, err := migrations.FS.ReadFile("309_channel_operations.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		tx, err := integrationDB.Begin()
		require.NoError(t, err)
		_, err = tx.Exec(string(raw))
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
	got := f.catalog(t)
	require.Equal(t, int64(2), got.Revision)
	require.Len(t, got.Accounts, 1)
	require.Equal(t, u, got.Accounts[0].UpstreamID)
}

type operationsUsageData struct {
	Attribution string `json:"attribution"`
	Summary     struct {
		Requests, Samples int
		Unknown           int      `json:"unknown_samples"`
		P50               *float64 `json:"p50_ms"`
	}
	Total int
	Rows  []struct {
		DimensionID int64 `json:"dimension_id"`
		Requests    int
	}
	Lineage []struct {
		Public   string `json:"public_model"`
		Recorded string `json:"recorded_model"`
		Upstream string `json:"upstream_model"`
	}
}

func (f *channelOperationsFixture) usage(t *testing.T, filter service.ChannelOperationsFilter) operationsUsageData {
	t.Helper()
	snapshot := f.repo.ReadChannelOperations(context.Background(), filter)
	require.Equal(t, "ready", snapshot.Usage.Status, string(snapshot.Usage.Data))
	var out operationsUsageData
	require.NoError(t, json.Unmarshal(snapshot.Usage.Data, &out))
	return out
}
func (f *channelOperationsFixture) filter() service.ChannelOperationsFilter {
	return service.ChannelOperationsFilter{ChannelID: f.channel, GroupID: f.group, From: time.Now().Add(-time.Hour), Until: time.Now(), ModelScope: "public", Dimension: "upstream", Page: 1, PageSize: 20}
}
func (f *channelOperationsFixture) addUsage(t *testing.T, model, public, upstream string, ttft *int, stream bool, tokens int, at time.Time, group, channel *int64) {
	t.Helper()
	duration := 500
	_, err := NewUsageLogRepository(integrationEntClient, integrationDB).Create(context.Background(), &service.UsageLog{UserID: f.user, APIKeyID: f.key, AccountID: f.account, RequestID: fmt.Sprintf("ops-%d", time.Now().UnixNano()), Model: model, RequestedModel: public, UpstreamModel: &upstream, FirstTokenMs: ttft, DurationMs: &duration, Stream: stream, InputTokens: tokens, CreatedAt: at, GroupID: group, ChannelID: channel})
	require.NoError(t, err)
}
func TestChannelOperationsUsageIdentityBoundariesAndUnassignedDenominator(t *testing.T) {
	f := newChannelOperationsFixture(t)
	filter := f.filter()
	filter.From = filter.From.Truncate(time.Microsecond)
	filter.Until = filter.Until.Truncate(time.Microsecond)
	fast, slow := 100, 1000
	at := filter.From.Add(time.Minute)
	f.addUsage(t, "routing", "public", "provider", &fast, true, 10, at, &f.group, &f.channel)
	f.addUsage(t, "routing", "public", "provider", &slow, true, 10, at, &f.group, &f.channel)
	f.addUsage(t, "routing", "public", "provider", nil, true, 10, at, &f.group, &f.channel)
	f.addUsage(t, "routing", "public", "provider", &slow, false, 10, at, &f.group, &f.channel)
	f.addUsage(t, "routing", "public", "provider", &slow, true, 0, at, &f.group, &f.channel)            // zero-cost placeholder has no latency evidence
	f.addUsage(t, "routing", "public", "provider", &fast, true, 10, filter.Until, &f.group, &f.channel) // exclusive until
	f.addUsage(t, "routing", "public", "provider", &fast, true, 10, filter.From.Add(-time.Second), &f.group, &f.channel)
	f.addUsage(t, "routing", "public", "provider", &fast, true, 10, at, nil, &f.channel) // group filter excludes
	f.addUsage(t, "routing", "public", "provider", &fast, true, 10, at, &f.group, nil)   // no historical channel: never guess
	filter.Model = "public"
	got := f.usage(t, filter)
	require.Equal(t, 5, got.Summary.Requests)
	require.Equal(t, 2, got.Summary.Samples)
	require.Equal(t, 1, got.Summary.Unknown)
	require.NotNil(t, got.Summary.P50)
	require.InDelta(t, 550, *got.Summary.P50, .01)
	require.Equal(t, int64(0), got.Rows[0].DimensionID)
	filter.Model = "routing"
	require.Zero(t, f.usage(t, filter).Summary.Requests)
	filter.ModelScope = "recorded"
	require.Equal(t, 5, f.usage(t, filter).Summary.Requests)
	filter.ModelScope = "upstream"
	filter.Model = "provider"
	require.Equal(t, 5, f.usage(t, filter).Summary.Requests)
	filter.UpstreamID = -1
	require.Equal(t, 5, f.usage(t, filter).Summary.Requests)
	u := f.upstream(t, "Classified")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u}))
	require.Zero(t, f.usage(t, filter).Summary.Requests)
	filter.UpstreamID = u
	require.Equal(t, 5, f.usage(t, filter).Summary.Requests)
	filter.UpstreamID = 0
	got = f.usage(t, filter)
	require.Equal(t, 5, got.Summary.Requests)
	require.Equal(t, u, got.Rows[0].DimensionID)
	filter.Page = 2
	got = f.usage(t, filter)
	require.Empty(t, got.Rows)
	require.Equal(t, 1, got.Total)
	require.Equal(t, 5, got.Summary.Requests)
	// Reassignment changes only today's operational grouping, never historical
	// channel/model lineage or the original usage rows.
	var beforeHistory, afterHistory []byte
	historySQL := `SELECT jsonb_agg(jsonb_build_array(channel_id,requested_model,model,upstream_model,model_mapping_chain,created_at) ORDER BY id) FROM usage_logs WHERE account_id=$1`
	require.NoError(t, integrationDB.QueryRow(historySQL, f.account).Scan(&beforeHistory))
	replacement := f.upstream(t, "Reassigned")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: replacement, ExpectedUpstreamID: u, Confirm: true}))
	require.NoError(t, integrationDB.QueryRow(historySQL, f.account).Scan(&afterHistory))
	require.JSONEq(t, string(beforeHistory), string(afterHistory))
	filter.Page = 1
	filter.UpstreamID = u
	require.Zero(t, f.usage(t, filter).Summary.Requests)
	filter.UpstreamID = replacement
	got = f.usage(t, filter)
	require.Equal(t, 5, got.Summary.Requests)
	require.Equal(t, "current_operational_membership", got.Attribution)
	filter.UpstreamID = 0
	filter.Model = "absent"
	got = f.usage(t, filter)
	require.Zero(t, got.Summary.Requests)
	require.Nil(t, got.Summary.P50)
}

func TestChannelOperationsQualityMeasuredRoundHistoryAndScope(t *testing.T) {
	f := newChannelOperationsFixture(t)
	ctx := context.Background()
	filter := f.filter()
	filter.ModelScope = "upstream"
	filter.Model = "provider-model"
	var plan int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans(account_id,model_id,cron_expression,enabled,pelican_config) VALUES($1,'edited-current-plan','*/30 * * * *',false,'{"quality":{}}') RETURNING id`, f.account).Scan(&plan))
	at := filter.From.Add(time.Minute)
	probe := func(round, status, verdict, action, message, model string, parallel any) {
		t.Helper()
		cfgFields := map[string]any{"quality": map[string]any{}, "model_id": model}
		if parallel != nil {
			cfgFields["parallel_count"] = parallel
		}
		cfg, _ := json.Marshal(cfgFields)
		var judgment any
		if verdict != "" {
			raw, _ := json.Marshal(map[string]string{"verdict": verdict})
			judgment = string(raw)
		}
		_, err := integrationDB.ExecContext(ctx, `INSERT INTO scheduled_test_results(plan_id,status,started_at,finished_at,created_at,pelican_config,quality_round_id,quality_action,quality_judgment,error_message) VALUES($1,$2,$3,$3,$3,$4,$5,$6,$7,$8)`, plan, status, at, string(cfg), round, action, judgment, message)
		require.NoError(t, err)
	}
	for i := 0; i < 2; i++ {
		probe("pass", "success", "correct", "passed", "", "provider-model", 2)
	}
	probe("degraded", "success", "correct", "failure_counted:1/2", "", "provider-model", 2)
	probe("degraded", "failed", "incorrect", "failure_counted:1/2", "answer_mismatch", "provider-model", 2)
	probe("partial", "success", "correct", "passed", "", "provider-model", 2)
	probe("partial-wrong", "failed", "incorrect", "failure_counted:1/2", "answer_mismatch", "provider-model", 2)
	probe("transport", "success", "correct", "inconclusive", "", "provider-model", 2)
	probe("transport", "failed", "inconclusive", "inconclusive", "timeout", "provider-model", 2)
	probe("missing-verdict", "success", "correct", "passed", "", "provider-model", 2)
	probe("missing-verdict", "success", "", "passed", "", "provider-model", 2)
	probe("stale", "success", "correct", "stale_run", "", "provider-model", 1)
	probe("bad-count", "success", "correct", "passed", "", "provider-model", "1")
	probe("missing-count", "success", "correct", "passed", "", "provider-model", nil)
	probe("", "failed", "incorrect", "failure_counted:1/2", "answer_mismatch", "provider-model", 1)
	probe("multi-model", "success", "correct", "passed", "", "other-provider", 1)
	probe("no-frozen-model", "success", "correct", "passed", "", "", 1)
	type data struct {
		Summary struct{ Rounds, Passed, Degraded, Inconclusive int }
		Rows    []struct {
			ID      int64
			Model   string `json:"measured_model"`
			Outcome string
		}
		More bool `json:"has_more"`
	}
	read := func() data {
		t.Helper()
		snapshot := f.repo.ReadChannelOperations(ctx, filter)
		require.Equal(t, "ready", snapshot.Quality.Status, string(snapshot.Quality.Data))
		var out data
		require.NoError(t, json.Unmarshal(snapshot.Quality.Data, &out))
		return out
	}
	got := read()
	require.Equal(t, 10, got.Summary.Rounds)
	require.Equal(t, 1, got.Summary.Passed)
	require.Equal(t, 1, got.Summary.Degraded)
	require.Equal(t, 8, got.Summary.Inconclusive)
	// The frozen result model is authoritative even when the plan is edited/disabled.
	filter.Model = "edited-current-plan"
	require.Zero(t, read().Summary.Rounds)
	filter.Model = "provider-model"
	filter.ModelScope = "public"
	snapshot := f.repo.ReadChannelOperations(ctx, filter)
	require.Equal(t, "unsupported_scope", snapshot.Quality.Status)
	require.JSONEq(t, "null", string(snapshot.Quality.Data))
	filter.ModelScope = "upstream"
	filter.PageSize = 2
	got = read()
	require.Len(t, got.Rows, 2)
	require.True(t, got.More)
	require.Equal(t, 10, got.Summary.Rounds)
	filter.QualityBefore = got.Rows[1].ID
	older := read()
	require.Len(t, older.Rows, 2)
	require.Less(t, older.Rows[0].ID, got.Rows[1].ID)
	require.Equal(t, 10, older.Summary.Rounds)
	filter.GroupID = f.group + 999999
	require.Zero(t, read().Summary.Rounds)
	filter.GroupID = f.group
	filter.UpstreamID = -1
	require.Equal(t, 10, read().Summary.Rounds)
	upstream := f.upstream(t, "Quality upstream")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: upstream}))
	require.Zero(t, read().Summary.Rounds)
	filter.UpstreamID = upstream
	require.Equal(t, 10, read().Summary.Rounds)
	// A quality action can remove canonical groups. Explicitly registered members
	// retain their measured history without silently restoring those groups.
	_, err := integrationDB.Exec(`DELETE FROM account_groups WHERE account_id=$1`, f.account)
	require.NoError(t, err)
	filter.GroupID = 0
	require.Equal(t, 10, read().Summary.Rounds)
	filter.GroupID = f.group
	require.Zero(t, read().Summary.Rounds)

}

func TestChannelOperationsErrorsUseExplicitCurrentCohortAndActualGroup(t *testing.T) {
	f := newChannelOperationsFixture(t)
	filter := f.filter()
	filter.Model = "public"
	at := filter.From.Add(time.Minute)
	for _, countTokens := range []bool{false, true} {
		_, err := integrationDB.Exec(`INSERT INTO ops_error_logs(account_id,group_id,model,requested_model,upstream_model,error_phase,error_type,status_code,upstream_status_code,is_count_tokens,created_at) VALUES($1,$2,'recorded','public','provider','upstream','http_error',502,503,$3,$4)`, f.account, f.group, countTokens, at)
		require.NoError(t, err)
	}
	type data struct {
		Records int
		FiveXX  int      `json:"upstream_5xx"`
		Rate    *float64 `json:"error_rate"`
	}
	read := func() data {
		t.Helper()
		got := f.repo.ReadChannelOperations(context.Background(), filter)
		require.Equal(t, "ready", got.Errors.Status)
		var d data
		require.NoError(t, json.Unmarshal(got.Errors.Data, &d))
		return d
	}
	got := read()
	require.Equal(t, 1, got.Records)
	require.Equal(t, 1, got.FiveXX)
	require.Nil(t, got.Rate)
	filter.Model = "recorded"
	require.Zero(t, read().Records)
	filter.ModelScope = "recorded"
	require.Equal(t, 1, read().Records)
	filter.Model = "provider"
	filter.ModelScope = "upstream"
	require.Equal(t, 1, read().Records)
	filter.GroupID = f.group + 999999
	require.Zero(t, read().Records)
	filter.GroupID = f.group
	filter.UpstreamID = -1
	require.Equal(t, 1, read().Records)
	u := f.upstream(t, "Error cohort")
	require.NoError(t, f.mutate(t, service.ChannelOperationsMutation{Action: "assign", AccountID: f.account, UpstreamID: u}))
	require.Zero(t, read().Records)
	filter.UpstreamID = u
	require.Equal(t, 1, read().Records)
	_, err := integrationDB.Exec(`DELETE FROM account_groups WHERE account_id=$1`, f.account)
	require.NoError(t, err)
	require.Equal(t, 1, read().Records, "registered operations cohort survives canonical group removal")

}

// Model rows use the canonical plan-round completeness envelope before any
// requested model filter. A model-level pass must never imply a whole-plan pass.
func TestChannelOperationsQualityWaitsForAllFrozenModels(t *testing.T) {
	f := newChannelOperationsFixture(t)
	ctx := context.Background()
	filter := f.filter()
	filter.ModelScope = "upstream"
	filter.Model = "round-a"
	var plan int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO scheduled_test_plans(account_id,model_id,cron_expression,enabled,pelican_config) VALUES($1,'edited-current-model','*/30 * * * *',false,'{"quality":{}}') RETURNING id`, f.account).Scan(&plan))
	probe := func(model, status, verdict, action, message string) int64 {
		t.Helper()
		cfg, _ := json.Marshal(map[string]any{"quality": map[string]any{}, "model_id": model, "model_ids": []string{"round-a", "round-b"}, "parallel_count": 1})
		judgment, _ := json.Marshal(map[string]string{"verdict": verdict})
		var id int64
		require.NoError(t, integrationDB.QueryRow(`INSERT INTO scheduled_test_results(plan_id,status,started_at,finished_at,created_at,pelican_config,quality_round_id,quality_action,quality_judgment,error_message) VALUES($1,$2,$3,$3,$3,$4,'two-model-review',$5,$6,$7) RETURNING id`, plan, status, filter.From.Add(time.Minute), string(cfg), action, string(judgment), message).Scan(&id))
		return id
	}
	type row struct {
		Model    string `json:"measured_model"`
		Outcome  string
		Complete bool `json:"round_complete"`
		Probes   int  `json:"round_probes"`
		Expected int  `json:"round_expected"`
	}
	type data struct {
		Summary struct{ Rounds, Passed, Degraded, Inconclusive int }
		Rows    []row
	}
	read := func() data {
		t.Helper()
		snapshot := f.repo.ReadChannelOperations(ctx, filter)
		require.Equal(t, "ready", snapshot.Quality.Status)
		var out data
		require.NoError(t, json.Unmarshal(snapshot.Quality.Data, &out))
		return out
	}
	canonical := func() *service.QualityHistoryResult {
		t.Helper()
		page, err := service.NewScheduledTestService(nil, NewScheduledTestResultRepository(integrationDB)).ListQualityHistory(ctx, 0)
		require.NoError(t, err)
		for _, item := range page.Items {
			if item.PlanID == plan {
				return item
			}
		}
		t.Fatal("canonical plan history missing")
		return nil
	}
	probe("round-a", "success", "correct", "passed", "")
	got := read()
	require.Equal(t, 0, got.Summary.Passed, "missing peer model cannot pass")
	require.Equal(t, 1, got.Summary.Inconclusive)
	require.Len(t, got.Rows, 1)
	require.False(t, got.Rows[0].Complete)
	require.Equal(t, 1, got.Rows[0].Probes)
	require.Equal(t, 2, got.Rows[0].Expected)
	history := canonical()
	require.Equal(t, 2, history.TotalCount)
	require.NotEqual(t, "success", history.Status, "canonical full plan also rejects the missing-model round")
	// Filtering only round-a must not conceal a transport failure in its peer.
	// Once both rows exist, round-a is a measured model pass, while the complete
	// plan remains non-passing; the API/UI explicitly distinguish these scopes.
	peer := probe("round-b", "failed", "inconclusive", "inconclusive", "timeout")
	got = read()
	require.Equal(t, 1, got.Summary.Passed)
	require.True(t, got.Rows[0].Complete)
	require.Equal(t, 2, got.Rows[0].Probes)
	require.NotEqual(t, "success", canonical().Status)
	filter.Model = ""
	got = read()
	require.Equal(t, 2, got.Summary.Rounds)
	require.Equal(t, 1, got.Summary.Passed)
	require.Equal(t, 1, got.Summary.Inconclusive)
	require.Zero(t, got.Summary.Degraded)
	_, err := integrationDB.Exec(`UPDATE scheduled_test_results SET status='success',quality_action='passed',quality_judgment='{"verdict":"correct"}',error_message='' WHERE id=$1`, peer)
	require.NoError(t, err)
	got = read()
	require.Equal(t, 2, got.Summary.Passed)
	require.Equal(t, "success", canonical().Status)
}
