package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type twoFALoginSettings struct {
	SettingRepository
	mu     sync.Mutex
	raw    string
	setErr error
	writes int
}

func (s *twoFALoginSettings) GetValue(context.Context, string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.raw, nil
}

func (s *twoFALoginSettings) Set(_ context.Context, _, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.setErr != nil {
		return s.setErr
	}
	s.raw = value
	s.writes++
	return nil
}

func newTwoFATestService(t *testing.T, server *httptest.Server) *AccountTokenGuardService {
	t.Helper()
	cfg := defaultAccountTokenGuardConfig()
	cfg.Enabled, cfg.AutoRelogin = false, false
	cfg.ReloginEndpoint = server.URL
	cfg.ReloginHeaders["X-Test-Client"] = "{{uuid}}"
	raw, err := json.Marshal(cfg)
	require.NoError(t, err)
	svc := NewAccountTokenGuardService(&twoFALoginSettings{raw: string(raw)}, nil, nil, nil, nil)
	svc.httpClient = server.Client()
	t.Cleanup(svc.clearTwoFALogins)
	return svc
}

func waitTwoFALogin(t *testing.T, svc *AccountTokenGuardService, id string) *OpenAITwoFALoginJob {
	t.Helper()
	var job *OpenAITwoFALoginJob
	require.Eventually(t, func() bool {
		var ok bool
		job, ok = svc.TwoFALogin(id)
		return ok && job.Status != "running"
	}, 3*time.Second, 5*time.Millisecond)
	return job
}

func TestTwoFALoginInitialLoginWithoutExistingAccount(t *testing.T) {
	requests := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error("invalid login payload")
		}
		payload["test_client"] = r.Header.Get("X-Test-Client")
		payload["relogin_header"] = r.Header.Get("X-Session-Studio-Relogin")
		requests <- payload
		_, _ = io.WriteString(w, `{"type":"result","payload":{"credential":{"access_token":"access","refresh_token":"refresh","id_token":"id","account_id":"workspace","password":"echo-password","mfa_secret":"echo-secret","model_mapping":{"bad":"bad"}}}}`)
	}))
	defer server.Close()
	svc := newTwoFATestService(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	job, err := svc.StartTwoFALogin(ctx, AccountTokenGuardReloginAccount{Email: " USER@example.com ", Password: "p,a!ss", MFASecret: "TEST-SECRET"})
	require.NoError(t, err)
	cancel() // The HTTP request may finish immediately after acceptance.
	result := waitTwoFALogin(t, svc, job.ID)
	require.Equal(t, "succeeded", result.Status)
	cfg, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.False(t, cfg.Enabled)
	require.False(t, cfg.AutoRelogin)
	require.Equal(t, []AccountTokenGuardReloginAccount{{Email: "user@example.com", Password: "p,a!ss", MFASecret: "TEST-SECRET"}}, cfg.ReloginAccounts)
	require.Equal(t, "user@example.com", result.Credential["email"])
	require.Equal(t, "workspace", result.Credential["account_id"])
	require.NotContains(t, result.Credential, "password")
	require.NotContains(t, result.Credential, "mfa_secret")
	require.NotContains(t, result.Credential, "model_mapping")
	payload := <-requests
	require.Equal(t, "password_2fa", payload["auth_mode"])
	require.Equal(t, "start", payload["action"])
	require.Equal(t, "p,a!ss", payload["password"])
	require.Equal(t, "1", payload["relogin_header"])
	require.NotEmpty(t, payload["test_client"])
	require.NotEqual(t, "{{uuid}}", payload["test_client"])
	result.Credential["access_token"] = "mutated"
	original, ok := svc.TwoFALogin(job.ID)
	require.True(t, ok)
	require.Equal(t, "access", original.Credential["access_token"])
	svc.DeleteTwoFALogin(job.ID)
	_, ok = svc.TwoFALogin(job.ID)
	require.False(t, ok)
}

func TestTwoFALoginFailureDoesNotExposeProviderData(t *testing.T) {
	for _, body := range []string{
		`{"error":{"code":"password-canary-secret","message":"secret"}}`,
		`{"credential":{"access_token":"access-only-secret"}}`,
	} {
		t.Run("redacted", func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			svc := newTwoFATestService(t, server)
			job, err := svc.StartTwoFALogin(context.Background(), AccountTokenGuardReloginAccount{Email: "a@example.com", Password: "secret", MFASecret: "secret"})
			require.NoError(t, err)
			result := waitTwoFALogin(t, svc, job.ID)
			require.Equal(t, "failed", result.Status)
			raw, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(raw), "secret")
			require.Nil(t, result.Credential)
			cfg, err := svc.GetConfig(context.Background())
			require.NoError(t, err)
			require.Empty(t, cfg.ReloginAccounts)
			settings, ok := svc.settings.(*twoFALoginSettings)
			require.True(t, ok)
			require.Zero(t, settings.writes)
		})
	}
}

