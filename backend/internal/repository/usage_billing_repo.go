package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type usageBillingRepository struct {
	db *sql.DB
}

const (
	gatewayBalanceReservationStatusReserved = "reserved"
	gatewayBalanceReservationStatusSettled  = "settled"
	gatewayBalanceReservationStatusReleased = "released"
	reservationAmountEpsilon                = 0.00000001
)

func NewUsageBillingRepository(_ *dbent.Client, sqlDB *sql.DB) service.UsageBillingRepository {
	return &usageBillingRepository{db: sqlDB}
}

// ReserveGatewayBalance atomically moves the user's currently available
// balance into frozen_balance and records the request-scoped hold. When
// ReserveAvailableBalance is set, the authoritative wallet row is locked and
// read inside this transaction, so callers do not need a second DB round trip
// that could observe a stale balance.
func (r *usageBillingRepository) ReserveGatewayBalance(ctx context.Context, cmd *service.BalanceReservationCommand) (_ *service.BalanceReservationResult, err error) {
	if cmd == nil || strings.TrimSpace(cmd.RequestID) == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	amount := service.QuantizeUsageBillingAmount(cmd.Amount)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	// A retry/failover can see zero available balance because the original
	// request already moved it to frozen_balance. Reuse an existing durable hold
	// before applying the new amount gate.
	var existingUserID int64
	var existingStatus string
	lookupErr := tx.QueryRowContext(ctx, `
		SELECT user_id, status
		FROM gateway_balance_reservations
		WHERE request_id = $1
		FOR UPDATE
	`, cmd.RequestID).Scan(&existingUserID, &existingStatus)
	if lookupErr == nil {
		if existingUserID != cmd.UserID {
			return nil, service.ErrUsageBillingRequestConflict
		}
		if existingStatus != gatewayBalanceReservationStatusReserved {
			return nil, service.ErrBalanceReservationClosed
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.BalanceReservationResult{Applied: false}, nil
	}
	if !errors.Is(lookupErr, sql.ErrNoRows) {
		return nil, lookupErr
	}
	if cmd.ReserveAvailableBalance {
		// Lock the wallet before deriving the hold amount. Concurrent requests for
		// the same user serialize here and each sees the post-commit balance.
		if err := tx.QueryRowContext(ctx, `
			SELECT balance
			FROM users
			WHERE id = $1 AND deleted_at IS NULL
			FOR UPDATE
		`, cmd.UserID).Scan(&amount); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
					return nil, existsErr
				} else if !exists {
					return nil, service.ErrUserNotFound
				}
				return nil, service.ErrInsufficientBalance
			}
			return nil, err
		}
		amount = service.QuantizeUsageBillingAmount(amount)
		minimum := service.QuantizeUsageBillingAmount(cmd.MinimumBalance)
		if minimum > 0 && amount < minimum {
			return nil, service.ErrInsufficientBalance
		}
	} else if amount <= 0 {
		return nil, service.ErrInsufficientBalance
	}
	if amount <= 0 {
		return nil, service.ErrInsufficientBalance
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO gateway_balance_reservations
			(request_id, api_key_id, user_id, reserved_amount, status, expires_at)
		VALUES ($1, $2, $3, $4, $5, NOW() + INTERVAL '30 minutes')
		ON CONFLICT (request_id) DO NOTHING
		RETURNING id
	`, cmd.RequestID, cmd.APIKeyID, cmd.UserID, amount, gatewayBalanceReservationStatusReserved).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		if err := tx.QueryRowContext(ctx, `
			SELECT user_id, status
			FROM gateway_balance_reservations
			WHERE request_id = $1
		`, cmd.RequestID).Scan(&existingUserID, &existingStatus); err != nil {
			return nil, err
		}
		if existingUserID != cmd.UserID {
			return nil, service.ErrUsageBillingRequestConflict
		}
		if existingStatus != gatewayBalanceReservationStatusReserved {
			return nil, service.ErrBalanceReservationClosed
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		tx = nil
		return &service.BalanceReservationResult{Applied: false, Reserved: amount}, nil
	}
	if err != nil {
		return nil, err
	}

	var balance, frozen float64
	err = tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			frozen_balance = COALESCE(frozen_balance, 0) + $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance, frozen_balance
	`, amount, cmd.UserID).Scan(&balance, &frozen)
	if errors.Is(err, sql.ErrNoRows) {
		if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
			return nil, existsErr
		} else if !exists {
			return nil, service.ErrUserNotFound
		}
		return nil, service.ErrInsufficientBalance
	}
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return &service.BalanceReservationResult{
		Applied:       true,
		Reserved:      amount,
		NewBalance:    balance,
		FrozenBalance: frozen,
	}, nil
}

