package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type excelReauthTestRepo struct {
	mu sync.Mutex
	*reauthTestRepo
	ciphertext string
	diagnosis  ExcelBPSCredentialState
	replaceErr error
	applied    bool
	ids        []int64
}

func (r *excelReauthTestRepo) CreateExcelTask(ctx context.Context, id int64, hash string) (*OpenAIOAuthReauthTaskRecord, error) {
	task, err := r.CreateTask(ctx, id, hash)
	if err == nil {
		task.OAuthProfile = "excel"
	}
	return task, err
}
func (r *excelReauthTestRepo) GetExcelCredentials(context.Context, int64) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ciphertext, nil
}
func (r *excelReauthTestRepo) ApplyExcelCredentials(_ context.Context, record *OpenAIOAuthReauthTaskRecord, _ map[string]any, ciphertext string, _ ...ExcelBPSCredentialState) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replaceErr != nil {
		return false, r.replaceErr
	}
	r.ciphertext = ciphertext
	r.applied = true
	r.task.Status = OpenAIOAuthReauthStatusSucceeded
	r.task.Stage = OpenAIOAuthReauthStageSucceeded
	return true, nil
}
func (r *excelReauthTestRepo) ReplaceExcelCredentials(_ context.Context, _ int64, expected, replacement string, diagnosis ...ExcelBPSCredentialState) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.replaceErr != nil {
		return false, r.replaceErr
	}
	if r.ciphertext != expected {
		return false, nil
	}
	r.ciphertext = replacement
	if len(diagnosis) > 0 {
		r.diagnosis = diagnosis[0]
	}
	return true, nil
}
func (r *excelReauthTestRepo) DeleteExcelCredentials(_ context.Context, _ int64, expected string, _ ...ExcelBPSCredentialState) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ciphertext == expected {
		r.ciphertext = ""
	}
	return nil
}
func (r *excelReauthTestRepo) ListMissingExcelAuthorizations(context.Context, int) ([]int64, error) {
	return r.ids, nil
}

