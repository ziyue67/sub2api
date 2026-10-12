package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/redis/go-redis/v9"
)

var _ basispoints.StateStore = (*gatewayCache)(nil)

const bpsStateTTL = 2 * time.Hour
const bpsStateEntryLimit = 1 << 20
const bpsStateByteLimit = 16 << 20
const bpsStateCountLimit = 1024
const bpsStatePrefix = "{sub2api:bps-state:v1}:"

func bpsStateKey(scope, kind string) string {
	return fmt.Sprintf("%s%x", bpsStatePrefix, sha256.Sum256([]byte(scope+"\x00"+kind)))
}

// Shared global LRU and byte accounting bound Redis usage across all sessions.
// Every touched key has the same cluster hash tag. Payloads and index expire;
// all scripts validate budgets and compare opaque revision strings atomically.
const bpsStateScriptPrelude = `
local key,lru,sizes=KEYS[1],KEYS[2],KEYS[3]
local clock=redis.call('TIME')
local now=tonumber(clock[1])*1000+math.floor(tonumber(clock[2])/1000)
local ttl=tonumber(ARGV[1])
local function remove(victim)
 local weight=tonumber(redis.call('HGET',sizes,victim) or '0')
 redis.call('HINCRBY',sizes,'__bytes',-weight)
 redis.call('HDEL',sizes,victim)
 redis.call('ZREM',lru,victim)
 redis.call('DEL',victim)
end
for _,victim in ipairs(redis.call('ZRANGEBYSCORE',lru,'-inf',now-ttl)) do remove(victim) end
local function touch()
 redis.call('ZADD',lru,now,key)
 redis.call('PEXPIRE',key,ttl)
 redis.call('PEXPIRE',lru,ttl)
 redis.call('PEXPIRE',sizes,ttl)
end
`

var bpsStateRead = redis.NewScript(bpsStateScriptPrelude + `
local raw=redis.call('HGET',key,'raw')
if not raw then
 if redis.call('ZSCORE',lru,key) then remove(key) end
 return {'','0'}
end
touch()
return {raw,redis.call('HGET',key,'revision') or '0'}
`)
var bpsStateWrite = redis.NewScript(bpsStateScriptPrelude + `
local expected,revision,raw=ARGV[2],ARGV[3],ARGV[4]
local current=redis.call('HGET',key,'revision') or '0'
if expected~='*' and current~=expected then return 0 end
local weight=string.len(raw)+string.len(key)+128
local old=tonumber(redis.call('HGET',sizes,key) or '0')
redis.call('HINCRBY',sizes,'__bytes',weight-old)
redis.call('HSET',sizes,key,weight)
redis.call('HSET',key,'raw',raw,'revision',revision)
touch()
while redis.call('ZCARD',lru)>tonumber(ARGV[5]) or tonumber(redis.call('HGET',sizes,'__bytes') or '0')>tonumber(ARGV[6]) do
 local victim=redis.call('ZRANGE',lru,0,0)[1]
 if not victim then break end
 remove(victim)
end
if redis.call('EXISTS',key)==0 then return -1 end
return 1
`)

func (c *gatewayCache) LoadBPSState(ctx context.Context, scope, kind string) ([]byte, uint64, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := bpsStateRead.Run(ctx, c.rdb, []string{bpsStateKey(scope, kind), bpsStatePrefix + "lru", bpsStatePrefix + "sizes"}, bpsStateTTL.Milliseconds()).StringSlice()
	if err != nil || len(result) != 2 {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	version, err := strconv.ParseUint(result[1], 10, 64)
	if err != nil {
		return nil, 0, basispoints.ErrStateUnavailable
	}
	if result[0] == "" {
		return nil, version, nil
	}
	return []byte(result[0]), version, nil
}
func (c *gatewayCache) SaveBPSState(ctx context.Context, scope, kind string, expected *uint64, raw []byte) (bool, error) {
	if len(raw) > bpsStateEntryLimit {
		return false, basispoints.ErrStateCapacity
	}
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return false, basispoints.ErrStateUnavailable
	}
	revision := binary.BigEndian.Uint64(nonce[:])
	if revision == 0 {
		revision = 1
	}
	match := "*"
	if expected != nil {
		match = strconv.FormatUint(*expected, 10)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := bpsStateWrite.Run(ctx, c.rdb, []string{bpsStateKey(scope, kind), bpsStatePrefix + "lru", bpsStatePrefix + "sizes"}, bpsStateTTL.Milliseconds(), match, strconv.FormatUint(revision, 10), raw, bpsStateCountLimit, bpsStateByteLimit).Int()
	if err != nil {
		return false, basispoints.ErrStateUnavailable
	}
	if result < 0 {
		return false, basispoints.ErrStateCapacity
	}
	return result == 1, nil
}
