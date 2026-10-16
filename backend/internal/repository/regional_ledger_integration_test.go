//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/stretchr/testify/require"
)

func regionalLedgerUser(t *testing.T) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRow(`INSERT INTO users(email,password_hash,status,balance) VALUES($1,'test','active',10) RETURNING id`, fmt.Sprintf("ledger-%d@test.invalid", time.Now().UnixNano())).Scan(&id))
	t.Cleanup(func() {
		_, _ = integrationDB.Exec(`DELETE FROM regional_usage_outbox WHERE event_id IN (SELECT request_id FROM regional_local_requests WHERE lease_id IN (SELECT lease_id FROM regional_local_leases WHERE user_id=$1))`, id)
		_, _ = integrationDB.Exec(`DELETE FROM regional_local_requests WHERE lease_id IN (SELECT lease_id FROM regional_local_leases WHERE user_id=$1)`, id)
		_, _ = integrationDB.Exec(`DELETE FROM regional_local_leases WHERE user_id=$1`, id)
		_, _ = integrationDB.Exec(`DELETE FROM regional_settled_events WHERE lease_id IN (SELECT lease_id FROM regional_quota_leases WHERE user_id=$1)`, id)
		_, _ = integrationDB.Exec(`DELETE FROM regional_quota_leases WHERE user_id=$1`, id)
		_, _ = integrationDB.Exec(`DELETE FROM users WHERE id=$1`, id)
	})
	return id
}
func regionalLedgerLease(id string, user int64, amount regional.Amount) regional.Lease {
	now := time.Now().UTC().Add(-time.Second).Truncate(time.Microsecond)
	return regional.Lease{SchemaVersion: 1, LeaseID: id, RegionID: "us", UserID: user, Granted: amount, IssuedAt: now, ExpiresAt: now.Add(time.Hour)}
}
func regionalLedgerReservation(l regional.Lease, id string, maximum regional.Amount) ledger.Reservation {
	return ledger.Reservation{RequestID: id, LeaseID: l.LeaseID, RegionID: l.RegionID, UserID: l.UserID, APIKeyID: 1, AccountID: 2, AdmittedAt: time.Now().UTC().Add(-time.Millisecond), Maximum: maximum, PayloadSHA256: strings.Repeat("a", 64), PricingVersion: "test-1", Model: "test-model"}
}
func regionalLedgerEvent(r ledger.Reservation, charge regional.Amount) ledger.Event {
	return ledger.Event{Usage: regional.UsageEvent{SchemaVersion: 1, EventID: r.RequestID, LeaseID: r.LeaseID, RegionID: r.RegionID, UserID: r.UserID, APIKeyID: r.APIKeyID, AccountID: r.AccountID, AdmittedAt: r.AdmittedAt, CompletedAt: time.Now().UTC(), Charge: charge}, Model: r.Model, PricingVersion: r.PricingVersion, PayloadSHA256: r.PayloadSHA256, InputTokens: 10, OutputTokens: 5}
}
func TestRegionalLedgerIssuanceIdempotencyAndRegions(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	p := &ledger.Primary{DB: integrationDB}
	l := regionalLedgerLease(fmt.Sprintf("lease-%d", user), user, 6_000_000_000)
	var wg sync.WaitGroup
	errorsCh := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errorsCh <- p.Issue(ctx, l) }()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		require.NoError(t, err)
	}
	var balance string
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, "4.00000000", balance)
	conflict := l
	conflict.Granted = 5_000_000_000
	require.ErrorIs(t, p.Issue(ctx, conflict), ledger.ErrConflict)
	second := regionalLedgerLease(fmt.Sprintf("lease-other-%d", user), user, 6_000_000_000)
	second.RegionID = "eu"
	require.ErrorIs(t, p.Issue(ctx, second), ledger.ErrExhausted)
	local := &ledger.Local{DB: integrationDB, Region: "us"}
	require.NoError(t, local.Install(ctx, l))
	require.NoError(t, local.Install(ctx, l))
	require.ErrorIs(t, local.Install(ctx, conflict), ledger.ErrConflict)
	require.ErrorIs(t, (&ledger.Local{DB: integrationDB, Region: "eu"}).Install(ctx, l), ledger.ErrConflict)
}
func TestRegionalLedgerOutboxRecoveryLateSettlementAndClosure(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	p := &ledger.Primary{DB: integrationDB}
	local := &ledger.Local{DB: integrationDB, Region: "us"}
	l := regionalLedgerLease(fmt.Sprintf("recovery-%d", user), user, 5_000_000_000)
	require.NoError(t, p.Issue(ctx, l))
	require.NoError(t, local.Install(ctx, l))
	r := regionalLedgerReservation(l, fmt.Sprintf("req-%d", user), 4_000_000_000)
	fresh, err := local.Reserve(ctx, r)
	require.NoError(t, err)
	require.True(t, fresh)
	fresh, err = local.Reserve(ctx, r)
	require.NoError(t, err)
	require.False(t, fresh)
	bad := r
	bad.Maximum -= 10
	_, err = local.Reserve(ctx, bad)
	require.ErrorIs(t, err, ledger.ErrConflict)
	require.NoError(t, local.Start(ctx, r.RequestID))
	require.ErrorIs(t, local.Start(ctx, r.RequestID), ledger.ErrAlreadyDispatched)
	// A new ledger object models process death: started work remains occupied and
	// cannot be canceled or repeated just because no final usage was received.
	local = &ledger.Local{DB: integrationDB, Region: "us"}
	require.ErrorIs(t, local.CancelBeforeStart(ctx, r.RequestID), ledger.ErrAlreadyDispatched)
	require.NoError(t, local.Drain(ctx, l.LeaseID))
	_, err = local.Close(ctx, l.LeaseID)
	require.Error(t, err)
	e := regionalLedgerEvent(r, 2_000_000_000)
	e.Usage.CompletedAt = l.ExpiresAt.Add(time.Minute)
	// Completing AFTER expiry is allowed for a previously admitted request.
	require.NoError(t, local.Complete(ctx, e))
	require.NoError(t, local.Complete(ctx, e))
	conflict := e
	conflict.OutputTokens++
	require.ErrorIs(t, local.Complete(ctx, conflict), ledger.ErrConflict)
	pending, err := local.Pending(ctx, 100)
	require.NoError(t, err)
	require.Contains(t, pending, e)
	_, err = local.Close(ctx, l.LeaseID)
	require.Error(t, err)
	// No verifier means no event, no spend and no local acknowledgement.
	_, err = p.Settle(ctx, "us", e, nil)
	require.Error(t, err)
	failedVerifier := func(context.Context, *sql.Tx, ledger.Event) error { return fmt.Errorf("pricing unavailable") }
	_, err = p.Settle(ctx, "us", e, failedVerifier)
	require.Error(t, err)
	verified := 0
	verify := func(context.Context, *sql.Tx, ledger.Event) error { verified++; return nil }
	applied, err := p.Settle(ctx, "us", e, verify)
	require.NoError(t, err)
	require.True(t, applied)
	applied, err = p.Settle(ctx, "us", e, verify)
	require.NoError(t, err)
	require.False(t, applied)
	require.Equal(t, 1, verified)
	_, err = p.Settle(ctx, "us", conflict, verify)
	require.ErrorIs(t, err, ledger.ErrConflict)
	require.ErrorIs(t, local.Acknowledge(ctx, conflict), ledger.ErrConflict)
	require.NoError(t, local.Acknowledge(ctx, e))
	receipt, err := local.Close(ctx, l.LeaseID)
	require.NoError(t, err)
	require.Equal(t, e.Usage.Charge, receipt.Spent)
	require.ErrorIs(t, p.Close(ctx, "eu", receipt), ledger.ErrConflict)
	require.NoError(t, p.Close(ctx, "us", receipt))
	require.NoError(t, p.Close(ctx, "us", receipt))
	var balance string
	require.NoError(t, integrationDB.QueryRow(`SELECT balance::text FROM users WHERE id=$1`, user).Scan(&balance))
	require.Equal(t, "8.00000000", balance)
	// Closed local leases cannot dispatch newly submitted work even after restore.
	r2 := regionalLedgerReservation(l, fmt.Sprintf("req-after-%d", user), 10)
	_, err = local.Reserve(ctx, r2)
	require.ErrorIs(t, err, ledger.ErrExhausted)
}
func TestRegionalLedgerConcurrentReservationsCannotOverspend(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	local := &ledger.Local{DB: integrationDB, Region: "us"}
	l := regionalLedgerLease(fmt.Sprintf("concurrent-%d", user), user, 1_000_000_000)
	require.NoError(t, local.Install(ctx, l))
	type outcome struct {
		r   ledger.Reservation
		err error
	}
	results := make(chan outcome, 12)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := regionalLedgerReservation(l, fmt.Sprintf("concurrent-%d-%d", user, i), 250_000_000)
			_, err := local.Reserve(ctx, r)
			results <- outcome{r, err}
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := []ledger.Reservation{}
	for result := range results {
		if result.err == nil {
			accepted = append(accepted, result.r)
		} else {
			require.ErrorIs(t, result.err, ledger.ErrExhausted)
		}
	}
	require.Len(t, accepted, 4)
	require.NoError(t, local.Drain(ctx, l.LeaseID))
	for _, r := range accepted {
		require.ErrorIs(t, local.Start(ctx, r.RequestID), ledger.ErrExhausted)
		require.NoError(t, local.CancelBeforeStart(ctx, r.RequestID))
		require.NoError(t, local.CancelBeforeStart(ctx, r.RequestID))
	}
	receipt, err := local.Close(ctx, l.LeaseID)
	require.NoError(t, err)
	require.Zero(t, receipt.Spent)
}