func newExcelReauthTestService(t *testing.T) (*OpenAIOAuthReauthService, *reauthTestAccountReader, *excelReauthTestRepo, *reauthTestUpdater, *reauthTestRuntimeBlocker) {
	t.Helper()
	svc, reader, repo, updater, _, blocker := newReauthTestService("acct-1")
	savePasswordReauthConfig(t, svc)
	excelRepo := &excelReauthTestRepo{reauthTestRepo: repo}
	svc.repo = excelRepo
	return svc, reader, excelRepo, updater, blocker
}
func excelTestCredentials(tokenLabel string, expiry time.Time) map[string]any {
	auth := map[string]any{"chatgpt_account_id": "acct-1", "user_id": "user-1"}
	return map[string]any{
		"client_id": openai.ExcelClientID, "refresh_token": "excel-refresh-" + tokenLabel, "chatgpt_account_id": "acct-1",
		"access_token": reauthTestJWT(map[string]any{"client_id": openai.ExcelClientID, "exp": expiry.Unix(), "jti": tokenLabel, "https://api.openai.com/auth": auth}),
		// sid is a session identifier in Excel; workspace comes from the access token.
		"id_token": reauthTestJWT(map[string]any{"aud": []string{openai.ExcelClientID}, "email": "user@example.com", "sid": "excel-session", "exp": expiry.Unix(), "https://api.openai.com/auth": map[string]any{"user_id": "user-1"}}),
	}
}
func storeExcelTestCredentials(t *testing.T, svc *OpenAIOAuthReauthService, repo *excelReauthTestRepo, credentials map[string]any) {
	t.Helper()
	raw, err := json.Marshal(credentials)
	require.NoError(t, err)
	repo.ciphertext, err = svc.encryptor.Encrypt(string(raw))
	require.NoError(t, err)
}
func TestExcelOAuthDualAuthorizationPreservesCodex(t *testing.T) {
	svc, reader, repo, updater, blocker := newExcelReauthTestService(t)
	before := cloneReauthMap(reader.account.Credentials)
	task, err := svc.CreateTaskForProfile(context.Background(), 42, "excel")
	require.NoError(t, err)
	require.Equal(t, "excel", task.OAuthProfile)
	claim, err := svc.ClaimTask(context.Background(), "worker")
	require.NoError(t, err)
	require.Equal(t, "excel", claim.OAuthProfile)
	require.Equal(t, "acct-1", claim.ExpectedWorkspace)
	credentials := excelTestCredentials("one", time.Now().Add(time.Hour))
	completed, err := svc.SubmitCredentials(context.Background(), claim.TaskID, "worker", credentials, nil)
	require.NoError(t, err)
	require.Equal(t, OpenAIOAuthReauthStatusSucceeded, completed.Status)
	require.True(t, repo.applied)
	require.False(t, updater.applied)
	require.Zero(t, blocker.clearedAccountID)
	require.Equal(t, before, reader.account.Credentials)
	require.True(t, strings.HasPrefix(repo.ciphertext, "encrypted:"), "storage receives the encryptor output")
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
	require.NoError(t, err)
	require.Equal(t, credentials["access_token"], token)
	// A subsequent Codex task stays Codex even while the account routes BPS.
	reader.account.Extra = map[string]any{"openai_excel_bps": true}
	_, err = svc.CreateTask(context.Background(), 42)
	require.NoError(t, err)
	claim, err = svc.ClaimTask(context.Background(), "codex-worker")
	require.NoError(t, err)
	require.Equal(t, "codex", claim.OAuthProfile)
	_, err = svc.SubmitCredentials(context.Background(), claim.TaskID, "codex-worker", directReauthCredentials("acct-1", "user-1", "user@example.com"), nil)
	require.NoError(t, err)
	require.True(t, updater.applied)
	require.Equal(t, "new-access", updater.credentials["access_token"])
	require.NotEmpty(t, repo.ciphertext)
}
func TestExcelOAuthRejectsWrongIdentityProfileAndFailedPersistence(t *testing.T) {
	for _, scenario := range []string{"workspace", "user", "email", "client", "expired", "storage", "snapshot"} {
		t.Run(scenario, func(t *testing.T) {
			svc, reader, repo, updater, _ := newExcelReauthTestService(t)
			_, err := svc.CreateTaskForProfile(context.Background(), 42, "excel")
			require.NoError(t, err)
			claim, err := svc.ClaimTask(context.Background(), "worker")
			require.NoError(t, err)
			credentials := excelTestCredentials("one", time.Now().Add(time.Hour))
			switch scenario {
			case "workspace":
				credentials["chatgpt_account_id"] = "other"
			case "user":
				credentials["access_token"] = reauthTestJWT(map[string]any{"client_id": openai.ExcelClientID, "exp": time.Now().Add(time.Hour).Unix(), "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-1", "user_id": "other"}})
			case "email":
				credentials["id_token"] = reauthTestJWT(map[string]any{"aud": []string{openai.ExcelClientID}, "email": "other@example.com"})
			case "client":
				credentials["client_id"] = openai.ClientID
			case "expired":
				credentials = excelTestCredentials("one", time.Now().Add(-time.Hour))
			case "storage":
				repo.replaceErr = errors.New("database down")
			case "snapshot":
				reader.account.Credentials["access_token"] = "admin-replacement"
			}
			_, err = svc.SubmitCredentials(context.Background(), claim.TaskID, "worker", credentials, nil)
			require.Error(t, err)
			require.False(t, repo.applied)
			require.False(t, updater.applied)
			require.Empty(t, repo.ciphertext)
		})
	}
	svc, _, _, updater, _ := newExcelReauthTestService(t)
	_, err := svc.CreateTask(context.Background(), 42)
	require.NoError(t, err)
	claim, err := svc.ClaimTask(context.Background(), "worker")
	require.NoError(t, err)
	_, err = svc.SubmitCredentials(context.Background(), claim.TaskID, "worker", excelTestCredentials("one", time.Now().Add(time.Hour)), nil)
	require.Error(t, err)
	require.False(t, updater.applied)
}
func TestExcelOAuthQueueExistingEnabledAccounts(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	reader.account.Extra = map[string]any{"openai_excel_bps": true}
	repo.ids = []int64{42}
	require.NoError(t, svc.QueueMissingExcelAuthorizations(context.Background()))
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, "excel", repo.task.OAuthProfile)
	repo.createErr = errors.New("busy")
	require.NoError(t, svc.QueueMissingExcelAuthorizations(context.Background()))
	require.Equal(t, "excel", repo.task.OAuthProfile)
}

