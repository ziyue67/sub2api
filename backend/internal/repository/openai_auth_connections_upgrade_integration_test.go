//go:build integration

package repository

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestIssue594UpgradeRealLegacyDatabasePreservesEveryCredentialSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	dsn, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	name := "issue594_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := pq.QuoteIdentifier(name)
	_, err = integrationDB.ExecContext(ctx, "CREATE DATABASE "+quoted)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		_, e := integrationDB.ExecContext(cleanup, "DROP DATABASE "+quoted)
		require.NoError(t, e)
	})
	dsn.Path = "/" + name
	db, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	// Apply the actual production migration set before this feature, then seed
	// all three old storage slots before any connection table exists.
	legacy := fstest.MapFS{}
	entries, err := migrations.FS.ReadDir(".")
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() >= "312_" {
			continue
		}
		raw, e := migrations.FS.ReadFile(entry.Name())
		require.NoError(t, e)
		legacy[entry.Name()] = &fstest.MapFile{Data: raw, Mode: 0444}
	}
	require.NoError(t, applyMigrationsFS(ctx, db, legacy))
	var id int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,credentials,extra) VALUES('legacy upgrade fixture','openai','oauth','{"access_token":"synthetic-primary-access","refresh_token":"synthetic-primary-refresh","client_id":"synthetic-primary-client"}','{"openai_siwc":true}') RETURNING id`).Scan(&id))
	_, err = db.ExecContext(ctx, `INSERT INTO openai_excel_oauth_credentials(account_id,credentials_ciphertext) VALUES($1,'synthetic-encrypted-bps-slot')`, id)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO openai_siwc_credentials(account_id,host_id,credentials_ciphertext) VALUES($1,'urn:uuid:synthetic-host','synthetic-encrypted-siwc-slot')`, id)
	require.NoError(t, err)
	var primaryBefore, extraBefore, bpsBefore, siwcBefore, hostBefore string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials::text,extra::text FROM accounts WHERE id=$1`, id).Scan(&primaryBefore, &extraBefore))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials_ciphertext FROM openai_excel_oauth_credentials WHERE account_id=$1`, id).Scan(&bpsBefore))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials_ciphertext,host_id FROM openai_siwc_credentials WHERE account_id=$1`, id).Scan(&siwcBefore, &hostBefore))
	for range 2 {
		require.NoError(t, ApplyMigrations(ctx, db))
		var primaryAfter, extraAfter, bpsAfter, siwcAfter, hostAfter string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials::text,extra::text FROM accounts WHERE id=$1`, id).Scan(&primaryAfter, &extraAfter))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials_ciphertext FROM openai_excel_oauth_credentials WHERE account_id=$1`, id).Scan(&bpsAfter))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT credentials_ciphertext,host_id FROM openai_siwc_credentials WHERE account_id=$1`, id).Scan(&siwcAfter, &hostAfter))
		require.Equal(t, primaryBefore, primaryAfter)
		require.Equal(t, extraBefore, extraAfter)
		require.Equal(t, bpsBefore, bpsAfter)
		require.Equal(t, siwcBefore, siwcAfter)
		require.Equal(t, hostBefore, hostAfter)
		repo := NewOpenAIOAuthReauthRepository(db).(*openAIOAuthReauthRepository)
		managed, e := repo.AuthConnectionsManaged(ctx, id)
		require.NoError(t, e)
		require.False(t, managed, "upgrade cannot opt into new routing")
		connections, e := repo.ListAuthConnections(ctx, id)
		require.NoError(t, e)
		require.Empty(t, connections, "upgrade cannot duplicate a rotating token into a second lifecycle owner")
	}
	// The new protection trigger must preserve legacy scalar/null JSON updates;
	// this is also exercised by the full usage-billing statement-variant suite.
	// Generic legacy quota rows permit scalar JSON. OpenAI rows already have
	// a separate long-context trigger that requires an object; preserve it.
	_, err = db.ExecContext(ctx, `UPDATE accounts SET platform='anthropic' WHERE id=$1`, id)
	require.NoError(t, err)
	for _, value := range []string{`null`, `"legacy scalar"`, `42`, `[]`} {
		_, e := db.ExecContext(ctx, `UPDATE accounts SET extra=$2::jsonb WHERE id=$1`, id, value)
		require.NoError(t, e)
		var got string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT extra::text FROM accounts WHERE id=$1`, id).Scan(&got))
		require.JSONEq(t, value, got)
	}

	_, err = db.ExecContext(ctx, `UPDATE accounts SET extra='{}'::jsonb,platform='openai' WHERE id=$1`, id)
	require.NoError(t, err)
	// Publication itself normalizes old shapes, while the general trigger
	// above leaves untouched legacy scalar writes usable by existing quota SQL.
	connectionID := uuid.NewString()
	_, err = db.ExecContext(ctx, `INSERT INTO openai_auth_connections(id,account_id,kind,label) VALUES($1,$2,'siwc','upgrade pending registration')`, connectionID, id)
	require.NoError(t, err)
	var objectType string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT jsonb_typeof(extra) FROM accounts WHERE id=$1`, id).Scan(&objectType))
	require.Equal(t, "object", objectType)

}
