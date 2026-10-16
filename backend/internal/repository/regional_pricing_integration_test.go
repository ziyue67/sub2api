//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRegionalPricingCanonicalSettlementAndLostAck(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	client := testEntClient(t)
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user, Key: "sk-mock-" + uuid.NewString(), Name: "regional-pricing"})
	account := mustCreateAccount(t, client, &service.Account{Name: "mock-" + uuid.NewString(), Type: service.AccountTypeAPIKey})
	p := &ledger.Primary{DB: integrationDB}
	l := regionalLedgerLease(fmt.Sprintf("price-%d", user), user, 5_000_000_000)
	require.NoError(t, p.Issue(ctx, l))
	local := &ledger.Local{DB: integrationDB, Region: l.RegionID}
	require.NoError(t, local.Install(ctx, l))
	price := ledger.Price{Model: "mock-text", Input: 1_000_000_000, Output: 2_000_000_000, CacheCreation: 3_000_000_000, CacheRead: 100_000_000, MaxInput: 1000, MaxOutput: 100}
	require.NoError(t, ledger.InstallPrice(ctx, integrationDB, price))
	require.NoError(t, ledger.InstallPrice(ctx, integrationDB, price))
	_, err := integrationDB.ExecContext(ctx, "UPDATE regional_prices SET price='{}' WHERE version=$1", price.Version())
	require.Error(t, err)
	r := regionalLedgerReservation(l, fmt.Sprintf("price-request-%d", user), 4_000_000_000)
	r.APIKeyID = key.ID
	r.AccountID = account.ID
	r.Model = price.Model
	r.PricingVersion = price.Version()
	fresh, err := local.Reserve(ctx, r)
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, local.Start(ctx, r.RequestID))
	charge, err := price.Charge(10, 5, 0, 0)
	require.NoError(t, err)
	e := regionalLedgerEvent(r, charge)
	require.NoError(t, local.Complete(ctx, e))
	verified := 0
	scope := func(context.Context, *sql.Tx, ledger.Event) error { verified++; return nil }
	bad := e
	bad.Usage.Charge += 10
	_, err = p.Settle(ctx, l.RegionID, bad, ledger.PricingVerifier(scope))
	require.ErrorIs(t, err, ledger.ErrConflict)
	require.Zero(t, verified)
	_, err = p.Settle(ctx, l.RegionID, e, ledger.PricingVerifier(nil))
	require.Error(t, err)
	failedScope := ledger.PricingVerifier(func(context.Context, *sql.Tx, ledger.Event) error { return errors.New("admission not authorized") })
	_, err = p.Settle(ctx, l.RegionID, e, failedScope)
	require.Error(t, err)
	applied, err := p.Settle(ctx, l.RegionID, e, ledger.PricingVerifier(scope))
	require.NoError(t, err)
	require.True(t, applied)
	// A fresh process replays the exact outbox after authority commit/ack loss.
	local = &ledger.Local{DB: integrationDB, Region: l.RegionID}
	pending, err := local.Pending(ctx, 100)
	require.NoError(t, err)
	require.Contains(t, pending, e)
	applied, err = (&ledger.Primary{DB: integrationDB}).Settle(ctx, l.RegionID, e, ledger.PricingVerifier(scope))
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, 1, verified)
	var count int
	var amount string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT count(*),min(actual_cost)::text FROM usage_logs WHERE request_id=$1 AND api_key_id=$2", r.RequestID, key.ID).Scan(&count, &amount))
	require.Equal(t, 1, count)
	require.Equal(t, "0.0000200000", amount)
	require.NoError(t, local.Acknowledge(ctx, e))
	require.NoError(t, local.Drain(ctx, l.LeaseID))
	receipt, err := local.Close(ctx, l.LeaseID)
	require.NoError(t, err)
	require.NoError(t, p.Close(ctx, l.RegionID, receipt))
	var balance string
	require.NoError(t, integrationDB.QueryRow("SELECT balance::text FROM users WHERE id=$1", user).Scan(&balance))
	require.Equal(t, "9.99998000", balance)
}

func TestRegionalGrantCompetesWithPrimaryInflightHold(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	t.Cleanup(func() { _, _ = integrationDB.Exec("DELETE FROM regional_primary_requests WHERE user_id=$1", user) })
	primary := &ledger.Primary{DB: integrationDB}
	holds := &ledger.PrimaryRequests{DB: integrationDB, Authorize: func(context.Context, *sql.Tx, ledger.Reservation) error { return nil }, SettleScope: func(context.Context, *sql.Tx, ledger.Event) error { return nil }, CancelScope: func(context.Context, *sql.Tx, ledger.Reservation) error { return nil }}
	r := ledger.Reservation{RequestID: fmt.Sprintf("hold-%d", user), UserID: user, Maximum: 6_000_000_000, PayloadSHA256: strings.Repeat("a", 64), Model: "mock-text", PricingVersion: "mock-v1", AdmittedAt: time.Now()}
	l := regionalLedgerLease(fmt.Sprintf("grant-race-%d", user), user, 6_000_000_000)
	var wg sync.WaitGroup
	wg.Add(2)
	errs := make(chan error, 2)
	go func() { defer wg.Done(); _, err := holds.Reserve(ctx, r); errs <- err }()
	go func() { defer wg.Done(); errs <- primary.Issue(ctx, l) }()
	wg.Wait()
	close(errs)
	success, exhausted := 0, 0
	for err := range errs {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, ledger.ErrExhausted)
			exhausted++
		}
	}
	require.Equal(t, 1, success)
	require.Equal(t, 1, exhausted)
	var balance string
	require.NoError(t, integrationDB.QueryRow("SELECT balance::text FROM users WHERE id=$1", user).Scan(&balance))
	require.Equal(t, "4.00000000", balance)
}