type excelRefreshTestClient struct {
	reauthTestOAuthClient
	calls                  int
	credentials            map[string]any
	refreshToken, clientID string
	err                    error
}

func (c *excelRefreshTestClient) RefreshTokenWithClientID(_ context.Context, rt, _ string, clientID string) (*openai.TokenResponse, error) {
	c.calls++
	c.refreshToken = rt
	c.clientID = clientID
	if c.err != nil {
		return nil, c.err
	}
	return &openai.TokenResponse{AccessToken: reauthMapString(c.credentials, "access_token"), RefreshToken: reauthMapString(c.credentials, "refresh_token"), IDToken: reauthMapString(c.credentials, "id_token"), ExpiresIn: 3600}, nil
}
func TestExcelOAuthRefreshIsIndependentAndConcurrent(t *testing.T) {
	for name, rejected := range map[string]bool{"expired": false, "rejected before expiry": true} {
		t.Run(name, func(t *testing.T) {
			svc, reader, repo, _, _ := newExcelReauthTestService(t)
			before := cloneReauthMap(reader.account.Credentials)
			expiry := time.Now().Add(-time.Hour)
			if rejected {
				expiry = time.Now().Add(time.Hour)
			}
			stale := excelTestCredentials("old", expiry)
			storeExcelTestCredentials(t, svc, repo, stale)
			if rejected {
				require.NoError(t, svc.invalidateExcelAccessToken(context.Background(), reader.account.ID, reauthMapString(stale, "access_token"), nil))
			}
			fresh := excelTestCredentials("fresh", time.Now().Add(time.Hour))
			client := &excelRefreshTestClient{credentials: fresh}
			svc.oauth.oauthClient = client
			var wg sync.WaitGroup
			results := make(chan string, 12)
			errs := make(chan error, 12)
			for i := 0; i < 12; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
					results <- token
					errs <- err
				}()
			}
			wg.Wait()
			close(results)
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			for token := range results {
				require.Equal(t, fresh["access_token"], token)
			}
			require.Equal(t, 1, client.calls)
			require.Equal(t, openai.ExcelClientID, client.clientID)
			require.Equal(t, stale["refresh_token"], client.refreshToken)
			require.Equal(t, before, reader.account.Credentials)
			plain, err := svc.encryptor.Decrypt(repo.ciphertext)
			require.NoError(t, err)
			require.Contains(t, plain, fresh["refresh_token"])
		})
	}
}
func TestExcelOAuthRefreshFailureDoesNotReturnUnsavedToken(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	storeExcelTestCredentials(t, svc, repo, excelTestCredentials("expired", time.Now().Add(-time.Hour)))
	previous := repo.ciphertext
	repo.replaceErr = errors.New("disk full")
	svc.oauth.oauthClient = &excelRefreshTestClient{credentials: excelTestCredentials("fresh", time.Now().Add(time.Hour))}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
	require.Error(t, err)
	require.Empty(t, token)
	require.Equal(t, previous, repo.ciphertext)
	require.Equal(t, "old-access", reader.account.GetCredential("access_token"))
}
func TestExcelOAuth401RefreshesOnlyUsedGrant(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	credentials := excelTestCredentials("fresh", time.Now().Add(time.Hour))
	storeExcelTestCredentials(t, svc, repo, credentials)
	gateway := openAIClientToolsTestService(nil)
	authRepo, _ := setupExcelBPSAuth(gateway)
	gateway.excelOAuthReauth = svc
	fresh := excelTestCredentials("refreshed", time.Now().Add(time.Hour))
	client := &excelRefreshTestClient{credentials: fresh}
	svc.oauth.oauthClient = client
	previous := repo.ciphertext
	gateway.handleExcelBPSUnauthorized(context.Background(), reader.account, http.StatusUnauthorized, http.Header{}, []byte(`{}`), "old-excel-token")
	require.Equal(t, previous, repo.ciphertext)
	gateway.handleExcelBPSUnauthorized(context.Background(), reader.account, http.StatusUnauthorized, http.Header{}, []byte(`{}`), reauthMapString(credentials, "access_token"))
	require.NotEmpty(t, repo.ciphertext, "401 must preserve the refresh token")
	require.Equal(t, "auth_failed", repo.diagnosis.Status)
	plain, err := svc.encryptor.Decrypt(repo.ciphertext)
	require.NoError(t, err)
	require.Contains(t, plain, credentials["refresh_token"])
	require.Zero(t, client.calls, "401 handling must not block on an OAuth request")
	require.Zero(t, authRepo.errorCalls+authRepo.tempCalls)
	require.False(t, gateway.isOpenAIAccountRuntimeBlocked(reader.account))
	require.Equal(t, "old-access", reader.account.GetCredential("access_token"))
	token, err := gateway.getExcelBPSAccessToken(context.Background(), reader.account)
	require.NoError(t, err)
	require.Equal(t, fresh["access_token"], token)
	require.Equal(t, 1, client.calls)
	require.Equal(t, "not_expired", repo.diagnosis.Status)
	require.Equal(t, credentials["refresh_token"], client.refreshToken)
	require.Equal(t, openai.ExcelClientID, client.clientID)
	// A late 401 from the old access token cannot reject the rotated grant.
	gateway.handleExcelBPSUnauthorized(context.Background(), reader.account, http.StatusUnauthorized, http.Header{}, []byte(`{}`), reauthMapString(credentials, "access_token"))
	token, err = gateway.getExcelBPSAccessToken(context.Background(), reader.account)
	require.NoError(t, err)
	require.Equal(t, fresh["access_token"], token)
	require.Equal(t, 1, client.calls)
	require.Nil(t, repo.task, "refresh must not create a password login task")
}

