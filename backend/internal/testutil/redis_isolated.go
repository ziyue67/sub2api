package testutil

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// NewIsolatedRedisGatewayCache uses DB 15 on a disposable loopback Redis supplied
// by CI. Callers must run serially; DB 15 is cleared before and after each case.
// Ordinary unit fixtures keep using miniredis through NewRedisGatewayCache.
func NewIsolatedRedisGatewayCache(t *testing.T, addr string) service.GatewayCache {
	t.Helper()
	host, _, err := net.SplitHostPort(addr)
	require.NoError(t, err)
	require.Contains(t, []string{"127.0.0.1", "::1", "localhost"}, host, "isolated fixture must never address a non-loopback Redis")
	client := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	info, err := client.Info(ctx, "server").Result()
	require.NoError(t, err)
	require.True(t, strings.Contains(info, "redis_version:"), "real Redis server required; miniredis is not network integration evidence")
	require.NoError(t, client.FlushDB(ctx).Err())
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = client.FlushDB(cleanupCtx).Err()
		_ = client.Close()
	})
	return repository.NewGatewayCache(client)
}
