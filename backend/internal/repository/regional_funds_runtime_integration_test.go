//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
	"github.com/stretchr/testify/require"
)

func TestRegionalFundsBrokerPreservesPendingPayload(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM regional_broker_operations WHERE operation='quota/issue' AND (payload->>'user_id')::bigint=$1", user)
	})
	broker := &control.Broker{DB: integrationDB, Quota: &ledger.Local{DB: integrationDB, Region: "us"}}
	target := RegionalFundsTarget{UserID: user, APIKeyID: 1, AccountID: 2, Amount: 1000000000}
	require.NoError(t, ensureRegionalFunds(ctx, integrationDB, broker, "us", target, time.Minute))
	var firstID string
	var firstRaw []byte
	require.NoError(t, integrationDB.QueryRow("SELECT operation_id,payload FROM regional_broker_operations WHERE operation='quota/issue' AND (payload->>'user_id')::bigint=$1", user).Scan(&firstID, &firstRaw))
	// New worker and changed desired amount must replay old pending operation.
	broker = &control.Broker{DB: integrationDB, Quota: &ledger.Local{DB: integrationDB, Region: "us"}}
	target.Amount *= 2
	require.NoError(t, ensureRegionalFunds(ctx, integrationDB, broker, "us", target, time.Minute))
	var count int
	var secondID string
	var secondRaw []byte
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM regional_broker_operations WHERE operation='quota/issue' AND (payload->>'user_id')::bigint=$1", user).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, integrationDB.QueryRow("SELECT operation_id,payload FROM regional_broker_operations WHERE operation='quota/issue' AND (payload->>'user_id')::bigint=$1", user).Scan(&secondID, &secondRaw))
	require.Equal(t, firstID, secondID)
	require.JSONEq(t, string(firstRaw), string(secondRaw))
	require.True(t, json.Valid(firstRaw))
}

func TestRegionalFundsImportsLegacyUsageOnce(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	epoch := fmt.Sprintf("funds-%d", user)
	_, err := integrationDB.Exec("INSERT INTO user_platform_quotas(user_id,platform,daily_limit_usd,daily_usage_usd,weekly_usage_usd,monthly_usage_usd) VALUES($1,'openai',10,1,2,3)", user)
	require.NoError(t, err)
	key := userPlatformQuotaCacheKey(user, "openai")
	require.NoError(t, integrationRedis.HSet(ctx, key, "daily_usage", "4.00000000", "weekly_usage", "5.00000000", "monthly_usage", "6.00000000").Err())
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM regional_funds_imports WHERE epoch=$1", epoch)
		_, _ = integrationDB.Exec("DELETE FROM regional_funds_members WHERE epoch=$1", epoch)
		_ = integrationRedis.Del(ctx, key).Err()
	})
	capacity := &regionalConcurrencyCache{runtimeConfig: &RegionalConcurrencyRuntimeConfig{Epoch: epoch, PrimaryInstances: []string{"one"}}, primary: &slots.Primary{DB: integrationDB, GrantGuard: func(context.Context, *sql.Tx) error { return nil }}}
	require.Error(t, importRegionalFundsUsage(ctx, integrationDB, integrationRedis, capacity))
	_, err = integrationDB.Exec("INSERT INTO regional_funds_members(epoch,instance_id) VALUES($1,'one')", epoch)
	require.NoError(t, err)
	require.NoError(t, importRegionalFundsUsage(ctx, integrationDB, integrationRedis, capacity))
	_, err = integrationDB.Exec("UPDATE user_platform_quotas SET daily_usage_usd=7 WHERE user_id=$1", user)
	require.NoError(t, err)
	require.NoError(t, importRegionalFundsUsage(ctx, integrationDB, integrationRedis, capacity))
	var actual string
	require.NoError(t, integrationDB.QueryRow("SELECT daily_usage_usd::text FROM user_platform_quotas WHERE user_id=$1", user).Scan(&actual))
	require.Equal(t, "7.0000000000", actual)
}

type fundsLostClose struct {
	primary *ledger.Primary
	lost    bool
}

func (r *fundsLostClose) IssueSlots(context.Context, slots.Grant) error {
	return fmt.Errorf("unexpected slots")
}
func (r *fundsLostClose) RenewSlots(context.Context, slots.Grant) error {
	return fmt.Errorf("unexpected slots")
}
func (r *fundsLostClose) IssueQuota(ctx context.Context, l regional.Lease) error {
	return r.primary.Issue(ctx, l)
}
func (r *fundsLostClose) CloseQuota(ctx context.Context, receipt ledger.DrainReceipt) error {
	if err := r.primary.Close(ctx, "us", receipt); err != nil {
		return err
	}
	if !r.lost {
		r.lost = true
		return fmt.Errorf("lost close acknowledgment")
	}
	return nil
}
func TestRegionalFundsCloseRestartAndUnknownHold(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	p := &ledger.Primary{DB: integrationDB}
	local := &ledger.Local{DB: integrationDB, Region: "us"}
	lease := regionalLedgerLease(fmt.Sprintf("funds-close-%d", user), user, 1_000_000_000)
	require.NoError(t, p.Issue(ctx, lease))
	require.NoError(t, local.Install(ctx, lease))
	request := regionalLedgerReservation(lease, fmt.Sprintf("funds-unknown-%d", user), lease.Granted)
	_, err := local.Reserve(ctx, request)
	require.NoError(t, err)
	require.NoError(t, local.Start(ctx, request.RequestID))
	remote := &fundsLostClose{primary: p}
	broker := &control.Broker{DB: integrationDB, Remote: remote, Quota: local}
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM regional_broker_operations WHERE operation_id=$1", "close:"+lease.LeaseID)
	})
	require.NoError(t, local.Drain(ctx, lease.LeaseID))
	require.NoError(t, drainRegionalFunds(ctx, integrationDB, broker, local))
	var count int
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM regional_broker_operations WHERE operation_id=$1", "close:"+lease.LeaseID).Scan(&count))
	require.Zero(t, count)
	e := regionalLedgerEvent(request, 100_000_000)
	require.NoError(t, local.Complete(ctx, e))
	_, err = p.Settle(ctx, "us", e, func(context.Context, *sql.Tx, ledger.Event) error { return nil })
	require.NoError(t, err)
	require.NoError(t, local.Acknowledge(ctx, e))
	// Close committed locally, process dies before enqueue: background reconstructs.
	_, err = local.Close(ctx, lease.LeaseID)
	require.NoError(t, err)
	require.NoError(t, drainRegionalFunds(ctx, integrationDB, broker, local))
	require.Error(t, broker.Replay(ctx, "close:"+lease.LeaseID))
	broker = &control.Broker{DB: integrationDB, Remote: remote, Quota: local}
	require.NoError(t, broker.Replay(ctx, "close:"+lease.LeaseID))
	var balance string
	require.NoError(t, integrationDB.QueryRow("SELECT balance::text FROM users WHERE id=$1", user).Scan(&balance))
	require.Equal(t, "9.90000000", balance)
	var state string
	require.NoError(t, integrationDB.QueryRow("SELECT state FROM regional_broker_operations WHERE operation_id=$1", "close:"+lease.LeaseID).Scan(&state))
	require.Equal(t, "installed", state)
}
