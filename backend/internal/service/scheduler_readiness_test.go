package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type readinessCacheStub struct {
	SchedulerCache
	buckets []SchedulerBucket
	age     time.Duration
}

func (c *readinessCacheStub) CheckSnapshotReadiness(_ context.Context, b []SchedulerBucket, age time.Duration) error {
	c.buckets = b
	c.age = age
	return ErrSchedulerCacheNotReady
}

func TestSchedulerReadinessDisabledAndReaderBoundary(t *testing.T) {
	require.NoError(t, (*SchedulerSnapshotService)(nil).CheckReadiness(context.Background()))
	s := NewSchedulerSnapshotService(nil, nil, nil, nil, &config.Config{})
	require.NoError(t, s.CheckReadiness(context.Background()))
	c := &config.Config{Runtime: config.RuntimeConfig{Role: config.RuntimeRoleGateway, SchedulerReadiness: config.SchedulerReadinessConfig{Enabled: true, Buckets: []string{"17:openai:single"}, MaxAgeSeconds: 60}}}
	s = NewSchedulerSnapshotService(nil, nil, nil, nil, c)
	require.ErrorIs(t, s.CheckReadiness(context.Background()), ErrSchedulerCacheNotReady)
	cache := &readinessCacheStub{}
	s = NewSchedulerSnapshotService(cache, nil, nil, nil, c)
	require.ErrorIs(t, s.CheckReadiness(context.Background()), ErrSchedulerCacheNotReady)
	require.Equal(t, []SchedulerBucket{{GroupID: 17, Platform: PlatformOpenAI, Mode: SchedulerModeSingle}}, cache.buckets)
	require.Equal(t, time.Minute, cache.age)
	c.Runtime.Role = config.RuntimeRoleFull
	cache.buckets = nil
	require.ErrorIs(t, s.CheckReadiness(context.Background()), ErrSchedulerCacheNotReady)
	require.Nil(t, cache.buckets, "full publisher must not silently use gateway readiness")
}
