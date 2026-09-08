//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	conditionalBalanceDeductSQL   = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance`
	userExistsSQL                 = `(?s)SELECT EXISTS\(\s*SELECT 1 FROM users WHERE id = \$1 AND deleted_at IS NULL\s*\)`
	reserveBatchImageHoldSQL      = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL      = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL      = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	userExistsForBillingSQL       = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`
	reserveGatewayInsertSQL       = `(?s)INSERT INTO gateway_balance_reservations\s+\(request_id, api_key_id, user_id, reserved_amount, status, expires_at\)`
	reservationLookupSQL          = `(?s)SELECT user_id, status\s+FROM gateway_balance_reservations\s+WHERE request_id = \$1\s+FOR UPDATE`
	reserveGatewayUpdateSQL       = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	reserveGatewayWalletSelectSQL = `(?s)SELECT balance\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL\s+FOR UPDATE`
	reservationSelectSQL          = `(?s)SELECT id, user_id, reserved_amount, status\s+FROM gateway_balance_reservations\s+WHERE request_id = \$1\s+FOR UPDATE`
	reservationUserUpdateSQL      = `(?s)UPDATE users\s+SET balance = balance \+ \$1 - \$2,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	reservationStatusUpdateSQL    = `(?s)UPDATE gateway_balance_reservations\s+SET actual_amount = \$1, status = \$2, updated_at = NOW\(\)\s+WHERE id = \$3`
)