type excelRefreshLockTestCache struct {
	OpenAITokenCache
	acquisitions int
	releases     int
	granted      bool
	err          error
	key          string
}

func (c *excelRefreshLockTestCache) AcquireRefreshLock(_ context.Context, key string, _ time.Duration) (bool, error) {
	c.acquisitions++
	c.key = key
	return c.granted, c.err
}
func (c *excelRefreshLockTestCache) ReleaseRefreshLock(context.Context, string) error {
	c.releases++
	return nil
}
func TestExcelOAuthReadyTokenDoesNotLockButRefreshFailsClosed(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	ready := excelTestCredentials("fresh", time.Now().Add(time.Hour))
	storeExcelTestCredentials(t, svc, repo, ready)
	cache := &excelRefreshLockTestCache{err: errors.New("Redis unavailable")}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, cache)
	require.NoError(t, err)
	require.Equal(t, ready["access_token"], token)
	require.Zero(t, cache.acquisitions)
	storeExcelTestCredentials(t, svc, repo, excelTestCredentials("expired", time.Now().Add(-time.Hour)))
	client := &excelRefreshTestClient{credentials: ready}
	svc.oauth.oauthClient = client
	token, err = svc.ExcelAccessToken(context.Background(), reader.account, cache)
	require.Error(t, err)
	require.Empty(t, token)
	require.Zero(t, client.calls)
	require.Equal(t, "openai:excel:account:42", cache.key)
	require.NotEqual(t, OpenAITokenCacheKey(reader.account), cache.key)
}
func TestExcelOAuthInvalidGrantRequeuesWithoutChangingCodex(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	storeExcelTestCredentials(t, svc, repo, excelTestCredentials("expired", time.Now().Add(-time.Hour)))
	svc.oauth.oauthClient = &excelRefreshTestClient{err: errors.New("invalid_grant")}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
	require.Error(t, err)
	require.Empty(t, token)
	require.Empty(t, repo.ciphertext)
	require.Equal(t, "old-refresh", reader.account.GetCredential("refresh_token"))
}

