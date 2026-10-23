//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Execute the actual UNION, casts, joins and precedence in PostgreSQL. These
// requests have no billing row, including HTTP 200 with a failed SSE terminal.
func TestConnectionsWithoutUsageAndLegacyErrors(t *testing.T) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	repo := &usageLogRepository{sql: tx}
	now := time.Now().UTC().Truncate(time.Millisecond)
	host := "diagnostics-" + uuid.NewString() + ".example.com"
	cases := []struct {
		state, source, network string
		detail                 string
	}{
		{"completed", "application", "", `{"version":1,"status":200,"terminal":"completed","canceled":true,"attempts":[{"error":"canceled","cleanup_canceled":true}]}`},
		{"failed", "application", "", `{"version":1,"status":200,"terminal":"failed","outcome":"failed"}`},
		{"downstream_write_failed", "application", "", `{"version":1,"status":200,"downstream_error":true}`},
		{"client_disconnected", "application", "", `{"version":1,"status":200,"client_disconnect":true}`},
		{"incomplete", "application", "", `{"version":1,"status":200,"terminal":"incomplete"}`},
		{"canceled", "application", "", `{"version":1,"status":200,"canceled":true}`},
		{"unknown", "application", "", `{"version":1,"status":200}`},
		{"edge_failed", "nginx", "upstream_unavailable", `{"version":1,"status":502}`},
		{"edge_completed", "nginx", "", `{"version":1,"status":200}`},
	}
	ids := map[string]string{}
	for i, c := range cases {
		id := uuid.NewString()
		ids[id] = c.state
		_, err = tx.ExecContext(ctx, `INSERT INTO request_connection_traces(trace_id,started_at,request_id,client_request_id,user_id,api_key_id,path,stream,detail,source,host,network_error) VALUES($1,$2,$3,'',0,0,'/v1/responses',true,$4::jsonb,$5,$6,$7)`, id, now.Add(time.Duration(i)*time.Millisecond), "req-"+id, c.detail, c.source, host, c.network)
		require.NoError(t, err)
	}
	filter := service.ConnectionTraceFilter{Start: now.Add(-time.Minute), End: now.Add(time.Minute), Host: host, Page: 1, PageSize: 50, State: "all"}
	got, err := repo.ListConnectionTraces(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got.Items, len(cases))
	for _, row := range got.Items {
		require.Equal(t, ids[row.TraceID], row.State)
		require.Zero(t, row.UserID)
	}
	filter.PageSize = 1
	got, err = repo.ListConnectionTraces(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.True(t, got.HasMore)
	filter.PageSize = 50
	filter.State = "abnormal"
	got, err = repo.ListConnectionTraces(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got.Items, 6)
	filter.Source = "edge"
	got, err = repo.ListConnectionTraces(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.Equal(t, "edge_failed", got.Items[0].State)

	// Existing failures without a usage/timing row must remain visible after
	// upgrading. No synthetic claim that their transport flags were collected.
	rid := "connection-legacy-" + uuid.NewString()
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs(request_id,request_path,error_phase,error_type,severity,status_code,created_at,duration_ms) VALUES($1,'/v1/responses','upstream','api_error','error',503,$2,3500)`, rid, now)
	require.NoError(t, err)
	filter = service.ConnectionTraceFilter{Start: now.Add(-time.Minute), End: now.Add(time.Minute), RequestID: rid, Page: 1, PageSize: 20}
	got, err = repo.ListConnectionTraces(ctx, filter)
	require.NoError(t, err)
	require.Len(t, got.Items, 1)
	require.True(t, got.Items[0].Legacy)
	require.Zero(t, got.Items[0].Detail.Version)
	require.Equal(t, "failed", got.Items[0].State)
}

func TestEdgeConnectionImportIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo := &usageLogRepository{db: integrationDB}
	id := uuid.NewString()
	defer func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM request_connection_traces WHERE trace_id=$1", id)
	}()
	row := service.ConnectionTrace{TraceID: id, Source: "nginx", StartedAt: time.Now(), RequestID: fmt.Sprintf("edge-%s", id), Host: "edge.example.com", Path: "/v1/responses", Detail: requesttiming.Snapshot{Status: 502}}
	require.NoError(t, repo.ImportEdgeConnections(ctx, []service.ConnectionTrace{row}))
	require.NoError(t, repo.ImportEdgeConnections(ctx, []service.ConnectionTrace{row}))
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*) FROM request_connection_traces WHERE trace_id=$1", id).Scan(&count))
	require.Equal(t, 1, count)
	raw, err := json.Marshal(row.Detail)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "Authorization")
}