func TestDeductUsageBillingBalance_UsesSufficientBalanceGuard(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(7.5))
	mock.ExpectCommit()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 2.5)
	require.NoError(t, err)
	require.True(t, sufficient)
	require.InDelta(t, 7.5, newBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_RejectsInsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	newBalance, sufficient, err := deductUsageBillingBalance(ctx, tx, 42, 10)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.False(t, sufficient)
	require.Zero(t, newBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffectsRejectsInsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 10,
	}, result)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.Nil(t, result.NewBalance)
	require.False(t, result.BalanceOverdrafted)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_ReturnsUserNotFoundWhenNoUserUpdated(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(conditionalBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, _, err = deductUsageBillingBalance(ctx, tx, 42, 10)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveGatewayBalanceAtomicallyFreezesWallet(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(reservationLookupSQL).
		WithArgs("req-1").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(reserveGatewayInsertSQL).
		WithArgs("req-1", int64(7), int64(42), 10.0, "reserved").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(1))
	mock.ExpectQuery(reserveGatewayUpdateSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(0.0, 10.0))
	mock.ExpectCommit()
	result, err := repo.ReserveGatewayBalance(ctx, &service.BalanceReservationCommand{RequestID: "req-1", APIKeyID: 7, UserID: 42, Amount: 10})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.InDelta(t, 0.0, result.NewBalance, 1e-9)
	require.InDelta(t, 10.0, result.FrozenBalance, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveGatewayBalanceReusesExistingHoldAfterAvailableBalanceIsZero(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(reservationLookupSQL).
		WithArgs("req-retry").
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "status"}).AddRow(42, "reserved"))
	mock.ExpectCommit()
	result, err := repo.ReserveGatewayBalance(ctx, &service.BalanceReservationCommand{
		RequestID: "req-retry", APIKeyID: 99, UserID: 42, Amount: 0,
	})
	require.NoError(t, err)
	require.False(t, result.Applied)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveGatewayBalanceReadsAuthoritativeWalletInsideTransaction(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(reservationLookupSQL).
		WithArgs("req-authoritative").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(reserveGatewayWalletSelectSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(12.5))
	mock.ExpectQuery(reserveGatewayInsertSQL).
		WithArgs("req-authoritative", int64(7), int64(42), 12.5, "reserved").
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(9))
	mock.ExpectQuery(reserveGatewayUpdateSQL).
		WithArgs(12.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(0.0, 12.5))
	mock.ExpectCommit()

	result, err := repo.ReserveGatewayBalance(ctx, &service.BalanceReservationCommand{
		RequestID:               "req-authoritative",
		APIKeyID:                7,
		UserID:                  42,
		ReserveAvailableBalance: true,
	})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.InDelta(t, 12.5, result.Reserved, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveGatewayBalanceHonorsMinimumBalanceInsideTransaction(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(reservationLookupSQL).
		WithArgs("req-minimum").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(reserveGatewayWalletSelectSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow(0.005))
	mock.ExpectRollback()

	_, err = repo.ReserveGatewayBalance(ctx, &service.BalanceReservationCommand{
		RequestID:               "req-minimum",
		APIKeyID:                7,
		UserID:                  42,
		MinimumBalance:          0.01,
		ReserveAvailableBalance: true,
	})
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCleanupExpiredGatewayBalanceReservationsUsesBoundedSkipLockedBatch(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &usageBillingRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec(`(?s)WITH expired AS \(\s*SELECT id, user_id, reserved_amount\s+FROM gateway_balance_reservations.*FOR UPDATE SKIP LOCKED.*LIMIT \$1`).
		WithArgs(25).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	count, err := repo.CleanupExpiredGatewayBalanceReservations(ctx, 25)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureGatewayBalanceReturnsUnusedHoldAndRejectsOverrun(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(reservationSelectSQL).
		WithArgs("req-2").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "reserved_amount", "status"}).AddRow(2, 42, 10.0, "reserved"))
	mock.ExpectQuery(reservationUserUpdateSQL).
		WithArgs(10.0, 2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 0.0))
	mock.ExpectExec(reservationStatusUpdateSQL).
		WithArgs(2.5, "settled", int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &usageBillingRepository{db: db}
	result, err := repo.CaptureGatewayBalance(ctx, &service.BalanceReservationCommand{RequestID: "req-2", APIKeyID: 7, UserID: 42, Amount: 2.5})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.InDelta(t, 7.5, result.NewBalance, 1e-9)
	require.InDelta(t, 0.0, result.FrozenBalance, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureGatewayBalanceRejectsActualCostAboveHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectBegin()
	mock.ExpectQuery(reservationSelectSQL).
		WithArgs("req-3").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "reserved_amount", "status"}).AddRow(3, 42, 1.0, "reserved"))
	mock.ExpectRollback()

	repo := &usageBillingRepository{db: db}
	_, err = repo.CaptureGatewayBalance(ctx, &service.BalanceReservationCommand{RequestID: "req-3", APIKeyID: 7, UserID: 42, Amount: 1.1})
	require.ErrorIs(t, err, service.ErrBalanceReservationExceedsHold)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseGatewayBalanceReturnsFullHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	mock.ExpectQuery(reservationSelectSQL).
		WithArgs("req-release").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "reserved_amount", "status"}).AddRow(4, 42, 10.0, "reserved"))
	mock.ExpectQuery(reservationUserUpdateSQL).
		WithArgs(10.0, 0.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectExec(reservationStatusUpdateSQL).
		WithArgs(0.0, "released", int64(4)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	repo := &usageBillingRepository{db: db}
	result, err := repo.ReleaseGatewayBalance(ctx, &service.BalanceReservationCommand{RequestID: "req-release", APIKeyID: 88, UserID: 42})
	require.NoError(t, err)
	require.True(t, result.Applied)
	require.InDelta(t, 10.0, result.NewBalance, 1e-9)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_MovesAvailableToFrozen(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(2.5, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(7.5, 2.5))
	mock.ExpectCommit()

	result, err := reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 2.5})
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.NotNil(t, result.FrozenBalance)
	require.InDelta(t, 7.5, *result.NewBalance, 0.000001)
	require.InDelta(t, 2.5, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReserveUsageBillingBatchImageBalance_InsufficientBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(reserveBatchImageHoldSQL).
		WithArgs(10.0, int64(42)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 10})
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_ReleasesRemainder(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(captureBatchImageHoldSQL).
		WithArgs(1.0, 0.25, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(9.75, 0.0))
	mock.ExpectCommit()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 1, ActualAmount: 0.25})
	require.NoError(t, err)
	require.InDelta(t, 9.75, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCaptureUsageBillingBatchImageBalance_RejectsActualCostOverHold(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectRollback()

	_, err = captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.5, ActualAmount: 1})
	require.ErrorIs(t, err, service.ErrBatchImageSettlementCostExceedsHold)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_ReturnsFrozenToAvailable(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_release"), int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
	mock.ExpectQuery(releaseBatchImageHoldSQL).
		WithArgs(1.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"balance", "frozen_balance"}).AddRow(10.0, 0.0))
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_release", HoldAmount: 1})
	require.NoError(t, err)
	require.InDelta(t, 10.0, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.0, *result.FrozenBalance, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestReleaseUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与归档表均无 hold claim：说明该 job 从未成功冻结，
	// 释放必须跳过，不得从他人冻结资金池中凭空生成余额。
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID("imgbatch_phantom"), int64(7)).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectCommit()

	result, err := releaseUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, APIKeyID: 7, BatchID: "imgbatch_phantom", HoldAmount: 1})
	require.NoError(t, err)
	require.Nil(t, result.NewBalance)
	require.Nil(t, result.FrozenBalance)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}
