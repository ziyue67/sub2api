package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// 本文件是「在途预留」的 **DB 侧兜底**实现，对齐 new-api 的 reserveUserQuotaDB：
// 正常路径用 Redis（billing:reserved:*，带 TTL 自愈），Redis 不可用时不再直接
// fail-open，而是用数据库账本做同样语义的原子准入：钱包 scope 锁 users 行，
// 其它 scope 使用 billing_scope_reservations 和 advisory transaction lock。
//
// 为什么必须锁用户行：判定条件是「未过期预留总额 + 本笔 <= 预算」，读-判-写跨两条语句。
// 在 READ COMMITTED 下两个并发事务会读到同一份 SUM 并同时通过，超额放行原样复现。
// `SELECT ... FROM users WHERE id = $1 FOR UPDATE` 先锁用户行把同一用户的准入串行化；
// 结算路径的 deductBalanceToFloorSQL 也以 users 行为首把锁，**锁序一致**，不会互相死锁。
//
// 代价：Redis 故障期每个准入多一次事务与锁，单 scope 吞吐被串行化。这是刻意取舍：
// 故障期优先保"余额不为负、不产生坏账"，而不是吞吐。

// dbReservationStore stores durable reservations in the scope-specific tables.
type dbReservationStore struct {
	db *sql.DB
}

func newDBReservationStore(db *sql.DB) *dbReservationStore {
	if db == nil {
		return nil
	}
	return &dbReservationStore{db: db}
}

// redisReservationReceipt 是"Redis 账本上仍存活的一笔在途预留"的快照，
// 用于切到 DB 兜底账本时把它们**搬进** DB（见 importRedisReservationReceipts）。
type redisReservationReceipt struct {
	Scope     string
	RequestID string
	Amount    float64
	ExpiresAt time.Time
}

// importRedisReservationReceipts 把 Redis 账本上仍存活的在途预留登记进 DB 兜底账本。
//
// 为什么必须做：切到 DB 账本时，此前放行的请求已经在 Redis 里占了额度，但 DB 账本
// 对它们一无所知。若直接按 DB 账本判定，同一份额度会被两本账各算一遍
// （少算 = 超额放行，最坏可达"一个完整预算"的重复放行）。导入后 DB 账本就是
// "Redis 存量 + 后续新增"的完整视图，两个账本的语义重新对齐。
//
// 幂等性：按主键 (user_id/scope, request_id) 判重，行已存在时不重复累加金额。
// 调用方可能在多实例上并发导入，主键保证每笔只记一次。
func (s *dbReservationStore) importRedisReservationReceipts(ctx context.Context, receipts []redisReservationReceipt) (imported int, err error) {
	if s == nil || s.db == nil {
		return 0, errors.New("db reservation store unavailable")
	}
	if len(receipts) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, receipt := range receipts {
		if receipt.RequestID == "" || receipt.Scope == "" || receipt.Amount <= 0 {
			continue
		}
		expiresAt := receipt.ExpiresAt
		if expiresAt.IsZero() {
			expiresAt = time.Now().Add(billingReservationFallbackDefaultRowTTL)
		}
		if !expiresAt.After(time.Now()) {
			// 已过期：Redis 侧的 TTL 自愈会回收它，不需要（也不应该）导入。
			continue
		}
		if userID, ok := reservationUserIDFromScope(receipt.Scope); ok {
			res, execErr := tx.ExecContext(ctx, `
				INSERT INTO billing_balance_reservations (user_id, request_id, amount, expires_at)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (user_id, request_id) DO NOTHING
			`, userID, receipt.RequestID, receipt.Amount, expiresAt)
			if execErr != nil {
				return imported, execErr
			}
			if affected, rowsErr := res.RowsAffected(); rowsErr == nil && affected > 0 {
				imported++
			}
			continue
		}
		res, execErr := tx.ExecContext(ctx, `
			INSERT INTO billing_scope_reservations (scope, request_id, amount, expires_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (scope, request_id) DO NOTHING
		`, receipt.Scope, receipt.RequestID, receipt.Amount, expiresAt)
		if execErr != nil {
			return imported, execErr
		}
		if affected, rowsErr := res.RowsAffected(); rowsErr == nil && affected > 0 {
			imported++
		}
	}
	if err := tx.Commit(); err != nil {
		return imported, err
	}
	return imported, nil
}

// reservationUserIDFromScope 把预留 scope 解析成 userID。
//
// 钱包模式的 scope 是纯数字 userID，使用 billing_balance_reservations；其它 scope
// （订阅、user×platform、API Key）使用通用的 billing_scope_reservations 账本。
func reservationUserIDFromScope(scope string) (int64, bool) {
	userID, err := strconv.ParseInt(scope, 10, 64)
	if err != nil || userID <= 0 {
		return 0, false
	}
	return userID, true
}

