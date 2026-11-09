package repository

import (
	"context"
	"fmt"
	"time"
)

func apiKeyRouteCooldownKey(keyID, groupID int64) string {
	return fmt.Sprintf("apikey:route-cooldown:%d:%d", keyID, groupID)
}

func (c *apiKeyCache) APIKeyRouteCooling(ctx context.Context, keyID, groupID int64) (bool, error) {
	n, err := c.rdb.Exists(ctx, apiKeyRouteCooldownKey(keyID, groupID)).Result()
	return n > 0, err
}

func (c *apiKeyCache) SetAPIKeyRouteCooldown(ctx context.Context, keyID, groupID int64, ttl time.Duration) error {
	// Concurrent failures may extend, but never shorten, an existing cooldown.
	return c.rdb.Eval(ctx, `local ttl=redis.call('PTTL',KEYS[1]); if ttl < tonumber(ARGV[1]) then redis.call('PSETEX',KEYS[1],ARGV[1],'1') end; return 1`, []string{apiKeyRouteCooldownKey(keyID, groupID)}, ttl.Milliseconds()).Err()
}
