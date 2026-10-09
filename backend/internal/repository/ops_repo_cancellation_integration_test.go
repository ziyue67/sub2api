//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsClientCancellationMetricsAndDuration(t *testing.T) {
	ctx := context.Background()
	repo := NewOpsRepository(integrationDB).(*opsRepository)
	start := time.Date(2003, 1, 2, 3, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	day := start.Truncate(24 * time.Hour)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_error_logs WHERE request_id LIKE 'issue141-%'")
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_metrics_hourly WHERE bucket_start=$1", start)
		_, _ = integrationDB.ExecContext(ctx, "DELETE FROM ops_metrics_daily WHERE bucket_date=$1", day)
	})
	elapsed, zero := int64(24477), int64(0)
	rows := []*service.OpsInsertErrorLogInput{
		{RequestID: "issue141-canceled", Platform: "openai", ErrorPhase: "request", ErrorType: service.OpsClientCanceledCode, ErrorOwner: "client", StatusCode: 499, DurationMs: &elapsed, CreatedAt: start.Add(time.Minute)},
		{RequestID: "issue141-upstream", Platform: "openai", ErrorPhase: "upstream", ErrorType: "api_error", ErrorOwner: "provider", StatusCode: 502, DurationMs: &zero, CreatedAt: start.Add(2 * time.Minute)},
		{RequestID: "issue141-invalid", Platform: "openai", ErrorPhase: "request", ErrorType: "invalid_request_error", ErrorOwner: "client", StatusCode: 400, CreatedAt: start.Add(3 * time.Minute)},
		{RequestID: "issue141-recovered", Platform: "openai", ErrorPhase: "upstream", ErrorType: "upstream_error", ErrorOwner: "provider", StatusCode: 200, CreatedAt: start.Add(4 * time.Minute)},
	}
	for _, row := range rows {
		row.Severity = "P3"
	}
	_, err := repo.InsertErrorLog(ctx, rows[0])
	require.NoError(t, err)
	count, err := repo.BatchInsertErrorLogs(ctx, rows[1:])
	require.NoError(t, err)
	require.EqualValues(t, 3, count)
	for _, row := range rows[:3] {
		details, total, err := repo.ListRequestDetails(ctx, &service.OpsRequestDetailFilter{StartTime: &start, EndTime: &end, Kind: "error", RequestID: row.RequestID})
		require.NoError(t, err)
		require.EqualValues(t, 1, total)
		require.Len(t, details, 1)
		if row.DurationMs == nil {
			require.Nil(t, details[0].DurationMs)
		} else {
			require.NotNil(t, details[0].DurationMs)
			require.EqualValues(t, *row.DurationMs, *details[0].DurationMs)
		}
	}
	filter := &service.OpsDashboardFilter{StartTime: start, EndTime: end, Platform: "openai"}
	total, business, sla, provider, _, _, err := repo.queryErrorCounts(ctx, filter, start, end)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.EqualValues(t, 1, business, "local request validation is a user rejection")
	require.EqualValues(t, 1, sla, "cancellation and local validation are excluded; provider failure remains")
	require.EqualValues(t, 2, provider, "recovered provider attempts remain visible")
	trend, err := repo.GetErrorTrend(ctx, filter, 3600)
	require.NoError(t, err)
	require.NotEmpty(t, trend.Points)
	var trendTotal, trendSLA, trendProvider int64
	for _, point := range trend.Points {
		trendTotal += point.ErrorCountTotal
		trendSLA += point.ErrorCountSLA
		trendProvider += point.UpstreamErrorCountExcl429529
	}
	require.Equal(t, total, trendTotal)
	require.Equal(t, sla, trendSLA)
	require.Equal(t, provider, trendProvider)
	distribution, err := repo.GetErrorDistribution(ctx, filter)
	require.NoError(t, err)
	found := false
	for _, item := range distribution.Items {
		if item.StatusCode == 499 {
			found = true
			require.EqualValues(t, 1, item.Total)
			require.Zero(t, item.SLA)
			require.Zero(t, item.BusinessLimited)
		}
	}
	require.True(t, found)
	require.NoError(t, repo.UpsertHourlyMetrics(ctx, start, end))
	require.NoError(t, repo.UpsertDailyMetrics(ctx, day, day.Add(24*time.Hour)))
	for _, period := range []string{"hourly", "daily"} {
		column, stamp := "bucket_start", start
		if period == "daily" {
			column, stamp = "bucket_date", day
		}
		var gotTotal, gotSLA, gotProvider int64
		err := integrationDB.QueryRowContext(ctx, "SELECT error_count_total,error_count_sla,upstream_error_count_excl_429_529 FROM ops_metrics_"+period+" WHERE "+column+"=$1 AND platform='openai' AND group_id IS NULL", stamp).Scan(&gotTotal, &gotSLA, &gotProvider)
		require.NoError(t, err)
		require.Equal(t, total, gotTotal)
		require.Equal(t, sla, gotSLA)
		require.Equal(t, provider, gotProvider)
	}
}
