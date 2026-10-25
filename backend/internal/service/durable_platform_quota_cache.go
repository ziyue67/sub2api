package service

import (
	"context"
	"errors"
	"time"
)

var ErrUserPlatformQuotaCacheNotUpdated = errors.New("platform quota cache missing or schema mismatch; increment was not applied")

// Combined DB totals must never be flushed back as a legacy baseline. A separate
// cache namespace leaves old dirty entries available to the legacy flusher.
type durablePlatformQuotaCache interface {
	GetDurablePlatformQuotaCache(context.Context, int64, string) (*UserPlatformQuotaCacheEntry, bool, error)
	SetDurablePlatformQuotaCache(context.Context, int64, string, *UserPlatformQuotaCacheEntry, time.Duration) error
	DeleteDurablePlatformQuotaCache(context.Context, int64, string) error
}

func (s *BillingCacheService) getPlatformQuotaCache(ctx context.Context, user int64, platform string) (*UserPlatformQuotaCacheEntry, bool, error) {
	if cache, ok := s.cache.(durablePlatformQuotaCache); ok {
		return cache.GetDurablePlatformQuotaCache(ctx, user, platform)
	}
	return s.cache.GetUserPlatformQuotaCache(ctx, user, platform)
}

func (s *BillingCacheService) setPlatformQuotaCache(ctx context.Context, user int64, platform string, entry *UserPlatformQuotaCacheEntry, ttl time.Duration) error {
	if cache, ok := s.cache.(durablePlatformQuotaCache); ok {
		return cache.SetDurablePlatformQuotaCache(ctx, user, platform, entry, ttl)
	}
	return s.cache.SetUserPlatformQuotaCache(ctx, user, platform, entry, ttl)
}

func (s *BillingCacheService) InvalidateDurablePlatformQuota(ctx context.Context, user int64, platform string) error {
	if s == nil || s.cache == nil {
		return nil
	}
	if cache, ok := s.cache.(durablePlatformQuotaCache); ok {
		return cache.DeleteDurablePlatformQuotaCache(ctx, user, platform)
	}
	return s.cache.DeleteUserPlatformQuotaCache(ctx, user, platform)
}
