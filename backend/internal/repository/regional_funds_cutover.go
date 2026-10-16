package repository

import (
	"context"
	"database/sql"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/redis/go-redis/v9"
	"strconv"
	"strings"
)

// Import uses max(existing, cached), never a balance reset. All legacy workers
// must already be stopped by the runtime deployment fence. The committed epoch
// prevents an old absolute cache snapshot from overwriting subsequent SQL usage.
func importRegionalFundsUsage(ctx context.Context, db *sql.DB, rdb *redis.Client, capacity *regionalConcurrencyCache) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	epoch := capacity.runtimeConfig.Epoch
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", "funds-import:"+epoch); err != nil {
		return err
	}
	var imported bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_funds_imports WHERE epoch=$1)", epoch).Scan(&imported); err != nil {
		return err
	}
	if imported {
		return nil
	}
	if capacity.primary == nil || capacity.primary.GrantGuard == nil {
		return errors.New("funds cutover authority missing")
	}
	if err = capacity.primary.GrantGuard(ctx, tx); err != nil {
		return err
	}
	for _, instance := range capacity.runtimeConfig.PrimaryInstances {
		var ready bool
		if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_funds_members WHERE epoch=$1 AND instance_id=$2 AND heartbeat_at>clock_timestamp()-interval '30 seconds')", epoch, instance).Scan(&ready); err != nil {
			return err
		}
		if !ready {
			return errors.New("funds primary membership incomplete")
		}
	}
	var cursor uint64
	for {
		keys, next, err := rdb.Scan(ctx, cursor, "billing:user_platform_quota:*", 100).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			parts := strings.Split(strings.TrimPrefix(key, "billing:user_platform_quota:"), ":")
			if len(parts) != 2 {
				return errors.New("invalid legacy quota key")
			}
			user, err := strconv.ParseInt(parts[0], 10, 64)
			if err != nil || user <= 0 {
				return errors.New("invalid legacy quota user")
			}
			values, err := rdb.HGetAll(ctx, key).Result()
			if err != nil {
				return err
			}
			for _, window := range []string{"daily", "weekly", "monthly"} {
				value := values[window+"_usage"]
				if value == "" {
					return errors.New("legacy quota evidence missing")
				}
				if _, err = ledger.ParseUSD(value); err != nil {
					return err
				}
				query := "UPDATE user_platform_quotas SET " + window + "_usage_usd=GREATEST(" + window + "_usage_usd,$3::numeric),updated_at=NOW() WHERE user_id=$1 AND platform=$2 AND deleted_at IS NULL"
				if _, err = tx.ExecContext(ctx, query, user, parts[1], value); err != nil {
					return err
				}
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	// Every dirty member must still have its cache evidence. Missing/expired
	// evidence cannot be converted to zero. Dirty keys are retained for audit.
	members, err := rdb.SMembers(ctx, userPlatformQuotaDirtySetKey()).Result()
	if err != nil {
		return err
	}
	for _, member := range members {
		key, ok := parseUserPlatformQuotaDirtyMember(member)
		if !ok {
			return errors.New("invalid dirty quota identity")
		}
		n, err := rdb.Exists(ctx, userPlatformQuotaCacheKey(key.UserID, key.Platform)).Result()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("unmigrated dirty quota evidence missing")
		}
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO regional_funds_imports(epoch) VALUES($1)", epoch); err != nil {
		return err
	}
	return tx.Commit()
}