// tryReserveUserBalance 在 DB 侧原子登记一笔在途预留。
//
// 返回 (登记后的预留总额, 是否接受, 错误)。语义与 Redis 脚本
// tryReserveBalanceScript 一致：只校验「新总额不超过 maxTotal」，被拒绝时不写任何行。
func (s *dbReservationStore) tryReserveUserBalance(
	ctx context.Context,
	scope string,
	requestID string,
	amount float64,
	maxTotal float64,
	ttl time.Duration,
) (float64, bool, error) {
	if s == nil || s.db == nil {
		return 0, false, errors.New("db reservation store unavailable")
	}
	if amount <= 0 {
		return 0, true, nil
	}
	userID, ok := reservationUserIDFromScope(scope)
	if !ok {
		return 0, false, fmt.Errorf("db reservation fallback does not support scope %q", scope)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	// 未提交即返回（含被拒绝的分支）一律回滚：拒绝路径不留下任何写入。
	defer func() { _ = tx.Rollback() }()

	// 1) 锁用户行：同一用户的并发准入在这里串行化。
	var balance float64
	if err := tx.QueryRowContext(ctx,
		`SELECT balance FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE`,
		userID,
	).Scan(&balance); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, false, fmt.Errorf("reserve for user %d: %w", userID, sql.ErrNoRows)
		}
		return 0, false, err
	}

	// 2) TTL 自愈：先清掉本用户已过期的预留，再求和。等价于 Redis 侧的 TTL 过期，
	//    保证崩溃/丢失释放不会永久占用额度。
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM billing_balance_reservations WHERE user_id = $1 AND expires_at <= NOW()`,
		userID,
	); err != nil {
		return 0, false, err
	}

	var reserved float64
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(amount), 0) FROM billing_balance_reservations WHERE user_id = $1`,
		userID,
	).Scan(&reserved); err != nil {
		return 0, false, err
	}

	// 3) 与 Redis 原子脚本同口径的上限判定（容忍浮点尾差，与脚本里的 1e-12 一致）。
	if reserved+amount > maxTotal+1e-12 {
		return reserved, false, nil
	}

	expiresAt := time.Now().Add(ttl)

	// 4) 幂等登记：同一 (user_id, request_id) 重复预留只刷新过期时间，不重复累加。
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing_balance_reservations (user_id, request_id, amount, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id, request_id) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		userID, requestID, amount, expiresAt,
	); err != nil {
		return 0, false, err
	}

	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return reserved + amount, true, nil
}

// releaseUserBalanceReservation 归还本笔预留。
//
// 按主键删除；行不存在时返回 ErrBillingReservationExpired（与 Redis 侧凭据缺失的
// 语义一致，调用方据此把它当作"TTL 自愈已回收"而不是故障）。
func (s *dbReservationStore) releaseUserBalanceReservation(ctx context.Context, scope, requestID string) error {
	if s == nil || s.db == nil {
		return errors.New("db reservation store unavailable")
	}
	userID, ok := reservationUserIDFromScope(scope)
	if !ok {
		return fmt.Errorf("db reservation fallback does not support scope %q", scope)
	}
	result, err := s.db.ExecContext(ctx,
		`DELETE FROM billing_balance_reservations WHERE user_id = $1 AND request_id = $2`,
		userID, requestID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrBillingReservationExpired
	}
	return nil
}

// renewUserBalanceReservation 为长请求续期本笔预留（心跳）。
//
// `expires_at > NOW()` 是必需的：已过期的行不得被"复活"，否则一笔崩溃后残留的记录会被
// 心跳永久续命，护栏反过来把用户钉死。0 行受影响即返回 ErrBillingReservationExpired，
// 让调用方停止心跳。
func (s *dbReservationStore) renewUserBalanceReservation(ctx context.Context, scope, requestID string, ttl time.Duration) error {
	if s == nil || s.db == nil {
		return errors.New("db reservation store unavailable")
	}
	userID, ok := reservationUserIDFromScope(scope)
	if !ok {
		return fmt.Errorf("db reservation fallback does not support scope %q", scope)
	}
	expiresAt := time.Now().Add(ttl)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE billing_balance_reservations
		SET expires_at = $3
		WHERE user_id = $1 AND request_id = $2 AND expires_at > NOW()`,
		userID, requestID, expiresAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrBillingReservationExpired
	}
	return tx.Commit()
}
