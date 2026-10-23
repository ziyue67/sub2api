package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestAuthCacheResubscriptionInvalidatesPositiveAndNegativeL1(t *testing.T) {
	resubscribe := make(chan struct{})
	acknowledged := make(chan struct{}, 3)
	cache := &authInvalidationCacheStub{subscribeFn: func(ctx context.Context, handler func(string)) error {
		NotifyAuthCacheSubscriptionReady(ctx)
		acknowledged <- struct{}{}
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-resubscribe:
				// Same long-lived adapter call: go-redis reconnects internally.
				NotifyAuthCacheSubscriptionReady(ctx)
				acknowledged <- struct{}{}
			}
		}
	}}
	cfg := &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L1Size: 128, L1TTLSeconds: 60, NegativeTTLSeconds: 60}}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, cfg)
	require.NotNil(t, svc.authCacheL1)
	require.NotNil(t, svc.authNegativeCacheL1)
	t.Cleanup(svc.authCacheL1.Close)
	t.Cleanup(svc.authNegativeCacheL1.Close)
	t.Cleanup(svc.StopAuthCacheInvalidationSubscriber)
	peer := NewAPIKeyService(nil, nil, nil, nil, nil, nil, cfg)
	t.Cleanup(peer.authCacheL1.Close)
	t.Cleanup(peer.authNegativeCacheL1.Close)
	positive := &APIKeyAuthCacheEntry{}
	negative := &APIKeyAuthCacheEntry{NotFound: true}
	warm := func(node *APIKeyService) {
		t.Helper()
		node.setAuthCacheL1("fixture-positive", positive)
		node.setAuthCacheL1("fixture-negative", negative)
		node.authCacheL1.Wait()
		node.authNegativeCacheL1.Wait()
		for _, key := range []string{"fixture-positive", "fixture-negative"} {
			_, ok := node.getAuthCacheEntry(context.Background(), key)
			require.True(t, ok)
		}
	}
	ack := func() {
		t.Helper()
		select {
		case <-acknowledged:
		case <-time.After(time.Second):
			t.Fatal("subscription checkpoint not acknowledged")
		}
	}
	warm(svc) // Entries loaded before the first subscription can also be stale.
	warm(peer)
	svc.StartAuthCacheInvalidationSubscriber(context.Background())
	ack()
	for _, key := range []string{"fixture-positive", "fixture-negative"} {
		_, ok := svc.getAuthCacheEntry(context.Background(), key)
		require.False(t, ok, "first subscription must discard pre-subscription cache")
	}
	warm(svc)
	epoch := svc.authCacheL1Epoch.Load()
	resubscribe <- struct{}{}
	ack()
	require.Greater(t, svc.authCacheL1Epoch.Load(), epoch)
	for _, key := range []string{"fixture-positive", "fixture-negative"} {
		_, ok := svc.getAuthCacheEntry(context.Background(), key)
		require.False(t, ok, "missed invalidations must not survive a resubscription checkpoint")
		_, peerHit := peer.getAuthCacheEntry(context.Background(), key)
		require.True(t, peerHit, "one node's reconnect must not reset another node's healthy L1")
	}
	warm(svc) // Fresh entries remain cacheable after the checkpoint.
	svc.invalidateLocalAuthCache("fixture-positive")
	svc.invalidateLocalAuthCache("fixture-negative")
	svc.authCacheL1.Wait()
	svc.authNegativeCacheL1.Wait()
	for _, key := range []string{"fixture-positive", "fixture-negative"} {
		_, ok := svc.getAuthCacheEntry(context.Background(), key)
		require.False(t, ok, "ordinary per-key invalidation must retain its contract")
	}
	require.True(t, svc.AuthCacheInvalidationSubscriberHealth().Connected)
	require.Zero(t, svc.AuthCacheInvalidationSubscriberHealth().Failures, "an internal reconnect checkpoint is not a service-level retry failure")
}