// CleanupExpiredGatewayBalanceReservations releases a bounded batch of holds
// left behind by crashed processes. SKIP LOCKED keeps cleanup from blocking a
// live capture/release transaction, and the limit prevents a large backlog
// from monopolizing a database connection.
func (r *usageBillingRepository) CleanupExpiredGatewayBalanceReservations(ctx context.Context, limit int) (count int, err error) {
	if r == nil || r.db == nil {
		return 0, errors.New("usage billing repository db is nil")
	}
	if limit <= 0 {
		limit = 100
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()
	res, err := tx.ExecContext(ctx, `
		WITH expired AS (
			SELECT id, user_id, reserved_amount
			FROM gateway_balance_reservations
			WHERE status = 'reserved' AND expires_at <= NOW()
			ORDER BY expires_at ASC, id ASC
			FOR UPDATE SKIP LOCKED
			LIMIT $1
		), released AS (
			UPDATE gateway_balance_reservations r
			SET actual_amount = 0, status = 'released', updated_at = NOW()
			FROM expired e
			WHERE r.id = e.id
			RETURNING e.user_id, e.reserved_amount
		), totals AS (
			SELECT user_id, SUM(reserved_amount) AS amount
			FROM released
			GROUP BY user_id
		)
		UPDATE users u
		SET balance = u.balance + totals.amount,
			frozen_balance = GREATEST(COALESCE(u.frozen_balance, 0) - totals.amount, 0),
			updated_at = NOW()
		FROM totals
		WHERE u.id = totals.user_id AND u.deleted_at IS NULL
	`, limit)
	if err != nil {
		return 0, err
	}
	var affected int64
	affected, err = res.RowsAffected()
	if err != nil {
		return 0, err
	}
	count = int(affected)
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	tx = nil
	return count, nil
}

// CaptureGatewayBalance settles a reservation and returns any unused hold to
// the available balance. It never permits actual cost to exceed the hold.
func (r *usageBillingRepository) CaptureGatewayBalance(ctx context.Context, cmd *service.BalanceReservationCommand) (_ *service.BalanceReservationResult, err error) {
	return r.applyGatewayBalanceReservation(ctx, cmd, service.BalanceReservationCapture)
}

// ReleaseGatewayBalance returns the full hold after an upstream failure or
// request cancellation.
func (r *usageBillingRepository) ReleaseGatewayBalance(ctx context.Context, cmd *service.BalanceReservationCommand) (_ *service.BalanceReservationResult, err error) {
	return r.applyGatewayBalanceReservation(ctx, cmd, service.BalanceReservationRelease)
}

func (r *usageBillingRepository) applyGatewayBalanceReservation(ctx context.Context, cmd *service.BalanceReservationCommand, operation service.BalanceReservationOperation) (_ *service.BalanceReservationResult, err error) {
	if cmd == nil || strings.TrimSpace(cmd.RequestID) == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	var reservationID, userID int64
	var reserved float64
	var status string
	err = tx.QueryRowContext(ctx, `
		SELECT id, user_id, reserved_amount, status
		FROM gateway_balance_reservations
		WHERE request_id = $1
		FOR UPDATE
	`, cmd.RequestID).Scan(&reservationID, &userID, &reserved, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrBalanceReservationNotFound
	}
	if err != nil {
		return nil, err
	}
	if userID != cmd.UserID {
		return nil, service.ErrUsageBillingRequestConflict
	}
	if status != gatewayBalanceReservationStatusReserved {
		return nil, service.ErrBalanceReservationClosed
	}

	actual := service.QuantizeUsageBillingAmount(cmd.Amount)
	if operation == service.BalanceReservationRelease {
		actual = 0
	}
	if actual > reserved+reservationAmountEpsilon {
		return nil, service.ErrBalanceReservationExceedsHold
	}

	var balance, frozen float64
	if err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1 - $2,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, reserved, actual, cmd.UserID).Scan(&balance, &frozen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrBalanceReservationClosed
		}
		return nil, err
	}
	newStatus := gatewayBalanceReservationStatusSettled
	if operation == service.BalanceReservationRelease {
		newStatus = gatewayBalanceReservationStatusReleased
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE gateway_balance_reservations
		SET actual_amount = $1, status = $2, updated_at = NOW()
		WHERE id = $3
	`, actual, newStatus, reservationID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return &service.BalanceReservationResult{
		Applied:       true,
		Reserved:      reserved,
		Actual:        actual,
		NewBalance:    balance,
		FrozenBalance: frozen,
	}, nil
}

func (r *usageBillingRepository) Apply(ctx context.Context, cmd *service.UsageBillingCommand) (_ *service.UsageBillingApplyResult, err error) {
	if cmd == nil {
		return &service.UsageBillingApplyResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}

	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingKey(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.UsageBillingApplyResult{Applied: false}, nil
	}

	result := &service.UsageBillingApplyResult{Applied: true}
	if err := r.applyUsageBillingEffects(ctx, tx, cmd, result); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) claimUsageBillingKey(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand) (bool, error) {
	return r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
}

func (r *usageBillingRepository) claimUsageBillingRequest(ctx context.Context, tx *sql.Tx, requestID string, apiKeyID int64, requestFingerprint string) (bool, error) {
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO usage_billing_dedup (request_id, api_key_id, request_fingerprint)
		VALUES ($1, $2, $3)
		ON CONFLICT (request_id, api_key_id) DO NOTHING
		RETURNING id
	`, requestID, apiKeyID, requestFingerprint).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		var existingFingerprint string
		if err := tx.QueryRowContext(ctx, `
			SELECT request_fingerprint
			FROM usage_billing_dedup
			WHERE request_id = $1 AND api_key_id = $2
		`, requestID, apiKeyID).Scan(&existingFingerprint); err != nil {
			return false, err
		}
		if strings.TrimSpace(existingFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var archivedFingerprint string
	err = tx.QueryRowContext(ctx, `
		SELECT request_fingerprint
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, requestID, apiKeyID).Scan(&archivedFingerprint)
	if err == nil {
		if strings.TrimSpace(archivedFingerprint) != strings.TrimSpace(requestFingerprint) {
			return false, service.ErrUsageBillingRequestConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	return true, nil
}

func (r *usageBillingRepository) ReserveBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, reserveUsageBillingBatchImageBalance)
}

func (r *usageBillingRepository) CaptureBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, captureUsageBillingBatchImageBalance)
}

func (r *usageBillingRepository) ReleaseBatchImageBalance(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	return r.applyBatchImageBalanceHold(ctx, cmd, releaseUsageBillingBatchImageBalance)
}

func (r *usageBillingRepository) applyBatchImageBalanceHold(
	ctx context.Context,
	cmd *service.BatchImageBalanceHoldCommand,
	apply func(context.Context, *sql.Tx, *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error),
) (_ *service.BatchImageBalanceHoldResult, err error) {
	if cmd == nil {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if r == nil || r.db == nil {
		return nil, errors.New("usage billing repository db is nil")
	}
	cmd.Normalize()
	if cmd.RequestID == "" {
		return nil, service.ErrUsageBillingRequestIDRequired
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	applied, err := r.claimUsageBillingRequest(ctx, tx, cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
	if err != nil {
		return nil, err
	}
	if !applied {
		return &service.BatchImageBalanceHoldResult{Applied: false}, nil
	}

	result, err := apply(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	if result == nil {
		result = &service.BatchImageBalanceHoldResult{}
	}
	result.Applied = true

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	tx = nil
	return result, nil
}

func (r *usageBillingRepository) applyUsageBillingEffects(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, result *service.UsageBillingApplyResult) error {
	if cmd.SubscriptionCost > 0 && cmd.SubscriptionID != nil {
		if err := incrementUsageBillingSubscription(ctx, tx, *cmd.SubscriptionID, cmd.SubscriptionCost); err != nil {
			return err
		}
	}

	if cmd.BalanceCost > 0 {
		reservation, found, err := captureGatewayBalanceReservationTx(ctx, tx, cmd, cmd.BalanceCost)
		if err != nil {
			return err
		}
		if found {
			result.NewBalance = &reservation.NewBalance
			result.BalanceReservationSettled = true
		} else {
			newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, cmd.UserID, cmd.BalanceCost)
			if err != nil {
				return err
			}
			result.NewBalance = &newBalance
			result.BalanceOverdrafted = !sufficient
		}
	} else if cmd.BillingType == service.BillingTypeBalance {
		// A zero-cost response still has to close a full-wallet reservation.
		// Otherwise a free/error response would strand frozen funds.
		reservation, found, err := captureGatewayBalanceReservationTx(ctx, tx, cmd, 0)
		if err != nil {
			return err
		}
		if found {
			result.NewBalance = &reservation.NewBalance
			result.BalanceReservationSettled = true
		}
	}

	if cmd.APIKeyQuotaCost > 0 {
		exhausted, err := incrementUsageBillingAPIKeyQuota(ctx, tx, cmd.APIKeyID, cmd.APIKeyQuotaCost)
		if err != nil {
			return err
		}
		result.APIKeyQuotaExhausted = exhausted
	}

	if cmd.APIKeyRateLimitCost > 0 {
		if err := incrementUsageBillingAPIKeyRateLimit(ctx, tx, cmd.APIKeyID, cmd.APIKeyRateLimitCost); err != nil {
			return err
		}
	}

	if cmd.AccountQuotaCost > 0 && (strings.EqualFold(cmd.AccountType, service.AccountTypeAPIKey) || strings.EqualFold(cmd.AccountType, service.AccountTypeBedrock)) {
		quotaState, err := incrementUsageBillingAccountQuota(ctx, tx, cmd.AccountID, cmd.AccountQuotaCost)
		if err != nil {
			return err
		}
		result.QuotaState = quotaState
	}

	return nil
}

// captureGatewayBalanceReservationTx settles an existing gateway hold inside
// the caller's billing transaction. found=false means this request predates
// gateway precharge or belongs to a non-precharged path and must use the
// guarded direct deduction fallback.
func captureGatewayBalanceReservationTx(ctx context.Context, tx *sql.Tx, cmd *service.UsageBillingCommand, actualAmount float64) (*service.BalanceReservationResult, bool, error) {
	if cmd == nil || strings.TrimSpace(cmd.RequestID) == "" {
		return nil, false, nil
	}
	var reservationID, userID int64
	var reserved float64
	var status string
	err := tx.QueryRowContext(ctx, `
		SELECT id, user_id, reserved_amount, status
		FROM gateway_balance_reservations
		WHERE request_id = $1
		FOR UPDATE
	`, cmd.RequestID).Scan(&reservationID, &userID, &reserved, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if userID != cmd.UserID {
		return nil, true, service.ErrUsageBillingRequestConflict
	}
	if status != gatewayBalanceReservationStatusReserved {
		return nil, true, service.ErrBalanceReservationClosed
	}
	actual := service.QuantizeUsageBillingAmount(actualAmount)
	if actual > reserved+reservationAmountEpsilon {
		return nil, true, service.ErrBalanceReservationExceedsHold
	}
	var balance, frozen float64
	if err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1 - $2,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, reserved, actual, cmd.UserID).Scan(&balance, &frozen); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, true, service.ErrBalanceReservationClosed
		}
		return nil, true, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE gateway_balance_reservations
		SET actual_amount = $1, status = $2, updated_at = NOW()
		WHERE id = $3
	`, actual, gatewayBalanceReservationStatusSettled, reservationID); err != nil {
		return nil, true, err
	}
	return &service.BalanceReservationResult{
		Applied:       true,
		Reserved:      reserved,
		Actual:        actual,
		NewBalance:    balance,
		FrozenBalance: frozen,
	}, true, nil
}

