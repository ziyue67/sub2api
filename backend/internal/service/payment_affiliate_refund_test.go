//go:build unit

package service

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/sql/schema"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/migrate"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/stretchr/testify/require"
)

func affiliateRefundFixture(t *testing.T, quota, frozen, balance float64, pending bool) (*PaymentService, *RefundPlan, int64) {
	t.Helper()
	ctx := context.Background()
	c := newAffiliateRefundTestClient(t)
	_, err := c.ExecContext(ctx, `CREATE TABLE user_affiliates(user_id BIGINT PRIMARY KEY,aff_quota NUMERIC,aff_frozen_quota NUMERIC,aff_history_quota NUMERIC,updated_at TIMESTAMP)`)
	require.NoError(t, err)
	idColumn := "INTEGER PRIMARY KEY AUTOINCREMENT"
	if os.Getenv("REFUND_TEST_POSTGRES_DSN") != "" {
		idColumn = "BIGSERIAL PRIMARY KEY"
	}
	_, err = c.ExecContext(ctx, `CREATE TABLE user_affiliate_ledger(id `+idColumn+`,user_id BIGINT,action TEXT,amount NUMERIC,source_user_id BIGINT,source_order_id BIGINT,frozen_until TIMESTAMP,balance_after NUMERIC,aff_quota_after NUMERIC,aff_frozen_quota_after NUMERIC,aff_history_quota_after NUMERIC,created_at TIMESTAMP,updated_at TIMESTAMP)`)
	require.NoError(t, err)
	a, err := c.User.Create().SetEmail("inviter@test.invalid").SetPasswordHash("fake").SetBalance(balance).Save(ctx)
	require.NoError(t, err)
	b, err := c.User.Create().SetEmail("invitee@test.invalid").SetPasswordHash("fake").Save(ctx)
	require.NoError(t, err)
	status := OrderStatusRefunding
	if pending {
		status = OrderStatusRefundPending
	}
	o, err := c.PaymentOrder.Create().SetUserID(b.ID).SetUserEmail(b.Email).SetUserName("test").SetAmount(100).SetPayAmount(100).SetFeeRate(0).SetRechargeCode("test-refund").SetPaymentTradeNo("synthetic").SetClientIP("127.0.0.1").SetSrcHost("test.invalid").SetPaymentType("alipay").SetOutTradeNo("refund-affiliate").SetStatus(status).SetExpiresAt(time.Now().Add(time.Hour)).Save(ctx)
	require.NoError(t, err)
	_, err = c.ExecContext(ctx, `INSERT INTO user_affiliates VALUES($1,$2,$3,20,$4)`, a.ID, quota, frozen, time.Now())
	require.NoError(t, err)
	var until any
	if frozen > 0 {
		until = time.Now().Add(time.Hour)
	}
	_, err = c.ExecContext(ctx, `INSERT INTO user_affiliate_ledger(user_id,action,amount,source_user_id,source_order_id,frozen_until,created_at,updated_at) VALUES($1,'accrue',20,$2,$3,$4,$5,$5)`, a.ID, b.ID, o.ID, until, time.Now())
	require.NoError(t, err)
	return &PaymentService{entClient: c, affiliateService: &AffiliateService{}}, &RefundPlan{OrderID: o.ID, Order: o, RefundAmount: 100}, a.ID
}

func affiliateRefundSnapshot(t *testing.T, c *dbent.Client, id int64) (float64, float64, float64, float64) {
	t.Helper()
	rows, err := c.QueryContext(context.Background(), `SELECT ua.aff_quota,ua.aff_frozen_quota,u.balance,COALESCE((SELECT SUM(-amount) FROM user_affiliate_ledger WHERE user_id=ua.user_id AND action='refund_reversal'),0) FROM user_affiliates ua JOIN users u ON u.id=ua.user_id WHERE ua.user_id=$1`, id)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var q, f, b, r float64
	require.NoError(t, rows.Scan(&q, &f, &b, &r))
	return q, f, b, r
}

