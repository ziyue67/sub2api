//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestUnifiedRouteCooldownSharedExpiryAndScope(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	a, b := &apiKeyCache{rdb: client}, &apiKeyCache{rdb: client}
	ctx := context.Background()
	require.NoError(t, a.SetAPIKeyRouteCooldown(ctx, 1, 2, 60*time.Second))
	require.NoError(t, b.SetAPIKeyRouteCooldown(ctx, 1, 2, 10*time.Second))
	server.FastForward(15 * time.Second)
	cooling, err := b.APIKeyRouteCooling(ctx, 1, 2)
	require.NoError(t, err)
	require.True(t, cooling, "shorter concurrent failure must not truncate cooldown")
	cooling, err = b.APIKeyRouteCooling(ctx, 2, 2)
	require.NoError(t, err)
	require.False(t, cooling, "another key must not be penalized")
	server.FastForward(46 * time.Second)
	cooling, err = b.APIKeyRouteCooling(ctx, 1, 2)
	require.NoError(t, err)
	require.False(t, cooling)
}
