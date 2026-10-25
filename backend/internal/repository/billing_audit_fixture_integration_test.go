//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

// Billing repositories commit their own transactions and recovery workers scan
// every pending event. An outer rollback cannot isolate these fixtures. Reuse
// the existing migrated, container-only database fixture instead of the shared
// testEntClient, including for independently constructed repositories/workers.
func newBillingAuditDatabase(t *testing.T) (*sql.DB, *dbent.Client) {
	t.Helper()
	before := sharedBillingAuditRows(t)
	var name string
	// LIFO: worker/trigger cleanups registered by the caller run first, then the
	// fixture closes its pool and drops only its own database, then we verify.
	t.Cleanup(func() {
		require.Equal(t, before, sharedBillingAuditRows(t), "billing fixture must not change shared routing/statistics or recovery rows")
		if name != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var exists bool
			require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&exists))
			require.False(t, exists, "the complete private billing fixture must be removed, including task/usage/owned-money references")
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, client := newImageUpgradeDatabase(t, ctx)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT current_database()").Scan(&name))
	return db, client
}

func sharedBillingAuditRows(t *testing.T) []int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// These are observations only. Never truncate/reset shared tables or delete
	// another test's rows to make global routing/dashboard assertions pass.
	var users, keys, accounts, groups, usage, events, claims, archive, quotes int64
	var jobs, items, jobEvents, holds, quotas, totals, requests, allocations int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT
	 (SELECT count(*) FROM users), (SELECT count(*) FROM api_keys),
	 (SELECT count(*) FROM accounts), (SELECT count(*) FROM groups),
	 (SELECT count(*) FROM usage_logs), (SELECT count(*) FROM usage_billing_events),
	 (SELECT count(*) FROM usage_billing_dedup), (SELECT count(*) FROM usage_billing_dedup_archive),
	 (SELECT count(*) FROM usage_billing_quotes), (SELECT count(*) FROM batch_image_jobs),
	 (SELECT count(*) FROM batch_image_items), (SELECT count(*) FROM batch_image_events),
	 (SELECT count(*) FROM batch_image_money_holds), (SELECT count(*) FROM user_platform_quotas),
	 (SELECT count(*) FROM usage_platform_quota_totals), (SELECT count(*) FROM regional_primary_requests),
	 (SELECT count(*) FROM regional_budget_allocations)`).Scan(&users, &keys, &accounts, &groups, &usage, &events,
		&claims, &archive, &quotes, &jobs, &items, &jobEvents, &holds, &quotas, &totals, &requests, &allocations))
	return []int64{users, keys, accounts, groups, usage, events, claims, archive, quotes, jobs, items, jobEvents, holds, quotas, totals, requests, allocations}
}
