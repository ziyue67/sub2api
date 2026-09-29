package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Keep the legacy queries active-only, as in the production repository. A
// stub returning every account for those methods would hide the regression.
type guardRecoveryAccounts struct {
	AccountRepository
	items []Account
}

type guardManagedAccounts struct {
	guardRecoveryAccounts
	ids []int64
	err error
}

func (a *guardManagedAccounts) ListCredentialOperationsAccountIDs(context.Context) ([]int64, error) {
	return a.ids, a.err
}

func TestTokenGuardRecoveryExcludesCredentialOperations(t *testing.T) {
	accounts := &guardManagedAccounts{guardRecoveryAccounts: guardRecoveryAccounts{items: []Account{
		guardRecoveryAccount(1, StatusError), guardRecoveryAccount(2, StatusActive),
	}}, ids: []int64{1}}
	repo := &guardMemoryRepo{states: []AccountTokenGuardState{{AccountID: 1}, {AccountID: 2}}}
	raw, err := json.Marshal(defaultAccountTokenGuardConfig())
	require.NoError(t, err)
	svc := NewAccountTokenGuardService(&twoFALoginSettings{raw: string(raw)}, repo, accounts, nil, nil)
	got, err := svc.listAccounts(context.Background(), defaultAccountTokenGuardConfig())
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].ID)
	_, err = svc.ReloginAccount(context.Background(), 1)
	require.ErrorContains(t, err, "账号已加入凭证运营")
	status, err := svc.Status(context.Background())
	require.NoError(t, err)
	require.Len(t, status.Accounts, 1)
	require.Equal(t, int64(2), status.Accounts[0].AccountID)
	accounts.err = fmt.Errorf("private storage error")
	_, err = svc.listAccounts(context.Background(), defaultAccountTokenGuardConfig())
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private storage error")
	_, err = svc.ReloginAccount(context.Background(), 1)
	require.Error(t, err)
}

func (a *guardRecoveryAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	for i := range a.items {
		if a.items[i].ID == id {
			account := a.items[i]
			return &account, nil
		}
	}
	return nil, ErrAccountNotFound
}

func (a *guardRecoveryAccounts) ListByPlatform(ctx context.Context, platform string) ([]Account, error) {
	return a.ListAllWithFilters(ctx, platform, "", StatusActive, "", 0, "")
}

func (a *guardRecoveryAccounts) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	return a.ListAllWithFilters(ctx, "", "", StatusActive, "", groupID, "")
}

func (a *guardRecoveryAccounts) ListAllWithFilters(_ context.Context, platform, accountType, status, _ string, groupID int64, _ string) ([]Account, error) {
	var out []Account
	for _, account := range a.items {
		if platform != "" && account.Platform != platform || accountType != "" && account.Type != accountType ||
			status != "" && account.Status != status || groupID > 0 && !slices.Contains(account.GroupIDs, groupID) {
			continue
		}
		out = append(out, account)
	}
	return out, nil
}

type guardRecoveryAdmin struct {
	AdminService
	accounts *guardRecoveryAccounts
	writes   int
}

func (a *guardRecoveryAdmin) UpdateAccount(_ context.Context, id int64, input *UpdateAccountInput) (*Account, error) {
	a.writes++
	for i := range a.accounts.items {
		if a.accounts.items[i].ID == id {
			a.accounts.items[i].Credentials = input.Credentials
			if input.Extra != nil {
				a.accounts.items[i].Extra = input.Extra
			}
			return &a.accounts.items[i], nil
		}
	}
	return nil, ErrAccountNotFound
}

func (a *guardRecoveryAdmin) ClearAccountError(_ context.Context, id int64) (*Account, error) {
	for i := range a.accounts.items {
		if a.accounts.items[i].ID == id {
			a.accounts.items[i].Status = StatusActive
			a.accounts.items[i].ErrorMessage = ""
			return &a.accounts.items[i], nil
		}
	}
	return nil, ErrAccountNotFound
}

func (a *guardRecoveryAdmin) SetAccountSchedulable(_ context.Context, id int64, value bool) (*Account, error) {
	for i := range a.accounts.items {
		if a.accounts.items[i].ID == id {
			a.accounts.items[i].Schedulable = value
			return &a.accounts.items[i], nil
		}
	}
	return nil, ErrAccountNotFound
}

func guardRecoveryAccount(id int64, status string, groups ...int64) Account {
	return Account{ID: id, Name: fmt.Sprintf("account%d@example.com", id), Platform: PlatformOpenAI,
		Type: AccountTypeOAuth, Status: status, GroupIDs: groups,
		Credentials: map[string]any{"access_token": "old-access", "refresh_token": "old-refresh", "model_mapping": map[string]any{"alias": "model"}}}
}

