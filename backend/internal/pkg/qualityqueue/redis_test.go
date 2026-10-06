package qualityqueue

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestRedisQueueIsolatesTimeoutsAndPreservesNewEpisodes(t *testing.T) {
	server := miniredis.RunT(t)
	shared := redis.NewClient(&redis.Options{Addr: server.Addr(), ReadTimeout: 3 * time.Second, PoolSize: 50})
	t.Cleanup(func() { _ = shared.Close() })
	q := NewRedis(shared)
	t.Cleanup(func() { _ = q.Close() })
	require.Equal(t, 3*time.Second, shared.Options().ReadTimeout)
	require.Equal(t, 150*time.Millisecond, q.client.Options().ReadTimeout)
	require.True(t, q.client.Options().ContextTimeoutEnabled)
	ctx := context.Background()
	at := time.Now().Truncate(time.Millisecond)
	require.NoError(t, q.Enqueue(ctx, 9, at))
	require.NoError(t, q.Enqueue(ctx, 9, at))
	score, err := q.Score(ctx, 9)
	require.NoError(t, err)
	require.True(t, score.After(at))
	require.NoError(t, q.Acknowledge(ctx, Signal{AccountID: 9, ObservedAt: at}))
	entries, err := q.Pending(ctx, at.Add(-time.Minute))
	require.NoError(t, err)
	require.Equal(t, []Signal{{AccountID: 9, ObservedAt: score}}, entries)
	require.NoError(t, q.Acknowledge(ctx, entries[0]))
	entries, err = q.Pending(ctx, at.Add(-time.Minute))
	require.NoError(t, err)
	require.Empty(t, entries)
}
