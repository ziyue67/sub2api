//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAuthInvalidationFanoutPreservesIndependentSubscriberLifecycle(t *testing.T) {
	// The integration TestMain owns this isolated Docker Redis. Pub/Sub channels
	// are global to Redis, so keep this test serial and join every subscriber.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var caches [3]service.APIKeyCache
	var connectionIDs [3]int64
	for i := range caches {
		opts := *integrationRedis.Options()
		opts.PoolSize = 1
		opts.ContextTimeoutEnabled = true
		client := redis.NewClient(&opts)
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		id, err := client.ClientID(ctx).Result()
		require.NoError(t, err)
		connectionIDs[i] = id
		caches[i] = NewAPIKeyCache(client)
	}
	for i := range connectionIDs {
		for j := 0; j < i; j++ {
			require.NotEqual(t, connectionIDs[j], connectionIDs[i], "publisher and readers must have independent connection pools")
		}
	}
	awaitSubscribers := func(want int64) {
		t.Helper()
		require.Eventually(t, func() bool {
			counts, err := integrationRedis.PubSubNumSub(ctx, authCacheInvalidateChannel).Result()
			return err == nil && counts[authCacheInvalidateChannel] == want
		}, 5*time.Second, 10*time.Millisecond, "unexpected number of live auth invalidation subscriptions")
	}
	awaitSubscribers(0)
	type subscription struct {
		cancel   context.CancelFunc
		messages chan string
		done     chan struct{}
		err      error
	}
	start := func(cache service.APIKeyCache) *subscription {
		t.Helper()
		subscriberCtx, stop := context.WithCancel(ctx)
		sub := &subscription{cancel: stop, messages: make(chan string, 8), done: make(chan struct{})}
		go func() {
			defer close(sub.done)
			sub.err = cache.SubscribeAuthCacheInvalidation(subscriberCtx, func(value string) {
				select {
				case sub.messages <- value:
				case <-subscriberCtx.Done():
				}
			})
		}()
		t.Cleanup(func() {
			stop()
			select {
			case <-sub.done:
			case <-time.After(5 * time.Second):
				t.Error("auth invalidation subscriber cleanup did not finish")
			}
		})
		return sub
	}
	receive := func(sub *subscription, want string) {
		t.Helper()
		select {
		case value := <-sub.messages:
			require.Equal(t, want, value)
		case <-sub.done:
			t.Fatalf("auth invalidation subscriber exited before delivery: %v", sub.err)
		case <-ctx.Done():
			t.Fatal("auth invalidation event did not arrive before the deadline")
		}
	}
	publish := func(label string) string {
		t.Helper()
		// Match the production cache-key shape, using only a synthetic fixture.
		digest := sha256.Sum256([]byte(t.Name() + ":" + label))
		value := hex.EncodeToString(digest[:])
		require.NoError(t, caches[2].PublishAuthCacheInvalidation(ctx, value))
		return value
	}
	first, peer := start(caches[0]), start(caches[1])
	awaitSubscribers(2)
	initial := publish("initial")
	receive(first, initial)
	receive(peer, initial)

	first.cancel()
	select {
	case <-first.done:
		require.True(t, errors.Is(first.err, context.Canceled))
	case <-ctx.Done():
		t.Fatal("canceling one subscriber did not join its adapter call")
	}
	awaitSubscribers(1)
	peerOnly := publish("peer-remains-live")
	receive(peer, peerOnly)

	// Reusing the first client's pool must create a new subscription without
	// stopping the peer. This is an explicit restart, not go-redis reconnect.
	restarted := start(caches[0])
	awaitSubscribers(2)
	afterRestart := publish("after-explicit-restart")
	receive(restarted, afterRestart)
	receive(peer, afterRestart)
}
