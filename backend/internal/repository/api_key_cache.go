package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

const (
	apiKeyRateLimitKeyPrefix   = "apikey:ratelimit:"
	apiKeyRateLimitDuration    = 24 * time.Hour
	apiKeyCreateCountKeyPrefix = "apikey:create_count:"
	apiKeyAuthCachePrefix      = "apikey:auth:"
	authCacheInvalidateChannel = "auth:cache:invalidate"
)

// apiKeyRateLimitKey generates the Redis key for API key creation rate limiting.
func apiKeyRateLimitKey(userID int64) string {
	return fmt.Sprintf("%s%d", apiKeyRateLimitKeyPrefix, userID)
}

// apiKeyCreateCountKey generates the Redis key for per-user API key creation counting.
func apiKeyCreateCountKey(userID int64) string {
	return fmt.Sprintf("%s%d", apiKeyCreateCountKeyPrefix, userID)
}

func apiKeyAuthCacheKey(key string) string {
	return fmt.Sprintf("%s%s", apiKeyAuthCachePrefix, key)
}

type apiKeyCache struct {
	rdb *redis.Client
}

func NewAPIKeyCache(rdb *redis.Client) service.APIKeyCache {
	return &apiKeyCache{rdb: rdb}
}

// apiKeyCache 同时实现 service.APIKeyQuotaUsedLedger：把"结算后的 DB 真值"发布为
// API Key 已用额度的共享高水位。账本与鉴权缓存共用同一条 Redis，键与脚本定义见
// billing_cache.go（apiKeyQuotaUsedLedgerKey / apiKeyQuotaUsedLedgerKeyPrefix）。
var _ service.APIKeyQuotaUsedLedger = (*apiKeyCache)(nil)

// GetAPIKeyQuotaUsedLedger 读取账本高水位；键不存在（含已过期）返回 0。
func (c *apiKeyCache) GetAPIKeyQuotaUsedLedger(ctx context.Context, apiKeyID int64) (float64, error) {
	if c == nil || c.rdb == nil || apiKeyID <= 0 {
		return 0, nil
	}
	val, err := c.rdb.Get(ctx, apiKeyQuotaUsedLedgerKey(apiKeyID)).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return 0, nil
		}
		return 0, err
	}
	used, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return 0, fmt.Errorf("parse api key quota used ledger: %w", err)
	}
	return used, nil
}

// SetAPIKeyQuotaUsedLedger 以"只增不减"的方式发布结算后的 quota_used。
//
// 只增不减是必需的：并发结算的完成顺序与 DB 递增顺序无关，若允许覆盖写小，
// 就会出现"账本比 DB 真值小"的窗口，护栏又会按偏小的已用额度放行。
func (c *apiKeyCache) SetAPIKeyQuotaUsedLedger(ctx context.Context, apiKeyID int64, quotaUsed float64) error {
	if c == nil || c.rdb == nil || apiKeyID <= 0 {
		return nil
	}
	if quotaUsed <= 0 || math.IsNaN(quotaUsed) || math.IsInf(quotaUsed, 0) {
		return nil
	}
	_, err := setAPIKeyQuotaUsedLedgerScript.Run(ctx, c.rdb,
		[]string{apiKeyQuotaUsedLedgerKey(apiKeyID)},
		strconv.FormatFloat(quotaUsed, 'f', -1, 64),
		apiKeyQuotaUsedLedgerTTL.Milliseconds(),
	).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return err
	}
	return nil
}

// ClearAPIKeyQuotaUsedLedger 清除账本（管理员重置 quota_used / 关闭额度时必须调用）。
func (c *apiKeyCache) ClearAPIKeyQuotaUsedLedger(ctx context.Context, apiKeyID int64) error {
	if c == nil || c.rdb == nil || apiKeyID <= 0 {
		return nil
	}
	return c.rdb.Del(ctx, apiKeyQuotaUsedLedgerKey(apiKeyID)).Err()
}

func (c *apiKeyCache) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	key := apiKeyRateLimitKey(userID)
	count, err := c.rdb.Get(ctx, key).Int()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return count, err
}

func (c *apiKeyCache) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	key := apiKeyRateLimitKey(userID)
	pipe := c.rdb.Pipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, apiKeyRateLimitDuration)
	_, err := pipe.Exec(ctx)
	return err
}

// IncrementCreateCount 在固定窗口内累加创建次数并返回累加后的值。
// ExpireNX 只在首次创建计数键时设置过期，后续创建不会延长窗口；
// MULTI 保证 INCR 与 EXPIRE 同时生效，避免计数键丢失 TTL 后永久封禁。
func (c *apiKeyCache) IncrementCreateCount(ctx context.Context, userID int64, window time.Duration) (int64, error) {
	key := apiKeyCreateCountKey(userID)
	pipe := c.rdb.TxPipeline()
	incr := pipe.Incr(ctx, key)
	pipe.ExpireNX(ctx, key, window)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	return incr.Val(), nil
}

func (c *apiKeyCache) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return c.rdb.Incr(ctx, apiKey).Err()
}

func (c *apiKeyCache) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return c.rdb.Expire(ctx, apiKey, ttl).Err()
}

func (c *apiKeyCache) GetAuthCache(ctx context.Context, key string) (*service.APIKeyAuthCacheEntry, error) {
	val, err := c.rdb.Get(ctx, apiKeyAuthCacheKey(key)).Bytes()
	if err != nil {
		return nil, err
	}
	var entry service.APIKeyAuthCacheEntry
	if err := json.Unmarshal(val, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

func (c *apiKeyCache) SetAuthCache(ctx context.Context, key string, entry *service.APIKeyAuthCacheEntry, ttl time.Duration) error {
	if entry == nil {
		return nil
	}
	payload, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	return c.rdb.Set(ctx, apiKeyAuthCacheKey(key), payload, ttl).Err()
}

func (c *apiKeyCache) DeleteAuthCache(ctx context.Context, key string) error {
	return c.rdb.Del(ctx, apiKeyAuthCacheKey(key)).Err()
}

// PublishAuthCacheInvalidation publishes a cache invalidation message to all instances
func (c *apiKeyCache) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return c.rdb.Publish(ctx, authCacheInvalidateChannel, cacheKey).Err()
}

// SubscribeAuthCacheInvalidation subscribes to cache invalidation messages
func (c *apiKeyCache) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	pubsub := c.rdb.Subscribe(ctx, authCacheInvalidateChannel)

	// Verify subscription is working
	_, err := pubsub.Receive(ctx)
	if err != nil {
		_ = pubsub.Close()
		return fmt.Errorf("subscribe to auth cache invalidation: %w", err)
	}

	defer func() {
		if err := pubsub.Close(); err != nil {
			log.Printf("Warning: failed to close auth cache invalidation pubsub: %v", err)
		}
	}()
	service.NotifyAuthCacheSubscriptionReady(ctx)

	ch := pubsub.Channel()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg, ok := <-ch:
			if !ok {
				return errors.New("auth cache invalidation pubsub channel closed")
			}
			if msg != nil {
				handler(msg.Payload)
			}
		}
	}
}
