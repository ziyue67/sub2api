//go:build integration

package authoutbox_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/timezone"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

type countedKeyRepository struct {
	service.APIKeyRepository
	reads atomic.Int64
}

func (r *countedKeyRepository) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	r.reads.Add(1)
	return r.APIKeyRepository.GetByKeyForAuth(ctx, key)
}

type countedAuthCache struct {
	service.APIKeyCache
	reads atomic.Int64
}

func (c *countedAuthCache) GetAuthCache(ctx context.Context, key string) (*service.APIKeyAuthCacheEntry, error) {
	c.reads.Add(1)
	return c.APIKeyCache.GetAuthCache(ctx, key)
}

type observedWorkerCache struct {
	service.APIKeyCache
	target        string
	deleted       atomic.Bool
	published     atomic.Bool
	denied        atomic.Bool
	publishDenied atomic.Bool
}

func (c *observedWorkerCache) DeleteAuthCache(ctx context.Context, key string) error {
	err := c.APIKeyCache.DeleteAuthCache(ctx, key)
	if err != nil && key == c.target && strings.Contains(err.Error(), "NOPERM") {
		c.denied.Store(true)
	}
	if err == nil && key == c.target {
		_, readErr := c.APIKeyCache.GetAuthCache(ctx, key)
		c.deleted.Store(errors.Is(readErr, redis.Nil))
	}
	return err
}

func (c *observedWorkerCache) PublishAuthCacheInvalidation(ctx context.Context, key string) error {
	err := c.APIKeyCache.PublishAuthCacheInvalidation(ctx, key)
	if err != nil && key == c.target && strings.Contains(err.Error(), "NOPERM") {
		c.publishDenied.Store(true)
	}
	if err == nil && key == c.target {
		c.published.Store(true)
	}
	return err
}

func TestDatabaseOutboxFirstPassRevokesTwoHotHTTPReaders(t *testing.T) {
	testDatabaseOutboxHTTPRevocation(t, outboxHTTPScenario{})
}

func TestDatabaseOutboxSafetyPassClearsLateSnapshotFromTwoHotHTTPReaders(t *testing.T) {
	testDatabaseOutboxHTTPRevocation(t, outboxHTTPScenario{safetyPass: true})
}

func TestDatabaseOutboxRealRedisDeleteFailureRecoversThroughSafetyPass(t *testing.T) {
	testDatabaseOutboxHTTPRevocation(t, outboxHTTPScenario{safetyPass: true, rejectDelete: true})
}

func TestDatabaseOutboxRealRedisPublishFailureAfterDeleteRecoversThroughSafetyPass(t *testing.T) {
	testDatabaseOutboxHTTPRevocation(t, outboxHTTPScenario{safetyPass: true, rejectPublish: true})
}

type outboxHTTPScenario struct {
	safetyPass    bool
	rejectDelete  bool
	rejectPublish bool
	crashClaim    bool
}

