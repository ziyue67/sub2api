//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestPR610MeasuredCohortPreservesLegacyOperations(t *testing.T) {
	f := newChannelOperationsFixture(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	_, err := integrationDB.Exec(`UPDATE accounts SET type='apikey',credentials='{"base_url":"https://synthetic.invalid/v1"}',extra='{"cost_multiplier":0.5}' WHERE id=$1`, f.account)
	require.NoError(t, err)
	repo := newUsageLogRepositoryWithSQL(integrationEntClient, integrationDB)
	for i := 0; i < 4; i++ {
		first, duration := 2000, 5000
		row := &service.UsageLog{UserID: f.user, APIKeyID: f.key, AccountID: f.account, GroupID: &f.group, ChannelID: &f.channel, RequestID: fmt.Sprintf("pr610-%d-%d", f.account, i), Model: "queue-fixture", Stream: true, InputTokens: 1, FirstTokenMs: &first, DurationMs: &duration, TotalCost: 2, ActualCost: 1, RateMultiplier: 1, CreatedAt: now.Add(-time.Minute)}
		if i > 0 {
			q := 30000
			if i == 2 {
				q = 0
			}
			row.QueueWaitMs = &q
		}
		if i >= 2 {
			q := int64(*row.QueueWaitMs)
			sf := int64(first) + q
			ff := int64(first)
			row.QueueTiming = &service.QueueTiming{Status: "measured", Source: "http_ingress_responses", Scope: "pre_final_forward", ServerFirstTokenMs: &sf, ServerDurationMs: int64(duration) + q, ForwardFirstTokenMs: &ff, ForwardDurationMs: int64(duration)}
		}
		inserted, err := repo.Create(ctx, row)
		require.NoError(t, err)
		require.True(t, inserted)
		saved, err := repo.GetByID(ctx, row.ID)
		require.NoError(t, err)
		require.Equal(t, row.QueueWaitMs, saved.QueueWaitMs)
		require.Equal(t, row.QueueTiming, saved.QueueTiming)
		require.Equal(t, row.FirstTokenMs, saved.FirstTokenMs)
	}
	check := func(raw json.RawMessage) {
		t.Helper()
		var data struct{ Summary map[string]any }
		require.NoError(t, json.Unmarshal(raw, &data))
		s := data.Summary
		require.Equal(t, 2000.0, s["p90_ms"], "legacy operations TTFT must retain its original population and values")
		require.Equal(t, 5000.0, s["duration_p90_ms"])
		require.Equal(t, 2.0, s["queue_measured_requests"])
		require.Equal(t, 2.0, s["queue_unknown_requests"], "historical NULL and pre-provenance rows cannot claim measured coverage")
		require.Equal(t, 2.0, s["queue_inclusive_samples"])
		require.Equal(t, 29000.0, s["queue_inclusive_p90_ms"])
		require.Equal(t, []any{"http_ingress_responses"}, s["queue_timing_sources"])
	}
	q := service.UpstreamOperationsFilter{From: now.Add(-time.Hour), Until: now, AccountID: f.account, GroupID: f.group, Dimension: "account", ModelScope: "public", Sort: "requests", Direction: "desc", Page: 1, PageSize: 20}
	upstream, err := f.repo.ReadUpstreamOperations(ctx, q)
	require.NoError(t, err)
	check(upstream.Usage)
	profit, err := f.repo.ReadProfitOperations(ctx, q)
	require.NoError(t, err)
	check(profit.Usage)
	var money struct {
		Summary struct {
			Income float64 `json:"request_income"`
			Cost   float64 `json:"estimated_cost"`
			Profit float64 `json:"estimated_profit"`
		}
	}
	require.NoError(t, json.Unmarshal(profit.Usage, &money))
	require.Equal(t, 4.0, money.Summary.Income)
	require.Equal(t, 4.0, money.Summary.Cost)
	require.Zero(t, money.Summary.Profit, "queue timing must not change profit provenance")
	channel := f.repo.ReadChannelOperations(ctx, service.ChannelOperationsFilter{From: q.From, Until: q.Until, ChannelID: f.channel, AccountID: f.account, GroupID: f.group, Dimension: "account", ModelScope: "public", Page: 1, PageSize: 20})
	require.Equal(t, "ready", channel.Usage.Status)
	check(channel.Usage.Data)
	var provenance map[string]any
	require.NoError(t, json.Unmarshal(channel.Usage.Data, &provenance))
	require.Equal(t, "legacy_forward_stream_ttft", provenance["latency_measurement"])
	// Values beyond int4's maximum must remain valid in the additive bigint cohort.
	_, err = integrationDB.Exec(`UPDATE usage_logs SET duration_ms=2147483647,queue_timing=jsonb_set(queue_timing,'{server_duration_ms}','2151083647'::jsonb) WHERE account_id=$1 AND queue_timing IS NOT NULL`, f.account)
	require.NoError(t, err)
	upstream, err = f.repo.ReadUpstreamOperations(ctx, q)
	require.NoError(t, err)
	var large struct{ Summary map[string]any }
	require.NoError(t, json.Unmarshal(upstream.Usage, &large))
	require.Equal(t, 2151083647.0, large.Summary["queue_inclusive_duration_p90_ms"])
}

