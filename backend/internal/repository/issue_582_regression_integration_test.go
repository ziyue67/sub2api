//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/redissession"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// These tests use the harness's real Redis container, never miniredis. DB 15
// isolates legacy snapshot keys, including keys constructed inside Lua scripts.
func issue582Redis(t *testing.T) *redis.Client {
	t.Helper()
	opts := *integrationRedis.Options()
	opts.DB = 15
	rdb := redis.NewClient(&opts)
	ctx := context.Background()
	require.NoError(t, rdb.FlushDB(ctx).Err())
	t.Cleanup(func() {
		_ = rdb.FlushDB(ctx).Err()
		_ = rdb.Close()
	})
	info, err := rdb.Info(ctx, "server").Result()
	require.NoError(t, err)
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			t.Log(strings.TrimSpace(line))
			if expected := os.Getenv("ISSUE_582_REDIS_VERSION"); expected != "" {
				require.True(t, strings.HasPrefix(line, "redis_version:"+expected+"."), line)
			}
		}
	}
	return rdb
}

func issue582Account(id int64, platform string) service.Account {
	return service.Account{ID: id, Name: "issue-582", Platform: platform,
		Type: service.AccountTypeAPIKey, Status: service.StatusActive, Schedulable: true,
		Concurrency: 2, Credentials: map[string]any{"model_mapping": map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}}}
}

// Seed an upgrade-era snapshot with ordinary commands, so the baseline can read
// its old candidates even though its SET GET writer is broken on Redis 6.0.
func issue582SeedSnapshot(t *testing.T, rdb *redis.Client, bucket service.SchedulerBucket, account service.Account) {
	t.Helper()
	ctx := context.Background()
	full, meta, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)
	id := strconv.FormatInt(account.ID, 10)
	require.NoError(t, rdb.Set(ctx, schedulerAccountKey(id), full, 0).Err())
	require.NoError(t, rdb.Set(ctx, schedulerAccountMetaKey(id), meta, 0).Err())
	require.NoError(t, rdb.ZAdd(ctx, schedulerSnapshotKey(bucket, "1"), redis.Z{Score: 1, Member: id}).Err())
	require.NoError(t, rdb.Set(ctx, schedulerBucketKey(schedulerActivePrefix, bucket), "1", 0).Err())
	require.NoError(t, rdb.Set(ctx, schedulerBucketKey(schedulerVersionPrefix, bucket), "1", 0).Err())
	require.NoError(t, rdb.Set(ctx, schedulerBucketKey(schedulerReadyPrefix, bucket), "1", 0).Err())
	require.NoError(t, rdb.SAdd(ctx, schedulerBucketSetKey, bucket.String()).Err())
}

func TestIssue582SnapshotMemberUpdate(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	cache := NewSchedulerCache(rdb)
	bucket := service.SchedulerBucket{GroupID: 582, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	old := issue582Account(58201, service.PlatformOpenAI)
	added := issue582Account(58202, service.PlatformOpenAI)
	issue582SeedSnapshot(t, rdb, bucket, old)
	before, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, before, 1)
	require.Equal(t, old.ID, before[0].ID)

	// An existing metadata key may have an obsolete TTL; replacement must clear
	// it, just as SET GET did. A hot last_used side key must survive the rebuild.
	metaKey := schedulerAccountMetaKey(strconv.FormatInt(old.ID, 10))
	require.NoError(t, rdb.Expire(ctx, metaKey, time.Minute).Err())
	lastUsed := time.Now().UTC().Truncate(time.Millisecond)
	require.NoError(t, cache.UpdateLastUsed(ctx, map[int64]time.Time{old.ID: lastUsed}))
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	require.NoError(t, cache.SetSnapshot(ctx, bucket, token, []service.Account{old, added}), "member update must publish on the documented Redis minimum")
	after, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, after, 2)
	require.Equal(t, added.ID, after[1].ID)
	require.Equal(t, lastUsed, *after[0].LastUsedAt)
	require.Equal(t, -time.Nanosecond, rdb.PTTL(ctx, metaKey).Val())
	ttl, err := rdb.TTL(ctx, schedulerSnapshotKey(bucket, "1")).Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, time.Duration(snapshotGraceTTLSeconds)*time.Second)

	// Cross-platform replacement must invalidate an already warm old bucket.
	old.Platform = service.PlatformGemini
	require.NoError(t, cache.SetAccount(ctx, &old))
	require.NotEmpty(t, rdb.Get(ctx, schedulerAccountMetaRevisionKey).Val())
	after, hit, err = cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, service.PlatformGemini, after[0].Platform)
}