type regionalLedgerSettlementClient struct {
	p            *ledger.Primary
	disconnected bool
	calls        int
}

func (c *regionalLedgerSettlementClient) Settle(ctx context.Context, e ledger.Event) error {
	c.calls++
	if c.disconnected {
		return fmt.Errorf("authority disconnected")
	}
	_, err := c.p.Settle(ctx, "us", e, func(context.Context, *sql.Tx, ledger.Event) error { return nil })
	return err
}
func TestRegionalLedgerOutboxDisconnectAndAcknowledgementLoss(t *testing.T) {
	ctx := context.Background()
	user := regionalLedgerUser(t)
	l := regionalLedgerLease(fmt.Sprintf("worker-%d", user), user, 1_000_000_000)
	p := &ledger.Primary{DB: integrationDB}
	local := &ledger.Local{DB: integrationDB, Region: "us"}
	require.NoError(t, p.Issue(ctx, l))
	require.NoError(t, local.Install(ctx, l))
	r := regionalLedgerReservation(l, fmt.Sprintf("worker-req-%d", user), 1_000_000_000)
	fresh, err := local.Reserve(ctx, r)
	require.NoError(t, err)
	require.True(t, fresh)
	require.NoError(t, local.Start(ctx, r.RequestID))
	e := regionalLedgerEvent(r, 500_000_000)
	require.NoError(t, local.Complete(ctx, e))
	client := &regionalLedgerSettlementClient{p: p, disconnected: true}
	_, err = local.Flush(ctx, client, 100)
	require.Error(t, err)
	events, err := local.Pending(ctx, 100)
	require.NoError(t, err)
	require.Contains(t, events, e)
	// Primary commits, then the node dies before its local acknowledgement.
	_, err = p.Settle(ctx, "us", e, func(context.Context, *sql.Tx, ledger.Event) error { return nil })
	require.NoError(t, err)
	local = &ledger.Local{DB: integrationDB, Region: "us"}
	client.disconnected = false
	_, err = local.Flush(ctx, client, 100)
	require.NoError(t, err)
	events, err = local.Pending(ctx, 100)
	require.NoError(t, err)
	for _, event := range events {
		require.NotEqual(t, e.Usage.EventID, event.Usage.EventID)
	}
	var spent int64
	require.NoError(t, integrationDB.QueryRow(`SELECT spent FROM regional_quota_leases WHERE lease_id=$1`, l.LeaseID).Scan(&spent))
	require.Equal(t, int64(e.Usage.Charge), spent)
}
