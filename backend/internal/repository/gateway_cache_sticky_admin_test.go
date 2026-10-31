package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheStickySessionAdminCountAndClear(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	cache := NewGatewayCache(client)
	ctx := context.Background()

	set := func(key string, value any) {
		require.NoError(t, client.Set(ctx, key, value, time.Hour).Err())
	}
	set("sticky_session:2:openai:session-a", 1)
	set("sticky_session:2:openai:session-b", 2)
	set("sticky_session:2:openai:session-c", 2)
	set("sticky_session:2:openai:response:resp-1", 3)
	set("sticky_session:2:openai:http-response-owner:user:u1", 4)
	set("sticky_session:3:openai:session-d", 5)

	admin, ok := cache.(service.StickySessionAdminCache)
	require.True(t, ok)
	counts, err := admin.CountStickySessionBindings(ctx, 2)
	require.NoError(t, err)
	require.Equal(t, map[int64]int{1: 1, 2: 2}, counts)

	deleted, err := admin.ClearStickySessionBindings(ctx, 2, map[int64]struct{}{2: {}})
	require.NoError(t, err)
	require.Equal(t, 2, deleted)
	require.True(t, mr.Exists("sticky_session:2:openai:session-a"))
	require.False(t, mr.Exists("sticky_session:2:openai:session-b"))
	require.False(t, mr.Exists("sticky_session:2:openai:session-c"))
	require.True(t, mr.Exists("sticky_session:2:openai:response:resp-1"))
	require.True(t, mr.Exists("sticky_session:2:openai:http-response-owner:user:u1"))
	require.True(t, mr.Exists("sticky_session:3:openai:session-d"))

	deleted, err = admin.ClearStickySessionBindings(ctx, 2, map[int64]struct{}{})
	require.NoError(t, err)
	require.Zero(t, deleted)
	require.True(t, mr.Exists("sticky_session:2:openai:session-a"))

	deleted, err = admin.ClearStickySessionBindings(ctx, 2, nil)
	require.NoError(t, err)
	require.Equal(t, 1, deleted)
	require.False(t, mr.Exists("sticky_session:2:openai:session-a"))
	require.True(t, mr.Exists("sticky_session:2:openai:response:resp-1"))
	require.True(t, mr.Exists("sticky_session:2:openai:http-response-owner:user:u1"))

	// Non-numeric values are not account bindings and must not be counted or deleted.
	set("sticky_session:2:openai:session-bad", "not-an-account-id")
	counts, err = admin.CountStickySessionBindings(ctx, 2)
	require.NoError(t, err)
	require.Empty(t, counts)
	deleted, err = admin.ClearStickySessionBindings(ctx, 2, nil)
	require.NoError(t, err)
	require.Zero(t, deleted)
}