func TestIssue582SnapshotWriteFailureKeepsActiveAndInvalidates(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	cache := NewSchedulerCache(rdb)
	bucket := service.SchedulerBucket{GroupID: 583, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	old := issue582Account(58301, service.PlatformOpenAI)
	issue582SeedSnapshot(t, rdb, bucket, old)
	bad := issue582Account(58303, service.PlatformOpenAI)
	badKey := schedulerAccountMetaKey(strconv.FormatInt(bad.ID, 10))
	require.NoError(t, rdb.HSet(ctx, badKey, "corrupt", "metadata").Err())
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	// The first metadata write returns nil (missing key); it must not hide the
	// following WRONGTYPE or allow a partially written version to be activated.
	err = cache.SetSnapshot(ctx, bucket, token, []service.Account{issue582Account(58302, service.PlatformOpenAI), bad})
	require.ErrorContains(t, err, "WRONGTYPE")
	require.Equal(t, "1", rdb.Get(ctx, schedulerBucketKey(schedulerActivePrefix, bucket)).Val())
	require.Equal(t, "hash", rdb.Type(ctx, badKey).Val())
	require.Eventually(t, func() bool { return rdb.Get(ctx, schedulerAccountMetaRevisionKey).Val() != "" }, 5*time.Second, 10*time.Millisecond)
}

func TestIssue582OutboxMakesNewMemberSchedulable(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	client := testEntClient(t)
	require.NoError(t, func() error { _, err := integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox"); return err }())
	accounts := newAccountRepositoryWithSQL(client, integrationDB, nil)
	groups := newGroupRepositoryWithSQL(client, integrationDB)
	group := &service.Group{Name: "issue-582", Platform: service.PlatformOpenAI, Status: service.StatusActive, RateMultiplier: 1, SubscriptionType: service.SubscriptionTypeStandard}
	require.NoError(t, groups.Create(ctx, group))
	old := issue582Account(0, service.PlatformOpenAI)
	added := issue582Account(0, service.PlatformOpenAI)
	require.NoError(t, accounts.Create(ctx, &old))
	require.NoError(t, accounts.Create(ctx, &added))
	require.NoError(t, accounts.AddToGroup(ctx, old.ID, group.ID, 1))
	cache := NewSchedulerCache(rdb)
	bucket := service.SchedulerBucket{GroupID: group.ID, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	issue582SeedSnapshot(t, rdb, bucket, old)
	cfg := &config.Config{RunMode: config.RunModeStandard, Gateway: config.GatewayConfig{Scheduling: config.GatewaySchedulingConfig{OutboxPollIntervalSeconds: 1, DbFallbackEnabled: false}}}
	svc := service.NewSchedulerSnapshotService(cache, NewSchedulerOutboxRepository(integrationDB), accounts, groups, cfg)
	var initialID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COALESCE(MAX(id),0) FROM scheduler_outbox").Scan(&initialID))
	svc.Start()
	t.Cleanup(svc.Stop)
	require.Eventually(t, func() bool {
		wm, err := cache.GetOutboxWatermark(ctx)
		return err == nil && wm >= initialID && rdb.Get(ctx, schedulerBucketKey(schedulerActivePrefix, bucket)).Val() != "1"
	}, 30*time.Second, 100*time.Millisecond, "initial old snapshot must remain writable and outbox must drain")
	before, _, err := svc.ListSchedulableAccounts(ctx, &group.ID, service.PlatformOpenAI, false)
	require.NoError(t, err)
	require.Len(t, before, 1)
	require.Equal(t, old.ID, before[0].ID)

	require.NoError(t, accounts.AddToGroup(ctx, added.ID, group.ID, 1))
	var eventID int64
	// The worker may already have consumed and deleted this event. The isolated
	// database sequence still proves the membership write enqueued a newer ID.
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT last_value FROM scheduler_outbox_id_seq").Scan(&eventID))
	require.Greater(t, eventID, initialID)
	require.Eventually(t, func() bool {
		wm, err := cache.GetOutboxWatermark(ctx)
		if err != nil || wm < eventID {
			return false
		}
		candidates, _, err := svc.ListSchedulableAccounts(ctx, &group.ID, service.PlatformOpenAI, false)
		if err != nil {
			return false
		}
		for _, candidate := range candidates {
			if candidate.ID == added.ID && candidate.Status == service.StatusActive && candidate.Schedulable && candidate.GetModelMapping()["gpt-6.1-sol"] == "gpt-6.1-sol" {
				return true
			}
		}
		return false
	}, 30*time.Second, 100*time.Millisecond, "consumed membership event must expose the new candidate without DB fallback")
	full, err := cache.GetAccount(ctx, added.ID)
	require.NoError(t, err)
	require.Contains(t, full.GroupIDs, group.ID)
}

func TestIssue582SingleUseSessions(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	store := redissession.New(rdb, "issue582:siwc", time.Minute)
	require.NoError(t, store.Set(ctx, "one", map[string]string{"session": "ciphertext-placeholder"}))
	var wg sync.WaitGroup
	results := make(chan bool, 16)
	errors := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var payload map[string]string
			ok, err := store.Take(ctx, "one", &payload)
			results <- ok
			errors <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	winners := 0
	for ok := range results {
		if ok {
			winners++
		}
	}
	for err := range errors {
		require.NoError(t, err)
	}
	require.Equal(t, 1, winners, "only one replica may consume the session")
	require.NoError(t, rdb.Set(ctx, "issue582:siwc:expired", "{}", time.Millisecond).Err())
	require.Eventually(t, func() bool { return rdb.Exists(ctx, "issue582:siwc:expired").Val() == 0 }, time.Second, time.Millisecond)
	var dest map[string]string
	ok, err := store.Take(ctx, "expired", &dest)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, rdb.HSet(ctx, "issue582:siwc:wrong-type", "field", "value").Err())
	_, err = store.Take(ctx, "wrong-type", &dest)
	require.ErrorContains(t, err, "WRONGTYPE")
	require.Equal(t, "hash", rdb.Type(ctx, "issue582:siwc:wrong-type").Val())
	require.NoError(t, rdb.Set(ctx, "issue582:siwc:invalid", "not-json", 0).Err())
	_, err = store.Take(ctx, "invalid", &dest)
	require.Error(t, err)
	require.Zero(t, rdb.Exists(ctx, "issue582:siwc:invalid").Val())

}

func TestIssue582FixedWindow(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	key := apiKeyCreateCountKey(582)
	counter := NewAPIKeyCache(rdb)
	count, err := counter.IncrementCreateCount(ctx, 582, time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	require.NoError(t, rdb.PExpire(ctx, key, 10*time.Second).Err())
	count, err = counter.IncrementCreateCount(ctx, 582, time.Minute)
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	require.LessOrEqual(t, rdb.PTTL(ctx, key).Val(), 10*time.Second)
	require.NoError(t, rdb.Persist(ctx, key).Err())
	_, err = counter.IncrementCreateCount(ctx, 582, time.Minute)
	require.NoError(t, err)
	require.Positive(t, rdb.PTTL(ctx, key).Val(), "repair a legacy counter with no TTL")

}

func TestIssue582PasskeyTake(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	// Passkey sessions share the same atomic take guarantee as SIWC.
	passkeys := NewPasskeySessionStore(rdb)
	raw, err := json.Marshal(service.PasskeySession{})
	require.NoError(t, err)
	require.NoError(t, rdb.Set(ctx, passkeySessionPrefix+"test", raw, time.Minute).Err())
	_, err = passkeys.Consume(ctx, "test")
	require.NoError(t, err)
	_, err = passkeys.Consume(ctx, "test")
	require.ErrorIs(t, err, service.ErrPasskeySession)
}

func TestIssue582ConcurrentSnapshotPublication(t *testing.T) {
	ctx := context.Background()
	rdb := issue582Redis(t)
	cache := NewSchedulerCache(rdb)
	bucket := service.SchedulerBucket{GroupID: 584, Platform: service.PlatformOpenAI, Mode: service.SchedulerModeSingle}
	token, err := cache.CaptureBucketWriteToken(ctx, bucket)
	require.NoError(t, err)
	var wg sync.WaitGroup
	failures := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures <- cache.SetSnapshot(ctx, bucket, token, []service.Account{issue582Account(int64(58400+i), service.PlatformOpenAI)})
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	active, err := rdb.Get(ctx, schedulerBucketKey(schedulerActivePrefix, bucket)).Result()
	require.NoError(t, err)
	require.Equal(t, "16", active, "a slower older publisher must not replace the newest version")
	candidates, hit, err := cache.GetSnapshot(ctx, bucket)
	require.NoError(t, err)
	require.True(t, hit)
	require.Len(t, candidates, 1)
	members, err := rdb.ZRange(ctx, schedulerSnapshotKey(bucket, active), 0, -1).Result()
	require.NoError(t, err)
	require.Equal(t, []string{strconv.FormatInt(candidates[0].ID, 10)}, members)
}
