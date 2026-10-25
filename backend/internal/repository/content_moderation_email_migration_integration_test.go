//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestModerationEmailMigrationLegacyDefaultAndAuditSnapshot(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	// Recreate the immediately preceding schema inside a rolled-back transaction.
	_, err := tx.ExecContext(ctx, `ALTER TABLE content_moderation_user_state DROP COLUMN email_exempt;
        ALTER TABLE content_moderation_user_actions DROP COLUMN email_exempt;`)
	require.NoError(t, err)
	var id int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash,status) VALUES('moderation-mail-migration@example.test','unused-fixture','disabled') RETURNING id`).Scan(&id))
	_, err = tx.ExecContext(ctx, `INSERT INTO content_moderation_user_state(user_id,version,generation,auto_ban_exempt,keyword_exempt,api_audit_exempt,reason) VALUES($1,2,3,TRUE,TRUE,TRUE,'legacy')`, id)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO content_moderation_user_actions(user_id,actor_id,action,reason,version,auto_ban_exempt,keyword_exempt,api_audit_exempt) VALUES($1,7,'exemption','legacy',2,TRUE,TRUE,TRUE)`, id)
	require.NoError(t, err)
	migration, err := dbmigrations.FS.ReadFile("301_content_moderation_email_exemption.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var email, oldEmail bool
	var version, generation int64
	var status string
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT s.email_exempt,s.version,s.generation,u.status FROM content_moderation_user_state s JOIN users u ON u.id=s.user_id WHERE u.id=$1`, id).Scan(&email, &version, &generation, &status))
	require.False(t, email)
	require.EqualValues(t, 2, version)
	require.EqualValues(t, 3, generation)
	require.Equal(t, "disabled", status)
	_, err = tx.ExecContext(ctx, `UPDATE content_moderation_user_state SET email_exempt=TRUE WHERE user_id=$1`, id)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT email_exempt FROM content_moderation_user_state WHERE user_id=$1`, id).Scan(&email))
	require.True(t, email, "migration replay must preserve the explicit setting")
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT email_exempt FROM content_moderation_user_actions WHERE user_id=$1`, id).Scan(&oldEmail))
	require.False(t, oldEmail, "old action snapshots must not inherit current state")
	// Exercise real PostgreSQL filter semantics for a mail-only exemption.
	_, err = tx.ExecContext(ctx, `UPDATE content_moderation_user_state SET auto_ban_exempt=FALSE,keyword_exempt=FALSE,api_audit_exempt=FALSE WHERE user_id=$1`, id)
	require.NoError(t, err)
	for _, filter := range []string{"exempt", "email_exempt", "enforced", "auto_ban_exempt", "keyword_exempt", "api_audit_exempt"} {
		var count int
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_moderation_user_state s WHERE user_id=$1 AND `+moderationExemptionFilter("$2"), id, filter).Scan(&count))
		want := 0
		if filter == "exempt" || filter == "email_exempt" {
			want = 1
		}
		require.Equal(t, want, count, filter)
	}
}
