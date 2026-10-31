//go:build integration

package issue578_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func review2Selector(t *testing.T, f *fixture) *service.OpenAIGatewayService {
	t.Helper()
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.DbFallbackEnabled = true
	snapshot := service.NewSchedulerSnapshotService(f.cache, nil, f.repoB, nil, cfg)
	g := gateway(f.repoB, snapshot)
	t.Cleanup(g.CloseOpenAIWSPool)
	return g
}

func review2Query100(t *testing.T, f *fixture) *service.OpenAIQuotaService {
	t.Helper()
	return quota(t, f.repoB, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/backend-api/wham/usage" {
			_, _ = w.Write([]byte(usage100))
			return
		}
		_, _ = w.Write([]byte(`{"available_count":0}`))
	})
}

func review2AssertPaused(t *testing.T, f *fixture, selector *service.OpenAIGatewayService) {
	t.Helper()
	ctx := context.Background()
	stored, err := f.repoB.GetByID(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, stored.Extra["codex_5h_used_percent"], "stale partial observation must not relabel old 5h usage")
	cached, err := f.cache.GetAccount(ctx, f.account.ID)
	require.NoError(t, err)
	require.NotNil(t, cached)
	require.Equal(t, 100.0, cached.Extra["codex_5h_used_percent"], "generic repository publication must not regress Redis after the usage CAS")
	require.True(t, service.EvaluateAccountSchedulingThreshold(cached, nil, time.Now()).ShouldPause)
	selected, err := selector.SelectAccountForModel(ctx, nil, "", "gpt-5.3-codex")
	require.Error(t, err, "actual Redis-backed selection must reject the explicit 90% threshold")
	require.Nil(t, selected)
}

// Block a regular publisher AFTER it read 89 but BEFORE Redis receives it.
// The other repository commits a real QueryUsage=100 while that object is held.
// Every publisher must share the usage publication lock and read inside it.
func TestIssue578Review2GenericRepositoryPublicationCannotRegress(t *testing.T) {
	for _, path := range []string{"observational UpdateExtra", "status ClearError"} {
		t.Run(path, func(t *testing.T) {
			f := setup(t)
			ctx := context.Background()
			require.NoError(t, f.repoA.UpdateExtra(ctx, f.account.ID, map[string]any{
				"codex_5h_used_percent": 89.0, "codex_7d_used_percent": 20.0,
				"codex_5h_reset_at":      time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
				"codex_usage_updated_at": time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano),
			}))
			selector := review2Selector(t, f)
			control, err := selector.SelectAccountForModel(ctx, nil, "", "gpt-5.3-codex")
			require.NoError(t, err, "below-threshold control must actually select this fixture")
			require.Equal(t, f.account.ID, control.ID)
			blocked := &blockingCache{SchedulerCache: f.cache, entered: make(chan struct{}), release: make(chan struct{})}
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
			oldRepo := repository.NewAccountRepository(client, f.db, blocked)
			t.Cleanup(func() {
				select {
				case <-blocked.release:
				default:
					close(blocked.release)
				}
			})
			blocked.armed.Store(true)
			old := make(chan error, 1)
			go func() {
				if path == "status ClearError" {
					old <- oldRepo.ClearError(ctx, f.account.ID)
				} else {
					old <- oldRepo.UpdateExtra(ctx, f.account.ID, map[string]any{"synthetic_observation": true})
				}
			}()
			select {
			case <-blocked.entered:
			case <-time.After(3 * time.Second):
				t.Fatal("generic publisher never reached the delayed read/publication seam")
			}
			q := review2Query100(t, f)
			fresh := make(chan error, 1)
			go func() {
				u, e := q.QueryUsage(ctx, f.account.ID)
				if e == nil && !u.UsageCachePersisted {
					e = context.DeadlineExceeded
				}
				fresh <- e
			}()
			require.Eventually(t, func() bool {
				x, e := f.repoB.GetByID(ctx, f.account.ID)
				return e == nil && x.Extra["codex_5h_used_percent"] == 100.0
			}, 3*time.Second, 5*time.Millisecond, "QueryUsage CAS must commit across the held old read")
			// Old code can complete the new publication while the stale object is
			// held. With a shared lock it must wait; either way release deterministically.
			select {
			case err := <-fresh:
				require.NoError(t, err)
				fresh <- nil
			case <-time.After(150 * time.Millisecond):
			}
			close(blocked.release)
			require.NoError(t, <-old)
			require.NoError(t, <-fresh)
			review2AssertPaused(t, f, selector)
		})
	}
}

// Real 30-second worker, two coordinator domains, disjoint t1/t3 payloads.
// A newer 7d-only observation must not lend its timestamp to pending 5h=89.
func TestIssue578Review2PartialPendingCannotRestampOlderWindow(t *testing.T) {
	f := setup(t)
	ctx := context.Background()
	selector := review2Selector(t, f)
	a := gateway(f.repoA, nil)
	t.Cleanup(a.CloseOpenAIWSPool)
	a.UpdateCodexUsageSnapshotFromHeaders(ctx, f.account.ID, headers("88"))
	require.Eventually(t, func() bool {
		x, e := f.repoB.GetByID(ctx, f.account.ID)
		return e == nil && x.Extra["codex_5h_used_percent"] == 88.0
	}, 3*time.Second, 5*time.Millisecond)
	control, err := selector.SelectAccountForModel(ctx, nil, "", "gpt-5.3-codex")
	require.NoError(t, err, "below-threshold control must actually select this fixture")
	require.Equal(t, f.account.ID, control.ID)
	five := headers("89")
	five.Del("x-codex-secondary-used-percent")
	five.Del("x-codex-secondary-window-minutes")
	five.Del("x-codex-secondary-reset-after-seconds")
	a.UpdateCodexUsageSnapshotFromHeaders(ctx, f.account.ID, five) // t1
	u, err := review2Query100(t, f).QueryUsage(ctx, f.account.ID)  // t2
	require.NoError(t, err)
	require.True(t, u.UsageCachePersisted)
	seven := http.Header{}
	seven.Set("x-codex-secondary-used-percent", "27")
	seven.Set("x-codex-secondary-window-minutes", "10080")
	seven.Set("x-codex-secondary-reset-after-seconds", "86400")
	a.UpdateCodexUsageSnapshotFromHeaders(ctx, f.account.ID, seven) // t3
	time.Sleep(31 * time.Second)
	stored, err := f.repoB.GetByID(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 27.0, stored.Extra["codex_7d_used_percent"], "the new partial window must not be dropped")
	review2AssertPaused(t, f, selector)
	cached, err := f.cache.GetAccount(ctx, f.account.ID)
	require.NoError(t, err)
	require.Equal(t, 27.0, cached.Extra["codex_7d_used_percent"])
}
