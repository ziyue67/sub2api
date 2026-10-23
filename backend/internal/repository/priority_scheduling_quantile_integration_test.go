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

func TestPriorityQuantileSQLUsesAttributableAttemptNotClientTiming(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "quantile@test.invalid"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-offline-quantile", Name: "test"})
	account := mustCreateAccount(t, client, &service.Account{Name: "quantile", Platform: service.PlatformOpenAI})
	for i, delay := range []int{1000, 2000, 10000, 20000, 40000} {
		legacy := 99000
		log := &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: fmt.Sprintf("quantile-%d", i), Model: "m", InputTokens: 1, Stream: true, FirstTokenMs: &legacy, CreatedAt: time.Now().Add(-time.Duration(10-i) * time.Minute)}
		_, err := repo.Create(ctx, log)
		require.NoError(t, err)
		detail := map[string]any{"version": 1, "status": 200, "outcome": "success", "terminal": "completed", "truncated": false, "body_complete": true, "canceled": false, "client_disconnect": false, "downstream_error": false, "body_read_ms": 300000, "downstream_write_ms": 900000,
			"events": map[string]any{"first_visible": 400000 + delay},
			"attempts": []any{
				map[string]any{"kind": "http", "account_id": account.ID + 999, "start_ms": 0, "status": 503, "error": "", "events": map[string]any{"first_semantic": 90000}},
				map[string]any{"kind": "egress", "account_id": account.ID, "start_ms": 0, "status": 200, "events": map[string]any{"first_semantic": 100000}},
				map[string]any{"kind": "http", "account_id": account.ID, "start_ms": 1000, "status": 200, "error": "", "terminal": "completed", "events": map[string]any{"first_semantic": 1000 + delay}},
			}}
		b, err := json.Marshal(detail)
		require.NoError(t, err)
		_, err = tx.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,detail) VALUES($1,$2,$3)`, log.ID, uuid.NewString(), string(b))
		require.NoError(t, err)
	}
	missing := 1
	_, err := repo.Create(ctx, &service.UsageLog{UserID: user.ID, APIKeyID: key.ID, AccountID: account.ID, RequestID: "quantile-missing", Model: "m", InputTokens: 1, Stream: true, FirstTokenMs: &missing, CreatedAt: time.Now().Add(-time.Minute)})
	require.NoError(t, err)
	for _, tc := range []struct {
		q    int
		want float64
	}{{50, 10000}, {70, 18000}, {90, 32000}, {95, 36000}} {
		query := service.PrioritySchedulingQuery{AccountIDs: []int64{account.ID}, Model: "m", UsageSince: time.Now().Add(-time.Hour), QualitySince: time.Now().Add(-time.Hour)}
		// JSON keeps this regression executable against the old struct, before implementation.
		b, _ := json.Marshal(map[string]any{"LatencyPercentile": tc.q, "LatencyMeasurement": "attributed_v1", "TargetTTFTMs": 15000})
		require.NoError(t, json.Unmarshal(b, &query))
		signals, err := repo.ReadPrioritySchedulingSignals(ctx, query)
		require.NoError(t, err)
		signal := signals[account.ID]
		require.Equal(t, 5, signal.Samples, "missing trace cannot silently use a 1ms legacy sample")
		raw, _ := json.Marshal(signal)
		var out map[string]any
		require.NoError(t, json.Unmarshal(raw, &out))
		require.InDelta(t, tc.want, out["selected_ttft_ms"], .01)
		require.InDelta(t, 32000, signal.P90TTFTMs, .01, "P90 stays P90 when selecting P50/P70/P95")
		require.Equal(t, float64(1), out["latency_unknown_samples"])
		require.Equal(t, float64(5), out["experience_samples"])
		require.Greater(t, out["experience_p90_ms"].(float64), 400000.0)
		query.Model = "other"
		signals, err = repo.ReadPrioritySchedulingSignals(ctx, query)
		require.NoError(t, err)
		require.Zero(t, signals[account.ID].Samples)
	}
}
