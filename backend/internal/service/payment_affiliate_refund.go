package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
)

// reverseRefundAffiliateTx runs in the same transaction as the refund terminal
// state. Order state serializes repeated finalization; the affiliate row lock
// serializes against transfer/withdraw/accrual. Never infer historical rebates
// from today's rate: only reverse ledger entries tied to this order.
func (s *PaymentService) reverseRefundAffiliateTx(ctx context.Context, client *dbent.Client, p *RefundPlan) error {
	if s.affiliateService == nil || p.Order.Amount <= 0 || p.RefundAmount <= 0 {
		return nil
	}
	rows, err := client.QueryContext(ctx, `SELECT DISTINCT user_id FROM user_affiliate_ledger WHERE source_order_id = $1 AND action = 'accrue' ORDER BY user_id`, p.OrderID)
	if err != nil {
		return fmt.Errorf("find refund affiliate owners: %w", err)
	}
	var owners []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		owners = append(owners, id)
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, id := range owners {
		// UPDATE obtains the same row lock as affiliate transfers, including when
		// quota is zero. Locks are acquired before reading freeze/ledger state.
		res, err := client.ExecContext(ctx, `UPDATE user_affiliates SET updated_at = updated_at WHERE user_id = $1`, id)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("missing affiliate profile for refund owner %d", id)
		}
		rows, err = client.QueryContext(ctx, `SELECT COALESCE(SUM(CASE WHEN action = 'accrue' THEN amount ELSE 0 END),0), COALESCE(SUM(CASE WHEN action = 'refund_reversal' THEN -amount ELSE 0 END),0), COALESCE(SUM(CASE WHEN frozen_until IS NOT NULL THEN amount ELSE 0 END),0) FROM user_affiliate_ledger WHERE user_id = $1 AND source_order_id = $2`, id, p.OrderID)
		if err != nil {
			return err
		}
		var accrued, reversed, frozen float64
		if !rows.Next() {
			_ = rows.Close()
			return fmt.Errorf("missing affiliate aggregate")
		}
		err = rows.Scan(&accrued, &reversed, &frozen)
		_ = rows.Close()
		if err != nil {
			return err
		}
		target := roundTo(accrued*math.Min(1, p.RefundAmount/p.Order.Amount), 8)
		amount := roundTo(math.Max(0, target-reversed), 8)
		if amount <= 0 {
			continue
		}
		// Cancel frozen rewards with equally frozen negative ledger entries, so
		// later thaw cannot resurrect money which was already refunded.
		frozenDebit := math.Min(amount, math.Max(0, frozen))
		if frozenDebit > 0 {
			rows, err = client.QueryContext(ctx, `SELECT MIN(id), SUM(amount) FROM user_affiliate_ledger WHERE user_id=$1 AND source_order_id=$2 AND action IN ('accrue','refund_reversal') AND frozen_until IS NOT NULL GROUP BY frozen_until HAVING SUM(amount)>0 ORDER BY frozen_until`, id, p.OrderID)
			if err != nil {
				return err
			}
			type frozenEntry struct {
				id     int64
				amount float64
			}
			var entries []frozenEntry
			for rows.Next() {
				var e frozenEntry
				if err = rows.Scan(&e.id, &e.amount); err != nil {
					_ = rows.Close()
					return err
				}
				entries = append(entries, e)
			}
			if err = rows.Err(); err != nil {
				_ = rows.Close()
				return err
			}
			_ = rows.Close()
			left := frozenDebit
			for _, e := range entries {
				take := math.Min(left, e.amount)
				if take <= 0 {
					continue
				}
				// Keep the original accrual amount for audit/proportional refunds. A
				// negative, equally frozen ledger entry cancels its later thaw exactly.
				_, err = client.ExecContext(ctx, `INSERT INTO user_affiliate_ledger (user_id,action,amount,source_user_id,source_order_id,frozen_until,created_at,updated_at) SELECT user_id,'refund_reversal',(0-CAST($1 AS NUMERIC)),source_user_id,source_order_id,frozen_until,$2,$2 FROM user_affiliate_ledger WHERE id=$3`, take, time.Now(), e.id)
				if err != nil {
					return err
				}
				left = roundTo(left-take, 8)
			}
		}
		remaining := roundTo(amount-frozenDebit, 8)
		rows, err = client.QueryContext(ctx, `SELECT aff_quota FROM user_affiliates WHERE user_id=$1`, id)
		if err != nil {
			return err
		}
		var quota float64
		if !rows.Next() {
			_ = rows.Close()
			return fmt.Errorf("missing affiliate quota")
		}
		err = rows.Scan(&quota)
		_ = rows.Close()
		if err != nil {
			return err
		}
		quotaDebit := math.Min(remaining, math.Max(0, quota))
		balanceDebit := roundTo(remaining-quotaDebit, 8)
		_, err = client.ExecContext(ctx, `UPDATE user_affiliates SET aff_quota=aff_quota-$1,aff_frozen_quota=aff_frozen_quota-$2,aff_history_quota=aff_history_quota-$3,updated_at=$4 WHERE user_id=$5`, quotaDebit, frozenDebit, amount, time.Now(), id)
		if err != nil {
			return err
		}
		if balanceDebit > 0 {
			// A spent/withdrawn rebate is debt, not silently forgiven at zero balance.
			_, err = client.ExecContext(ctx, `UPDATE users SET balance=balance-$1,updated_at=$2 WHERE id=$3`, balanceDebit, time.Now(), id)
			if err != nil {
				return err
			}
		}
		if remaining > 0 {
			_, err = client.ExecContext(ctx, `INSERT INTO user_affiliate_ledger (user_id,action,amount,source_user_id,source_order_id,balance_after,aff_quota_after,aff_frozen_quota_after,aff_history_quota_after,created_at,updated_at) SELECT ua.user_id,'refund_reversal',(0-CAST($1 AS NUMERIC)),$2,$3,u.balance,ua.aff_quota,ua.aff_frozen_quota,ua.aff_history_quota,$4,$4 FROM user_affiliates ua JOIN users u ON u.id=ua.user_id WHERE ua.user_id=$5`, remaining, p.Order.UserID, p.OrderID, time.Now(), id)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

// Invalidate only after commit, so cache refill cannot resurrect pre-commit
// balances. Covers transferred rebates even when the feature is disabled now.
func (s *PaymentService) invalidateRefundAffiliateCaches(ctx context.Context, orderID int64) {
	if s.affiliateService == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	rows, err := s.entClient.QueryContext(ctx, `SELECT DISTINCT user_id FROM user_affiliate_ledger WHERE source_order_id=$1 AND action='refund_reversal'`, orderID)
	if err != nil {
		slog.Error("refund affiliate cache invalidation lookup failed", "order_id", orderID, "error", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			slog.Error("refund affiliate cache invalidation scan failed", "order_id", orderID, "error", err)
			continue
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		slog.Error("refund affiliate cache invalidation rows failed", "order_id", orderID, "error", err)
	}
	_ = rows.Close()
	for _, id := range ids {
		s.affiliateService.invalidateAffiliateCaches(ctx, id)
	}
}
