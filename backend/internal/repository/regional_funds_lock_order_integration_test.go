//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

// Force the real Reserve user lock to overlap Settle's key lock and canonical
// usage FK check. The same permanent request/event IDs are used throughout.
func TestRegionalFundsReserveAndCanonicalSettlementAvoidFKDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	user := regionalLedgerUser(t)
	client := testEntClient(t)
	group := mustCreateGroup(t, client, &service.Group{Name: "fk-" + uuid.NewString(), Platform: service.PlatformOpenAI, RateMultiplier: 1})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user, GroupID: &group.ID, Key: "sk-mock-" + uuid.NewString(), Name: "fk"})
	account := mustCreateAccount(t, client, &service.Account{Name: "fk-" + uuid.NewString(), Type: service.AccountTypeAPIKey, Platform: service.PlatformOpenAI, Status: "active", Schedulable: true})
	_, err := integrationDB.Exec("INSERT INTO account_groups(account_id,group_id)VALUES($1,$2)", account.ID, group.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.Exec("DELETE FROM usage_logs WHERE user_id=$1", user)
		_, _ = integrationDB.Exec("DELETE FROM regional_primary_requests WHERE user_id=$1", user)
		_, _ = integrationDB.Exec("DELETE FROM regional_budget_allocations WHERE user_id=$1", user)
	})
	primary, holds, _ := NewRegionalFundsAuthorities(integrationDB, func(context.Context, *sql.Tx) error { return nil })
	lease := regionalLedgerLease("fk-"+uuid.NewString(), user, 1_000_000_000)
	lease.APIKeyID = key.ID
	lease.AccountID = account.ID
	require.NoError(t, primary.Issue(ctx, lease))
	local := &ledger.Local{DB: integrationDB, Region: lease.RegionID}
	require.NoError(t, local.Install(ctx, lease))
	price := ledger.Price{Model: "mock-fk", Input: 1_000_000_000, Output: 2_000_000_000, MaxInput: 100, MaxOutput: 100}
	require.NoError(t, ledger.InstallPrice(ctx, integrationDB, price))
	request := regionalLedgerReservation(lease, "fk-event-"+uuid.NewString(), 1_000_000_000)
	request.APIKeyID = key.ID
	request.AccountID = account.ID
	request.Model = price.Model
	request.PricingVersion = price.Version()
	_, err = local.Reserve(ctx, request)
	require.NoError(t, err)
	require.NoError(t, local.Start(ctx, request.RequestID))
	charge, err := price.Charge(10, 5, 0, 0)
	require.NoError(t, err)
	event := regionalLedgerEvent(request, charge)
	require.NoError(t, local.Complete(ctx, event))
	locked := make(chan struct{})
	allowInsert := make(chan struct{})
	scope := RegionalBudgetScope{}
	verifier := ledger.PricingVerifier(func(ctx context.Context, tx *sql.Tx, e ledger.Event) error {
		if err := scope.Charge(ctx, tx, "grant:"+lease.LeaseID, e); err != nil {
			return err
		}
		close(locked)
		select {
		case <-allowInsert:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	settled := make(chan error, 1)
	go func() { _, err := primary.Settle(ctx, lease.RegionID, event, verifier); settled <- err }()
	select {
	case <-locked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	app := "funds-fk-" + uuid.NewString()
	holds.Funding = func(ctx context.Context, tx *sql.Tx, u, k int64) (bool, error) {
		if _, err := tx.ExecContext(ctx, "SELECT set_config('application_name',$1,true)", app); err != nil {
			return false, err
		}
		return regionalBalanceFunding(ctx, tx, u, k)
	}
	next := request
	next.RequestID = "fk-main-" + uuid.NewString()
	next.LeaseID = ""
	next.AdmittedAt = time.Now()
	reserved := make(chan error, 1)
	go func() { _, err := holds.Reserve(ctx, next); reserved <- err }()
	blocked := false
	for i := 0; i < 100; i++ {
		require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%FOR SHARE OF k,g%')", app).Scan(&blocked))
		if blocked {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Always release the verifier even if the observation failed.
	close(allowInsert)
	require.True(t, blocked, "Reserve must hold user while waiting on the settled key")
	require.NoError(t, <-settled)
	require.NoError(t, <-reserved)
	var count int
	require.NoError(t, integrationDB.QueryRow("SELECT COUNT(*) FROM usage_logs WHERE request_id=$1", event.Usage.EventID).Scan(&count))
	require.Equal(t, 1, count, fmt.Sprintf("permanent event %s", event.Usage.EventID))
}
