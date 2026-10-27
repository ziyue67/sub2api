//go:build integration

package repository

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type credentialRefreshPolicyCountingRepo struct {
	service.AccountRepository
	reads int
}

func (r *credentialRefreshPolicyCountingRepo) GetByID(ctx context.Context, id int64) (*service.Account, error) {
	r.reads++
	return r.AccountRepository.GetByID(ctx, id)
}

func TestCredentialOperationRefreshPolicyPostgresTypeInvalidGrantRace(t *testing.T) {
	ctx := context.Background()
	account := mustCreateAccount(t, testEntClient(t), &service.Account{
		Name: "refresh-policy-fixture", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "synthetic-old", "access_token": "synthetic-old-access",
			"expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339)},
	})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, `DELETE FROM accounts WHERE id=$1`, account.ID)
		require.NoError(t, err)
	})
	accounts := newAccountRepositoryWithSQL(testEntClient(t), integrationDB, nil)
	counted := &credentialRefreshPolicyCountingRepo{AccountRepository: accounts}
	var requests atomic.Int32
	storeErrors := make(chan error, 1)
	requestTokens := make(chan string, 1)
	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if err := r.ParseForm(); err != nil {
			storeErrors <- err
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestTokens <- r.PostForm.Get("refresh_token")
		// Simulate another authorized refresher completing after our DB read.
		// This writes only the isolated CI fixture account, before rejection.
		storeErrors <- accounts.UpdateCredentials(ctx, account.ID, map[string]any{
			"refresh_token": "synthetic-new", "access_token": "synthetic-new-access",
			"expires_at": time.Now().Add(time.Hour).Format(time.RFC3339),
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_grant","message":"opaque","cookie":"synthetic-private"}}`))
	}))
	defer server.Close()
	oauth := service.NewOpenAIOAuthService(nil, &openaiOAuthService{tokenURL: server.URL})
	executor := service.NewOpenAITokenRefresher(oauth, counted)
	result, err := service.NewOAuthRefreshAPI(counted, nil).RefreshIfNeeded(ctx, account, executor, time.Minute)
	require.NoError(t, err, "the type marker must trigger recovery from the newly persisted PostgreSQL token")
	require.Equal(t, int32(1), requests.Load())
	require.NoError(t, <-storeErrors)
	require.Equal(t, "synthetic-old", <-requestTokens)
	require.Equal(t, 2, counted.reads, "initial read plus actual race-recovery DB reread")
	require.False(t, result.Refreshed)
	require.False(t, result.LockHeld)
	require.Nil(t, result.NewCredentials)
	require.Equal(t, "synthetic-new", result.Account.GetCredential("refresh_token"))
	require.Equal(t, "synthetic-new-access", result.Account.GetCredential("access_token"))
	require.Equal(t, service.StatusActive, result.Account.Status)
	require.True(t, result.Account.Schedulable)
	require.Equal(t, "synthetic-old", account.GetCredential("refresh_token"))
	stored, err := accounts.GetByID(ctx, account.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-new", stored.GetCredential("refresh_token"), "no stale overwrite after recovery")
}
