package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
)

// Keep v1 in a separate key: old readers continue to consume the two integer
// keys, while new readers consume one versioned record. No raw response ID,
// request body, upstream token or node address is stored in a Redis key.
func httpResponseOwnerKeys(groupID int64, responseID string) []string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(responseID)))
	id := hex.EncodeToString(sum[:])
	return []string{
		buildSessionKey(groupID, "openai:http-response-owner:v1:"+id),
		buildSessionKey(groupID, "openai:http-response-owner:user:"+id),
		buildSessionKey(groupID, "openai:http-response-owner:key:"+id),
	}
}

var errHTTPResponseOwnerUnavailable = errors.New("HTTP response owner unavailable")

var setHTTPResponseOwnerScript = redis.NewScript(`
redis.call('SET', KEYS[1], ARGV[1], 'PX', ARGV[4])
redis.call('SET', KEYS[2], ARGV[2], 'PX', ARGV[4])
redis.call('SET', KEYS[3], ARGV[3], 'PX', ARGV[4])
return 1
`)

var getHTTPResponseOwnerScript = redis.NewScript(`
local record = redis.call('GET', KEYS[1])
local legacy = redis.call('MGET', KEYS[2], KEYS[3])
if record ~= false then
    if legacy[1] == false or legacy[2] == false or record ~= 'v1:' .. legacy[1] .. ':' .. legacy[2] then
        return {'inconsistent'}
    end
    return {record}
end
if legacy[1] == false or legacy[2] == false then return {} end
return {'legacy', legacy[1], legacy[2]}
`)

func (c *gatewayCache) SetHTTPResponseOwner(ctx context.Context, groupID int64, responseID string, userID, apiKeyID int64, ttl time.Duration) error {
	if c == nil || c.rdb == nil || groupID < 0 || strings.TrimSpace(responseID) == "" || userID <= 0 || apiKeyID <= 0 || ttl < time.Millisecond {
		return errHTTPResponseOwnerUnavailable
	}
	user, key := strconv.FormatInt(userID, 10), strconv.FormatInt(apiKeyID, 10)
	// One atomic publication also preserves the old reader's exact wire format.
	if err := setHTTPResponseOwnerScript.Run(ctx, c.rdb, httpResponseOwnerKeys(groupID, responseID), "v1:"+user+":"+key, user, key, ttl.Milliseconds()).Err(); err != nil {
		return errHTTPResponseOwnerUnavailable
	}
	return nil
}

func (c *gatewayCache) GetHTTPResponseOwner(ctx context.Context, groupID int64, responseID string) (int64, int64, bool, error) {
	if c == nil || c.rdb == nil || groupID < 0 || strings.TrimSpace(responseID) == "" {
		return 0, 0, false, errHTTPResponseOwnerUnavailable
	}
	values, err := getHTTPResponseOwnerScript.Run(ctx, c.rdb, httpResponseOwnerKeys(groupID, responseID)).StringSlice()
	if err != nil {
		// Do not leak Redis topology or fall back to stale process-local grants.
		return 0, 0, false, errHTTPResponseOwnerUnavailable
	}
	if len(values) == 0 {
		return 0, 0, false, nil
	}
	var parts []string
	if len(values) == 1 {
		parts = strings.Split(values[0], ":")
		if len(parts) != 3 || parts[0] != "v1" {
			return 0, 0, false, errHTTPResponseOwnerUnavailable
		}
	} else if len(values) == 3 && values[0] == "legacy" {
		parts = values
	} else {
		return 0, 0, false, errHTTPResponseOwnerUnavailable
	}
	user, userErr := strconv.ParseInt(parts[1], 10, 64)
	key, keyErr := strconv.ParseInt(parts[2], 10, 64)
	if userErr != nil || keyErr != nil || user <= 0 || key <= 0 {
		return 0, 0, false, errHTTPResponseOwnerUnavailable
	}
	return user, key, true, nil
}

var _ service.OpenAIHTTPResponseOwnerCache = (*gatewayCache)(nil)
