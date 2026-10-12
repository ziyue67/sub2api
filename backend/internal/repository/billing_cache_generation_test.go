package repository

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// 本文件锁死审计 R9：余额缓存的"回源写回把旧快照重新发布"竞态。
//
// 余额缓存是"未命中回源 + 异步写回"：读取方从 DB 读到余额、到真正写回缓存之间可能
// 发生结算扣费。扣费会递减缓存，但读取方随后落下的旧快照会把**偏高**的余额重新发布，
// 使预检在整个缓存 TTL 内持续放行注定扣费失败的请求（原有的"钱包已耗尽"标记只堵住了
// 这一后果在"扣到底线"时的表现，没有修掉余额本身被写回的问题）。
//
// 修复方式：每次余额变动递增一个"变动代号"，读取方在**发起 DB 读取之前**取一份代号，
// 写回时只有代号未变才发布。

func newBalanceGenerationTestCache(t *testing.T) (*miniredis.Miniredis, *redis.Client) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func TestBillingCache_DiscardsStaleBalanceSnapshotAfterDeduction(t *testing.T) {
	_, rdb := newBalanceGenerationTestCache(t)
	cache, ok := NewBillingCache(rdb).(*billingCache)
	require.True(t, ok)
	ctx := context.Background()
	const userID int64 = 7101

	require.NoError(t, cache.SetUserBalance(ctx, userID, 100))

	// 读取方在发起 DB 读取之前取代号。
	generation, err := cache.BalanceGeneration(ctx, userID)
	require.NoError(t, err)

	// 读取方读 DB 的这段时间里发生了结算扣费：缓存被递减，代号被递增。
	require.NoError(t, cache.DeductUserBalance(ctx, userID, 10))
	afterDeduct, err := cache.GetUserBalance(ctx, userID)
	require.NoError(t, err)
	require.InDelta(t, 90, afterDeduct, 1e-9, "扣费应先递减缓存")

	// 读取方拿着"扣费前"的 100 回来写回：必须被丢弃。
	published, err := cache.SetUserBalanceIfGeneration(ctx, userID, 100, generation)
	require.NoError(t, err)
	require.False(t, published, "扣费之后的旧快照不得发布")

	balance, err := cache.GetUserBalance(ctx, userID)
	require.NoError(t, err)
	require.InDelta(t, 90, balance, 1e-9, "缓存必须保持扣费后的值，不能被旧快照复活")

	// 代号未变时正常发布（模拟"读 DB 期间没有发生余额变动"）。
	current, err := cache.BalanceGeneration(ctx, userID)
	require.NoError(t, err)
	published, err = cache.SetUserBalanceIfGeneration(ctx, userID, 88, current)
	require.NoError(t, err)
	require.True(t, published)
	balance, err = cache.GetUserBalance(ctx, userID)
	require.NoError(t, err)
	require.InDelta(t, 88, balance, 1e-9)
}

func TestBillingCache_InvalidateBumpsBalanceGeneration(t *testing.T) {
	_, rdb := newBalanceGenerationTestCache(t)
	cache, ok := NewBillingCache(rdb).(*billingCache)
	require.True(t, ok)
	ctx := context.Background()
	const userID int64 = 7102

	require.NoError(t, cache.SetUserBalance(ctx, userID, 5))
	generation, err := cache.BalanceGeneration(ctx, userID)
	require.NoError(t, err)

	// 加钱/失效路径：既删除旧值，也让在途快照作废。
	require.NoError(t, cache.InvalidateUserBalance(ctx, userID))

	published, err := cache.SetUserBalanceIfGeneration(ctx, userID, 5, generation)
	require.NoError(t, err)
	require.False(t, published, "失效之后的旧快照不得发布")

	// 键不存在时代号归一化为 "0"，与发布脚本一致（不 panic、不报错）。
	hydrated, err := cache.BalanceGeneration(ctx, userID)
	require.NoError(t, err)
	require.NotEqual(t, generation, hydrated, "失效必须递增代号")
}
