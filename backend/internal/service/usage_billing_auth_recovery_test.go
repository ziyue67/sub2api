//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type checkedBillingAuthRedis struct {
	APIKeyCache
	client *redis.Client
	fault  *atomic.Int32
	ready  chan struct{}
}

func (c *checkedBillingAuthRedis) DeleteAuthCache(ctx context.Context, key string) error {
	if c.fault.Load() == 2 {
		return errors.New("injected Redis delete failure")
	}
	return c.client.Del(ctx, key).Err()
}
func (c *checkedBillingAuthRedis) PublishAuthCacheInvalidation(ctx context.Context, key string) error {
	if c.fault.Load() == 3 {
		return errors.New("injected Redis broadcast failure")
	}
	return c.client.Publish(ctx, "billing-auth-test", key).Err()
}
func (c *checkedBillingAuthRedis) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(string)) error {
	sub := c.client.Subscribe(ctx, "billing-auth-test")
	defer func() { _ = sub.Close() }()
	if _, err := sub.Receive(ctx); err != nil {
		return err
	}
	NotifyAuthCacheSubscriptionReady(ctx)
	c.ready <- struct{}{}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-sub.Channel():
			if !ok {
				return errors.New("test subscriber closed")
			}
			handler(msg.Payload)
		}
	}
}

type exhaustedBillingReceipt struct {
	UsageBillingRepository
	debits   int
	applied  bool
	complete bool
}

func (*exhaustedBillingReceipt) StageUsageBilling(context.Context, *UsageBillingCommand, *UsageLog) error {
	return nil
}
func (r *exhaustedBillingReceipt) PendingUsageBilling(context.Context, int) ([]UsageBillingEventKey, error) {
	if r.complete {
		return nil, nil
	}
	return []UsageBillingEventKey{{RequestID: "exhausted", APIKeyID: 7}}, nil
}
func (r *exhaustedBillingReceipt) ApplyStagedUsageBilling(context.Context, UsageBillingEventKey) (*UsageBillingRecovery, error) {
	fresh := !r.applied
	if fresh {
		r.debits++
		r.applied = true
	}
	return &UsageBillingRecovery{Event: UsageBillingEvent{Command: UsageBillingCommand{UserID: 9}},
		Result: UsageBillingApplyResult{Applied: fresh, APIKeyQuotaExhausted: true}, NeedsCache: true}, nil
}
func (r *exhaustedBillingReceipt) AcknowledgeUsageBillingCache(context.Context, UsageBillingEventKey) error {
	r.complete = true
	return nil
}

func TestExhaustedBillingCheckedInvalidationFailuresRetryAcrossTwoNodes(t *testing.T) {
	for _, failure := range []int32{1, 2, 3} {
		t.Run(map[int32]string{1: "lookup", 2: "delete", 3: "broadcast"}[failure], func(t *testing.T) {
			ctx := context.Background()
			server := miniredis.RunT(t)
			client := redis.NewClient(&redis.Options{Addr: server.Addr()})
			var fault atomic.Int32
			cache := &checkedBillingAuthRedis{client: client, fault: &fault, ready: make(chan struct{}, 2)}
			repo := &authRepoStub{listKeysByUserID: func(context.Context, int64) ([]string, error) {
				if fault.Load() == 1 {
					return nil, errors.New("injected key lookup failure")
				}
				return []string{"synthetic-key"}, nil
			}}
			cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 128, L1TTLSeconds: 60, L2TTLSeconds: 60}}
			a := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			b := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
			t.Cleanup(func() {
				a.StopAuthCacheInvalidationSubscriber()
				b.StopAuthCacheInvalidationSubscriber()
				_ = client.Close()
			})
			a.StartAuthCacheInvalidationSubscriber(ctx)
			b.StartAuthCacheInvalidationSubscriber(ctx)
			for i := 0; i < 2; i++ {
				select {
				case <-cache.ready:
				case <-time.After(3 * time.Second):
					t.Fatal("both auth nodes must subscribe before invalidation")
				}
			}
			key := a.authCacheKey("synthetic-key")
			for _, node := range []*APIKeyService{a, b} {
				require.NotNil(t, node.authCacheL1)
				node.setAuthCacheL1(key, &APIKeyAuthCacheEntry{})
				node.authCacheL1.Wait()
				_, ok := node.authCacheL1.Get(key)
				require.True(t, ok)
			}
			require.NoError(t, client.Set(ctx, key, "cached", time.Minute).Err())
			fault.Store(failure)
			receipt := &exhaustedBillingReceipt{}
			require.Error(t, recoverUsageBillingBatch(ctx, receipt, nil, a))
			require.False(t, receipt.complete, "lookup/delete/broadcast errors must not acknowledge completion")
			require.Equal(t, 1, receipt.debits)
			_, stillCached := b.authCacheL1.Get(key)
			require.True(t, stillCached, "failed broadcast must not masquerade as remote invalidation")
			fault.Store(0)
			require.NoError(t, recoverUsageBillingBatch(ctx, receipt, nil, a))
			require.True(t, receipt.complete)
			require.Equal(t, 1, receipt.debits, "cache recovery must not debit again")
			require.Eventually(t, func() bool { _, ok := b.authCacheL1.Get(key); return !ok }, 3*time.Second, time.Millisecond)
			require.Equal(t, int64(0), client.Exists(ctx, key).Val())
		})
	}
}

func TestExhaustedBillingMissingCheckedInvalidatorRemainsPending(t *testing.T) {
	err := invalidateDurableUsageBilling(context.Background(), &UsageBillingRecovery{
		Result: UsageBillingApplyResult{APIKeyQuotaExhausted: true}, NeedsCache: true,
	}, nil, nil)
	require.ErrorContains(t, err, "checked auth cache invalidation unavailable")
}
