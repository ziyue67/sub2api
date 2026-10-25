//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountOpsIdlePollingIsReadOnlyAndNoticesOtherReplicaWork(t *testing.T) {
	ctx := context.Background()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('idle-poll','openai','apikey','active',true) RETURNING id`).Scan(&id))
	defer cleanupOnce(t, id)
	repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
	cfg := service.AccountOpsConfig{Enabled: true, BalanceThresholds: []service.AccountOpsBalanceRule{{AccountID: id, Enabled: true, Threshold: 10, Unit: "USD"}}}
	criteria, _, _, _ := cfg.ThresholdCriteria(id, "balance_threshold")
	require.NoError(t, repo.ObserveThreshold(ctx, service.AccountOpsEvent{AccountID: id, Kind: "balance_threshold", Criteria: criteria, Identity: "same"}, "resolved", time.Now(), true, true))

	// A separate read-only connection makes any idle write attempt fail, even an
	// UPDATE matching zero rows. It also cannot share a repository-local cache.
	readOnlyDB, err := sql.Open("postgres", integrationPostgresDSN)
	require.NoError(t, err)
	defer readOnlyDB.Close()
	readOnlyDB.SetMaxOpenConns(1)
	_, err = readOnlyDB.ExecContext(ctx, `SET default_transaction_read_only=on`)
	require.NoError(t, err)
	reader := NewAccountOpsRepository(readOnlyDB)
	require.NoError(t, reader.SuppressDisabled(ctx, cfg))
	claimed, err := reader.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, claimed)

	// Work inserted after an idle check must still be suppressed with the same
	// configuration, including work arriving through a different repository.
	event := service.AccountOpsEvent{AccountID: id, Kind: "balance_low", Signal: "balance_error_code"}
	require.NoError(t, NewAccountOpsRepository(integrationDB).Record(ctx, event))
	require.NoError(t, repo.SuppressDisabled(ctx, cfg))
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_alerts WHERE account_id=$1`, id).Scan(&state))
	require.Equal(t, "suppressed", state)

	require.NoError(t, repo.Record(ctx, event))
	claimed, err = NewAccountOpsRepository(integrationDB).Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	require.NoError(t, repo.Complete(ctx, claimed, "failed", time.Hour))
	claimed, err = reader.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, claimed, "future retries do not require idle writes")

	_, err = integrationDB.ExecContext(ctx, `UPDATE account_ops_alerts SET next_send_at=NOW()-INTERVAL '1 second' WHERE account_id=$1`, id)
	require.NoError(t, err)
	claimed, err = NewAccountOpsRepository(integrationDB).Claim(ctx)
	require.NoError(t, err)
	require.NotNil(t, claimed)
	_, err = integrationDB.ExecContext(ctx, `UPDATE account_ops_alerts SET attempts=3,lease_until=NOW()-INTERVAL '1 second' WHERE account_id=$1`, id)
	require.NoError(t, err)
	claimed, err = repo.Claim(ctx)
	require.NoError(t, err)
	require.Nil(t, claimed, "exhausted leases are cleaned up without another attempt")
	var lease string
	var retryDelayed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state,lease,next_send_at>NOW() FROM account_ops_alerts WHERE account_id=$1`, id).Scan(&state, &lease, &retryDelayed))
	require.Equal(t, "failed", state)
	require.Empty(t, lease)
	require.True(t, retryDelayed)

	cfg.BalanceThresholds[0].Enabled = false
	require.NoError(t, repo.SuppressDisabled(ctx, cfg))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT criteria FROM account_ops_threshold_monitors WHERE account_id=$1`, id).Scan(&criteria))
	require.Equal(t, "invalid", criteria)
	require.NoError(t, reader.SuppressDisabled(ctx, cfg))
}

func TestAccountOpsPeriodicSuppressionKeepsCriteriaInvalidation(t *testing.T) {
	ctx := context.Background()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type,status,schedulable) VALUES('idle-criteria','openai','apikey','active',true) RETURNING id`).Scan(&id))
	defer cleanupOnce(t, id)
	repo := NewAccountOpsRepository(integrationDB).(*accountOpsRepository)
	cfg := service.AccountOpsConfig{Enabled: true, BalanceThresholds: []service.AccountOpsBalanceRule{{AccountID: id, Enabled: true, Threshold: 10, Unit: "USD"}}}
	criteria, _, _, _ := cfg.ThresholdCriteria(id, "balance_threshold")
	require.NoError(t, repo.ObserveThreshold(ctx, service.AccountOpsEvent{AccountID: id, Kind: "balance_threshold", Criteria: criteria, Identity: "same"}, "active", time.Now(), true, true))
	cfg.BalanceThresholds[0].Threshold = 20
	require.NoError(t, repo.SuppressDisabled(ctx, cfg))
	var state string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT state FROM account_ops_threshold_events WHERE account_id=$1`, id).Scan(&state))
	require.Equal(t, "suppressed", state, "enabled rules still invalidate queued work with old criteria")
}
