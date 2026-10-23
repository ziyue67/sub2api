package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyCacheSubscriber_BlocksUntilContextCancellation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer func() { _ = client.Close() }()
	cache := NewAPIKeyCache(client)
	ctx, cancel := context.WithCancel(context.Background())
	received := make(chan string, 1)
	returned := make(chan error, 1)
	go func() {
		returned <- cache.SubscribeAuthCacheInvalidation(ctx, func(value string) { received <- value })
	}()

	var value string
	require.Eventually(t, func() bool {
		require.NoError(t, client.Publish(context.Background(), authCacheInvalidateChannel, "hash").Err())
		select {
		case value = <-received:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)
	require.Equal(t, "hash", value)
	select {
	case err := <-returned:
		t.Fatalf("subscriber returned while connection was active: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-returned:
		require.True(t, errors.Is(err, context.Canceled))
	case <-time.After(time.Second):
		t.Fatal("subscriber did not stop after context cancellation")
	}
}

func TestAPIKeyCacheSubscriberReportsAutomaticResubscription(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &apiKeyCache{rdb: client}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	ready := make(chan struct{}, 4)
	received := make(chan string, 4)
	done := make(chan error, 1)
	go func() {
		done <- cache.subscribeAuthCacheInvalidation(ctx, func(key string) { received <- key }, func() { ready <- struct{}{} })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("subscriber cleanup did not finish")
		}
	})
	awaitReady := func() {
		t.Helper()
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("go-redis adapter did not report subscription acknowledgement")
		}
	}
	awaitReady()
	// Force an actual connection loss on the isolated fixture only. Restart
	// preserves the port, so the same PubSub call must reconnect by itself.
	server.Close()
	require.NoError(t, server.Restart())
	awaitReady()
	require.NoError(t, client.Publish(ctx, authCacheInvalidateChannel, "fixture-key-after-reconnect").Err())
	select {
	case key := <-received:
		require.Equal(t, "fixture-key-after-reconnect", key)
	case <-time.After(time.Second):
		t.Fatal("messages stopped after the automatic resubscription")
	}
	select {
	case err := <-done:
		done <- err // Keep cleanup's one join intact on assertion failure.
		t.Fatalf("subscriber returned instead of reconnecting: %v", err)
	default:
	}
}
