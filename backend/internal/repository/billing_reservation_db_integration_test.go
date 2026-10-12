//go:build integration

package repository

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// BillingReservationDBFallbackSuite 验证审计 R2：Redis 侧的预留后端不可用时，
// 准入必须退化为数据库侧的原子预留（billing_balance_reservations），
// 而不是把错误抛回上层触发 fail-open（本笔无任何预留被放行）。
//
// 对标 new-api 的 reserveUserQuotaDB：`UPDATE ... WHERE quota >= ?` 式的条件更新，
// 让"降级"仍然保有原子性。
type BillingReservationDBFallbackSuite struct {
	IntegrationRedisSuite
}

func TestBillingReservationDBFallbackSuite(t *testing.T) {
	suite.Run(t, new(BillingReservationDBFallbackSuite))
}

// brokenRedisCache 构造一个连不上 Redis 的 billingCache（带 DB 兜底句柄），
// 用来复现"Redis 不可用"这一降级场景。
func (s *BillingReservationDBFallbackSuite) brokenRedisCache() *billingCache {
	s.T().Helper()
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1", // 必然拒绝连接
		DialTimeout:  200 * time.Millisecond,
		ReadTimeout:  200 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		MaxRetries:   -1, // 不重试，避免测试变慢
	})
	s.T().Cleanup(func() { _ = rdb.Close() })
	cache, ok := NewBillingCacheWithDB(rdb, integrationDB).(*billingCache)
	require.True(s.T(), ok, "NewBillingCacheWithDB 应返回 *billingCache")
	return cache
}

func (s *BillingReservationDBFallbackSuite) createUser(balance float64) int64 {
	s.T().Helper()
	ctx := context.Background()
	var id int64
	err := integrationDB.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, status, balance, concurrency)
		VALUES ($1, 'x', 'user', 'active', $2, 5)
		RETURNING id`,
		fmt.Sprintf("resv-db-%d@example.test", time.Now().UnixNano()), balance,
	).Scan(&id)
	require.NoError(s.T(), err, "创建测试用户")
	s.T().Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(),
			`DELETE FROM billing_balance_reservations WHERE user_id = $1`, id)
		_, _ = integrationDB.ExecContext(context.Background(),
			`DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func (s *BillingReservationDBFallbackSuite) reservationCount(userID int64) int {
	s.T().Helper()
	var n int
	require.NoError(s.T(), integrationDB.QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM billing_balance_reservations WHERE user_id = $1`, userID).Scan(&n))
	return n
}

// TestFallsBackToDatabaseWhenRedisIsUnavailable 是本文件的核心用例：
// Redis 全程不可用，但上限判定必须与 Redis 原子脚本同口径（预算 0.30、单笔 0.10 → 恰好 3 笔）。
func (s *BillingReservationDBFallbackSuite) TestFallsBackToDatabaseWhenRedisIsUnavailable() {
	cache := s.brokenRedisCache()
	ctx := context.Background()
	userID := s.createUser(10)
	scope := strconv.FormatInt(userID, 10)

	accepted := 0
	acceptedIDs := make([]string, 0, 3)
	for i := 0; i < 5; i++ {
		requestID := fmt.Sprintf("db-fallback-%d", i)
		total, ok, err := cache.TryReserveUserBalance(ctx, scope, requestID, 0.10, 0.30, 5*time.Minute)
		require.NoError(s.T(), err, "Redis 故障时必须已被 DB 兜底接住，不得把错误抛回上层（会 fail-open）")
		if ok {
			accepted++
			acceptedIDs = append(acceptedIDs, requestID)
			require.LessOrEqual(s.T(), total, 0.30+1e-9)
		}
	}
	require.Equal(s.T(), 3, accepted, "DB 兜底必须与原子上限判定同口径")
	require.Equal(s.T(), 3, s.reservationCount(userID))

	// 归还后额度必须立刻释放（DB 侧按主键删除）。
	for _, requestID := range acceptedIDs {
		require.NoError(s.T(), cache.ReleaseUserBalanceReservation(ctx, scope, requestID, 0.10, 5*time.Minute))
	}
	require.Equal(s.T(), 0, s.reservationCount(userID), "归还后不得残留行")

	// 归还一个不存在的凭据：DB 兜底可用、但本笔不在其中 ⇒ 与"凭据已随 TTL 回收"同义，
	// 必须返回 ErrBillingReservationExpired（调用方据此走"过期归还"计数，而不是报故障）。
	err := cache.ReleaseUserBalanceReservation(ctx, scope, "never-existed", 0.10, 5*time.Minute)
	require.ErrorIs(s.T(), err, service.ErrBillingReservationExpired)
	require.Equal(s.T(), 0, s.reservationCount(userID))
}

// TestExpiredReservationIsSweptOnNextAdmission 是 PR#10 那类故障的回归护栏：
// 预留必须按笔过期。若过期行不被清理，一次崩溃留下的记录会**永久**占用额度，
// 用户会在余额远高于封底时被持续 403 且无法自愈。
func (s *BillingReservationDBFallbackSuite) TestExpiredReservationIsSweptOnNextAdmission() {
	cache := s.brokenRedisCache()
	ctx := context.Background()
	userID := s.createUser(10)
	scope := strconv.FormatInt(userID, 10)

	// 手工塞一条已过期的残留预留（模拟进程崩溃后没有归还）。
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO billing_balance_reservations (user_id, request_id, amount, expires_at)
		VALUES ($1, 'stale', 0.30, NOW() - INTERVAL '1 minute')`, userID)
	require.NoError(s.T(), err)

	// 若过期行未被清理，在途总额 = 0.30，本笔 0.10 会被判超预算而拒绝；
	// 清理后总额 = 0，本笔应被接受。
	_, accepted, err := cache.TryReserveUserBalance(ctx, scope, "after-sweep", 0.10, 0.30, 5*time.Minute)
	require.NoError(s.T(), err)
	require.True(s.T(), accepted, "过期预留必须被 TTL 自愈清理，不得永久占用额度")
	require.Equal(s.T(), 1, s.reservationCount(userID), "残留行应已被清理")
}

// TestRenewDoesNotResurrectExpiredReservation 验证续期不会把已过期的行复活
// （否则一笔崩溃后残留的记录会被心跳永久续命，护栏反过来把用户钉死）。
func (s *BillingReservationDBFallbackSuite) TestRenewDoesNotResurrectExpiredReservation() {
	cache := s.brokenRedisCache()
	ctx := context.Background()
	userID := s.createUser(10)
	scope := strconv.FormatInt(userID, 10)

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO billing_balance_reservations (user_id, request_id, amount, expires_at)
		VALUES ($1, 'expired', 0.05, NOW() - INTERVAL '1 minute')`, userID)
	require.NoError(s.T(), err)

	require.ErrorIs(s.T(),
		cache.RenewUserBalanceReservation(ctx, scope, "expired", 5*time.Minute),
		service.ErrBillingReservationExpired,
		"已过期的预留不得被续期复活")
}
