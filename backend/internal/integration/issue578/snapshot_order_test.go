//go:build integration

package issue578_test

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/imroc/req/v3"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type fixture struct {
	db           *sql.DB
	repoA, repoB service.AccountRepository
	cache        service.SchedulerCache
	account      *service.Account
}

func setup(t *testing.T) *fixture {
	t.Helper()
	dsn, redisAddr := os.Getenv("ISSUE578_POSTGRES_DSN"), os.Getenv("ISSUE578_REDIS_ADDR")
	if dsn == "" || redisAddr == "" {
		t.Skip("dedicated issue-578 CI provides isolated PostgreSQL and Redis")
	}
	ctx := context.Background()
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	require.NoError(t, repository.ApplyMigrations(ctx, db))
	rdb := redis.NewClient(&redis.Options{Addr: redisAddr, DB: 9})
	t.Cleanup(func() { _ = rdb.Close() })
	require.NoError(t, rdb.Ping(ctx).Err())
	require.NoError(t, rdb.FlushDB(ctx).Err())
	cache := repository.NewSchedulerCache(rdb)
	clientA := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	clientB := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	repoA := repository.NewAccountRepository(clientA, db, cache)
	repoB := repository.NewAccountRepository(clientB, db, cache)
	a := &service.Account{Name: fmt.Sprintf("issue578-%d", time.Now().UnixNano()), Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, Concurrency: 10,
		Credentials: map[string]any{"access_token": "synthetic-token", "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339), "chatgpt_account_id": "fixture-account", "account_scheduling_threshold": 90},
		Extra:       map[string]any{"openai_excel_bps": true, "auto_pause_5h_threshold": 0.9}}
	require.NoError(t, repoA.Create(ctx, a))
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id=$1", a.ID)
		_, _ = db.ExecContext(ctx, "DELETE FROM accounts WHERE id=$1", a.ID)
	})
	return &fixture{db: db, repoA: repoA, repoB: repoB, cache: cache, account: a}
}

func gateway(repo service.AccountRepository, snapshot *service.SchedulerSnapshotService) *service.OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Runtime.Role = config.RuntimeRoleGateway
	return service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, nil, cfg, snapshot, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
}
func quota(t *testing.T, repo service.AccountRepository, handler http.HandlerFunc) *service.OpenAIQuotaService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	factory := func(string) (*req.Client, error) {
		return req.C().WrapRoundTripFunc(func(rt req.RoundTripper) req.RoundTripFunc {
			return func(r *req.Request) (*req.Response, error) {
				r.URL.Scheme, r.URL.Host = target.Scheme, target.Host
				return rt.RoundTrip(r)
			}
		}), nil
	}
	return service.NewOpenAIQuotaService(repo, nil, service.NewOpenAITokenProvider(repo, nil, nil), factory, nil)
}

const usage100 = `{"account_id":"fixture-account","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_after_seconds":86400}},"rate_limit_reset_credits":{"available_count":0}}`

func headers(used string) http.Header {
	h := http.Header{}
	h.Set("x-codex-primary-used-percent", used)
	h.Set("x-codex-primary-window-minutes", "300")
	h.Set("x-codex-primary-reset-after-seconds", "3600")
	h.Set("x-codex-secondary-used-percent", "20")
	h.Set("x-codex-secondary-window-minutes", "10080")
	h.Set("x-codex-secondary-reset-after-seconds", "86400")
	return h
}

