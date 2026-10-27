//go:build unit

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type credentialRefreshPolicyCase struct {
	Name         string `json:"name"`
	Body         string `json:"body"`
	Marker       string `json:"marker"`
	Category     string `json:"category"`
	NonRetryable bool   `json:"non_retryable"`
	Shared       bool   `json:"shared"`
	Race         bool   `json:"race"`
}

func credentialRefreshPolicyCases(t *testing.T) []credentialRefreshPolicyCase {
	t.Helper()
	raw, err := os.ReadFile("../../../tools/fixtures/credential-refresh-policy-errors.json")
	require.NoError(t, err)
	var cases []credentialRefreshPolicyCase
	require.NoError(t, json.Unmarshal(raw, &cases))
	require.GreaterOrEqual(t, len(cases), 48)
	return cases
}

func TestCredentialOperationRefreshPolicyStructuredSignals(t *testing.T) {
	for _, sample := range credentialRefreshPolicyCases(t) {
		t.Run(sample.Name, func(t *testing.T) {
			err := NewCredentialOperationRefreshError(400, []byte(sample.Body))
			var operation *CredentialOperationError
			require.ErrorAs(t, err, &operation)
			require.Equal(t, sample.Category, operation.Code)
			require.Equal(t, sample.Category, credentialOperationEnvelope([]byte(sample.Body)))
			require.Equal(t, "token refresh failed: status 400, code "+sample.Marker, infraerrors.Message(err))
			require.Equal(t, sample.Race, isInvalidGrantError(err))
			require.Equal(t, sample.NonRetryable, isNonRetryableRefreshError(err))
			require.Equal(t, sample.Shared, isSharedProviderRefreshError(err))
			require.NotContains(t, err.Error(), "synthetic-private")
			require.Less(t, len(err.Error()), 300)
		})
	}
}

func TestCredentialOperationRefreshPolicyTypeInvalidGrantRaceRecovery(t *testing.T) {
	account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive,
		Credentials: map[string]any{"refresh_token": "synthetic-old", "access_token": "synthetic-old-access"}}
	fresh := snapshotOAuthRefreshAccount(account)
	fresh.Credentials = map[string]any{"refresh_token": "synthetic-new", "access_token": "synthetic-new-access"}
	repo := &refreshAPIAccountRepoWithRace{refreshAPIAccountRepo: refreshAPIAccountRepo{account: account}, raceAccount: fresh}
	cache := &refreshAPICacheStub{lockResult: true}
	executor := &refreshAPIExecutorStub{needsRefresh: true,
		err: NewCredentialOperationRefreshError(400, []byte(`{"error":{"type":"invalid_grant","message":"opaque"}}`))}
	result, err := NewOAuthRefreshAPI(repo, cache).RefreshIfNeeded(context.Background(), account, executor, time.Minute)
	require.NoError(t, err, "type invalid_grant must recover after rereading the newly saved token")
	require.Equal(t, 2, repo.getByIDCalls)
	require.Equal(t, 1, executor.refreshCalls)
	require.Equal(t, 1, cache.releaseCalls)
	require.Zero(t, repo.updateCalls)
	require.False(t, result.Refreshed)
	require.False(t, result.LockHeld)
	require.Same(t, fresh, result.Account)
	require.Equal(t, "synthetic-new", result.Account.GetCredential("refresh_token"))
	require.Equal(t, "synthetic-old", account.GetCredential("refresh_token"))
}

func TestCredentialOperationRefreshPolicyTerminalAndClientScope(t *testing.T) {
	for _, sample := range credentialRefreshPolicyCases(t) {
		if !sample.NonRetryable {
			continue
		}
		t.Run(sample.Name, func(t *testing.T) {
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"refresh_token": "synthetic-old"}}
			repo := &tokenRefreshAccountRepo{}
			invalidator := &tokenCacheInvalidatorStub{}
			svc := &TokenRefreshService{accountRepo: repo, cacheInvalidator: invalidator,
				refreshPolicy: DefaultBackgroundRefreshPolicy(), cfg: &config.TokenRefreshConfig{MaxRetries: 3}}
			refresher := &tokenRefresherStub{err: NewCredentialOperationRefreshError(400, []byte(sample.Body))}
			err := svc.refreshWithRetry(context.Background(), account, refresher, nil, time.Minute)
			require.Error(t, err)
			require.Equal(t, 1, refresher.calls, "structured terminal errors must not consume further attempts")
			state := &tokenRefreshProviderState{service: svc}
			state.recordResult(err)
			if sample.Shared {
				var provider *providerConfigurationRefreshError
				var permanent *accountPermanentRefreshError
				require.ErrorAs(t, err, &provider)
				require.False(t, errors.As(err, &permanent))
				require.True(t, state.isTripped(), "client/scope configuration must contain the provider cycle")
				require.Zero(t, repo.setErrorCalls)
				require.Zero(t, invalidator.calls)
			} else {
				var permanent *accountPermanentRefreshError
				require.ErrorAs(t, err, &permanent)
				require.False(t, state.isTripped())
				require.Equal(t, 1, repo.setErrorCalls)
				require.Equal(t, 1, invalidator.calls)
			}
			require.Zero(t, repo.setTempUnschedCalls)
			require.Zero(t, repo.updateCalls)
			require.NotContains(t, repo.lastErrorMessage, "synthetic-private")
			require.Equal(t, StatusActive, account.Status)
			require.True(t, account.Schedulable)
			require.Equal(t, "synthetic-old", account.GetCredential("refresh_token"))
		})
	}
}
