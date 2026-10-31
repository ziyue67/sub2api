// Package rediscompat implements atomic operations using Redis 6.0 commands.
package rediscompat

import (
	"context"

	"github.com/redis/go-redis/v9"
)

var getDelScript = redis.NewScript(`
local value = redis.call('GET', KEYS[1])
if value then
    redis.call('DEL', KEYS[1])
end
return value
`)

// GetDel takes a string exactly once, including an empty string. Missing/expired
// keys return redis.Nil. GET errors (e.g. WRONGTYPE) leave the key untouched.
func GetDel(ctx context.Context, rdb redis.Scripter, key string) ([]byte, error) {
	value, err := getDelScript.Run(ctx, rdb, []string{key}).Text()
	if err != nil {
		return nil, err
	}
	return []byte(value), nil
}
