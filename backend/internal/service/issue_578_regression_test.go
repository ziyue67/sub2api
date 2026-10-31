package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// All upstream traffic in these regressions terminates at httptest. No model,
// reset-card consumption, database, Redis or real credential is used.
type issue578Repo struct {
	AccountRepository
	casMu     sync.Mutex
	mu        sync.Mutex
	accounts  map[int64]*Account
	writes    int
	fail      int
	entered   chan struct{}
	release   chan struct{}
	active    int
	maxActive int
}

func (r *issue578Repo) UpdateCodexUsageSnapshot(ctx context.Context, id int64, updates map[string]any) (bool, error) {
	r.casMu.Lock()
	defer r.casMu.Unlock()
	r.mu.Lock()
	current, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(r.accounts[id].Extra["codex_usage_updated_at"]))
	r.mu.Unlock()
	observed, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(updates["codex_usage_updated_at"]))
	if observed.Before(current) {
		display := make(map[string]any)
		for key, value := range updates {
			if key != "codex_usage_updated_at" && key != "codex_usage_field_versions" && !strings.HasPrefix(key, "codex_5h_") && !strings.HasPrefix(key, "codex_7d_") && !strings.HasPrefix(key, "codex_primary_") && !strings.HasPrefix(key, "codex_secondary_") {
				display[key] = value
			}
		}
		if len(display) > 0 {
			return false, r.UpdateExtra(ctx, id, display)
		}
		return false, nil
	}
	err := r.UpdateExtra(ctx, id, updates)
	return err == nil, err
}

func (r *issue578Repo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.accounts[id]
	if !ok {
		return nil, fmt.Errorf("fixture account missing")
	}
	copy := *a
	copy.Extra = make(map[string]any, len(a.Extra))
	for k, v := range a.Extra {
		copy.Extra[k] = v
	}
	return &copy, nil
}

func (r *issue578Repo) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	r.writes++
	n := r.writes
	r.active++
	if r.active > r.maxActive {
		r.maxActive = r.active
	}
	r.mu.Unlock()
	if n == 1 && r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active--
	if r.fail > 0 {
		r.fail--
		return errors.New("synthetic persistence failure")
	}
	for k, v := range updates {
		r.accounts[id].Extra[k] = v
	}
	return nil
}

func issue578Account() *Account {
	a := excelAccount()
	a.Extra["codex_5h_used_percent"] = 89.0
	a.Extra["codex_7d_used_percent"] = 20.0
	a.Extra["codex_5h_reset_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	a.Extra["codex_7d_reset_at"] = time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	a.Extra["codex_usage_updated_at"] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	a.Credentials["account_scheduling_threshold"] = 90
	a.Extra["auto_pause_5h_threshold"] = 0.9
	return a
}

func issue578WaitUsage(t *testing.T, r *issue578Repo, id int64, want float64) *Account {
	t.Helper()
	require.Eventually(t, func() bool {
		a, _ := r.GetByID(context.Background(), id)
		return a.Extra["codex_5h_used_percent"] == want
	}, 2*time.Second, time.Millisecond, "latest usage must reach the scheduling snapshot without another response")
	a, err := r.GetByID(context.Background(), id)
	require.NoError(t, err)
	return a
}

func TestIssue578HeaderLatestValue(t *testing.T) {
	a := issue578Account()
	r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
	s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(80 * time.Millisecond)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // downstream disconnect must not cancel snapshot bookkeeping
	s.UpdateCodexUsageSnapshotFromHeaders(ctx, a.ID, excelBPSQuotaHeaders("89", "20"))
	issue578WaitUsage(t, r, a.ID, 89)
	require.Eventually(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.writes == 1 }, time.Second, time.Millisecond)
	for i := 0; i < 200; i++ {
		s.UpdateCodexUsageSnapshotFromHeaders(ctx, a.ID, excelBPSQuotaHeaders("95", "20"))
	}
	s.UpdateCodexUsageSnapshotFromHeaders(ctx, a.ID, excelBPSQuotaHeaders("100", "20"))
	current := issue578WaitUsage(t, r, a.ID, 100)
	require.True(t, EvaluateAccountSchedulingThreshold(current, nil, time.Now()).ShouldPause, "89 -> 100 must close the explicit 90%% scheduling gate")
	paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), current)
	require.True(t, paused, "latest snapshot must also close the explicit auto-pause gate")
	r.mu.Lock()
	require.LessOrEqual(t, r.writes, 3, "burst must coalesce; no per-response database writes")
	r.mu.Unlock()
}

