//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAutoConfigRepositoryTransaction(t *testing.T) {
	for _, tc := range []struct {
		name    string
		success bool
		count   int
		next    int
		scope   bool
		stale   bool
	}{
		{"progress", true, 0, 3, true, false}, {"promote", true, 1, 4, true, false}, {"failure", false, 1, 3, true, false}, {"not in scope", true, 0, 3, false, false}, {"stale config", true, 0, 3, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, m, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()
			c := service.DefaultOAuthAutoConfig()
			c.UpgradeEnabled = true
			c.UpgradeGroupIDs = []int64{5}
			c.Revision = "r1"
			c.SuccessesPerStep = 2
			raw, _ := json.Marshal(c)
			event := service.AccountConcurrencyResult{AccountID: 7, StartedAt: time.Now(), Success: tc.success}
			m.ExpectBegin()
			m.ExpectQuery("SELECT value FROM settings").WithArgs(service.SettingKeyOAuthAutoConfig).WillReturnRows(sqlmock.NewRows([]string{"value"}).AddRow(string(raw)))
			expected := c
			if tc.stale {
				expected.Revision = "old"
			} else {
				rows := sqlmock.NewRows([]string{"concurrency", "extra"})
				if tc.scope {
					state, _ := json.Marshal(service.AutoConfigConcurrencyState{Revision: "r1", Concurrency: 3, Successes: tc.count})
					rows.AddRow(3, state)
				}
				m.ExpectQuery("SELECT concurrency.*FOR UPDATE").WithArgs(int64(7), sqlmock.AnyArg(), tc.success).WillReturnRows(rows)
				if tc.scope {
					m.ExpectExec("UPDATE accounts SET concurrency").WithArgs(int64(7), tc.next, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(0, 1))
					if tc.next != 3 {
						m.ExpectExec("INSERT INTO scheduler_outbox").WillReturnResult(sqlmock.NewResult(1, 1))
					}
					m.ExpectCommit()
				}
			}
			if tc.stale || !tc.scope {
				m.ExpectRollback()
			}
			repo := &accountRepository{sql: db}
			require.NoError(t, repo.RecordConcurrencyResult(context.Background(), event, expected))
			require.NoError(t, m.ExpectationsWereMet())
		})
	}
}