func TestRefundReversesAffiliateReward(t *testing.T) {
	for _, tc := range []struct {
		name                                                       string
		quota, frozen, balance, refund, wantQ, wantF, wantB, wantR float64
	}{
		{"available", 20, 0, 0, 100, 0, 0, 0, 20},
		{"transferred", 0, 0, 20, 100, 0, 0, 0, 20},
		{"spent_debt", 0, 0, 3, 100, 0, 0, -17, 20},
		{"frozen", 0, 20, 0, 100, 0, 0, 0, 20},
		{"partial_transferred", 0, 0, 20, 25, 0, 0, 15, 5},
		{"partial_frozen", 0, 20, 0, 25, 0, 15, 0, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, p, id := affiliateRefundFixture(t, tc.quota, tc.frozen, tc.balance, false)
			p.RefundAmount = tc.refund
			result, err := s.finishRefund(context.Background(), p, &payment.RefundResponse{Status: payment.ProviderStatusSuccess})
			require.NoError(t, err)
			require.True(t, result.Success)
			q, f, b, r := affiliateRefundSnapshot(t, s.entClient, id)
			require.Equal(t, tc.wantQ, q)
			require.Equal(t, tc.wantF, f)
			require.Equal(t, tc.wantB, b)
			require.Equal(t, tc.wantR, r)
			_, err = s.markRefundOk(context.Background(), p)
			require.Error(t, err)
			_, _, b2, r2 := affiliateRefundSnapshot(t, s.entClient, id)
			require.Equal(t, b, b2)
			require.Equal(t, r, r2)
		})
	}
}

func TestPendingRefundReversesAffiliateAndRollsBackAtomically(t *testing.T) {
	s, p, id := affiliateRefundFixture(t, 0, 0, 20, true)
	result, err := s.finalizePendingRefundSuccess(context.Background(), p)
	require.NoError(t, err)
	require.True(t, result.Success)
	_, _, b, r := affiliateRefundSnapshot(t, s.entClient, id)
	require.Zero(t, b)
	require.Equal(t, 20.0, r)
}

func TestRefundAffiliateReversalRollsBackWithFinalAudit(t *testing.T) {
	if os.Getenv("REFUND_TEST_POSTGRES_DSN") != "" {
		t.Skip("SQLite fault-injection trigger; PostgreSQL behavior covered separately")
	}
	s, p, id := affiliateRefundFixture(t, 0, 0, 20, false)
	_, err := s.entClient.ExecContext(context.Background(), `CREATE TRIGGER fail_refund_audit BEFORE INSERT ON payment_audit_logs WHEN NEW.action='REFUND_SUCCESS' BEGIN SELECT RAISE(ABORT,'synthetic audit failure'); END`)
	require.NoError(t, err)
	_, err = s.markRefundOk(context.Background(), p)
	require.Error(t, err)
	_, _, balance, reversed := affiliateRefundSnapshot(t, s.entClient, id)
	require.Equal(t, 20.0, balance)
	require.Zero(t, reversed)
	order, err := s.entClient.PaymentOrder.Get(context.Background(), p.OrderID)
	require.NoError(t, err)
	require.Equal(t, OrderStatusRefunding, order.Status)
}

func TestAffiliateReversalIsProportionalAndIdempotent(t *testing.T) {
	s, p, id := affiliateRefundFixture(t, 0, 20, 0, false)
	apply := func(amount float64) {
		tx, err := s.entClient.Tx(context.Background())
		require.NoError(t, err)
		p.RefundAmount = amount
		err = s.reverseRefundAffiliateTx(context.Background(), tx.Client(), p)
		if err != nil {
			_ = tx.Rollback()
		}
		require.NoError(t, err)
		require.NoError(t, tx.Commit())
	}
	apply(25)
	apply(25)
	apply(100)
	apply(100)
	q, f, b, r := affiliateRefundSnapshot(t, s.entClient, id)
	require.Zero(t, q)
	require.Zero(t, f)
	require.Zero(t, b)
	require.Equal(t, 20.0, r)
	rows, err := s.entClient.QueryContext(context.Background(), `SELECT COALESCE(SUM(amount),0) FROM user_affiliate_ledger WHERE frozen_until IS NOT NULL`)
	require.NoError(t, err)
	defer rows.Close()
	require.True(t, rows.Next())
	var thawable float64
	require.NoError(t, rows.Scan(&thawable))
	require.Zero(t, thawable, "a later thaw must not resurrect refunded rewards")
}

func TestPendingRefundDoesNotReverseBeforeGatewayConfirmation(t *testing.T) {
	s, p, id := affiliateRefundFixture(t, 0, 0, 20, false)
	result, err := s.finishRefund(context.Background(), p, &payment.RefundResponse{Status: payment.ProviderStatusPending, RefundID: "synthetic-pending"})
	require.NoError(t, err)
	require.NotNil(t, result)
	_, _, b, r := affiliateRefundSnapshot(t, s.entClient, id)
	require.Equal(t, 20.0, b)
	require.Zero(t, r)
}

