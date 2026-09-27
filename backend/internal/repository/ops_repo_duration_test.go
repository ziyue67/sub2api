package repository

import (
	"context"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpsErrorDurationPersistsInSingleAndBatchInserts(t *testing.T) {
	zero, elapsed, invalid := int64(0), int64(24477), int64(-1)
	for _, tc := range []struct {
		name     string
		value    *int64
		expected driver.Value
	}{
		{"missing", nil, nil}, {"zero", &zero, int64(0)}, {"measured", &elapsed, int64(24477)}, {"invalid", &invalid, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			repo := NewOpsRepository(db)
			input := &service.OpsInsertErrorLogInput{ErrorPhase: "request", ErrorType: "client_canceled", StatusCode: 499, DurationMs: tc.value, CreatedAt: time.Now()}
			args := make([]driver.Value, 39)
			for i := 0; i < 38; i++ {
				args[i] = sqlmock.AnyArg()
			}
			args[38] = tc.expected
			mock.ExpectQuery("(?s)INSERT INTO ops_error_logs.*duration_ms.*RETURNING id").WithArgs(args...).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			id, err := repo.InsertErrorLog(context.Background(), input)
			require.NoError(t, err)
			require.EqualValues(t, 1, id)
			mock.ExpectBegin()
			prepared := mock.ExpectPrepare("(?s)INSERT INTO ops_error_logs.*duration_ms")
			prepared.ExpectExec().WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
			prepared.ExpectExec().WithArgs(args...).WillReturnResult(sqlmock.NewResult(0, 1))
			mock.ExpectCommit()
			n, err := repo.BatchInsertErrorLogs(context.Background(), []*service.OpsInsertErrorLogInput{input, input})
			require.NoError(t, err)
			require.EqualValues(t, 2, n)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