func TestTwoFALoginCancellation(t *testing.T) {
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	svc := newTwoFATestService(t, server)
	job, err := svc.StartTwoFALogin(context.Background(), AccountTokenGuardReloginAccount{Email: "a@example.com", Password: "secret", MFASecret: "secret"})
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("login did not start")
	}
	svc.DeleteTwoFALogin(job.ID)
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream login was not canceled")
	}
	_, ok := svc.TwoFALogin(job.ID)
	require.False(t, ok)
	cfg, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.Empty(t, cfg.ReloginAccounts)
}

func TestTwoFALoginRejectsInvalidInputBeforeNetwork(t *testing.T) {
	svc := &AccountTokenGuardService{}
	for _, entry := range []AccountTokenGuardReloginAccount{
		{Email: "not-email", Password: "p", MFASecret: "s"},
		{Email: "User <a@example.com>", Password: "p", MFASecret: "s"},
		{Email: "a@example.com", Password: "p"},
		{Email: "a@example.com", Password: strings.Repeat("p", 4097), MFASecret: "s"},
	} {
		_, err := svc.StartTwoFALogin(context.Background(), entry)
		require.Error(t, err)
	}
}

func TestTwoFALoginMergesLatestGuardConfig(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		_, _ = io.WriteString(w, `{"type":"result","payload":{"credential":{"access_token":"test-access","refresh_token":"test-refresh","id_token":"test-id"}}}`)
	}))
	defer server.Close()
	svc := newTwoFATestService(t, server)
	// Release even if an assertion fails, so the server can close.
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	job, err := svc.StartTwoFALogin(context.Background(), AccountTokenGuardReloginAccount{Email: "USER@example.com", Password: "new-password", MFASecret: "new-mfa"})
	require.NoError(t, err)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("login did not start")
	}
	cfg, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	cfg.GroupIDs = []int64{7}
	cfg.IntervalSeconds = 123
	cfg.ReloginAccounts = []AccountTokenGuardReloginAccount{
		{Email: "user@example.com", Password: "old-password", MFASecret: "old-mfa"},
		{Email: "other@example.com", Password: "other-password", MFASecret: "other-mfa"},
	}
	cfg, err = svc.SaveConfig(context.Background(), cfg)
	require.NoError(t, err)
	unblock()
	require.Equal(t, "succeeded", waitTwoFALogin(t, svc, job.ID).Status)
	saved, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	cfg.ReloginAccounts[1] = AccountTokenGuardReloginAccount{Email: "user@example.com", Password: "new-password", MFASecret: "new-mfa"}
	require.Equal(t, cfg, saved)
	// A fresh service must see the credentials, not just the original cache.
	restarted := NewAccountTokenGuardService(svc.settings, nil, nil, nil, nil)
	persisted, err := restarted.GetConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, saved, persisted)
}

func TestTwoFALoginConcurrentGuardEnrollment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"type":"result","payload":{"credential":{"access_token":"test-access","refresh_token":"test-refresh","id_token":"test-id"}}}`)
	}))
	defer server.Close()
	svc := newTwoFATestService(t, server)
	var jobs []*OpenAITwoFALoginJob
	for i := 0; i < 10; i++ {
		job, err := svc.StartTwoFALogin(context.Background(), AccountTokenGuardReloginAccount{Email: fmt.Sprintf("user%d@example.com", i), Password: "test-password", MFASecret: "test-mfa"})
		require.NoError(t, err)
		jobs = append(jobs, job)
	}
	for _, job := range jobs {
		require.Equal(t, "succeeded", waitTwoFALogin(t, svc, job.ID).Status)
	}
	cfg, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, cfg.ReloginAccounts, 10)
}

func TestTwoFALoginGuardSaveFailureDoesNotReportSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"type":"result","payload":{"credential":{"access_token":"test-access","refresh_token":"test-refresh","id_token":"test-id"}}}`)
	}))
	defer server.Close()
	svc := newTwoFATestService(t, server)
	settings, ok := svc.settings.(*twoFALoginSettings)
	require.True(t, ok)
	settings.setErr = errors.New("storage-password-canary")
	job, err := svc.StartTwoFALogin(context.Background(), AccountTokenGuardReloginAccount{Email: "user@example.com", Password: "test-password", MFASecret: "test-mfa"})
	require.NoError(t, err)
	result := waitTwoFALogin(t, svc, job.ID)
	require.Equal(t, "failed", result.Status)
	require.Nil(t, result.Credential)
	raw, err := json.Marshal(result)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "canary")
	cfg, err := svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.Empty(t, cfg.ReloginAccounts)
	require.Empty(t, svc.currentConfig().ReloginAccounts)
	// The same input can be retried after persistence recovers.
	settings.mu.Lock()
	settings.setErr = nil
	settings.mu.Unlock()
	retry, err := svc.StartTwoFALogin(context.Background(), AccountTokenGuardReloginAccount{Email: "user@example.com", Password: "test-password", MFASecret: "test-mfa"})
	require.NoError(t, err)
	require.Equal(t, "succeeded", waitTwoFALogin(t, svc, retry.ID).Status)
	cfg, err = svc.GetConfig(context.Background())
	require.NoError(t, err)
	require.Len(t, cfg.ReloginAccounts, 1)
}
