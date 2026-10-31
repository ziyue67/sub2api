package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var _ service.SessionSecurityStore = (*refreshTokenCache)(nil)

func userSessionVersionKey(userID int64) string {
	return fmt.Sprintf("auth:session-version:%d", userID)
}
func (c *refreshTokenCache) GetSessionVersion(ctx context.Context, userID int64) (int64, error) {
	value, err := c.rdb.Get(ctx, userSessionVersionKey(userID)).Int64()
	if err == redis.Nil {
		return 0, nil
	}
	return value, err
}

// No TTL: expiring a generation could resurrect a still-valid generation-zero
// token. One small counter per user also covers old access-only JWTs without sid.
func (c *refreshTokenCache) RevokeUserSessions(ctx context.Context, userID int64) error {
	return c.rdb.Incr(ctx, userSessionVersionKey(userID)).Err()
}
func (c *refreshTokenCache) RevokeSession(ctx context.Context, familyID string, ttl time.Duration) error {
	return c.rdb.Set(ctx, "auth:revoked-session:"+familyID, "1", ttl).Err()
}
func (c *refreshTokenCache) IsSessionRevoked(ctx context.Context, familyID string) (bool, error) {
	n, err := c.rdb.Exists(ctx, "auth:revoked-session:"+familyID).Result()
	return n > 0, err
}
func (c *refreshTokenCache) GetRotatedRefreshToken(ctx context.Context, hash string) (*service.RefreshTokenData, error) {
	raw, err := c.rdb.Get(ctx, "auth:rotated-refresh:"+hash).Bytes()
	if err == redis.Nil {
		return nil, service.ErrRefreshTokenNotFound
	}
	if err != nil {
		return nil, err
	}
	var data service.RefreshTokenData
	if err = json.Unmarshal(raw, &data); err != nil {
		return nil, err
	}
	return &data, nil
}

var consumeRefreshScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then return 0 end
local ttl = redis.call('PTTL', KEYS[1])
if ttl <= 0 then return 0 end
redis.call('SET', KEYS[2], raw, 'PX', ttl)
redis.call('DEL', KEYS[1])
return 1
`)

func (c *refreshTokenCache) ConsumeRefreshToken(ctx context.Context, hash string) (bool, error) {
	n, err := consumeRefreshScript.Run(ctx, c.rdb, []string{refreshTokenKey(hash), "auth:rotated-refresh:" + hash}).Int()
	return n == 1, err
}
