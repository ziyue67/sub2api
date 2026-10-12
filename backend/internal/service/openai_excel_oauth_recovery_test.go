package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type excelAuditInterleavingClient struct {
	excelRefreshTestClient
	interleave func()
}

func (c *excelAuditInterleavingClient) RefreshTokenWithClientID(ctx context.Context, rt, proxy, client string) (*openai.TokenResponse, error) {
	c.interleave()
	return c.excelRefreshTestClient.RefreshTokenWithClientID(ctx, rt, proxy, client)
}

func TestExcelOAuthRecovery401DuringRefreshPreservesRotatedRT(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	old := excelTestCredentials("old", time.Now().Add(time.Minute))
	fresh := excelTestCredentials("rotated", time.Now().Add(time.Hour))
	storeExcelTestCredentials(t, svc, repo, old)
	svc.oauth.oauthClient = &excelAuditInterleavingClient{
		excelRefreshTestClient: excelRefreshTestClient{credentials: fresh},
		// Model a 401 arriving after the refresh reads its CAS snapshot but
		// before the token endpoint returns its rotated credentials.
		interleave: func() {
			require.NoError(t, svc.invalidateExcelAccessToken(context.Background(), reader.account.ID, reauthMapString(old, "access_token"), nil))
		},
	}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
	plain, decodeErr := svc.encryptor.Decrypt(repo.ciphertext)
	require.NoError(t, decodeErr)
	t.Logf("rotated RT persisted=%t; refresh error=%v", strings.Contains(plain, reauthMapString(fresh, "refresh_token")), err)
	require.NoError(t, err, "a 401 must not invalidate the in-flight refresh CAS snapshot")
	require.Equal(t, fresh["access_token"], token)
	require.Contains(t, plain, fresh["refresh_token"])
}

func TestExcelOAuthRecoveryInvalidGrantAfterConcurrentReplacement(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	old := excelTestCredentials("old", time.Now().Add(-time.Minute))
	fresh := excelTestCredentials("replaced", time.Now().Add(time.Hour))
	storeExcelTestCredentials(t, svc, repo, old)
	svc.oauth.oauthClient = &excelAuditInterleavingClient{
		excelRefreshTestClient: excelRefreshTestClient{err: errors.New("invalid_grant")},
		interleave:             func() { storeExcelTestCredentials(t, svc, repo, fresh) },
	}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
	require.NoError(t, err, "reread the newer RT before reporting reauthorization")
	require.Equal(t, fresh["access_token"], token)
}

func TestExcelOAuthRecovery401DistributedLock(t *testing.T) {
	for _, tc := range []struct {
		name    string
		granted bool
		err     error
	}{
		{name: "owned by another replica"},
		{name: "Redis unavailable", err: errors.New("unavailable")},
		{name: "acquired", granted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, reader, repo, _, _ := newExcelReauthTestService(t)
			credentials := excelTestCredentials("current", time.Now().Add(time.Hour))
			storeExcelTestCredentials(t, svc, repo, credentials)
			before := repo.ciphertext
			cache := &excelRefreshLockTestCache{granted: tc.granted, err: tc.err}
			gateway := openAIClientToolsTestService(nil)
			gateway.excelOAuthReauth = svc
			gateway.openAITokenProvider = &OpenAITokenProvider{tokenCache: cache}
			gateway.handleExcelBPSUnauthorized(context.Background(), reader.account, 401, nil, nil, reauthMapString(credentials, "access_token"))
			require.Equal(t, 1, cache.acquisitions)
			require.Equal(t, "openai:excel:account:42", cache.key)
			if tc.granted {
				require.NotEqual(t, before, repo.ciphertext)
				require.Equal(t, 1, cache.releases)
			} else {
				require.Equal(t, before, repo.ciphertext, "lock contention/error must not mutate a refresh snapshot")
				require.Zero(t, cache.releases, "never release another replica's lock")
			}
			// Failure/contended acquisition must also release the local mutex.
			unlock, err := lockExcelCredentials(context.Background(), reader.account.ID, nil, false)
			require.NoError(t, err)
			require.NotNil(t, unlock)
			unlock()
		})
	}
}

func TestExcelOAuthRecoveryConcurrentReplacementWins(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid_grant=%t", failure), func(t *testing.T) {
			for _, rejected := range []bool{false, true} {
				t.Run(fmt.Sprintf("replacement_rejected=%t", rejected), func(t *testing.T) {
					svc, reader, repo, _, _ := newExcelReauthTestService(t)
					old := excelTestCredentials("old", time.Now().Add(-time.Hour))
					rotated := excelTestCredentials("refresh-response", time.Now().Add(time.Hour))
					replacement := excelTestCredentials("new-login", time.Now().Add(time.Hour))
					if rejected {
						replacement[excelAccessTokenRejectedKey] = true
					}
					storeExcelTestCredentials(t, svc, repo, old)
					var refreshErr error
					if failure {
						refreshErr = errors.New("invalid_grant")
					}
					svc.oauth.oauthClient = &excelAuditInterleavingClient{
						excelRefreshTestClient: excelRefreshTestClient{credentials: rotated, err: refreshErr},
						interleave:             func() { storeExcelTestCredentials(t, svc, repo, replacement) },
					}
					token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
					if rejected {
						require.Error(t, err)
						require.Empty(t, token)
					} else {
						require.NoError(t, err)
						require.Equal(t, replacement["access_token"], token)
					}
					plain, err := svc.encryptor.Decrypt(repo.ciphertext)
					require.NoError(t, err)
					require.Contains(t, plain, replacement["refresh_token"])
					require.NotContains(t, plain, rotated["refresh_token"])
				})
			}
		})
	}
}

func TestExcelOAuthRecoveryRejectsReplacementIdentity(t *testing.T) {
	svc, reader, repo, _, _ := newExcelReauthTestService(t)
	storeExcelTestCredentials(t, svc, repo, excelTestCredentials("old", time.Now().Add(-time.Hour)))
	replacement := excelTestCredentials("other", time.Now().Add(time.Hour))
	replacement["chatgpt_account_id"] = "different-account"
	svc.oauth.oauthClient = &excelAuditInterleavingClient{
		excelRefreshTestClient: excelRefreshTestClient{err: errors.New("invalid_grant")},
		interleave:             func() { storeExcelTestCredentials(t, svc, repo, replacement) },
	}
	token, err := svc.ExcelAccessToken(context.Background(), reader.account, nil)
	require.Error(t, err)
	require.Empty(t, token)
	require.NotEmpty(t, repo.ciphertext, "do not delete an independently replaced grant")
}
