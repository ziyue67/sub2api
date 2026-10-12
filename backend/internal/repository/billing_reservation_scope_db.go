package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// DB fallback timing. The DB ledger is only used after Redis reservation calls
// fail, so these values do not affect the normal hot path.
const (
	billingReservationDBOperationTimeout    = 3 * time.Second
	billingReservationFallbackProbeInterval = time.Second
	billingReservationFallbackProbeTimeout  = 500 * time.Millisecond
	billingReservationFallbackDefaultRowTTL = 10 * time.Minute
)

// tryReserve dispatches a durable reservation to the scope-specific ledger.
//
// Numeric scopes are the wallet scopes and use billing_balance_reservations,
// which locks the users row so the admission decision stays serialized with
// the balance settlement path. All other scopes (subscriptions, user/platform
// quotas, and API-key quotas) use billing_scope_reservations with a per-scope
// advisory transaction lock.
func (s *dbReservationStore) tryReserve(
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
	if scope == "" || requestID == "" {
		return 0, false, errors.New("db reservation scope and request id are required")
	}
	if err := validateBillingReservationAmounts(amount, maxTotal); err != nil {
		return 0, false, err
	}
	if _, ok := reservationUserIDFromScope(scope); ok {
		return s.tryReserveUserBalance(ctx, scope, requestID, amount, maxTotal, ttl)
	}
	return s.tryReserveScopeReservation(ctx, scope, requestID, amount, maxTotal, ttl)
}

func (s *dbReservationStore) release(ctx context.Context, scope, requestID string) error {
	if s == nil || s.db == nil {
		return errors.New("db reservation store unavailable")
	}
	if _, ok := reservationUserIDFromScope(scope); ok {
		return s.releaseUserBalanceReservation(ctx, scope, requestID)
	}
	return s.releaseScopeReservation(ctx, scope, requestID)
}

func (s *dbReservationStore) renew(ctx context.Context, scope, requestID string, ttl time.Duration) error {
	if s == nil || s.db == nil {
		return errors.New("db reservation store unavailable")
	}
	if _, ok := reservationUserIDFromScope(scope); ok {
		return s.renewUserBalanceReservation(ctx, scope, requestID, ttl)
	}
	return s.renewScopeReservation(ctx, scope, requestID, ttl)
}

func (s *dbReservationStore) tryReserveScopeReservation(
	ctx context.Context,
	scope string,
	requestID string,
	amount float64,
	maxTotal float64,
	ttl time.Duration,
) (float64, bool, error) {
	if amount <= 0 {
		return 0, true, nil
	}
	expiresAt := time.Now().Add(normalizedBillingReservationTTL(ttl))

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = tx.Rollback() }()

	// Serialize every admission for the same scope. The SQL below performs a
	// read-modify-write over the active reservation sum, so the lock is the
	// database equivalent of the Redis Lua script's atomicity.
	if _, err := tx.ExecContext(ctx,
		`SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		scope,
	); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM billing_scope_reservations WHERE scope = $1 AND expires_at <= NOW()`,
		scope,
	); err != nil {
		return 0, false, err
	}

	// A retry with the same request ID must only refresh its receipt. It must
	// not add the amount to the active sum a second time.
	var existing float64
	err = tx.QueryRowContext(ctx, `
		SELECT amount::double precision
		FROM billing_scope_reservations
		WHERE scope = $1 AND request_id = $2 AND expires_at > NOW()
	`, scope, requestID).Scan(&existing)
	switch {
	case err == nil:
		if _, err := tx.ExecContext(ctx, `
			UPDATE billing_scope_reservations
			SET expires_at = $3
			WHERE scope = $1 AND request_id = $2
		`, scope, requestID, expiresAt); err != nil {
			return 0, false, err
		}
		var current float64
		if err := tx.QueryRowContext(ctx, `
			SELECT COALESCE(SUM(amount), 0)::double precision
			FROM billing_scope_reservations
			WHERE scope = $1 AND expires_at > NOW()
		`, scope).Scan(&current); err != nil {
			return 0, false, err
		}
		if err := tx.Commit(); err != nil {
			return 0, false, err
		}
		return current, true, nil
	case !errors.Is(err, sql.ErrNoRows):
		return 0, false, err
	}

	var current float64
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(amount), 0)::double precision
		FROM billing_scope_reservations
		WHERE scope = $1 AND expires_at > NOW()
	`, scope).Scan(&current); err != nil {
		return 0, false, err
	}
	if current+amount > maxTotal+1e-12 {
		return current, false, nil
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing_scope_reservations (scope, request_id, amount, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (scope, request_id) DO UPDATE
		SET expires_at = EXCLUDED.expires_at
	`, scope, requestID, amount, expiresAt); err != nil {
		return 0, false, err
	}
	if err := tx.Commit(); err != nil {
		return 0, false, err
	}
	return current + amount, true, nil
}

func (s *dbReservationStore) releaseScopeReservation(ctx context.Context, scope, requestID string) error {
	result, err := s.db.ExecContext(ctx, `
		DELETE FROM billing_scope_reservations
		WHERE scope = $1 AND request_id = $2
	`, scope, requestID)
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

func (s *dbReservationStore) renewScopeReservation(ctx context.Context, scope, requestID string, ttl time.Duration) error {
	expiresAt := time.Now().Add(normalizedBillingReservationTTL(ttl))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE billing_scope_reservations
		SET expires_at = $3
		WHERE scope = $1 AND request_id = $2 AND expires_at > NOW()
	`, scope, requestID, expiresAt)
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

func (s *dbReservationStore) activateFallbackWindow(ctx context.Context, ttl time.Duration) error {
	until := time.Now().Add(normalizedBillingReservationTTL(ttl))
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO billing_reservation_fallback_state (singleton, fallback_until, updated_at)
		VALUES (TRUE, $1, NOW())
		ON CONFLICT (singleton) DO UPDATE
		SET fallback_until = GREATEST(billing_reservation_fallback_state.fallback_until, EXCLUDED.fallback_until),
			updated_at = NOW()
	`, until)
	return err
}

