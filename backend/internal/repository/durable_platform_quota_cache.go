package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func durablePlatformQuotaCacheKey(user int64, platform string) string {
	return fmt.Sprintf("billing:durable_platform_quota:%d:%s", user, platform)
}

func (c *billingCache) GetDurablePlatformQuotaCache(ctx context.Context, user int64, platform string) (*service.UserPlatformQuotaCacheEntry, bool, error) {
	return c.getPlatformQuotaCache(ctx, durablePlatformQuotaCacheKey(user, platform))
}

func (c *billingCache) SetDurablePlatformQuotaCache(ctx context.Context, user int64, platform string, entry *service.UserPlatformQuotaCacheEntry, ttl time.Duration) error {
	return c.setPlatformQuotaCache(ctx, durablePlatformQuotaCacheKey(user, platform), entry, ttl)
}

func (c *billingCache) DeleteDurablePlatformQuotaCache(ctx context.Context, user int64, platform string) error {
	return c.rdb.Del(ctx, durablePlatformQuotaCacheKey(user, platform)).Err()
}
