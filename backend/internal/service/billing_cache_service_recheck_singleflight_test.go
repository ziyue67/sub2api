//go:build unit

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

var errRecheckBoom = errors.New("recheck boom")

// TestCheckBalanceEligibility_RecheckSingleflightMergesConcurrentBursts 覆盖
// "余额贴近底线时并发突发"的真实事故形态：每笔请求都要 DB 复核，若 N 笔
// 请求各自回源，连接池会被瞬时打满并 fail-closed 成 503。复核必须被
// singleflight 合并为一次回源，等待者共享同一结果。
func TestCheckBalanceEligibility_RecheckSingleflightMergesConcurrentBursts(t *testing.T) {
	// 缓存余额 0.15：贴近底线（reserve=0.1，复核带宽回退为 max(reserve, 2*reserve)=0.2），
	// 每笔请求都会触发 DB 复核。
	cache := &balanceEligibilityCacheStub{balance: 0.15}
	userRepo := &balanceLoadUserRepoStub{
		delay:   80 * time.Millisecond,
		balance: 0.15,
	}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.1
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	const goroutines = 16
	start := make(chan struct{})
	var wg sync.WaitGroup
	errCh := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := svc.checkBalanceEligibility(context.Background(), 99, 0)
			errCh <- err
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		require.NoError(t, err)
	}

	require.Equal(t, int64(1), userRepo.calls.Load(), "贴近底线的并发余额复核应被 singleflight 合并为一次 DB 回源")
	require.Eventually(t, func() bool {
		return cache.setCalls.Load() >= 1
	}, time.Second, 10*time.Millisecond, "复核成功后应把 DB 真值写回缓存")
}

// TestCheckBalanceEligibility_RecheckFailureStillFailClosed 确认合并复核
// 后的失败语义不变：DB 复核失败仍然 fail-closed（ErrBillingServiceUnavailable），
// 绝不因为"节省一次查询"而放行。
func TestCheckBalanceEligibility_RecheckFailureStillFailClosed(t *testing.T) {
	cache := &balanceEligibilityCacheStub{balance: 0.15}
	userRepo := &balanceLoadUserRepoStub{
		delay:   20 * time.Millisecond,
		balance: 0.15,
		err:     errRecheckBoom,
	}
	cfg := &config.Config{}
	cfg.Billing.MinimumBalanceReserve = 0.1
	svc := NewBillingCacheService(cache, userRepo, nil, nil, nil, nil, cfg, nil)
	t.Cleanup(svc.Stop)

	_, err := svc.checkBalanceEligibility(context.Background(), 99, 0)
	require.ErrorIs(t, err, ErrBillingServiceUnavailable)
	require.Equal(t, int64(1), userRepo.calls.Load())
}
