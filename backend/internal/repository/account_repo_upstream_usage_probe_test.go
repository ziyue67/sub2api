package repository

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestUpdateUpstreamUsageProbeSnapshotRequiresSameAccountIdentity(t *testing.T) {
	for _, tt := range []struct {
		name     string
		affected int64
		wantErr  error
	}{
		{name: "same identity", affected: 1},
		{name: "identity changed", affected: 0, wantErr: service.ErrUpstreamBillingProbeIdentityChanged},
	} {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })

			mock.ExpectBegin()
			tx, err := client.Tx(context.Background())
			require.NoError(t, err)
			mock.ExpectExec(`(?s)`+regexp.QuoteMeta("UPDATE accounts")+`.*`+
				regexp.QuoteMeta("WHERE id = $2")+`.*`+
				regexp.QuoteMeta("AND platform = $3")+`.*`+
				regexp.QuoteMeta("AND type = $4")+`.*`+
				regexp.QuoteMeta("AND credentials = $5::jsonb")+`.*`+
				regexp.QuoteMeta("AND proxy_id IS NOT DISTINCT FROM $6")).
				WithArgs(sqlmock.AnyArg(), int64(17), service.PlatformOpenAI, service.AccountTypeAPIKey, `{"api_key":"sk-test","base_url":"https://upstream.example"}`, nil).
				WillReturnResult(sqlmock.NewResult(0, tt.affected))

			repo := newAccountRepositoryWithSQL(client, db, nil)
			account := &service.Account{
				ID:       17,
				Platform: service.PlatformOpenAI,
				Type:     service.AccountTypeAPIKey,
				Credentials: map[string]any{
					"api_key":  "sk-test",
					"base_url": "https://upstream.example",
				},
			}
			snapshot := &service.UpstreamUsageProbeSnapshot{
				Status: service.UpstreamUsageProbeStatusOK,
				Data:   map[string]any{"remaining": 12.5, "unit": "USD"},
			}

			err = repo.UpdateUpstreamUsageProbeSnapshot(dbent.NewTxContext(context.Background(), tx), account, snapshot)

			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}
			mock.ExpectRollback()
			require.NoError(t, tx.Rollback())
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestUpdateUpstreamUsageProbeSnapshotRejectsChangedProxyIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	mock.ExpectQuery(`(?s)` + regexp.QuoteMeta("SELECT protocol, host, port") + `.*` + regexp.QuoteMeta("FOR SHARE")).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{"protocol", "host", "port", "username", "password", "status"}).
			AddRow("http", "new.example", 3128, "user", "pass", service.StatusActive))

	proxyID := int64(9)
	account := &service.Account{
		ID:          17,
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test"},
		ProxyID:     &proxyID,
		Proxy: &service.Proxy{
			ID: proxyID, Protocol: "http", Host: "old.example", Port: 3128,
			Username: "user", Password: "pass", Status: service.StatusActive,
		},
	}
	repo := newAccountRepositoryWithSQL(client, db, nil)
	err = repo.UpdateUpstreamUsageProbeSnapshot(
		dbent.NewTxContext(context.Background(), tx),
		account,
		&service.UpstreamUsageProbeSnapshot{Status: service.UpstreamUsageProbeStatusOK},
	)

	require.ErrorIs(t, err, service.ErrUpstreamBillingProbeIdentityChanged)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