func TestTokenGuardRecoveryIncludesErrorAccountsAndHonorsScope(t *testing.T) {
	parent := int64(1)
	shadow := guardRecoveryAccount(4, StatusError, 11)
	shadow.ParentAccountID = &parent
	otherPlatform := guardRecoveryAccount(5, StatusError, 11)
	otherPlatform.Platform = PlatformAnthropic
	apiKey := guardRecoveryAccount(6, StatusError, 11)
	apiKey.Type = AccountTypeAPIKey
	accounts := &guardRecoveryAccounts{items: []Account{
		guardRecoveryAccount(1, StatusActive, 11, 12), guardRecoveryAccount(2, StatusError, 11, 12),
		guardRecoveryAccount(3, StatusDisabled, 11), shadow, otherPlatform, apiKey,
		guardRecoveryAccount(7, StatusError, 13),
	}}
	svc := NewAccountTokenGuardService(nil, &guardMemoryRepo{}, accounts, nil, nil)
	for _, tc := range []struct {
		name   string
		groups []int64
		want   []int64
	}{
		{"all groups", nil, []int64{1, 2, 7}},
		{"selected groups deduplicate", []int64{11, 12}, []int64{1, 2}},
		{"outside scope", []int64{14}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaultAccountTokenGuardConfig()
			cfg.GroupIDs = tc.groups
			got, err := svc.listAccounts(context.Background(), cfg)
			require.NoError(t, err)
			var ids []int64
			for _, account := range got {
				ids = append(ids, account.ID)
			}
			require.Equal(t, tc.want, ids)
		})
	}
	old := time.Now().Add(-time.Hour)
	svc.repo = &guardMemoryRepo{states: []AccountTokenGuardState{{AccountID: 1, LastProbeAt: &old}}}
	cfg := defaultAccountTokenGuardConfig()
	cfg.MaxProbePerCycle = 1
	got, err := svc.listAccounts(context.Background(), cfg)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, int64(2), got[0].ID, "unprobed error accounts must enter the bounded rotation")
}

func TestTokenGuardRecoveryReloginsKnownRevocationDespiteProbe429(t *testing.T) {
	var probes, logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			probes.Add(1)
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		logins.Add(1)
		_, _ = io.WriteString(w, `{"credential":{"access_token":"new-access","refresh_token":"new-refresh","id_token":"new-id"}}`)
	}))
	defer server.Close()
	account := guardRecoveryAccount(1, StatusError)
	account.ErrorMessage = "Token revoked (401): private-upstream-message"
	accounts := &guardRecoveryAccounts{items: []Account{account}}
	repo := &guardMemoryRepo{}
	admin := &guardRecoveryAdmin{accounts: accounts}
	svc := NewAccountTokenGuardService(nil, repo, accounts, admin, nil)
	cfg := guardRecoveryConfig(server.URL, account.Name)
	svc.config.Store(cfg)
	stats, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, stats.Repaired)
	require.Zero(t, probes.Load(), "known business auth rejection should not depend on the probe provider")
	require.Equal(t, int32(1), logins.Load())
	require.Equal(t, StatusActive, accounts.items[0].Status)
	require.True(t, accounts.items[0].Schedulable)
	require.Empty(t, accounts.items[0].ErrorMessage)
	require.Equal(t, "new-access", accounts.items[0].GetCredential("access_token"))
	require.Equal(t, account.Credentials["model_mapping"], accounts.items[0].Credentials["model_mapping"])
	states, err := repo.ListStates(context.Background())
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Zero(t, states[0].FailStreak)
	require.Equal(t, StatusActive, states[0].AccountStatus)
	require.True(t, states[0].Schedulable)
	require.Equal(t, AccountTokenGuardProbeOK, states[0].ProbeState)
	require.NotContains(t, states[0].ProbeDetail, "private-upstream-message")
}

func guardRecoveryConfig(endpoint, email string) AccountTokenGuardConfig {
	cfg := defaultAccountTokenGuardConfig()
	cfg.ProbeEndpoint = endpoint + "/probe"
	cfg.ReloginEndpoint = endpoint + "/relogin"
	cfg.ProbeTimeoutSeconds = 5
	cfg.ProbeConcurrency = 1
	cfg.MaxProbePerCycle = 1
	cfg.ReloginAccounts = []AccountTokenGuardReloginAccount{{Email: email, Password: "test-password", MFASecret: "test-mfa"}}
	return cfg
}

