package repository

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/redis/go-redis/v9"
)

func imagePolicyKey(scope, kind string) string {
	return fmt.Sprintf("bps_image_policy:%x:%s", sha256.Sum256([]byte(scope)), kind)
}

var imagePolicyRead = redis.NewScript("local v=redis.call('GET',KEYS[1]); if v then redis.call('EXPIRE',KEYS[1],7200) end; return v or ''")
var imagePolicySave = redis.NewScript("local v=redis.call('GET',KEYS[1]) or ''; if v~=ARGV[1] then return 0 end; redis.call('SET',KEYS[1],ARGV[2],'EX',7200);return 1")
var imagePolicyWarn = redis.NewScript("local first=redis.call('SET',KEYS[1],'1','NX','EX',7200); if not first then redis.call('EXPIRE',KEYS[1],7200) end; return first and 1 or 0")

func (c *gatewayCache) LoadBPSImageProgress(ctx context.Context, scope string) (string, error) {
	return imagePolicyRead.Run(ctx, c.rdb, []string{imagePolicyKey(scope, "progress")}).Text()
}
func (c *gatewayCache) SaveBPSImageProgress(ctx context.Context, scope, previous, next string) error {
	saved, err := imagePolicySave.Run(ctx, c.rdb, []string{imagePolicyKey(scope, "progress")}, previous, next).Int()
	if err != nil {
		return err
	}
	if saved != 1 {
		return fmt.Errorf("image history changed concurrently")
	}
	return nil
}
func (c *gatewayCache) ClaimBPSImageWarning(ctx context.Context, scope string) (bool, error) {
	v, e := imagePolicyWarn.Run(ctx, c.rdb, []string{imagePolicyKey(scope, "warning")}).Int()
	return v == 1, e
}
func (c *gatewayCache) ResetBPSImageWarning(ctx context.Context, scope string) error {
	return c.rdb.Del(ctx, imagePolicyKey(scope, "warning")).Err()
}
