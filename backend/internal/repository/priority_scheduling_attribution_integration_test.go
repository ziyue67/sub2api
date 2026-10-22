//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPriorityQuantileSQLRejectsUnsafeAttribution(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "attribution@test.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-local-attribution-fixture", Name: "fixture"})
	account := mustCreateAccount(t, client, &service.Account{Name: "fixture", Platform: service.PlatformOpenAI})
	baseline := func() map[string]any {
		return map[string]any{"version": 1, "status": 200, "outcome": "success", "terminal": "completed", "truncated": false, "body_complete": true, "canceled": false, "client_disconnect": false, "downstream_error": false,
			"events":   map[string]any{"first_visible": 50000},
			"attempts": []any{map[string]any{"kind": "http", "account_id": account.ID, "start_ms": 1000, "status": 200, "terminal": "completed", "events": map[string]any{"first_semantic": 21000}}}}
	}
	mutations := []struct {
		name   string
		change func(map[string]any)
	}{
		{"incomplete", func(d map[string]any) { d["terminal"] = "incomplete" }},
		{"failed", func(d map[string]any) { d["outcome"] = "error" }},
		{"truncated", func(d map[string]any) { d["truncated"] = true }},
		{"canceled", func(d map[string]any) { d["canceled"] = true }},
		{"disconnect", func(d map[string]any) { d["client_disconnect"] = true }},
		{"downstream_error", func(d map[string]any) { d["downstream_error"] = true }},
		{"incomplete_upload", func(d map[string]any) { d["body_complete"] = false }},
		{"version", func(d map[string]any) { d["version"] = 2 }},
		{"write_before_semantic", func(d map[string]any) { d["events"].(map[string]any)["first_write"] = 500 }},
		{"malformed_write", func(d map[string]any) { d["events"].(map[string]any)["first_write"] = "bad" }},
		{"wrong_account", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["account_id"] = account.ID + 999 }},
		{"parent_only", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["kind"] = "egress" }},
		{"attempt_failed", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["status"] = 503 }},
		{"attempt_incomplete", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["terminal"] = "incomplete" }},
		{"attempt_error", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["error"] = "timeout" }},
		{"negative_start", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["start_ms"] = -1 }},
		{"malformed_start", func(d map[string]any) { d["attempts"].([]any)[0].(map[string]any)["start_ms"] = "bad" }},
		{"reversed_clock", func(d map[string]any) {
			d["attempts"].([]any)[0].(map[string]any)["events"].(map[string]any)["first_semantic"] = 500
		}},
		{"malformed_clock", func(d map[string]any) {
			d["attempts"].([]any)[0].(map[string]any)["events"].(map[string]any)["first_semantic"] = "bad"
		}},
		{"later_failed_attempt", func(d map[string]any) {
			d["attempts"] = append(d["attempts"].([]any), map[string]any{"kind": "http", "account_id": account.ID + 999, "status": 503})
		}},
		{"later_uninstrumented_parent", func(d map[string]any) {
			d["attempts"] = append(d["attempts"].([]any), map[string]any{"kind": "egress", "account_id": account.ID, "status": 200})
		}},
		{"malformed_attempts", func(d map[string]any) { d["attempts"] = map[string]any{} }},
	}
	now := time.Now().Add(-time.Second)
	badAt := now.Add(-11 * time.Minute)
	add := func(name string, detail map[string]any, at time.Time) int64 {
		t.Helper()
		legacy := 1
		usage := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: fmt.Sprintf("attrib-%s", name), Model: "fixture", InputTokens: 1, Stream: true, FirstTokenMs: &legacy, CreatedAt: at}
		_, err := repo.Create(ctx, usage)
		require.NoError(t, err)
		if detail != nil {
			raw, err := json.Marshal(detail)
			require.NoError(t, err)
			_, err = tx.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,detail) VALUES($1,$2,$3)`, usage.ID, uuid.NewString(), string(raw))
			require.NoError(t, err)
		}
		return usage.ID
	}
	// Five older bad samples and new fast evidence keep P90 slow without renewal.
	for i := 0; i < 5; i++ {
		add(fmt.Sprint(i), baseline(), badAt)
	}
	fast := baseline()
	fast["attempts"].([]any)[0].(map[string]any)["events"].(map[string]any)["first_semantic"] = 2000
	add("fast", fast, now)
	add("missing", nil, now)
	for _, tc := range mutations {
		d := baseline()
		tc.change(d)
		add(tc.name, d, now)
	}
	duplicate := baseline()
	id := add("duplicate", duplicate, now)
	raw, err := json.Marshal(duplicate)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,detail) VALUES($1,$2,$3)`, id, uuid.NewString(), string(raw))
	require.NoError(t, err)
	query := service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "fixture", LatencyPercentile: 90, LatencyMeasurement: "attributed_v1", TargetTTFTMs: 15000, UsageSince: now.Add(-time.Hour), UsageUntil: now.Add(time.Second), QualitySince: now.Add(-time.Hour)}
	signals, err := repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	signal := signals[account.ID]
	require.Equal(t, 6, signal.Samples)
	require.Equal(t, len(mutations)+2, signal.LatencyUnknownSamples)
	require.InDelta(t, 20000, signal.P90TTFTMs, .01)
	require.NotNil(t, signal.SelectedTTFTMs)
	require.NotNil(t, signal.LatestBadLatencyAt)
	require.WithinDuration(t, badAt, *signal.LatestBadLatencyAt, time.Millisecond)
	require.WithinDuration(t, now, *signal.LatestLatencyAt, time.Millisecond)
	query.Model = "absent"
	signals, err = repo.ReadPrioritySchedulingSignals(ctx, query)
	require.NoError(t, err)
	require.Nil(t, signals[account.ID].SelectedTTFTMs)
}