func TestTokenGuardRecoveryRefreshesPlanFromNewIDToken(t *testing.T) {
	const businessPlan = "self_serve_business_prolite"
	for _, tc := range []struct {
		name         string
		tokenPlan    string
		providerPlan any
		invalidToken bool
		wantPlan     string
	}{
		{name: "business downgraded to free", tokenPlan: "free", wantPlan: "free"},
		{name: "token overrides stale provider plan", tokenPlan: "free", providerPlan: businessPlan, wantPlan: "free"},
		{name: "paid plan changed", tokenPlan: "plus", wantPlan: "plus"},
		{name: "trim token plan", tokenPlan: " free ", wantPlan: "free"},
		{name: "missing claim uses provider plan", providerPlan: "free", wantPlan: "free"},
		{name: "missing claim preserves stored plan", wantPlan: businessPlan},
		{name: "blank claim preserves stored plan", tokenPlan: " ", wantPlan: businessPlan},
		{name: "invalid token preserves stored plan", invalidToken: true, wantPlan: businessPlan},
	} {
		t.Run(tc.name, func(t *testing.T) {
			credential := map[string]any{
				"access_token": "new-access", "refresh_token": "new-refresh",
				"id_token": reauthTestJWT(map[string]any{
					"exp":                         time.Now().Add(time.Hour).Unix(),
					"https://api.openai.com/auth": map[string]any{"chatgpt_plan_type": tc.tokenPlan},
				}),
			}
			if tc.invalidToken {
				credential["id_token"] = "unparseable-id-token"
			}
			if tc.providerPlan != nil {
				credential["plan_type"] = tc.providerPlan
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/relogin" {
					t.Errorf("unexpected request path: %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"credential": credential})
			}))
			defer server.Close()
			account := guardRecoveryAccount(1, StatusError)
			account.ErrorMessage = "Token revoked (401): token_revoked"
			account.Credentials["plan_type"] = businessPlan
			account.Extra = map[string]any{"openai_excel_bps": true, "privacy_mode": PrivacyModeTrainingOff}
			accounts := &guardRecoveryAccounts{items: []Account{account}}
			admin := &guardRecoveryAdmin{accounts: accounts}
			svc := NewAccountTokenGuardService(nil, &guardMemoryRepo{}, accounts, admin, nil)
			svc.config.Store(guardRecoveryConfig(server.URL, account.Name))

			stats, err := svc.RunCycle(context.Background(), false)
			require.NoError(t, err)
			require.Equal(t, 1, stats.Repaired)
			updated := accounts.items[0]
			require.Equal(t, tc.wantPlan, updated.GetCredential("plan_type"))
			require.Equal(t, "new-access", updated.GetCredential("access_token"))
			require.Equal(t, "new-refresh", updated.GetCredential("refresh_token"))
			require.Equal(t, account.Credentials["model_mapping"], updated.Credentials["model_mapping"])
			require.Equal(t, businessPlan, account.GetCredential("plan_type"), "do not mutate the old snapshot")
			require.Equal(t, tc.wantPlan != "free", updated.Extra["openai_excel_bps"])
			require.Equal(t, PrivacyModeTrainingOff, updated.Extra["privacy_mode"])
			require.Equal(t, true, account.Extra["openai_excel_bps"], "do not mutate the old settings")
			require.Equal(t, StatusActive, updated.Status)
			require.True(t, updated.Schedulable)
			require.Equal(t, tc.wantPlan != "free", updated.IsOpenAIChatGPTSubscription())
		})
	}
}

func TestTokenGuardRecoveryPreservesFailureThresholdAndLoginBudget(t *testing.T) {
	var loginBudget atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			_, _ = io.WriteString(w, `{"status":"failed","error":{"code":"invalid_token"}}`)
			return
		}
		_, _ = io.WriteString(w, `{"credential":{"access_token":"new-access","refresh_token":"new-refresh","id_token":"new-id"}}`)
	}))
	defer server.Close()
	account := guardRecoveryAccount(1, StatusActive)
	accounts := &guardRecoveryAccounts{items: []Account{account}}
	repo := &guardMemoryRepo{}
	admin := &guardRecoveryAdmin{accounts: accounts}
	svc := NewAccountTokenGuardService(nil, repo, accounts, admin, nil)
	svc.httpClient = &http.Client{Transport: guardRecoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/relogin" {
			deadline, ok := r.Context().Deadline()
			if ok {
				loginBudget.Store(int64(time.Until(deadline)))
			}
		}
		return server.Client().Transport.RoundTrip(r)
	})}
	cfg := guardRecoveryConfig(server.URL, account.Name)
	cfg.FailStreakThreshold = 2
	svc.config.Store(cfg)
	first, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, first.AuthFailed)
	require.Zero(t, admin.writes)
	states, err := repo.ListStates(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, states[0].FailStreak)
	second, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, second.Repaired, "progress persistence must not reset the previous cycle's fail streak")
	require.Equal(t, 1, admin.writes)
	require.Greater(t, time.Duration(loginBudget.Load()), 24*time.Minute, "automatic login needs its own budget, independent of probe duration")
}

