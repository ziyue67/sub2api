//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	// floorBalanceDeductSQL matches deductBalanceToFloorSQL: lock the wallet row
	// (only when balance > floor), clamp the new balance at the floor, and return
	// both the previous and the new balance so the caller can derive
	// collected/shortfall.
	floorBalanceDeductSQL         = `(?s)WITH target AS \(\s+SELECT id, balance\s+FROM users\s+WHERE id = \$2 AND deleted_at IS NULL AND balance > \$3\s+FOR UPDATE\s+\), updated AS \(\s+UPDATE users AS u\s+SET balance = GREATEST\(target\.balance - \$1, \$3\),\s+updated_at = NOW\(\)\s+FROM target\s+WHERE u\.id = target\.id\s+RETURNING target\.balance AS previous_balance, u\.balance AS new_balance\s+\)\s+SELECT previous_balance, new_balance FROM updated`
	deductUserExistsForBillingSQL = `(?s)SELECT EXISTS\(SELECT 1 FROM users WHERE id = \$1 AND deleted_at IS NULL\)`
	userExistsForBillingSQL       = `(?s)SELECT 1\s+FROM users\s+WHERE id = \$1 AND deleted_at IS NULL`
	reserveBatchImageHoldSQL      = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \$1\s+RETURNING balance, frozen_balance`
	reservedBatchImageHoldSQL     = `(?s)UPDATE users\s+SET balance = balance - \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) \+ \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND balance >= \(\$1 \+ \$3\)\s+RETURNING balance, frozen_balance`
	captureBatchImageHoldSQL      = `(?s)UPDATE users\s+SET balance = balance\s+\+ CASE WHEN \$1 > \$2 THEN \$1 - \$2 ELSE 0 END\s+- CASE WHEN \$2 > \$1 THEN \$2 - \$1 ELSE 0 END,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$3 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	releaseBatchImageHoldSQL      = `(?s)UPDATE users\s+SET balance = balance \+ \$1,\s+frozen_balance = COALESCE\(frozen_balance, 0\) - \$1,\s+updated_at = NOW\(\)\s+WHERE id = \$2 AND deleted_at IS NULL AND COALESCE\(frozen_balance, 0\) >= \$1\s+RETURNING balance, frozen_balance`
	apiKeyQuotaIncrementSQL       = `(?s)UPDATE api_keys\s+SET quota_used = quota_used \+ \$1,.*WHERE id = \$2 AND deleted_at IS NULL\s+RETURNING`
	apiKeyRateLimitIncrementSQL   = `(?s)UPDATE api_keys SET\s+usage_5h = .*WHERE id = \$2 AND deleted_at IS NULL`
)

// debtBalanceDeductSQL matches deductBalanceToDebtSQL (billing.settlement_debt_mode):
// the wallet row is locked for every non-deleted user and the balance is decremented
// without a lower bound, so the wallet may end up negative (debt).
const debtBalanceDeductSQL = `(?s)WITH target AS \(\s+SELECT id, balance\s+FROM users\s+WHERE id = \$2 AND deleted_at IS NULL\s+FOR UPDATE\s+\), updated AS \(\s+UPDATE users AS u\s+SET balance = target\.balance - \$1,\s+updated_at = NOW\(\)\s+FROM target\s+WHERE u\.id = target\.id\s+RETURNING target\.balance AS previous_balance, u\.balance AS new_balance\s+\)\s+SELECT previous_balance, new_balance FROM updated`