func TestIssue578HeaderSerialAndRetry(t *testing.T) {
	for _, fail := range []int{0, 1} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			a := issue578Account()
			r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}, fail: fail, entered: make(chan struct{}), release: make(chan struct{})}
			s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(40 * time.Millisecond)}
			s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("89", "20"))
			select {
			case <-r.entered:
			case <-time.After(time.Second):
				t.Fatal("first write did not start")
			}
			time.Sleep(70 * time.Millisecond) // let the throttle expire while the old write is blocked
			s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("100", "20"))
			time.Sleep(20 * time.Millisecond)
			r.mu.Lock()
			active := r.maxActive
			r.mu.Unlock()
			close(r.release)
			require.Equal(t, 1, active, "an older in-flight write must finish before the newer write starts")
			issue578WaitUsage(t, r, a.ID, 100)
		})
	}
	t.Run("retry without another response", func(t *testing.T) {
		a := issue578Account()
		r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}, fail: 2}
		s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(40 * time.Millisecond)}
		startedAt := time.Now()
		s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("100", "20"))
		issue578WaitUsage(t, r, a.ID, 100)
		require.GreaterOrEqual(t, time.Since(startedAt), 80*time.Millisecond, "failures must consume the write interval")
		r.mu.Lock()
		defer r.mu.Unlock()
		require.Equal(t, 3, r.writes, "failed writes must retry with the latest value at a bounded rate")
	})
}

const issue578UsageBody = `{"account_id":"test-account","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":20,"limit_window_seconds":604800,"reset_after_seconds":86400}},"credits":{"has_credits":true,"balance":"20.00"}}`

func issue578Quota(t *testing.T, r *issue578Repo, handler http.HandlerFunc) *OpenAIQuotaService {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	tokens := &stubQuotaTokenCache{tokens: map[string]string{OpenAITokenCacheKey(r.accounts[300]): "synthetic-oauth-token"}}
	return NewOpenAIQuotaService(r, nil, NewOpenAITokenProvider(r, tokens, nil), newQuotaRedirectingFactory(srv), nil)
}

func TestIssue578QueryUsageSynchronizesNonzero(t *testing.T) {
	a := issue578Account()
	r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
	q := issue578Quota(t, r, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "Bearer synthetic-oauth-token", req.Header.Get("Authorization"))
		require.Equal(t, "test-account", req.Header.Get("Chatgpt-Account-Id"))
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/backend-api/wham/usage" {
			_, _ = w.Write([]byte(issue578UsageBody))
			return
		}
		require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", req.URL.Path)
		require.Equal(t, http.MethodGet, req.Method, "queries must never consume reset cards")
		_, _ = w.Write([]byte(`{}`))
	})
	usage, err := q.QueryUsage(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, usage.RateLimit.PrimaryWindow.UsedPercent)
	current, err := r.GetByID(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, 100.0, current.Extra["codex_5h_used_percent"], "successful query must synchronously update scheduler windows")
	require.True(t, EvaluateAccountSchedulingThreshold(current, nil, time.Now()).ShouldPause)
	paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), current)
	require.True(t, paused)
}

func TestIssue578BPSTTLRefreshAndThrottle(t *testing.T) {
	a := issue578Account()
	require.False(t, a.IsOpenAIResponsesWebSocketV2Enabled())
	u := &UsageInfo{}
	applyExtraToUsage(u, a.Extra, time.Now())
	require.True(t, shouldRefreshOpenAICodexSnapshot(a, u, time.Now()), "complete BPS windows must expire without WSv2")
	r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
	var calls atomic.Int32
	q := issue578Quota(t, r, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "Bearer synthetic-oauth-token", req.Header.Get("Authorization"), "usage must use OAuth credentials, not the BPS grant")
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/backend-api/wham/usage" {
			calls.Add(1)
			_, _ = w.Write([]byte(issue578UsageBody))
			return
		}
		require.Equal(t, "/backend-api/wham/rate-limit-reset-credits", req.URL.Path)
		_, _ = w.Write([]byte(`{}`))
	})
	s := &AccountUsageService{accountRepo: r, openAIQuotaService: q, cache: NewUsageCache()}
	// Reuse the stale caller snapshot to verify the probe throttle itself.
	for i := 0; i < 3; i++ {
		copy := *a
		copy.Extra = map[string]any{}
		for k, v := range a.Extra {
			copy.Extra[k] = v
		}
		got, err := s.getOpenAIUsage(context.Background(), &copy, false)
		require.NoError(t, err)
		if i == 0 {
			require.Equal(t, 100.0, got.FiveHour.Utilization)
		}
	}
	require.EqualValues(t, 1, calls.Load(), "repeated stale reads must not query upstream without a TTL bound")
	issue578WaitUsage(t, r, a.ID, 100)
}