type guardRecoveryRoundTripper func(*http.Request) (*http.Response, error)

func (f guardRecoveryRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTokenGuardRecoveryDoesNotReloginOnProviderFailureOrWhenDisabled(t *testing.T) {
	for _, tc := range []struct {
		name        string
		status      string
		errorReason string
		autoRelogin bool
	}{
		{"active with probe 429", StatusActive, "", true},
		{"unrelated error with probe 429", StatusError, "upstream quota exhausted", true},
		{"manual disabled", StatusDisabled, "Token revoked (401): rejected", true},
		{"auto relogin off", StatusError, "Token revoked (401): rejected", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logins atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/relogin" {
					logins.Add(1)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()
			account := guardRecoveryAccount(1, tc.status)
			account.ErrorMessage = tc.errorReason
			accounts := &guardRecoveryAccounts{items: []Account{account}}
			svc := NewAccountTokenGuardService(nil, &guardMemoryRepo{}, accounts, nil, nil)
			cfg := guardRecoveryConfig(server.URL, account.Name)
			cfg.AutoRelogin = tc.autoRelogin
			svc.config.Store(cfg)
			_, err := svc.RunCycle(context.Background(), false)
			require.NoError(t, err)
			require.Zero(t, logins.Load())
			require.Equal(t, tc.status, accounts.items[0].Status)
		})
	}
}

func TestTokenGuardRecoveryRecognizesExplicitRevokedProtocolCodes(t *testing.T) {
	for _, code := range []string{"token_invalidated", "token_revoked", "refresh_token_reused", "refresh_token_invalidated"} {
		t.Run(code, func(t *testing.T) {
			svc := &AccountTokenGuardService{httpClient: &http.Client{Transport: guardRecoveryRoundTripper(func(*http.Request) (*http.Response, error) {
				body, err := json.Marshal(map[string]any{"status": "failed", "error": map[string]any{"code": code}})
				require.NoError(t, err)
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
			})}}
			account := guardRecoveryAccount(1, StatusActive)
			result := svc.probe(context.Background(), defaultAccountTokenGuardConfig(), &account)
			require.Equal(t, AccountTokenGuardProbeAuth, result.State)
		})
	}
}

func TestTokenGuardRecoveryStoredAuthEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		reason string
		want   string
	}{
		{"revoked prefix", "Token revoked (401): rejected", AccountTokenGuardProbeAuth},
		{"unauthorized prefix", "Unauthorized (401): rejected", AccountTokenGuardProbeAuth},
		{"structured account test rejection", `Authentication failed (401): {"error":{"code":"token_revoked","message":"private-response"},"status":401}`, AccountTokenGuardProbeAuth},
		{"unrelated 401 code", `Authentication failed (401): {"error":{"code":"provider_unauthorized","message":"token_revoked"}}`, AccountTokenGuardProbeTransient},
		{"unstructured 401", "Authentication failed (401): token_revoked", AccountTokenGuardProbeTransient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &AccountTokenGuardService{httpClient: &http.Client{Transport: guardRecoveryRoundTripper(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 429, Body: io.NopCloser(strings.NewReader("private-response"))}, nil
			})}}
			account := guardRecoveryAccount(1, StatusError)
			account.ErrorMessage = tc.reason
			result := svc.probe(context.Background(), defaultAccountTokenGuardConfig(), &account)
			require.Equal(t, tc.want, result.State)
			require.NotContains(t, result.Detail, "private-response")
		})
	}
}

