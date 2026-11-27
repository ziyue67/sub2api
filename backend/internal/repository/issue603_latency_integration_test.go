//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestIssue603RealLatencyAggregationSQL(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	u := mustCreateUser(t, client, &service.User{Email: "issue603@synthetic.invalid"})
	k := mustCreateApiKey(t, client, &service.APIKey{UserID: u.ID, Key: "synthetic-issue603", Name: "synthetic"})
	a := mustCreateAccount(t, client, &service.Account{Name: "synthetic603"})
	now := time.Now().UTC().Add(-time.Minute)
	effort := "xhigh"
	usage := &service.UsageLog{UserID: u.ID, APIKeyID: k.ID, AccountID: a.ID, Model: "fixture", ReasoningEffort: &effort, InputTokens: 1, CacheReadTokens: 99, CreatedAt: now}
	_, err := repo.Create(ctx, usage)
	require.NoError(t, err)
	end := 200.0
	snapshot := requesttiming.Snapshot{BodyBytes: 2 << 20, Attempts: []requesttiming.Attempt{
		{Kind: "egress", Number: 1, Events: map[string]float64{"headers_written": 0, "request_written": 1}},
		{Kind: "http", Parent: 1, Number: 2, AccountID: a.ID, StartMS: 10, EndMS: &end, Events: map[string]float64{"headers_written": 20, "request_written": 50, "first_semantic": 90, "first_visible": 120}},
		{Kind: "http", Parent: 1, Number: 3, AccountID: a.ID + 1, StartMS: 210, Events: map[string]float64{"headers_written": 220}},
	}}
	derived := requesttiming.DeriveLatency(snapshot)
	snapshot.Latency = &derived
	raw, err := json.Marshal(snapshot)
	require.NoError(t, err)
	traceID := uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,detail) VALUES($1,$2,$3::jsonb)`, usage.ID, traceID, string(raw))
	require.NoError(t, err)
	_, err = repo.Create(ctx, &service.UsageLog{UserID: u.ID, APIKeyID: k.ID, AccountID: a.ID, Model: "historical", CreatedAt: now})
	require.NoError(t, err)
	groups, err := repo.ReadLatencyReport(ctx, service.LatencyReportFilter{Start: now.Add(-time.Minute), End: now.Add(time.Minute), UserID: u.ID, Model: "fixture"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	g := groups[0]
	require.Equal(t, "1-4MiB", g.BodyBucket)
	require.Equal(t, "95%+", g.CacheBucket)
	require.Equal(t, "xhigh", g.ReasoningEffort)
	require.EqualValues(t, 2, g.Attempts)
	require.EqualValues(t, 1, g.UploadSamples)
	require.EqualValues(t, 1, g.WaitSamples)
	require.Equal(t, 30.0, *g.UploadAvgMS)
	require.Equal(t, 40.0, *g.WaitAvgMS)
	require.Equal(t, 70.0, *g.VisibleAvgMS)
	require.Nil(t, g.ExtraUploadMS, "partial attempt costs cannot be wall-time totals")
	require.EqualValues(t, 1, g.LargeBodySwitches)
	require.EqualValues(t, 1, g.FailoverSamples)
	groups, err = repo.ReadLatencyReport(ctx, service.LatencyReportFilter{Start: now.Add(-time.Minute), End: now.Add(time.Minute), UserID: u.ID, Model: "historical"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.EqualValues(t, 1, groups[0].Requests)
	require.Zero(t, groups[0].Attempts)
	require.Nil(t, groups[0].UploadAvgMS)
	require.Zero(t, groups[0].FailoverSamples)
	for _, id := range []string{traceID, uuid.NewString()} {
		_, err = tx.ExecContext(ctx, `INSERT INTO request_connection_traces(trace_id,started_at,request_id,client_request_id,user_id,api_key_id,account_id,model,path,stream,detail)
 VALUES($1,$2,$3,'',$4,$5,$6,'fixture','/v1/responses',true,$7::jsonb)`, id, now, "unbilled:"+id, u.ID, k.ID, a.ID, string(raw))
		require.NoError(t, err)
	}
	groups, err = readLatencyGroups(ctx, tx, service.LatencyReportFilter{Start: now.Add(-time.Minute), End: now.Add(time.Minute), UserID: u.ID, Model: "fixture", IncludeUnbilled: true, Platform: a.Platform})
	require.NoError(t, err)
	require.Len(t, groups, 2, "ops retains an unbilled trace in an unknown cache/effort cohort")
	var requests, attempts int64
	for _, g := range groups {
		requests += g.Requests
		attempts += g.Attempts
	}
	require.EqualValues(t, 2, requests, "linked application trace and timing detail must not double count")
	require.EqualValues(t, 4, attempts)
	snapshot.Truncated = true
	partial := requesttiming.DeriveLatency(snapshot)
	snapshot.Latency = &partial
	raw, err = json.Marshal(snapshot)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO request_timing_details(usage_log_id,trace_id,detail) VALUES($1,$2,$3::jsonb)`, usage.ID, uuid.NewString(), string(raw))
	require.NoError(t, err)
	groups, err = repo.ReadLatencyReport(ctx, service.LatencyReportFilter{Start: now.Add(-time.Minute), End: now.Add(time.Minute), UserID: u.ID, Model: "fixture"})
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.EqualValues(t, 2, groups[0].Traces)
	require.EqualValues(t, 1, groups[0].FailoverSamples, "partial trace must not increase complete failover sample count")
	require.EqualValues(t, 1, groups[0].LargeBodySwitches, "partial transition counts cannot be mixed into complete sample totals")
}

