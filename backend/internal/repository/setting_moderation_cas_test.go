//go:build unit

package repository

import (
	"context"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime" // Initialize Ent defaults when this regression runs in isolation.
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSettingModerationCompareAndSet(t *testing.T) {
	for _, tc := range []struct {
		name     string
		affected int64
		initial  bool
	}{{"updated", 1, false}, {"stale", 0, false}, {"initial", 1, true}, {"initial conflict", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			client := ent.NewClient(ent.Driver(sql.OpenDB(dialect.Postgres, db)))
			repo := &settingRepository{client: client}
			mock.ExpectBegin()
			previous := "old-json"
			if tc.initial {
				previous = ""
				mock.ExpectQuery(`INSERT INTO "settings".*ON CONFLICT`).WithArgs(service.SettingKeyContentModerationConfig, "", sqlmock.AnyArg()).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
			}
			mock.ExpectExec(regexp.QuoteMeta(`UPDATE "settings" SET "value" = $1, "updated_at" = $2 WHERE "settings"."key" = $3 AND "settings"."value" = $4`)).
				WithArgs("new-json", sqlmock.AnyArg(), service.SettingKeyContentModerationConfig, previous).WillReturnResult(sqlmock.NewResult(0, tc.affected))
			if tc.affected == 1 {
				mock.ExpectCommit()
			} else {
				mock.ExpectRollback()
			}
			err = repo.CompareAndSetContentModerationConfig(context.Background(), "new-json", previous, nil)
			if tc.affected == 1 {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, service.ErrContentModerationConfigConflict)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
