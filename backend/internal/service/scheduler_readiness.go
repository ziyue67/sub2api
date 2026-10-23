package service

import (
	"context"
	"time"
)

type schedulerReadinessCache interface {
	CheckSnapshotReadiness(context.Context, []SchedulerBucket, time.Duration) error
}

// CheckReadiness adds an opt-in shared-cache scope check to the HTTP lifecycle.
// Disabled deployments retain their previous readiness contract. Regional
// admission is deliberately not enabled by this shared-Redis check.
func (s *SchedulerSnapshotService) CheckReadiness(ctx context.Context) error {
	if s == nil || s.cfg == nil || !s.cfg.Runtime.SchedulerReadiness.Enabled {
		return nil
	}
	if s.writesSnapshots() || s.localSnapshotReader != nil {
		return ErrSchedulerCacheNotReady
	}
	checker, ok := s.cache.(schedulerReadinessCache)
	if !ok {
		return ErrSchedulerCacheNotReady
	}
	settings := s.cfg.Runtime.SchedulerReadiness
	buckets := make([]SchedulerBucket, 0, len(settings.Buckets))
	for _, value := range settings.Buckets {
		bucket, ok := ParseSchedulerBucket(value)
		if !ok {
			return ErrSchedulerCacheNotReady
		}
		buckets = append(buckets, bucket)
	}
	return checker.CheckSnapshotReadiness(ctx, buckets, settings.MaxAge())
}