func (r *excelReauthTestRepo) GetLatestExcelTask(context.Context, int64) (*OpenAIOAuthReauthTaskRecord, error) {
	if r.task != nil && r.task.OAuthProfile == "excel" {
		return r.task, nil
	}
	return nil, nil
}

func (r *excelReauthTestRepo) PrepareMissingExcelRoutes(context.Context) error { return nil }

func TestExcelOAuthRejectedTokenRefreshFailures(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		invalidGrant bool
	}{
		{name: "transport", err: errors.New("connection reset")},
		{name: "rate limited", err: errors.New("HTTP 429")},
		{name: "upstream unavailable", err: errors.New("HTTP 503")},
		{name: "invalid refresh token", err: errors.New("invalid_grant"), invalidGrant: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, reader, repo, _, _ := newExcelReauthTestService(t)
			credentials := excelTestCredentials("rejected", time.Now().Add(time.Hour))
			storeExcelTestCredentials(t, svc, repo, credentials)
			client := &excelRefreshTestClient{err: tc.err}
			svc.oauth.oauthClient = client
			require.NoError(t, svc.invalidateExcelAccessToken(context.Background(), reader.account.ID, reauthMapString(credentials, "access_token"), nil))
			marked := repo.ciphertext
			// Repeated 401s must not rewrite the grant while refresh is in flight.
			require.NoError(t, svc.invalidateExcelAccessToken(context.Background(), reader.account.ID, reauthMapString(credentials, "access_token"), nil))
			require.Equal(t, marked, repo.ciphertext)
			token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
			require.Error(t, err)
			require.Empty(t, token, "never return an access token rejected by BPS")
			require.Equal(t, 1, client.calls)
			require.Equal(t, "old-refresh", reader.account.GetCredential("refresh_token"))
			if tc.invalidGrant {
				require.Empty(t, repo.ciphertext)
				require.ErrorContains(t, err, "reauthorization")
				return
			}
			require.Equal(t, marked, repo.ciphertext)
			require.NotContains(t, err.Error(), "reauthoriz")
			// A later request recovers using the retained RT, without a login task.
			client.err = nil
			client.credentials = excelTestCredentials("recovered", time.Now().Add(time.Hour))
			token, err = svc.ExcelAccessToken(context.Background(), reader.account, nil)
			require.NoError(t, err)
			require.Equal(t, client.credentials["access_token"], token)
			require.Equal(t, credentials["refresh_token"], client.refreshToken)
			require.Nil(t, repo.task)
		})
	}
}

func TestExcelOAuthRejectedTokenUsesRefreshLock(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	credentials := excelTestCredentials("rejected", time.Now().Add(time.Hour))
	storeExcelTestCredentials(t, svc, repo, credentials)
	require.NoError(t, svc.invalidateExcelAccessToken(context.Background(), reader.account.ID, reauthMapString(credentials, "access_token"), nil))
	marked := repo.ciphertext
	client := &excelRefreshTestClient{credentials: excelTestCredentials("rotated", time.Now().Add(time.Hour))}
	svc.oauth.oauthClient = client
	cache := &excelRefreshLockTestCache{granted: false}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, cache)
	require.Error(t, err)
	require.Empty(t, token)
	require.Zero(t, client.calls)
	require.Equal(t, marked, repo.ciphertext)
	cache.granted = true
	token, err = svc.ExcelAccessToken(context.Background(), reader.account, cache)
	require.NoError(t, err)
	require.Equal(t, client.credentials["access_token"], token)
	require.Equal(t, 1, client.calls)
}
