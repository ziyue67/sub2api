//go:build unit

package redissession

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestStoreRoundTripAndSingleUse(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	store := New(rdb, "oauth:test", time.Minute)
	ctx := context.Background()

	require.NoError(t, store.Set(ctx, "sid", map[string]string{"state": "state"}))
	var got map[string]string
	ok, err := store.Get(ctx, "sid", &got)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "state", got["state"])

	ok, err = store.TryConsume(ctx, "sid")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = store.TryConsume(ctx, "sid")
	require.NoError(t, err)
	require.False(t, ok)

	require.NoError(t, store.Delete(ctx, "sid"))
	ok, err = store.Get(ctx, "sid", &got)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestStoreTakeAcrossReplicasIsAtomic(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	first := New(rdb, "oauth:take", time.Minute)
	second := New(rdb, "oauth:take", time.Minute)
	ctx := context.Background()
	require.NoError(t, first.Set(ctx, "sid", "ciphertext"))
	results := make(chan bool, 16)
	for i := 0; i < 16; i++ {
		go func() {
			var got string
			ok, err := second.Take(ctx, "sid", &got)
			results <- ok && err == nil && got == "ciphertext"
		}()
	}
	consumed := 0
	for i := 0; i < 16; i++ {
		if <-results {
			consumed++
		}
	}
	require.Equal(t, 1, consumed)
	var got string
	ok, err := first.Get(ctx, "sid", &got)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, first.Set(ctx, "expired", "ciphertext"))
	mr.FastForward(time.Minute + time.Second)
	ok, err = second.Take(ctx, "expired", &got)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, first.Set(ctx, "invalid", map[string]string{"wrong": "shape"}))
	ok, err = second.Take(ctx, "invalid", &got)
	require.Error(t, err)
	require.False(t, ok)
	ok, err = second.Take(ctx, "invalid", &got)
	require.NoError(t, err)
	require.False(t, ok)
}