func TestDeductUsageBillingBalance_FullDeductionWhenWalletCoversCost(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// reserve 未配置 → floor=0 作为 $3 传入；钱包 10.0 → 7.5，全额收取。
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(2.5, int64(42), 0.0).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(10.0, 7.5))
	mock.ExpectCommit()

	deduction, err := deductUsageBillingBalance(ctx, tx, 42, 2.5)
	require.NoError(t, err)
	require.InDelta(t, 7.5, deduction.NewBalance, 0.000001)
	require.InDelta(t, 2.5, deduction.Collected, 0.000001)
	require.Zero(t, deduction.Shortfall)
	require.False(t, deduction.PartiallyCollected())
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_WithReserve_DrainsToFloorAndReportsShortfall(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// 审查者给出的死区复现场景：balance=0.11, reserve=0.10, cost=0.05。
	// 旧的整笔拒绝会让余额永远停在 0.11 → 无限白嫖；现在扣到 floor：0.11 → 0.10，
	// 实收 0.01，差额 0.04 记为 shortfall，下一次预检 balance <= reserve → 403。
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(0.05, int64(42), 0.10).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(0.11, 0.10))
	mock.ExpectCommit()

	deduction, err := deductUsageBillingBalance(ctx, tx, 42, 0.05, 0.10)
	require.NoError(t, err)
	require.InDelta(t, 0.10, deduction.NewBalance, 0.000001)
	require.InDelta(t, 0.01, deduction.Collected, 0.000001)
	require.InDelta(t, 0.04, deduction.Shortfall, 0.000001)
	require.True(t, deduction.PartiallyCollected())
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestDeductUsageBillingBalance_WithReserve_RejectsWhenWalletAlreadyAtFloor(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// 余额已经 <= floor：`balance > $3` 锁不到任何行 → 不扣一分钱，返回 ErrInsufficientBalance。
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(10.0, int64(42), 0.10).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}))
	mock.ExpectQuery(deductUserExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	_, err = deductUsageBillingBalance(ctx, tx, 42, 10, 0.10)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_WithReserve_RejectsWhenWalletAlreadyAtFloor(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(10.0, int64(42), 0.10).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}))
	mock.ExpectQuery(deductUserExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectRollback()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{minimumBalanceReserve: 0.10}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 10,
	}, result)
	require.ErrorIs(t, err, service.ErrInsufficientBalance)
	require.Nil(t, result.NewBalance)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_PartialCollectionReportsShortfall(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// 钱包 0.30，成本 0.75，reserve 0.10 → 扣到 0.10，实收 0.20，差额 0.55。
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(0.75, int64(42), 0.10).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(0.30, 0.10))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{minimumBalanceReserve: 0.10}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 0.75,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 0.10, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.20, result.BalanceCollected, 0.000001)
	require.InDelta(t, 0.55, result.BalanceShortfall, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDeductUsageBillingBalanceWithMode_DebtModePushesWalletNegative 锁死
// billing.settlement_debt_mode 的核心语义（对照 new-api 的 decreaseUserQuota）：钱包付不满时
// 全额入账、余额扣成负数（欠款），差额不再记为 write-off，由下一次充值抵扣。
func TestDeductUsageBillingBalanceWithMode_DebtModePushesWalletNegative(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// 钱包 0.30、成本 0.75、reserve 0.10：债务模式不再夹到 floor，直接扣成 -0.45。
	mock.ExpectQuery(debtBalanceDeductSQL).
		WithArgs(0.75, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(0.30, -0.45))
	mock.ExpectCommit()

	deduction, err := deductUsageBillingBalanceWithMode(ctx, tx, 42, 0.75, 0.10, true)
	require.NoError(t, err)
	require.InDelta(t, -0.45, deduction.NewBalance, 0.000001)
	require.InDelta(t, 0.75, deduction.Collected, 0.000001)
	require.Zero(t, deduction.Shortfall)
	require.False(t, deduction.PartiallyCollected())
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDeductUsageBillingBalanceWithMode_FloorModeStillClamps 锁死零值安全：开关关闭（默认）时
// 必须继续走封底 SQL，余额不得变负。
func TestDeductUsageBillingBalanceWithMode_FloorModeStillClamps(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(0.75, int64(42), 0.10).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(0.30, 0.10))
	mock.ExpectCommit()

	deduction, err := deductUsageBillingBalanceWithMode(ctx, tx, 42, 0.75, 0.10, false)
	require.NoError(t, err)
	require.InDelta(t, 0.10, deduction.NewBalance, 0.000001)
	require.InDelta(t, 0.20, deduction.Collected, 0.000001)
	require.InDelta(t, 0.55, deduction.Shortfall, 0.000001)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestDeductUsageBillingBalanceWithMode_DebtModeUserNotFound：债务模式下无返回行只可能是
// 用户不存在，且不需要额外的 EXISTS 查询（谓词与 SQL 相同）。
func TestDeductUsageBillingBalanceWithMode_DebtModeUserNotFound(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(debtBalanceDeductSQL).
		WithArgs(10.0, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}))
	mock.ExpectRollback()

	_, err = deductUsageBillingBalanceWithMode(ctx, tx, 42, 10, 0.10, true)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestApplyUsageBillingEffects_DebtModeKeepsFullCost 锁死仓库接线：开关打开时统一结算路径走
// 债务 SQL，NewBalance 为负、BalanceShortfall 为 0（usage_log.actual_cost 保持真实成本）。
func TestApplyUsageBillingEffects_DebtModeKeepsFullCost(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(debtBalanceDeductSQL).
		WithArgs(0.75, int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(0.30, -0.45))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{minimumBalanceReserve: 0.10, settlementDebtMode: true}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:      42,
		BalanceCost: 0.75,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, -0.45, *result.NewBalance, 0.000001)
	require.InDelta(t, 0.75, result.BalanceCollected, 0.000001)
	require.Zero(t, result.BalanceShortfall)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_DeletedAPIKeyStillBillsBalance(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(10.0, int64(42), 0.0).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}).AddRow(100.0, 90.0))
	mock.ExpectQuery(apiKeyQuotaIncrementSQL).
		WithArgs(10.0, int64(7), service.StatusAPIKeyActive, service.StatusAPIKeyQuotaExhausted).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectCommit()

	result := &service.UsageBillingApplyResult{Applied: true}
	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		BalanceCost:         10,
		APIKeyQuotaCost:     10,
		APIKeyRateLimitCost: 10,
	}, result)
	require.NoError(t, err)
	require.NotNil(t, result.NewBalance)
	require.InDelta(t, 90.0, *result.NewBalance, 0.000001)
	require.False(t, result.APIKeyQuotaExhausted)
	require.NoError(t, tx.Commit())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyUsageBillingEffects_APIKeyCounterErrorStillFails(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectExec(apiKeyRateLimitIncrementSQL).
		WithArgs(10.0, int64(7)).
		WillReturnError(sql.ErrConnDone)
	mock.ExpectRollback()

	err = (&usageBillingRepository{}).applyUsageBillingEffects(ctx, tx, &service.UsageBillingCommand{
		UserID:              42,
		APIKeyID:            7,
		APIKeyRateLimitCost: 10,
	}, &service.UsageBillingApplyResult{Applied: true})
	require.ErrorIs(t, err, sql.ErrConnDone)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestNewUsageBillingRepository_ReadsBillingConfig 锁死配置接线：避免“开着债务模式却仍走封底”
// 这类静默失效，同时保证不传 cfg（旧装配/测试）时零值安全。
func TestNewUsageBillingRepository_ReadsBillingConfig(t *testing.T) {
	repo := NewUsageBillingRepository(nil, nil, &config.Config{Billing: config.BillingConfig{
		MinimumBalanceReserve: 0.25,
		SettlementDebtMode:    true,
	}})
	impl, ok := repo.(*usageBillingRepository)
	require.True(t, ok)
	require.InDelta(t, 0.25, impl.minimumBalanceReserve, 1e-9)
	require.True(t, impl.settlementDebtMode)

	legacy, ok := NewUsageBillingRepository(nil, nil).(*usageBillingRepository)
	require.True(t, ok)
	require.False(t, legacy.settlementDebtMode)
}

func TestDeductUsageBillingBalance_ReturnsUserNotFoundWhenNoUserUpdated(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	mock.ExpectQuery(floorBalanceDeductSQL).
		WithArgs(10.0, int64(42), 0.0).
		WillReturnRows(sqlmock.NewRows([]string{"previous_balance", "new_balance"}))
	mock.ExpectQuery(deductUserExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectRollback()

	_, err = deductUsageBillingBalance(ctx, tx, 42, 10)
	require.ErrorIs(t, err, service.ErrUserNotFound)
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSettleBalanceDeduction_QuantizesFloatNoise(t *testing.T) {
	for _, tc := range []struct {
		name          string
		amount        float64
		previous      float64
		current       float64
		wantCollected float64
		wantShortfall float64
	}{
		// 0.3 - 0.1 在二进制下 = 0.19999999999999998，previous - current 会带噪声；
		// 量化后必须判定为全额收取，不能出现 3e-17 的幽灵 shortfall。
		{name: "binary noise is not a shortfall", amount: 0.1, previous: 0.3, current: 0.19999999999999998, wantCollected: 0.1, wantShortfall: 0},
		{name: "exact full deduction", amount: 2.5, previous: 10, current: 7.5, wantCollected: 2.5, wantShortfall: 0},
		{name: "drained to floor", amount: 0.05, previous: 0.11, current: 0.10, wantCollected: 0.01, wantShortfall: 0.04},
		{name: "drained to zero floor", amount: 5, previous: 0.30, current: 0, wantCollected: 0.30, wantShortfall: 4.70},
		// 防御：DB 返回的差值不可能超过 amount，但即使出现也夹到 amount，不会“多收”。
		{name: "over-collection clamps to amount", amount: 1, previous: 3, current: 1.5, wantCollected: 1, wantShortfall: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := settleBalanceDeduction(tc.amount, tc.previous, tc.current)
			require.InDelta(t, tc.current, got.NewBalance, 1e-9)
			require.InDelta(t, tc.wantCollected, got.Collected, 1e-9)
			require.InDelta(t, tc.wantShortfall, got.Shortfall, 1e-9)
			require.Equal(t, tc.wantShortfall > 0, got.PartiallyCollected())
		})
	}
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

func TestReserveUsageBillingBatchImageBalance_WithReserve_RejectsSpendingIntoFloor(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// balance 0.15、hold 0.10、reserve 0.10 → 门槛 0.20 > 0.15 → 拒绝
	mock.ExpectQuery(reservedBatchImageHoldSQL).
		WithArgs(0.10, int64(42), 0.10).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(userExistsForBillingSQL).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"1"}).AddRow(1))
	mock.ExpectRollback()

	_, err = reserveUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{UserID: 42, HoldAmount: 0.10}, 0.10)
	require.ErrorIs(t, err, service.ErrBatchImageInsufficientBalance)
	require.NoError(t, tx.Rollback())
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
	// F10：capture 前先校验该 batch 确实冻结过（与 release 对称）
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WithArgs(service.BatchImageHoldRequestID(""), int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"?column?"}).AddRow(1))
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

// TestCaptureUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved 锁死审计 F10：
// 从未成功冻结过的 batch 不得走 capture（否则会扣掉同一用户其它 job 的冻结额）。
func TestCaptureUsageBillingBatchImageBalance_SkipsWhenHoldNeverReserved(t *testing.T) {
	ctx := context.Background()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectBegin()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	// dedup 与 archive 均无该 hold 记录
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery(`SELECT 1\s+FROM usage_billing_dedup_archive\s+WHERE request_id = \$1 AND api_key_id = \$2`).
		WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()

	result, err := captureUsageBillingBatchImageBalance(ctx, tx, &service.BatchImageBalanceHoldCommand{
		BatchID: "batch-never-held", UserID: 42, HoldAmount: 1, ActualAmount: 0.25,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Nil(t, result.NewBalance, "未冻结过的批次必须整体 no-op，不得改余额")
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}