func TestIssue603NetworkMinuteStorageAndBodySQL(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	now := time.Now().UTC().Truncate(time.Minute).Add(-2 * time.Minute)
	instance := uuid.NewString()
	zero := 0.0
	drops := int64(3)
	for i := range 2 {
		p := service.OpsNetworkPoint{At: now.Add(time.Duration(i) * time.Minute), Instance: instance, EgressStatus: "unavailable", TCPStatus: "no_segments", QdiscStatus: "unsupported_unprivileged_collector", WindowSeconds: 60}
		if i == 1 {
			p.EgressMbps = &zero
			p.EgressStatus = "measured"
			p.QdiscDrops = &drops
			p.QdiscStatus = "measured_root_egress_qdisc"
		}
		require.NoError(t, insertNetworkSample(ctx, tx, p))
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO request_connection_traces(trace_id,started_at,request_id,client_request_id,user_id,api_key_id,path,stream,detail) VALUES($1,$2,'synthetic603','',0,0,'/v1/responses',true,'{"body_bytes":1048577,"body_complete":false,"status":499}'::jsonb)`, uuid.NewString(), now)
	require.NoError(t, err)
	series, err := readNetworkSeries(ctx, tx, now, now.Add(2*time.Minute))
	require.NoError(t, err)
	var selected []service.OpsNetworkPoint
	for _, p := range series.Points {
		if p.Instance == instance {
			selected = append(selected, p)
		}
	}
	require.Len(t, selected, 2)
	require.Nil(t, selected[0].EgressMbps)
	require.NotNil(t, selected[1].EgressMbps)
	require.Equal(t, 0.0, *selected[1].EgressMbps)
	require.Nil(t, selected[0].QdiscDrops)
	require.Contains(t, selected[0].QdiscStatus, "unsupported")
	require.NotNil(t, selected[1].QdiscDrops)
	require.EqualValues(t, 3, *selected[1].QdiscDrops, "measured root qdisc drop delta must survive minute storage and aggregation")
	require.Equal(t, "measured_root_egress_qdisc", selected[1].QdiscStatus)
	var found bool
	for _, p := range series.BodyPoints {
		if p.At.Equal(now) {
			found = true
			require.GreaterOrEqual(t, p.Bytes, int64(1048577))
			require.GreaterOrEqual(t, p.Samples, int64(1))
		}
	}
	require.True(t, found, "failed/partial application reads must contribute to the real minute body-byte curve")
}
