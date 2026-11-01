//go:build unit

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

func TestPostgresAffiliateRefundFrozenThaw(t *testing.T) {
	dsn := os.Getenv("REFUND_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("requires disposable PostgreSQL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer base.Close()
	name := fmt.Sprintf("affiliate_thaw_%x", time.Now().UnixNano())
	_, err = base.ExecContext(ctx, "CREATE SCHEMA "+name)
	require.NoError(t, err)
	defer base.Exec("DROP SCHEMA " + name + " CASCADE")
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()
	c, err := dbent.Open(dialect.Postgres, u.String())
	require.NoError(t, err)
	defer c.Close()
	_, err = c.ExecContext(ctx, `CREATE TABLE user_affiliates(user_id BIGINT PRIMARY KEY, aff_quota NUMERIC, aff_frozen_quota NUMERIC, updated_at TIMESTAMP);
CREATE TABLE user_affiliate_ledger(user_id BIGINT, amount NUMERIC, action TEXT, frozen_until TIMESTAMP, updated_at TIMESTAMP);
INSERT INTO user_affiliates VALUES(1,0,0,NOW()),(2,0,15,NOW());
INSERT INTO user_affiliate_ledger VALUES(1,20,'accrue',NOW()-INTERVAL '1 hour',NOW()),(1,-20,'refund_reversal',NOW()-INTERVAL '1 hour',NOW()),(2,20,'accrue',NOW()-INTERVAL '1 hour',NOW()),(2,-5,'refund_reversal',NOW()-INTERVAL '1 hour',NOW());`)
	require.NoError(t, err)
	for _, tc := range []struct {
		id   int64
		want float64
	}{{1, 0}, {2, 15}} {
		tx, err := c.Tx(ctx)
		require.NoError(t, err)
		amount, err := thawFrozenQuotaTx(ctx, tx.Client(), tc.id)
		if err != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, err)
		require.Equal(t, tc.want, amount)
		require.NoError(t, tx.Commit())
		rows, err := c.QueryContext(ctx, `SELECT aff_quota,aff_frozen_quota,(SELECT COUNT(*) FROM user_affiliate_ledger WHERE user_id=$1 AND frozen_until IS NOT NULL) FROM user_affiliates WHERE user_id=$1`, tc.id)
		require.NoError(t, err)
		require.True(t, rows.Next())
		var available, frozen float64
		var pending int
		require.NoError(t, rows.Scan(&available, &frozen, &pending))
		rows.Close()
		require.Equal(t, tc.want, available)
		require.Zero(t, frozen)
		require.Zero(t, pending)
	}
}