func testDatabaseOutboxHTTPRevocation(t *testing.T, scenario outboxHTTPScenario) {
	t.Helper()
	require.False(t, scenario.rejectDelete && scenario.rejectPublish, "inject exactly one Redis command failure per scenario")
	require.False(t, scenario.crashClaim && (scenario.rejectDelete || scenario.rejectPublish), "keep claim crash and Redis command failure checkpoints separate")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// A failed one-shot Docker query cannot identify a transient daemon issue.
	// Retry only this read-only query within fixed deadlines, recording safe
	// failure classes without daemon output, environment or endpoint details.
	checkCtx, stopCheck := context.WithTimeout(ctx, 30*time.Second)
	checkStarted := time.Now()
	attempts, timedOut, nonzero := 0, 0, 0
	lastExitCode := 0
	missingCommand := false
	dockerReady := func() bool {
		for {
			attempts++
			attemptCtx, stopAttempt := context.WithTimeout(checkCtx, 3*time.Second)
			err := exec.CommandContext(attemptCtx, "docker", "info").Run()
			deadlineExceeded := errors.Is(attemptCtx.Err(), context.DeadlineExceeded)
			stopAttempt()
			if err == nil {
				return true
			}
			if errors.Is(err, exec.ErrNotFound) {
				missingCommand = true
				return false
			}
			if deadlineExceeded {
				timedOut++
			} else {
				nonzero++
				lastExitCode = -1
				var exitError *exec.ExitError
				if errors.As(err, &exitError) {
					lastExitCode = exitError.ExitCode()
				}
			}
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-checkCtx.Done():
				timer.Stop()
				return false
			case <-timer.C:
			}
		}
	}()
	stopCheck()
	if !dockerReady || attempts > 1 {
		t.Logf("isolated Docker readiness: ready=%t attempts=%d timed_out=%d nonzero=%d last_exit=%d missing_command=%t elapsed=%s", dockerReady, attempts, timedOut, nonzero, lastExitCode, missingCommand, time.Since(checkStarted).Round(time.Millisecond))
	}
	if !dockerReady {
		if os.Getenv("CI") != "" {
			t.Fatal("Docker is required for the isolated auth outbox fixture in CI")
		}
		t.Skip("Docker is required for the isolated auth outbox fixture")
	}
	require.NoError(t, timezone.Init("UTC"))
	// The worker claims the entire outbox table. Give it an exclusive database
	// instead of allowing it to consume another integration test's events.
	pg, err := tcpostgres.Run(ctx, "postgres:18.1-alpine3.23",
		tcpostgres.WithDatabase("auth_outbox_fixture"), tcpostgres.WithUsername("postgres"),
		tcpostgres.WithPassword("postgres"), tcpostgres.BasicWaitStrategies())
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		require.NoError(t, pg.Terminate(cleanupCtx))
	})
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable", "TimeZone=UTC")
	require.NoError(t, err)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, db.PingContext(ctx))
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	entClient := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))

	redisContainer, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 20*time.Second)
		defer stop()
		require.NoError(t, redisContainer.Terminate(cleanupCtx))
	})
	host, err := redisContainer.Host(ctx)
	require.NoError(t, err)
	port, err := redisContainer.MappedPort(ctx, "6379/tcp")
	require.NoError(t, err)
	newClient := func() *redis.Client {
		t.Helper()
		client := redis.NewClient(&redis.Options{
			Addr: fmt.Sprintf("%s:%d", host, port.Int()), PoolSize: 1,
			ContextTimeoutEnabled: true, DialTimeout: time.Second,
			ReadTimeout: time.Second, WriteTimeout: time.Second,
		})
		t.Cleanup(func() { require.NoError(t, client.Close()) })
		return client
	}
	controller := newClient()
	require.NoError(t, controller.Ping(ctx).Err())
	user, err := entClient.User.Create().SetEmail("auth-outbox@example.invalid").
		SetPasswordHash("fixture-only-not-a-login-password").SetRole(service.RoleUser).
		SetStatus(service.StatusActive).SetBalance(10).SetConcurrency(5).Save(ctx)
	require.NoError(t, err)
	keyRepo := repository.NewAPIKeyRepository(entClient, db)
	target := &service.APIKey{UserID: user.ID, Key: "fixture-outbox-target", Name: "target", Status: service.StatusActive}
	control := &service.APIKey{UserID: user.ID, Key: "fixture-outbox-control", Name: "control", Status: service.StatusActive}
	require.NoError(t, keyRepo.Create(ctx, target))
	require.NoError(t, keyRepo.Create(ctx, control))
	// Only this test owns this database. Discard fixture-creation events before
	// exercising the one security UPDATE, not a global/shared test outbox.
	_, err = db.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox")
	require.NoError(t, err)
	digest := sha256.Sum256([]byte(target.Key))
	cacheKey := hex.EncodeToString(digest[:])
	sharedCache := repository.NewAPIKeyCache(controller)
	cfg := &config.Config{RunMode: config.RunModeStandard, APIKeyAuth: config.APIKeyAuthCacheConfig{
		L1Size: 1024, L1TTLSeconds: 300, L2TTLSeconds: 300, JitterPercent: 0,
	}}
	gin.SetMode(gin.TestMode)
	var nodes [2]*service.APIKeyService
	var repositories [2]*countedKeyRepository
	var caches [2]*countedAuthCache
	var routes [2]*gin.Engine
	var admitted [2]atomic.Int64
	var connectionIDs [2]int64
	startReaders := func() {
		t.Helper()
		for node := range nodes {
			client := newClient()
			connectionIDs[node], err = client.ClientID(ctx).Result()
			require.NoError(t, err)
			repositories[node] = &countedKeyRepository{APIKeyRepository: keyRepo}
			caches[node] = &countedAuthCache{APIKeyCache: repository.NewAPIKeyCache(client)}
			nodes[node] = service.NewAPIKeyService(repositories[node], nil, nil, nil, nil, caches[node], cfg)
			// Bounded L1 workers live only in this dedicated test binary; no new
			// production destruction/test hook is needed for the fixture.
			t.Cleanup(nodes[node].StopAuthCacheInvalidationSubscriber)
			nodes[node].StartAuthCacheInvalidationSubscriber(ctx)
			routes[node] = gin.New()
			routes[node].Use(gin.HandlerFunc(middleware.NewAPIKeyAuthMiddleware(nodes[node], nil, cfg)))
			routes[node].GET("/v1/sub2api/billing", func(c *gin.Context) {
				admitted[node].Add(1)
				c.Status(http.StatusNoContent)
			})
		}
		require.NotEqual(t, connectionIDs[0], connectionIDs[1])
		require.Eventually(t, func() bool {
			counts, err := controller.PubSubNumSub(ctx, "auth:cache:invalidate").Result()
			return err == nil && counts["auth:cache:invalidate"] == 2 &&
				nodes[0].AuthCacheInvalidationSubscriberHealth().Connected && nodes[1].AuthCacheInvalidationSubscriberHealth().Connected
		}, 5*time.Second, 10*time.Millisecond)
	}
	startReaders()
	probe := func(node int, key string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/v1/sub2api/billing", nil).WithContext(ctx)
		request.Header.Set("Authorization", "Bearer "+key)
		response := httptest.NewRecorder()
		routes[node].ServeHTTP(response, request)
		return response
	}
	assertHot := func(node int, key string, status int) {
		t.Helper()
		require.Eventually(t, func() bool {
			dbReads, l2Reads := repositories[node].reads.Load(), caches[node].reads.Load()
			return probe(node, key).Code == status && repositories[node].reads.Load() == dbReads && caches[node].reads.Load() == l2Reads
		}, time.Second, 10*time.Millisecond, "HTTP lookup must hit L1, not DB or shared L2")
	}
	for node := range nodes {
		assertHot(node, target.Key, http.StatusNoContent)
		assertHot(node, control.Key, http.StatusNoContent)
	}
	entry, err := sharedCache.GetAuthCache(ctx, cacheKey)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.NotNil(t, entry.Snapshot)
	require.Equal(t, service.StatusActive, entry.Snapshot.Status)
	lateSnapshot := entry // Serialized active state captured before the DB mutation.
	beforeDB := repositories[0].reads.Load() + repositories[1].reads.Load()
	_, err = db.ExecContext(ctx, "UPDATE api_keys SET status = 'disabled' WHERE id = $1", target.ID)
	require.NoError(t, err)
	var stage int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT delivery_stage FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).Scan(&stage))
	require.Zero(t, stage, "the real security trigger must enqueue a first-pass event")
	for node := range nodes {
		dbReads, l2Reads := repositories[node].reads.Load(), caches[node].reads.Load()
		require.Equal(t, http.StatusNoContent, probe(node, target.Key).Code, "DB mutation alone must not fake cache invalidation")
		require.Equal(t, dbReads, repositories[node].reads.Load())
		require.Equal(t, l2Reads, caches[node].reads.Load())
	}
	entry, err = sharedCache.GetAuthCache(ctx, cacheKey)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.NotNil(t, entry.Snapshot)
	require.Equal(t, service.StatusActive, entry.Snapshot.Status, "shared L2 is also stale before the worker starts")
	const abandonedOwner = "fixture-crashed-owner"
	var abandonedEventID int64
	var abandonedUntil time.Time
	if scenario.crashClaim {
		child := startOutboxClaimProcess(t, dsn, abandonedOwner)
		var owner string
		var claimedAt time.Time
		var attempts int
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT id, claimed_by, claimed_at, attempts FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).
			Scan(&abandonedEventID, &owner, &claimedAt, &attempts))
		require.Equal(t, abandonedEventID, child.reply.ID)
		require.Zero(t, child.reply.Stage)
		require.Equal(t, abandonedOwner, owner)
		require.Zero(t, attempts)
		abandonedUntil = claimedAt.Add(30 * time.Second)
		early, err := repository.NewAuthCacheInvalidationOutboxRepository(db).Claim(ctx, "fixture-too-early", 1, 30*time.Second)
		require.NoError(t, err)
		require.Empty(t, early, "a committed live lease must exclude a second real claimant")
		child.crash(t) // Actual kill+wait; no graceful claim release or timestamp edit.
		var sameClaim time.Time
		var leaseStillLive bool
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT claimed_by, claimed_at, NOW() < $2 FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey, abandonedUntil).
			Scan(&owner, &sameClaim, &leaseStillLive))
		require.Equal(t, abandonedOwner, owner)
		require.Equal(t, claimedAt, sameClaim)
		require.True(t, leaseStillLive, "the fixture must crash before the unchanged lease expires")
		early, err = repository.NewAuthCacheInvalidationOutboxRepository(db).Claim(ctx, "fixture-after-crash", 1, 30*time.Second)
		require.NoError(t, err)
		require.Empty(t, early, "process death must not bypass the original committed lease")
		for node := range nodes {
			dbReads, l2Reads := repositories[node].reads.Load(), caches[node].reads.Load()
			require.Equal(t, http.StatusNoContent, probe(node, target.Key).Code)
			require.Equal(t, http.StatusNoContent, probe(node, control.Key).Code)
			require.Equal(t, dbReads, repositories[node].reads.Load())
			require.Equal(t, l2Reads, caches[node].reads.Load())
		}
	}
	workerCache := sharedCache
	const (
		workerUser     = "auth-fixture-worker"
		workerPassword = "fixture-only-del-rejection" // Dedicated temporary container, never a deployment credential.
	)
	rejectCommand := scenario.rejectDelete || scenario.rejectPublish
	deniedCommand := "del"
	if scenario.rejectPublish {
		deniedCommand = "publish"
	}
	if rejectCommand {
		// Only this test owns the Redis container. Deny one real command for
		// the worker alone; default/controller/readers keep their own access.
		require.NoError(t, controller.ACLSetUser(ctx, workerUser, "reset", "on", ">"+workerPassword, "~*", "&*", "+@all", "-"+deniedCommand).Err())
		t.Cleanup(func() {
			cleanupCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			require.NoError(t, controller.ACLDelUser(cleanupCtx, workerUser).Err())
		})
		opts := *controller.Options()
		opts.Username = workerUser
		opts.Password = workerPassword
		workerClient := redis.NewClient(&opts)
		t.Cleanup(func() { require.NoError(t, workerClient.Close()) })
		require.NoError(t, workerClient.Ping(ctx).Err(), "the fixture identity is healthy; only the selected command is denied")
		identity, err := workerClient.ACLWhoAmI(ctx).Result()
		require.NoError(t, err)
		require.Equal(t, workerUser, identity, "the real command must run as the denied fixture identity")
		workerCache = repository.NewAPIKeyCache(workerClient)
	}
	observed := &observedWorkerCache{APIKeyCache: workerCache, target: cacheKey}
	worker := service.NewAuthCacheInvalidationWorker(repository.NewAuthCacheInvalidationOutboxRepository(db), observed)
	t.Cleanup(worker.Stop)
	worker.Start() // No local-service shortcut: both readers require Pub/Sub.
	var retryAt time.Time
	var retryAttempts int
	firstPassBudget := 5 * time.Second
	if scenario.crashClaim {
		firstPassBudget = 45 * time.Second // The worker retains its original 30-second claim lease.
	}
	if rejectCommand {
		require.Eventually(t, func() bool {
			denied := observed.denied.Load()
			if scenario.rejectPublish {
				denied = observed.publishDenied.Load()
			}
			return worker.Health(ctx).Failures > 0 && denied
		}, 5*time.Second, 10*time.Millisecond)
		worker.Stop() // Join RetryClaimed before inspecting the real lease state.
		require.Zero(t, worker.Health(ctx).Processed)
		require.Equal(t, scenario.rejectPublish, observed.deleted.Load(), "PUBLISH failure must follow a real successful DEL; DEL failure must not delete")
		require.False(t, observed.published.Load(), "the rejected command must not produce a successful invalidation broadcast")
		var claimedAt sql.NullTime
		var claimedBy, lastError sql.NullString
		var createdAt time.Time
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT delivery_stage, attempts, available_at, claimed_at, claimed_by, last_error, created_at FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).
			Scan(&stage, &retryAttempts, &retryAt, &claimedAt, &claimedBy, &lastError, &createdAt))
		require.Zero(t, stage, "failed first pass must not become a safety pass or disappear")
		require.Positive(t, retryAttempts)
		require.False(t, claimedAt.Valid)
		require.False(t, claimedBy.Valid)
		require.Contains(t, lastError.String, "NOPERM")
		require.True(t, retryAt.After(createdAt), "the real RetryClaimed must schedule backoff")
		entry, err = sharedCache.GetAuthCache(ctx, cacheKey)
		if scenario.rejectPublish {
			require.ErrorIs(t, err, redis.Nil, "DEL already removed L2 even though the invalidation broadcast failed")
			require.Nil(t, entry)
			require.False(t, observed.denied.Load(), "the injected failure must be PUBLISH, not DEL")
		} else {
			require.NoError(t, err)
			require.NotNil(t, entry)
			require.NotNil(t, entry.Snapshot)
			require.Equal(t, service.StatusActive, entry.Snapshot.Status)
		}
		for node := range nodes {
			dbReads, l2Reads := repositories[node].reads.Load(), caches[node].reads.Load()
			require.Equal(t, http.StatusNoContent, probe(node, target.Key).Code)
			require.Equal(t, http.StatusNoContent, probe(node, control.Key).Code)
			require.Equal(t, dbReads, repositories[node].reads.Load())
			require.Equal(t, l2Reads, caches[node].reads.Load())
		}
		// Restore the exact denied capability, not an artificial cache result or
		// an outbox timestamp. A new worker must reclaim using the real retryAt.
		require.NoError(t, controller.ACLSetUser(ctx, workerUser, "+"+deniedCommand).Err())
		observed = &observedWorkerCache{APIKeyCache: workerCache, target: cacheKey}
		worker = service.NewAuthCacheInvalidationWorker(repository.NewAuthCacheInvalidationOutboxRepository(db), observed)
		t.Cleanup(worker.Stop)
		worker.Start()
		firstPassBudget = 15 * time.Second
	}
	require.Eventually(t, func() bool {
		health := worker.Health(ctx)
		return health.Processed == 1 && health.Failures == 0 && observed.deleted.Load() && observed.published.Load()
	}, firstPassBudget, 10*time.Millisecond)
	worker.Stop()
	var future bool
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT delivery_stage, available_at > NOW() FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).Scan(&stage, &future))
	require.Equal(t, 1, stage)
	require.True(t, future, "keep the existing delayed second pass; do not accelerate or claim final ack")
	if scenario.crashClaim {
		var recoveredID int64
		var attempts int
		var owner sql.NullString
		var leaseExpired bool
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT id, attempts, claimed_by, NOW() >= $2 FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey, abandonedUntil).
			Scan(&recoveredID, &attempts, &owner, &leaseExpired))
		require.Equal(t, abandonedEventID, recoveredID, "take over the existing event instead of replacing it")
		require.Zero(t, attempts, "lease expiry is not an explicit Redis failure retry")
		require.False(t, owner.Valid)
		require.True(t, leaseExpired, "the new worker cannot complete before the original lease expires")
		require.ErrorContains(t, repository.NewAuthCacheInvalidationOutboxRepository(db).DeleteClaimed(ctx, abandonedEventID, abandonedOwner), "no longer owned")
	}
	if rejectCommand {
		var attempts int
		var lastError sql.NullString
		var retryDue bool
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT attempts, last_error, NOW() >= $2 FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey, retryAt).Scan(&attempts, &lastError, &retryDue))
		require.Equal(t, retryAttempts, attempts, "recovery must retain the real failure attempt history")
		require.False(t, lastError.Valid, "successful first pass must clear the failure before its safety pass")
		require.True(t, retryDue, "recovery must not complete before the original retry deadline")
	}
	for node := range nodes {
		require.Eventually(t, func() bool {
			before := admitted[node].Load()
			response := probe(node, target.Key)
			return response.Code == http.StatusUnauthorized && strings.Contains(response.Body.String(), "API_KEY_DISABLED") && admitted[node].Load() == before
		}, 5*time.Second, 10*time.Millisecond)
		assertHot(node, target.Key, http.StatusUnauthorized)
		// The control key must remain in L1 after target-specific invalidation.
		dbReads, l2Reads := repositories[node].reads.Load(), caches[node].reads.Load()
		require.Equal(t, http.StatusNoContent, probe(node, control.Key).Code)
		require.Equal(t, dbReads, repositories[node].reads.Load())
		require.Equal(t, l2Reads, caches[node].reads.Load())
	}
	require.Greater(t, repositories[0].reads.Load()+repositories[1].reads.Load(), beforeDB, "revocation must force a fresh DB lookup after deleting L2")
	entry, err = sharedCache.GetAuthCache(ctx, cacheKey)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.NotNil(t, entry.Snapshot)
	require.Equal(t, service.StatusAPIKeyDisabled, entry.Snapshot.Status)
	if !scenario.safetyPass {
		return
	}

	var scheduledAt time.Time
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT available_at FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).Scan(&scheduledAt))
	// Explicit fault injection: model an old query completing after the first
	// pass. This is not a real concurrent SQL race or a new production policy.
	require.Equal(t, service.StatusActive, lateSnapshot.Snapshot.Status)
	require.NoError(t, sharedCache.SetAuthCache(ctx, cacheKey, lateSnapshot, 300*time.Second))
	oldConnectionIDs := connectionIDs
	for _, node := range nodes {
		node.StopAuthCacheInvalidationSubscriber()
	}
	startReaders() // Fresh processes are not implied: these are new service instances.
	for _, id := range connectionIDs {
		for _, oldID := range oldConnectionIDs {
			require.NotEqual(t, oldID, id)
		}
	}
	var l2ReadsBeforeSecond [2]int64
	for node := range nodes {
		assertHot(node, target.Key, http.StatusNoContent)
		assertHot(node, control.Key, http.StatusNoContent)
		require.Zero(t, repositories[node].reads.Load(), "both fresh readers must load the injected snapshot from L2, not the disabled DB row")
		require.Positive(t, caches[node].reads.Load())
		l2ReadsBeforeSecond[node] = caches[node].reads.Load()
	}
	var unchangedSchedule time.Time
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT available_at, available_at > NOW() FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey).Scan(&unchangedSchedule, &future))
	require.Equal(t, scheduledAt, unchangedSchedule, "do not change available_at or accelerate the real second pass")
	require.True(t, future, "both stale readers must be hot before the real second-pass deadline")
	secondObservation := &observedWorkerCache{APIKeyCache: workerCache, target: cacheKey}
	secondWorker := service.NewAuthCacheInvalidationWorker(repository.NewAuthCacheInvalidationOutboxRepository(db), secondObservation)
	t.Cleanup(secondWorker.Stop)
	secondWorker.Start()
	// Wait for the actual database deadline and unchanged 30-second worker
	// schedule. No fake clock, manual event claim or available_at UPDATE.
	require.Eventually(t, func() bool {
		health := secondWorker.Health(ctx)
		return health.Processed == 1 && health.Failures == 0 && secondObservation.deleted.Load() && secondObservation.published.Load()
	}, 45*time.Second, 100*time.Millisecond)
	secondWorker.Stop()
	var eventCount int
	var deadlineReached bool
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT (SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key = $1), NOW() >= $2", cacheKey, scheduledAt).Scan(&eventCount, &deadlineReached))
	require.Zero(t, eventCount, "the real stage-1 event must be acknowledged and removed")
	require.True(t, deadlineReached, "a completed second pass must not precede the original database deadline")
	for node := range nodes {
		require.Eventually(t, func() bool {
			before := admitted[node].Load()
			response := probe(node, target.Key)
			return response.Code == http.StatusUnauthorized && strings.Contains(response.Body.String(), "API_KEY_DISABLED") && admitted[node].Load() == before
		}, 5*time.Second, 10*time.Millisecond)
		require.Greater(t, caches[node].reads.Load(), l2ReadsBeforeSecond[node], "each hot reader must consume invalidation and leave its old L1")
		assertHot(node, target.Key, http.StatusUnauthorized)
		dbReads, l2Reads := repositories[node].reads.Load(), caches[node].reads.Load()
		require.Equal(t, http.StatusNoContent, probe(node, control.Key).Code)
		require.Equal(t, dbReads, repositories[node].reads.Load())
		require.Equal(t, l2Reads, caches[node].reads.Load())
	}
	require.Positive(t, repositories[0].reads.Load()+repositories[1].reads.Load(), "the second pass must force a fresh disabled snapshot from the database")
	entry, err = sharedCache.GetAuthCache(ctx, cacheKey)
	require.NoError(t, err)
	require.NotNil(t, entry)
	require.NotNil(t, entry.Snapshot)
	require.Equal(t, service.StatusAPIKeyDisabled, entry.Snapshot.Status)
}
