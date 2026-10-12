package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestExcelGrantDiagnosisConditionalTransaction(t *testing.T) {
	for _, tc := range []struct {
		name        string
		changed     int64
		outboxFails bool
	}{
		{"old response after rotation", 0, false}, {"matched response", 1, false}, {"outbox failure rolls back grant and diagnosis", 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			repo := &openAIOAuthReauthRepository{db: db}
			mock.ExpectBegin()
			mock.ExpectQuery("SELECT id FROM accounts .* FOR UPDATE").WithArgs(int64(7)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(7))
			mock.ExpectExec("DELETE FROM openai_excel_oauth_credentials WHERE account_id=\\$1 AND credentials_ciphertext=\\$2").WithArgs(int64(7), "expected-ciphertext").WillReturnResult(sqlmock.NewResult(0, tc.changed))
			if tc.changed > 0 {
				mock.ExpectExec("UPDATE accounts SET extra=jsonb_set").WithArgs(int64(7), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
				outbox := mock.ExpectExec("INSERT INTO scheduler_outbox")
				if tc.outboxFails {
					outbox.WillReturnError(errors.New("offline"))
				} else {
					outbox.WillReturnResult(sqlmock.NewResult(1, 1))
				}
			}
			if tc.changed == 0 || tc.outboxFails {
				mock.ExpectRollback()
			} else {
				mock.ExpectCommit()
			}
			err = repo.DeleteExcelCredentials(context.Background(), 7, "expected-ciphertext", service.ExcelBPSGrantFailure("token_revoked"))
			if tc.outboxFails {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
