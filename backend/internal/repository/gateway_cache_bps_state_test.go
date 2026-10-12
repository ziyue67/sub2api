package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestBPSSharedStateCrossInstanceCASIsolationExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	first := redis.NewClient(&redis.Options{Addr: server.Addr()})
	second := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })
	a, b := &gatewayCache{rdb: first}, &gatewayCache{rdb: second}
	ctx := context.Background()
	scope := "account:1/key:2/thread:example"
	zero := uint64(0)
	ok, err := a.SaveBPSState(ctx, scope, "catalog", &zero, []byte("first"))
	require.NoError(t, err)
	require.True(t, ok)
	raw, version, err := b.LoadBPSState(ctx, scope, "catalog")
	require.NoError(t, err)
	require.Equal(t, "first", string(raw))
	require.NotZero(t, version)
	ok, err = b.SaveBPSState(ctx, scope, "catalog", &zero, []byte("stale"))
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = b.SaveBPSState(ctx, scope, "catalog", &version, []byte("new"))
	require.NoError(t, err)
	require.True(t, ok)
	for _, other := range []string{"account:2/key:2/thread:example", "account:1/key:3/thread:example", "account:1/key:2/thread:other"} {
		raw, _, err = a.LoadBPSState(ctx, other, "catalog")
		require.NoError(t, err)
		require.Empty(t, raw)
	}
	server.FastForward(time.Hour)
	_, _, err = b.LoadBPSState(ctx, scope, "catalog")
	require.NoError(t, err)
	require.Equal(t, bpsStateTTL, server.TTL(bpsStateKey(scope, "catalog")))
	server.FastForward(bpsStateTTL + time.Second)
	raw, _, err = a.LoadBPSState(ctx, scope, "catalog")
	require.NoError(t, err)
	require.Empty(t, raw)
}

func TestBPSSharedStateBudgetsAndErrors(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	c := &gatewayCache{rdb: client}
	ctx := context.Background()
	_, err := c.SaveBPSState(ctx, "scope", "large", nil, []byte(strings.Repeat("x", bpsStateEntryLimit+1)))
	require.ErrorIs(t, err, basispoints.ErrStateCapacity)
	for i := 0; i < 20; i++ {
		_, err = c.SaveBPSState(ctx, fmt.Sprint(i), "replay", nil, []byte(strings.Repeat("x", bpsStateEntryLimit-100)))
		require.NoError(t, err)
	}
	size, err := client.HGet(ctx, bpsStatePrefix+"sizes", "__bytes").Int64()
	require.NoError(t, err)
	require.LessOrEqual(t, size, int64(bpsStateByteLimit))
	count, err := client.ZCard(ctx, bpsStatePrefix+"lru").Result()
	require.NoError(t, err)
	require.LessOrEqual(t, count, int64(bpsStateCountLimit))
	// Closed client fails immediately; no live Redis or network outage timing needed.
	require.NoError(t, client.Close())
	_, _, err = c.LoadBPSState(ctx, "scope", "catalog")
	require.ErrorIs(t, err, basispoints.ErrStateUnavailable)
}
