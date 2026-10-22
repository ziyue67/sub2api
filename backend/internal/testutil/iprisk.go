package testutil

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// NewRedisIPRiskManager keeps Redis construction outside handler tests while
// exercising the real IP risk manager against the caller's isolated server.
func NewRedisIPRiskManager(t *testing.T, store iprisk.Store, server *miniredis.Miniredis) *iprisk.Manager {
	t.Helper()
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	manager := iprisk.New(store, client, nil, false)
	t.Cleanup(manager.Stop)
	return manager
}
