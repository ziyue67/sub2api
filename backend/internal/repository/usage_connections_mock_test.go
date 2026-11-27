package repository

import (
	"context"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestConnectionTraceRecognizedUserNameAndRejectionRead(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := newUsageLogRepositoryWithSQL(nil, db)
	now := time.Now()
	columns := []string{"trace_id", "started_at", "request_id", "client_request_id", "user_id", "api_key_id", "account_id", "group_id", "model", "path", "client_ip", "user_agent", "stream", "detail", "legacy", "state", "source", "host", "scheme", "method", "http_version", "tls_version", "upstream_status", "network_error", "email", "key_name", "account_name", "group_name", "user_name"}
	detail := `{"version":1,"status":401,"identity_source":"recognized_credential","auth_rejection":"USER_INACTIVE","events":{},"attempts":[]}`
	mock.ExpectQuery("WITH source AS").WithArgs(now.Add(-time.Hour), now, 21, 0).WillReturnRows(sqlmock.NewRows(columns).AddRow("trace", now, "request", "", 9, 7, 0, 8, "", "/v1/responses", "8.8.8.8", "", false, detail, false, "failed", "application", "example.test", "https", "POST", "HTTP/1.1", "", "", "", "", "example-key", "", "example-group", "disabled-example"))
	mock.ExpectQuery("SELECT DISTINCT ON").WithArgs(sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"request_id", "id", "error_type", "status_code"}))
	page, err := repo.ListConnectionTraces(context.Background(), service.ConnectionTraceFilter{Start: now.Add(-time.Hour), End: now, Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	require.Equal(t, "disabled-example", page.Items[0].UserName)
	require.Equal(t, "USER_INACTIVE", page.Items[0].ErrorType)
	require.Equal(t, 401, page.Items[0].ErrorStatus)
	require.NoError(t, mock.ExpectationsWereMet())
}