func incrementUsageBillingSubscription(ctx context.Context, tx *sql.Tx, subscriptionID int64, costUSD float64) error {
	const updateSQL = `
		UPDATE user_subscriptions us
		SET
			daily_usage_usd = us.daily_usage_usd + $1,
			weekly_usage_usd = us.weekly_usage_usd + $1,
			monthly_usage_usd = us.monthly_usage_usd + $1,
			updated_at = NOW()
		FROM groups g
		WHERE us.id = $2
			AND us.deleted_at IS NULL
			AND us.group_id = g.id
			AND g.deleted_at IS NULL
	`
	res, err := tx.ExecContext(ctx, updateSQL, costUSD, subscriptionID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected > 0 {
		return nil
	}
	return service.ErrSubscriptionNotFound
}

func deductUsageBillingBalance(ctx context.Context, tx *sql.Tx, userID int64, amount float64) (float64, bool, error) {
	if amount <= 0 {
		return 0, true, nil
	}
	var newBalance float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance
	`, amount, userID).Scan(&newBalance)
	if err == nil {
		return newBalance, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	// Do not fall back to an unconditional UPDATE. That fallback was the
	// overdraft bug: concurrent requests could all pass the stale preflight and
	// then drive the wallet below zero. Distinguish a missing user from an
	// existing user whose balance is insufficient while remaining atomic with the
	// surrounding billing transaction.
	var exists bool
	if err := tx.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM users WHERE id = $1 AND deleted_at IS NULL
		)
	`, userID).Scan(&exists); err != nil {
		return 0, false, err
	}
	if !exists {
		return 0, false, service.ErrUserNotFound
	}
	return 0, false, service.ErrInsufficientBalance
}

func reserveUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance - $1,
			frozen_balance = COALESCE(frozen_balance, 0) + $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND balance >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, service.ErrBatchImageInsufficientBalance
}

func captureUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 && cmd.ActualAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	if cmd.ActualAmount-cmd.HoldAmount > 0.00000001 {
		return nil, service.ErrBatchImageSettlementCostExceedsHold
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance
				+ CASE WHEN $1 > $2 THEN $1 - $2 ELSE 0 END
				- CASE WHEN $2 > $1 THEN $2 - $1 ELSE 0 END,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $3 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.ActualAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

func releaseUsageBillingBatchImageBalance(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error) {
	if cmd.HoldAmount <= 0 {
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	// 释放前校验该 job 确实预留过 hold（hold request id 已被 claim），
	// 防止从未成功冻结的 job 触发"幻影释放"，从其他用户的冻结资金池中凭空生成余额。
	held, heldErr := batchImageHoldClaimExists(ctx, tx, service.BatchImageHoldRequestID(cmd.BatchID), cmd.APIKeyID)
	if heldErr != nil {
		return nil, heldErr
	}
	if !held {
		logger.LegacyPrintf("repository.usage_billing", "[BatchImage] release skipped, hold was never reserved: batch=%s", cmd.BatchID)
		return &service.BatchImageBalanceHoldResult{}, nil
	}
	var balance, frozen float64
	err := tx.QueryRowContext(ctx, `
		UPDATE users
		SET balance = balance + $1,
			frozen_balance = COALESCE(frozen_balance, 0) - $1,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL AND COALESCE(frozen_balance, 0) >= $1
		RETURNING balance, frozen_balance
	`, cmd.HoldAmount, cmd.UserID).Scan(&balance, &frozen)
	if err == nil {
		return &service.BatchImageBalanceHoldResult{NewBalance: &balance, FrozenBalance: &frozen}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if exists, existsErr := userExistsForBilling(ctx, tx, cmd.UserID); existsErr != nil {
		return nil, existsErr
	} else if !exists {
		return nil, service.ErrUserNotFound
	}
	return nil, errors.New("batch image frozen balance is insufficient")
}

// batchImageHoldClaimExists 检查 hold request id 是否已在 dedup（或归档）表中被 claim，
// 即该 batch 的冻结操作确实成功提交过。
func batchImageHoldClaimExists(ctx context.Context, tx *sql.Tx, holdRequestID string, apiKeyID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	err = tx.QueryRowContext(ctx, `
		SELECT 1
		FROM usage_billing_dedup_archive
		WHERE request_id = $1 AND api_key_id = $2
	`, holdRequestID, apiKeyID).Scan(&exists)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, err
}

func userExistsForBilling(ctx context.Context, tx *sql.Tx, userID int64) (bool, error) {
	var exists int
	err := tx.QueryRowContext(ctx, `
		SELECT 1
		FROM users
		WHERE id = $1 AND deleted_at IS NULL
	`, userID).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func incrementUsageBillingAPIKeyQuota(ctx context.Context, tx *sql.Tx, apiKeyID int64, amount float64) (bool, error) {
	var exhausted bool
	err := tx.QueryRowContext(ctx, `
		UPDATE api_keys
		SET quota_used = quota_used + $1,
			status = CASE
				WHEN quota > 0
					AND status = $3
					AND quota_used < quota
					AND quota_used + $1 >= quota
				THEN $4
				ELSE status
			END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING quota > 0 AND quota_used >= quota AND quota_used - $1 < quota
	`, amount, apiKeyID, service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).Scan(&exhausted)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrAPIKeyNotFound
	}
	if err != nil {
		return false, err
	}
	return exhausted, nil
}

func incrementUsageBillingAPIKeyRateLimit(ctx context.Context, tx *sql.Tx, apiKeyID int64, cost float64) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE api_keys SET
			usage_5h = CASE WHEN window_5h_start IS NOT NULL AND window_5h_start + INTERVAL '5 hours' <= NOW() THEN $1 ELSE usage_5h + $1 END,
			usage_1d = CASE WHEN window_1d_start IS NOT NULL AND window_1d_start + INTERVAL '24 hours' <= NOW() THEN $1 ELSE usage_1d + $1 END,
			usage_7d = CASE WHEN window_7d_start IS NOT NULL AND window_7d_start + INTERVAL '7 days' <= NOW() THEN $1 ELSE usage_7d + $1 END,
			window_5h_start = CASE WHEN window_5h_start IS NULL OR window_5h_start + INTERVAL '5 hours' <= NOW() THEN NOW() ELSE window_5h_start END,
			window_1d_start = CASE WHEN window_1d_start IS NULL OR window_1d_start + INTERVAL '24 hours' <= NOW() THEN date_trunc('day', NOW()) ELSE window_1d_start END,
			window_7d_start = CASE WHEN window_7d_start IS NULL OR window_7d_start + INTERVAL '7 days' <= NOW() THEN date_trunc('day', NOW()) ELSE window_7d_start END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`, cost, apiKeyID)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrAPIKeyNotFound
	}
	return nil
}

func incrementUsageBillingAccountQuota(ctx context.Context, tx *sql.Tx, accountID int64, amount float64) (*service.AccountQuotaState, error) {
	rows, err := tx.QueryContext(ctx,
		`UPDATE accounts SET extra = (
			COALESCE(extra, '{}'::jsonb)
			|| jsonb_build_object('quota_used', COALESCE((extra->>'quota_used')::numeric, 0) + $1)
			|| CASE WHEN COALESCE((extra->>'quota_daily_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_daily_used',
					CASE WHEN `+dailyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_daily_used')::numeric, 0) + $1 END,
					'quota_daily_start',
					CASE WHEN `+dailyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_daily_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+dailyExpiredExpr+` AND `+nextDailyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_daily_reset_at', `+nextDailyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
			|| CASE WHEN COALESCE((extra->>'quota_weekly_limit')::numeric, 0) > 0 THEN
				jsonb_build_object(
					'quota_weekly_used',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN $1
					ELSE COALESCE((extra->>'quota_weekly_used')::numeric, 0) + $1 END,
					'quota_weekly_start',
					CASE WHEN `+weeklyExpiredExpr+`
					THEN `+nowUTC+`
					ELSE COALESCE(extra->>'quota_weekly_start', `+nowUTC+`) END
				)
				|| CASE WHEN `+weeklyExpiredExpr+` AND `+nextWeeklyResetAtExpr+` IS NOT NULL
				   THEN jsonb_build_object('quota_weekly_reset_at', `+nextWeeklyResetAtExpr+`)
				   ELSE '{}'::jsonb END
			ELSE '{}'::jsonb END
		), updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
		RETURNING
			COALESCE((extra->>'quota_used')::numeric, 0),
			COALESCE((extra->>'quota_limit')::numeric, 0),
			COALESCE((extra->>'quota_daily_used')::numeric, 0),
			COALESCE((extra->>'quota_daily_limit')::numeric, 0),
			COALESCE((extra->>'quota_weekly_used')::numeric, 0),
			COALESCE((extra->>'quota_weekly_limit')::numeric, 0)`,
		amount, accountID)
	if err != nil {
		return nil, err
	}

	var state service.AccountQuotaState
	if rows.Next() {
		if err := rows.Scan(
			&state.TotalUsed, &state.TotalLimit,
			&state.DailyUsed, &state.DailyLimit,
			&state.WeeklyUsed, &state.WeeklyLimit,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
	} else {
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
		return nil, service.ErrAccountNotFound
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	// 必须在执行下一条 SQL 前显式关闭 rows：pq 驱动在同一连接上
	// 不允许前一条查询的结果集未耗尽时启动新查询，否则会返回
	// "unexpected Parse response" 错误。
	if err := rows.Close(); err != nil {
		return nil, err
	}
	// 任意维度额度在本次递增中从"未超"跨越到"已超"时，必须刷新调度快照，
	// 否则 Redis 中缓存的 Account 仍显示旧的 used 值，后续请求会继续选中本账号，
	// 最终观察到 daily_used / weekly_used 大幅超过配置的 limit。
	// 对于日/周额度，即使本次触发了周期重置（pre=0、post=amount），
	// 判定式 (post-amount) < limit 同样成立，逻辑与总额度保持一致。
	crossedTotal := state.TotalLimit > 0 && state.TotalUsed >= state.TotalLimit && (state.TotalUsed-amount) < state.TotalLimit
	crossedDaily := state.DailyLimit > 0 && state.DailyUsed >= state.DailyLimit && (state.DailyUsed-amount) < state.DailyLimit
	crossedWeekly := state.WeeklyLimit > 0 && state.WeeklyUsed >= state.WeeklyLimit && (state.WeeklyUsed-amount) < state.WeeklyLimit
	if crossedTotal || crossedDaily || crossedWeekly {
		if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
			logger.LegacyPrintf("repository.usage_billing", "[SchedulerOutbox] enqueue quota exceeded failed: account=%d err=%v", accountID, err)
			return nil, err
		}
	}
	return &state, nil
}
