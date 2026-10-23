package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

var errWSNodeOwnerUnavailable = errors.New("websocket node owner unavailable")

func wsNodeOwnerKey(ref service.WSNodeOwnerRef) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(ref.ResponseID)))
	return fmt.Sprintf("ws_node_owner:%d:%d:%d:%s", ref.GroupID, ref.APIKeyID, ref.AccountID, hex.EncodeToString(digest[:]))
}

func validWSNodeOwner(ref service.WSNodeOwnerRef, owner string) bool {
	parts := strings.Split(owner, ":")
	return ref.GroupID >= 0 && ref.APIKeyID > 0 && ref.AccountID > 0 && strings.TrimSpace(ref.ResponseID) != "" &&
		len(owner) <= 192 && len(parts) == 4 && parts[0] == "v1" && parts[1] != "" && parts[2] != "" && parts[3] != ""
}

var claimWSNodeOwnerScript = redis.NewScript(`
local current = redis.call('GET', KEYS[1])
if current ~= false and current ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[2])
return 1
`)

func (c *gatewayCache) ClaimWSNodeOwner(ctx context.Context, ref service.WSNodeOwnerRef, owner string, ttl time.Duration) (bool, error) {
	if c == nil || c.rdb == nil || !validWSNodeOwner(ref, owner) || ttl < time.Millisecond {
		return false, errWSNodeOwnerUnavailable
	}
	claimed, err := claimWSNodeOwnerScript.Run(ctx, c.rdb, []string{wsNodeOwnerKey(ref)}, owner, ttl.Milliseconds()).Int64()
	if err != nil {
		return false, errWSNodeOwnerUnavailable
	}
	return claimed == 1, nil
}

func (c *gatewayCache) MatchesWSNodeOwner(ctx context.Context, ref service.WSNodeOwnerRef, owner string) (bool, error) {
	if c == nil || c.rdb == nil || !validWSNodeOwner(ref, owner) {
		return false, errWSNodeOwnerUnavailable
	}
	current, err := c.rdb.Get(ctx, wsNodeOwnerKey(ref)).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, errWSNodeOwnerUnavailable
	}
	return current == owner, nil
}

var _ service.OpenAIWSNodeOwnerCache = (*gatewayCache)(nil)