func TestRefundDoesNotGuessUnlinkedHistoricalRebates(t *testing.T) {
	s, p, id := affiliateRefundFixture(t, 0, 0, 20, false)
	_, err := s.entClient.ExecContext(context.Background(), `UPDATE user_affiliate_ledger SET source_order_id=NULL`)
	require.NoError(t, err)
	_, err = s.markRefundOk(context.Background(), p)
	require.NoError(t, err)
	_, _, balance, reversed := affiliateRefundSnapshot(t, s.entClient, id)
	require.Equal(t, 20.0, balance)
	require.Zero(t, reversed)
}

func TestRefundedOrderCannotAccrueDelayedAffiliateReward(t *testing.T) {
	s, p, _ := affiliateRefundFixture(t, 0, 0, 20, false)
	for _, status := range []string{OrderStatusRefunding, OrderStatusRefundPending, OrderStatusRefunded, OrderStatusPartiallyRefunded} {
		_, err := s.entClient.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(status).Save(context.Background())
		require.NoError(t, err)
		// p.Order deliberately carries the stale pre-refund snapshot.
		require.NoError(t, s.applyAffiliateRebateForOrder(context.Background(), p.Order))
	}
	count, err := s.entClient.PaymentAuditLog.Query().Count(context.Background())
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestPostgresRefundWaitsForAffiliateTransfer(t *testing.T) {
	if os.Getenv("REFUND_TEST_POSTGRES_DSN") == "" {
		t.Skip("requires disposable PostgreSQL for real row locking")
	}
	s, p, id := affiliateRefundFixture(t, 20, 0, 0, false)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	transfer, err := s.entClient.Tx(ctx)
	require.NoError(t, err)
	defer transfer.Rollback()
	_, err = transfer.Client().ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=0 WHERE user_id=$1`, id)
	require.NoError(t, err)
	_, err = transfer.Client().ExecContext(ctx, `UPDATE users SET balance=balance+20 WHERE id=$1`, id)
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() { _, err := s.markRefundOk(ctx, p); done <- err }()
	// Prove the refund reaches a PostgreSQL lock wait before releasing the transfer.
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		rows, err := s.entClient.QueryContext(ctx, `SELECT COUNT(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE 'UPDATE user_affiliates SET updated_at%'`)
		require.NoError(t, err)
		require.True(t, rows.Next())
		var waiting int
		require.NoError(t, rows.Scan(&waiting))
		rows.Close()
		if waiting > 0 {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("refund finished before transfer commit: %v", err)
		case <-deadline.C:
			t.Fatal("refund did not reach affiliate row lock")
		case <-tick.C:
		}
	}
	require.NoError(t, transfer.Commit())
	require.NoError(t, <-done)
	q, f, b, r := affiliateRefundSnapshot(t, s.entClient, id)
	require.Zero(t, q)
	require.Zero(t, f)
	require.Zero(t, b)
	require.Equal(t, 20.0, r)
}

// Optional real PostgreSQL run uses a fresh schema for each fixture. The supplied
// DSN must point at a disposable test database, never a production database.
func newAffiliateRefundTestClient(t *testing.T) *dbent.Client {
	t.Helper()
	dsn := os.Getenv("REFUND_TEST_POSTGRES_DSN")
	if dsn == "" {
		return newPaymentConfigServiceTestClient(t)
	}
	base, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schemaName := "refund_" + strings.ToLower(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '_'
	}, t.Name()))
	if len(schemaName) > 45 {
		schemaName = schemaName[:45]
	}
	schemaName += fmt.Sprintf("_%x", time.Now().UnixNano()%100000000)
	_, err = base.Exec(`CREATE SCHEMA ` + schemaName)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = base.Exec(`DROP SCHEMA ` + schemaName + ` CASCADE`); _ = base.Close() })
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schemaName)
	u.RawQuery = q.Encode()
	c, err := dbent.Open(dialect.Postgres, u.String())
	require.NoError(t, err)
	require.NoError(t, migrate.Create(context.Background(), c.Schema, []*schema.Table{migrate.UsersTable, migrate.PaymentOrdersTable, migrate.PaymentAuditLogsTable}, migrate.WithForeignKeys(false)))
	t.Cleanup(func() { _ = c.Close() })
	return c
}