func (s *dbReservationStore) fallbackWindowUntil(ctx context.Context) (time.Time, error) {
	var until time.Time
	err := s.db.QueryRowContext(ctx, `
		SELECT fallback_until
		FROM billing_reservation_fallback_state
		WHERE singleton = TRUE
	`).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	return until, err
}

// liveReservationReceipts 列出 DB 兜底账本上仍未过期的全部预留（窗口结束时导回 Redis）。
func (s *dbReservationStore) liveReservationReceipts(ctx context.Context) ([]redisReservationReceipt, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT user_id::text, request_id, amount::float8, expires_at
		FROM billing_balance_reservations
		WHERE expires_at > NOW()
		UNION ALL
		SELECT scope, request_id, amount::float8, expires_at
		FROM billing_scope_reservations
		WHERE expires_at > NOW()
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var receipts []redisReservationReceipt
	for rows.Next() {
		var receipt redisReservationReceipt
		if err := rows.Scan(&receipt.Scope, &receipt.RequestID, &receipt.Amount, &receipt.ExpiresAt); err != nil {
			return nil, err
		}
		if receipt.Amount > 0 && receipt.Scope != "" && receipt.RequestID != "" {
			receipts = append(receipts, receipt)
		}
	}
	return receipts, rows.Err()
}

func validateBillingReservationAmounts(amount, maxTotal float64) error {
	if amount < 0 || maxTotal < 0 ||
		math.IsNaN(amount) || math.IsNaN(maxTotal) ||
		math.IsInf(amount, 0) || math.IsInf(maxTotal, 0) {
		return fmt.Errorf("billing reservation amount and limit must be finite and nonnegative, got amount=%v limit=%v", amount, maxTotal)
	}
	return nil
}

func normalizedBillingReservationTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return billingReservationFallbackDefaultRowTTL
	}
	return ttl
}

// scanRedisReservationReceipts 把 Redis 账本上存活的在途预留导出为可导入 DB 的凭据快照。
//
// 键形如 billing:resv_item:<scope>:<requestID>；requestID 由服务层生成且**不含 ':'
// （去连字符 UUID），因此按最后一个 ':' 切分即可无歧义地还原 scope 与 requestID。
// TTL 用 PTTL 读取：<= 0（无过期或已被回收）时按缺省自愈 TTL 处理，宁可保守多占
// 一段时间，也不让存量预留凭空消失。
//
// 仅用于"Redis 故障切 DB 账本"这一低频路径（见 activateReservationFallback），
// 正常热路径零额外开销。
func (c *billingCache) scanRedisReservationReceipts(ctx context.Context) ([]redisReservationReceipt, error) {
	if c == nil || c.rdb == nil {
		return nil, errors.New("redis client unavailable")
	}
	const scanBatch = 256
	var (
		cursor   uint64
		receipts = make([]redisReservationReceipt, 0, scanBatch)
	)
	for {
		keys, next, err := c.rdb.Scan(ctx, cursor, billingReservedItemKeyPrefix+"*", scanBatch).Result()
		if err != nil {
			return receipts, err
		}
		if len(keys) > 0 {
			pipe := c.rdb.Pipeline()
			gets := make([]*redis.StringCmd, len(keys))
			pttls := make([]*redis.DurationCmd, len(keys))
			for i, key := range keys {
				gets[i] = pipe.Get(ctx, key)
				pttls[i] = pipe.PTTL(ctx, key)
			}
			if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
				return receipts, err
			}
			now := time.Now()
			for i, key := range keys {
				amountText, err := gets[i].Result()
				if err != nil {
					// 键在 SCAN 与 GET 之间被归还/过期：跳过。
					continue
				}
				amount, err := strconv.ParseFloat(amountText, 64)
				if err != nil || amount <= 0 {
					continue
				}
				ttl, err := pttls[i].Result()
				if err != nil || ttl <= 0 {
					ttl = billingReservationFallbackDefaultRowTTL
				}
				scope, requestID, ok := splitReservationItemKey(key)
				if !ok {
					continue
				}
				receipts = append(receipts, redisReservationReceipt{
					Scope:     scope,
					RequestID: requestID,
					Amount:    amount,
					ExpiresAt: now.Add(ttl),
				})
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	return receipts, nil
}

// splitReservationItemKey 把 billing:resv_item:<scope>:<requestID> 拆成 (scope, requestID)。
// 前缀不匹配 / 缺少分隔符 / 任一段为空时返回 ok=false（非法键一律跳过，不猜测）。
func splitReservationItemKey(key string) (scope, requestID string, ok bool) {
	rest, found := strings.CutPrefix(key, billingReservedItemKeyPrefix)
	if !found {
		return "", "", false
	}
	// 当前键格式把 scope 包在 hash tag 里：{<scope>}:<requestID>。
	if strings.HasPrefix(rest, "{") {
		end := strings.LastIndex(rest, "}:")
		if end <= 1 || end+2 >= len(rest) {
			return "", "", false
		}
		return rest[1:end], rest[end+2:], true
	}
	index := strings.LastIndex(rest, ":")
	if index <= 0 || index >= len(rest)-1 {
		return "", "", false
	}
	scope = rest[:index]
	requestID = rest[index+1:]
	if scope == "" || requestID == "" {
		return "", "", false
	}
	return scope, requestID, true
}