func TestPR610Migrations320321322JointUpgradeIdempotencyAndLegacyRead(t *testing.T) {
	ctx := context.Background()
	for _, first := range []string{"fresh", "320_ops_network_minutes.sql", "321_usage_log_queue_wait.sql"} {
		t.Run(first, func(t *testing.T) {
			database := fmt.Sprintf("pr610_%d", time.Now().UnixNano())
			_, err := integrationDB.Exec(`CREATE DATABASE ` + database)
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = integrationDB.Exec(`DROP DATABASE ` + database) })
			u, err := url.Parse(integrationPostgresDSN)
			require.NoError(t, err)
			u.Path = "/" + database
			db, err := sql.Open("postgres", u.String())
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			_, err = db.Exec(`CREATE TABLE usage_logs(id bigserial PRIMARY KEY, first_token_ms integer, duration_ms integer); INSERT INTO usage_logs(first_token_ms,duration_ms)VALUES(2000,5000)`)
			require.NoError(t, err)
			files := fstest.MapFS{}
			for _, name := range []string{"320_ops_network_minutes.sql", "321_usage_log_queue_wait.sql", "322_usage_log_queue_timing.sql"} {
				raw, err := migrations.FS.ReadFile(name)
				require.NoError(t, err)
				files[name] = &fstest.MapFile{Data: raw}
			}
			if first != "fresh" {
				require.NoError(t, applyMigrationsFS(ctx, db, fstest.MapFS{first: files[first]}))
			}
			require.NoError(t, applyMigrationsFS(ctx, db, files))
			require.NoError(t, applyMigrationsFS(ctx, db, files))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&count))
			require.Equal(t, 3, count, "321 already applied must not prevent later insertion of missing 320")
			var queue sql.NullInt64
			var timing []byte
			var legacyFirst, legacyDuration int
			require.NoError(t, db.QueryRow(`SELECT first_token_ms,duration_ms,queue_wait_ms,queue_timing FROM usage_logs WHERE id=1`).Scan(&legacyFirst, &legacyDuration, &queue, &timing))
			require.False(t, queue.Valid)
			require.Nil(t, timing)
			// Old binaries' projection remains readable; rollback does not remove columns.
			require.NoError(t, db.QueryRow(`SELECT first_token_ms,duration_ms FROM usage_logs WHERE id=1`).Scan(&legacyFirst, &legacyDuration))
			require.Equal(t, 2000, legacyFirst)
			require.Equal(t, 5000, legacyDuration)
			_, err = db.Exec(`INSERT INTO ops_network_minutes(instance_id,sampled_at,egress_status,tcp_status,qdisc_status,window_seconds) VALUES('00000000-0000-0000-0000-000000000610',now(),'unknown','unknown','unknown',60)`)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE usage_logs SET queue_wait_ms=0,queue_timing='{"status":"measured","source":"fixture"}'`)
			require.NoError(t, err)
			require.NoError(t, db.QueryRow(`SELECT queue_wait_ms FROM usage_logs WHERE id=1`).Scan(&queue))
			require.True(t, queue.Valid)
			require.Zero(t, queue.Int64)
		})
	}
}