// Separate repository objects AND gateway coordinators share only real storage.
// A's header is delayed in the real production worker; B's query lands first.
func TestIssue578ReviewCrossInstanceHeaderCASRedisSelection(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	snapshotConfig := &config.Config{}
	snapshotConfig.Gateway.Scheduling.DbFallbackEnabled = true
	snapshot := service.NewSchedulerSnapshotService(f.cache, nil, f.repoB, nil, snapshotConfig)
	a, b := gateway(f.repoA, nil), gateway(f.repoB, snapshot)
	t.Cleanup(a.CloseOpenAIWSPool)
	t.Cleanup(b.CloseOpenAIWSPool)
	a.UpdateCodexUsageSnapshotFromHeaders(ctx, f.account.ID, headers("88"))
	require.Eventually(t, func() bool {
		x, e := f.repoB.GetByID(ctx, f.account.ID)
		return e == nil && x.Extra["codex_5h_used_percent"] == 88.0
	}, 3*time.Second, 10*time.Millisecond)
	before, err := b.SelectAccountForModel(ctx, nil, "", "gpt-5.3-codex")
	require.NoError(t, err, "below-threshold control must actually select this fixture")
	require.NotNil(t, before)
	require.Equal(t, f.account.ID, before.ID)
	staleFull, err := f.repoA.GetByID(ctx, f.account.ID)
	require.NoError(t, err)
	a.UpdateCodexUsageSnapshotFromHeaders(ctx, f.account.ID, headers("89"))
	q := quota(t, f.repoB, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/backend-api/wham/usage" {
			_, _ = w.Write([]byte(usage100))
			return
		}
		_, _ = w.Write([]byte(`{"available_count":0}`))
	})
	fresh, err := q.QueryUsage(ctx, f.account.ID)
	require.NoError(t, err)
	require.True(t, fresh.UsageCachePersisted)
	var usageAfterQuery, versionAfterQuery string
	require.NoError(t, f.db.QueryRowContext(ctx, `SELECT extra->>'codex_5h_used_percent',COALESCE(extra->>'codex_usage_observed_at_ns','') FROM accounts WHERE id=$1`, f.account.ID).Scan(&usageAfterQuery, &versionAfterQuery))
	t.Logf("synthetic query CAS persisted usage=%s version=%s", usageAfterQuery, versionAfterQuery)
	require.Equal(t, "100", usageAfterQuery, "QueryUsage must persist the accepted 100 before the delayed header worker fires")
	// The production 30-second throttle must actually expire; no test-only shortcut.
	time.Sleep(31 * time.Second)
	stored, err := f.repoA.GetByID(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, stored.Extra["codex_5h_used_percent"])
	t.Logf("after delayed header DB updated_at=%v usage=%v version=%v", stored.Extra["codex_usage_updated_at"], stored.Extra["codex_5h_used_percent"], stored.Extra["codex_usage_observed_at_ns"])
	cached, err := f.cache.GetAccount(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, cached.Extra["codex_5h_used_percent"])
	require.True(t, service.EvaluateAccountSchedulingThreshold(stored, nil, time.Now()).ShouldPause)
	require.True(t, service.EvaluateAccountSchedulingThreshold(cached, nil, time.Now()).ShouldPause)
	selected, err := b.SelectAccountForModel(ctx, nil, "", "gpt-5.3-codex")
	require.Error(t, err, "actual selection must reject the explicit 90%% paused account")
	require.Nil(t, selected)
	// A stale bulk/admin object and a late scheduler publisher must not restore
	// their old Extra after the usage CAS has already committed.
	stale := *staleFull
	stale.Name = "unrelated admin edit"
	require.NoError(t, f.repoA.Update(ctx, &stale))
	require.NoError(t, snapshot.UpdateAccountInCache(ctx, before))
	cached, err = f.cache.GetAccount(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, cached.Extra["codex_5h_used_percent"])
}

type blockingCache struct {
	service.SchedulerCache
	once             sync.Once
	entered, release chan struct{}
	armed            atomic.Bool
}

func (c *blockingCache) SetAccount(ctx context.Context, a *service.Account) error {
	if c.armed.Load() && a.Extra["codex_5h_used_percent"] == 89.0 {
		c.once.Do(func() { close(c.entered) })
		select {
		case <-c.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.SchedulerCache.SetAccount(ctx, a)
}
func TestIssue578ReviewCachePublicationCannotReorder(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	blocked := &blockingCache{SchedulerCache: f.cache, entered: make(chan struct{}), release: make(chan struct{})}
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
	oldRepo := repository.NewAccountRepository(client, f.db, blocked)
	var once sync.Once
	unblock := func() { once.Do(func() { close(blocked.release) }) }
	t.Cleanup(unblock)
	blocked.armed.Store(true)
	t1 := time.Now()
	old := make(chan error, 1)
	go func() {
		old <- oldRepo.UpdateExtra(ctx, f.account.ID, map[string]any{"codex_5h_used_percent": 89.0, "codex_usage_updated_at": t1.Format(time.RFC3339Nano)})
	}()
	select {
	case <-blocked.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("old publication did not block")
	}
	newer := make(chan error, 1)
	go func() {
		newer <- f.repoB.UpdateExtra(ctx, f.account.ID, map[string]any{"codex_5h_used_percent": 100.0, "codex_usage_updated_at": t1.Add(time.Second).Format(time.RFC3339Nano)})
	}()
	require.Eventually(t, func() bool {
		x, e := f.repoB.GetByID(ctx, f.account.ID)
		return e == nil && x.Extra["codex_5h_used_percent"] == 100.0
	}, 2*time.Second, 5*time.Millisecond)
	// On the rejected source, wait for 100 to reach Redis before releasing
	// the stale publication. Under the shared lock that publisher must wait;
	// releasing after the bounded wait lets it republish the latest row.
	select {
	case err := <-newer:
		require.NoError(t, err)
		newer <- nil
	case <-time.After(150 * time.Millisecond):
	}
	unblock()
	require.NoError(t, <-old)
	require.NoError(t, <-newer)
	cached, err := f.cache.GetAccount(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, cached.Extra["codex_5h_used_percent"])
}