func TestTokenGuardRecoveryCancelDuringLogin(t *testing.T) {
	loginStarted := make(chan struct{})
	account := guardRecoveryAccount(1, StatusActive)
	accounts := &guardRecoveryAccounts{items: []Account{account}}
	admin := &guardRecoveryAdmin{accounts: accounts}
	svc := NewAccountTokenGuardService(nil, &guardMemoryRepo{}, accounts, admin, nil)
	svc.httpClient = &http.Client{Transport: guardRecoveryRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/probe" {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("{\"error\":{\"code\":\"invalid_token\"}}"))}, nil
		}
		close(loginStarted)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})}
	svc.config.Store(guardRecoveryConfig("http://guard.test", account.Name))
	job, err := svc.StartRun(true)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = svc.CancelRun(job.ID)
		svc.wg.Wait()
	})
	select {
	case <-loginStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("login did not start")
	}
	_, err = svc.CancelRun(job.ID)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		current, ok := svc.Job(job.ID)
		return ok && current.Status == AccountTokenGuardJobCanceled
	}, 5*time.Second, 10*time.Millisecond)
	svc.wg.Wait()
	require.Zero(t, admin.writes)
}

func TestTokenGuardRecoveryMissingLoginCredentialsKeepsError(t *testing.T) {
	account := guardRecoveryAccount(1, StatusError)
	account.ErrorMessage = `Authentication failed (401): {"error":{"code":"token_revoked"}}`
	accounts := &guardRecoveryAccounts{items: []Account{account}}
	repo := &guardMemoryRepo{}
	svc := NewAccountTokenGuardService(nil, repo, accounts, nil, nil)
	svc.httpClient = &http.Client{Transport: guardRecoveryRoundTripper(func(*http.Request) (*http.Response, error) {
		t.Error("missing login credentials must not issue an HTTP request")
		return nil, fmt.Errorf("unexpected request")
	})}
	cfg := guardRecoveryConfig("http://guard.test", account.Name)
	cfg.ReloginAccounts = nil
	svc.config.Store(cfg)
	stats, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, stats.AuthFailed)
	require.Equal(t, 1, stats.Failed)
	require.Zero(t, stats.Repaired)
	require.Equal(t, StatusError, accounts.items[0].Status)
	states, err := repo.ListStates(context.Background())
	require.NoError(t, err)
	require.Contains(t, states[0].LastFixResult, "缺少该账号的重登凭据")
}

func TestTokenGuardRecoveryProgressPreservesLatestManualRepair(t *testing.T) {
	oldFix := time.Now().Add(-time.Hour)
	newFix := time.Now()
	repo := &guardMemoryRepo{states: []AccountTokenGuardState{{AccountID: 1, LastFixAt: &oldFix, LastFixResult: "old repair"}}}
	account := guardRecoveryAccount(1, StatusActive)
	svc := NewAccountTokenGuardService(nil, repo, &guardRecoveryAccounts{items: []Account{account}}, nil, nil)
	svc.httpClient = &http.Client{Transport: guardRecoveryRoundTripper(func(*http.Request) (*http.Response, error) {
		if err := repo.UpsertState(context.Background(), AccountTokenGuardState{
			AccountID: 1, LastFixAt: &newFix, LastFixAction: "manual login", LastFixResult: "latest repair",
		}); err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	svc.config.Store(guardRecoveryConfig("http://guard.test", account.Name))
	_, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	states, err := repo.ListStates(context.Background())
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, newFix, *states[0].LastFixAt)
	require.Equal(t, "manual login", states[0].LastFixAction)
	require.Equal(t, "latest repair", states[0].LastFixResult)
}

func TestTokenGuardRecoveryRetriesFailedLoginOnNextCycle(t *testing.T) {
	var logins atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/probe" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if logins.Add(1) == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = io.WriteString(w, "private-login-response")
			return
		}
		_, _ = io.WriteString(w, `{"credential":{"access_token":"new-access","refresh_token":"new-refresh","id_token":"new-id"}}`)
	}))
	defer server.Close()
	account := guardRecoveryAccount(1, StatusError)
	account.ErrorMessage = "Token revoked (401): rejected"
	accounts := &guardRecoveryAccounts{items: []Account{account}}
	admin := &guardRecoveryAdmin{accounts: accounts}
	repo := &guardMemoryRepo{}
	svc := NewAccountTokenGuardService(nil, repo, accounts, admin, nil)
	svc.config.Store(guardRecoveryConfig(server.URL, account.Name))
	first, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, first.Failed)
	require.Zero(t, admin.writes)
	require.Equal(t, StatusError, accounts.items[0].Status)
	states, err := repo.ListStates(context.Background())
	require.NoError(t, err)
	require.Contains(t, states[0].LastFixResult, "HTTP 429")
	require.NotContains(t, states[0].LastFixResult, "private-login-response")
	second, err := svc.RunCycle(context.Background(), false)
	require.NoError(t, err)
	require.Equal(t, 1, second.Repaired)
	require.Equal(t, 1, admin.writes)
	require.Equal(t, int32(2), logins.Load())
}
