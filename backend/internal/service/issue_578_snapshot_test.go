package service

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestIssue578ThresholdSemantics(t *testing.T) {
	for _, tc := range []struct {
		name                string
		threshold           any
		pause               any
		wantGate, wantPause bool
	}{
		{"unconfigured credits allowed", nil, nil, false, false},
		{"explicit 90 percent", 90, 0.9, true, true},
		{"100 disables account gate", 100, nil, false, false},
		{"auto pause 1 means 100 percent", 100, 1.0, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := issue578Account()
			a.Extra["codex_5h_used_percent"] = 100.0
			a.Extra["codex_usage_updated_at"] = time.Now().UTC().Format(time.RFC3339)
			a.Credentials["account_scheduling_threshold"] = tc.threshold
			a.Extra["auto_pause_5h_threshold"] = tc.pause
			require.Equal(t, tc.wantGate, EvaluateAccountSchedulingThreshold(a, nil, time.Now()).ShouldPause)
			paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), a)
			require.Equal(t, tc.wantPause, paused)
			require.False(t, ResolveOpenAIAutoResetCreditConfig(a).Enabled, "usage/credits do not opt into consuming reset cards")
		})
	}
}

func TestIssue578QuerySnapshotGuards(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		fail       int
		want       bool
	}{
		{"valid", issue578UsageBody, 0, true},
		{"write failure reported", issue578UsageBody, 1, false},
		{"wrong account", strings.ReplaceAll(issue578UsageBody, "test-account", "other-account"), 0, false},
		{"missing usage", `{"rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`, 0, false},
		{"null usage", `{"rate_limit":{"primary_window":{"used_percent":null,"limit_window_seconds":18000}}}`, 0, false},
		{"unknown window", `{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":60}}}`, 0, false},
		{"negative usage", `{"rate_limit":{"primary_window":{"used_percent":-1,"limit_window_seconds":18000}}}`, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := issue578Account()
			r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}, fail: tc.fail}
			q := issue578Quota(t, r, func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if req.URL.Path == "/backend-api/wham/usage" {
					_, _ = w.Write([]byte(tc.body))
					return
				}
				require.Equal(t, http.MethodGet, req.Method)
				_, _ = w.Write([]byte(`{}`))
			})
			usage, err := q.QueryUsage(context.Background(), a.ID)
			require.NoError(t, err)
			require.Equal(t, tc.want, usage.UsageCachePersisted)
			current, err := r.GetByID(context.Background(), a.ID)
			require.NoError(t, err)
			want := 89.0
			if tc.want {
				want = 100
			}
			require.Equal(t, want, current.Extra["codex_5h_used_percent"])
		})
	}
}

func TestIssue578SparkQueryIndependentWindow(t *testing.T) {
	parent := issue578Account()
	shadow := &Account{ID: 301, Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent.ID, QuotaDimension: QuotaDimensionSpark, Extra: map[string]any{}}
	r := &issue578Repo{accounts: map[int64]*Account{parent.ID: parent, shadow.ID: shadow}}
	q := issue578Quota(t, r, func(w http.ResponseWriter, req *http.Request) {
		require.Equal(t, "test-account", req.Header.Get("Chatgpt-Account-Id"))
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/backend-api/wham/usage" {
			_, _ = w.Write([]byte(`{"rate_limit":{"primary_window":{"used_percent":100,"limit_window_seconds":18000}},"additional_rate_limits":[{"metered_feature":"codex_bengalfox","rate_limit":{"primary_window":{"used_percent":42,"limit_window_seconds":18000,"reset_after_seconds":3600},"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_after_seconds":86400}}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	usage, err := q.QueryUsage(context.Background(), shadow.ID)
	require.NoError(t, err)
	require.True(t, usage.UsageCachePersisted)
	current, _ := r.GetByID(context.Background(), shadow.ID)
	require.Equal(t, 42.0, current.Extra["codex_5h_used_percent"])
	current, _ = r.GetByID(context.Background(), parent.ID)
	require.Equal(t, 89.0, current.Extra["codex_5h_used_percent"], "shadow must not overwrite parent")
}

func TestIssue578ExplicitRefreshWinsDelayedHeader(t *testing.T) {
	a := issue578Account()
	r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
	s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(120 * time.Millisecond)}
	s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("89", "20"))
	require.Eventually(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.writes == 1 }, time.Second, time.Millisecond)
	s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("95", "20"))
	q := issue578Quota(t, r, func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/backend-api/wham/usage" {
			_, _ = w.Write([]byte(issue578UsageBody))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	usage, err := q.QueryUsage(context.Background(), a.ID)
	require.NoError(t, err)
	require.True(t, usage.UsageCachePersisted)
	time.Sleep(200 * time.Millisecond)
	current, _ := r.GetByID(context.Background(), a.ID)
	require.Equal(t, 100.0, current.Extra["codex_5h_used_percent"])
	r.mu.Lock()
	defer r.mu.Unlock()
	require.Equal(t, 2, r.writes, "older queued headers must not undo the newer query")
}

func TestIssue578ConcurrentTTLReservation(t *testing.T) {
	s := &AccountUsageService{cache: NewUsageCache()}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	now := time.Now()
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.shouldProbeOpenAICodexSnapshot(300, now) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, accepted.Load())
}

func TestIssue578OlderQueryCannotUndoLaterHeaders(t *testing.T) {
	a := issue578Account()
	r := &issue578Repo{accounts: map[int64]*Account{a.ID: a}}
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	q := issue578Quota(t, r, func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if request.URL.Path == "/backend-api/wham/usage" {
			close(entered)
			<-release
			_, _ = w.Write([]byte(strings.ReplaceAll(issue578UsageBody, `"used_percent":100`, `"used_percent":89`)))
			return
		}
		_, _ = w.Write([]byte(`{}`))
	})
	done := make(chan error, 1)
	go func() { _, err := q.QueryUsage(context.Background(), a.ID); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("quota query did not reach the local server")
	}
	s := &OpenAIGatewayService{accountRepo: r, codexSnapshotThrottle: newAccountWriteThrottle(40 * time.Millisecond)}
	s.UpdateCodexUsageSnapshotFromHeaders(context.Background(), a.ID, excelBPSQuotaHeaders("100", "20"))
	issue578WaitUsage(t, r, a.ID, 100)
	unblock()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("quota query did not complete")
	}
	current, _ := r.GetByID(context.Background(), a.ID)
	require.Equal(t, 100.0, current.Extra["codex_5h_used_percent"])
}

func TestIssue578PartialSnapshotDoesNotInventReset(t *testing.T) {
	now := time.Now().UTC()
	updates := openAIQuotaSchedulingUpdates([]byte(`{"rate_limit":{"primary_window":{"used_percent":95,"limit_window_seconds":18000}}}`), false, "test-account", now)
	require.Equal(t, 95.0, updates["codex_5h_used_percent"])
	require.NotContains(t, updates, "codex_5h_reset_at")
	require.NotContains(t, updates, "codex_7d_used_percent")
}
