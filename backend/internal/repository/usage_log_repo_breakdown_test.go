//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestResolveEndpointColumn(t *testing.T) {
	tests := []struct {
		endpointType string
		want         string
	}{
		{"inbound", "ul.inbound_endpoint"},
		{"upstream", "ul.upstream_endpoint"},
		{"path", "ul.inbound_endpoint || ' -> ' || ul.upstream_endpoint"},
		{"", "ul.inbound_endpoint"},        // default
		{"unknown", "ul.inbound_endpoint"}, // fallback
	}

	for _, tc := range tests {
		t.Run(tc.endpointType, func(t *testing.T) {
			got := resolveEndpointColumn(tc.endpointType)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveModelDimensionExpression(t *testing.T) {
	tests := []struct {
		modelType string
		want      string
	}{
		{usagestats.ModelSourceRequested, "COALESCE(NULLIF(TRIM(requested_model), ''), model)"},
		{usagestats.ModelSourceUpstream, "COALESCE(NULLIF(TRIM(upstream_model), ''), model)"},
		{usagestats.ModelSourceMapping, "(COALESCE(NULLIF(TRIM(requested_model), ''), model) || ' -> ' || COALESCE(NULLIF(TRIM(upstream_model), ''), model))"},
		{"", "COALESCE(NULLIF(TRIM(requested_model), ''), model)"},
		{"invalid", "COALESCE(NULLIF(TRIM(requested_model), ''), model)"},
	}

	for _, tc := range tests {
		t.Run(tc.modelType, func(t *testing.T) {
			got := resolveModelDimensionExpression(tc.modelType)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestGetUserBreakdownStatsRequestTypeIncludesLegacyFallback(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	requestType := int16(service.RequestTypeStream)

	legacyFilter := `(ul.request_type = $3 OR (ul.request_type = 0 AND ul.stream = TRUE AND ul.openai_ws_mode = FALSE))`
	mock.ExpectQuery(regexp.QuoteMeta(legacyFilter)).
		WithArgs(start, end, requestType).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "email", "requests", "input_tokens", "output_tokens",
			"cache_tokens", "total_tokens", "cost", "actual_cost", "account_cost",
		}))

	rows, err := repo.GetUserBreakdownStats(context.Background(), start, end, usagestats.UserBreakdownDimension{
		RequestType: &requestType,
	}, 0)

	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTokenLeaderboardWithFiltersUsesAllowlistedOrderAndLegacyRequestType(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	requestType := int16(service.RequestTypeStream)

	queryPattern := `(?s)SELECT.*SUM\(u\.total_cost\).*` +
		regexp.QuoteMeta(`(u.request_type = $3 OR (u.request_type = 0 AND u.stream = TRUE AND u.openai_ws_mode = FALSE))`) +
		`.*u\.billing_mode = \$4.*ORDER BY cost DESC, total_tokens DESC, requests DESC, u\.user_id ASC.*LIMIT \$5`
	mock.ExpectQuery(queryPattern).
		WithArgs(start, end, requestType, "video", 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "email", "requests", "total_tokens", "input_tokens", "output_tokens",
			"cache_tokens", "image_output_tokens", "cost", "actual_cost", "account_cost", "last_active_at",
		}).AddRow(42, "user@example.com", 3, 120, 20, 30, 40, 30, 0.3, 0.2, 0.25, end))

	rows, err := repo.GetTokenLeaderboardWithFilters(context.Background(), start, end, 20, usagestats.TokenLeaderboardQuery{
		RequestType: &requestType,
		BillingMode: "video",
		SortBy:      "cost",
	})

	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, float64(0.2), rows[0].ActualCost)
	require.Equal(t, float64(0.25), rows[0].AccountCost)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTokenLeaderboardWithFiltersUsesAccountNameAndEmailSearch(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &usageLogRepository{sql: db}
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)

	queryPattern := `(?s)LEFT JOIN accounts a ON u\.account_id = a\.id.*a\.name ILIKE \$3.*COALESCE\(a\.credentials->>'email', a\.extra->>'email', a\.extra->>'email_address', ''\) ILIKE \$4.*LIMIT \$5`
	mock.ExpectQuery(queryPattern).
		WithArgs(start, end, "%Codex%", "%admin@example.com%", 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"user_id", "email", "requests", "total_tokens", "input_tokens", "output_tokens",
			"cache_tokens", "image_output_tokens", "cost", "actual_cost", "account_cost", "last_active_at",
		}))

	rows, err := repo.GetTokenLeaderboardWithFilters(context.Background(), start, end, 20, usagestats.TokenLeaderboardQuery{
		AccountName:  "Codex",
		AccountEmail: "admin@example.com",
	})

	require.NoError(t, err)
	require.Empty(t, rows)
	require.NoError(t, mock.ExpectationsWereMet())
}
