//go:build unit

package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCredentialOperationRefreshPolicyHTTPContract(t *testing.T) {
	raw, err := os.ReadFile("../../../tools/fixtures/credential-refresh-policy-errors.json")
	require.NoError(t, err)
	var cases []struct{ Name, Body, Marker, Category string }
	require.NoError(t, json.Unmarshal(raw, &cases))
	for _, sample := range cases {
		t.Run(sample.Name, func(t *testing.T) {
			server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(sample.Body))
			}))
			defer server.Close()
			client := &openaiOAuthService{tokenURL: server.URL}
			_, err := client.RefreshTokenWithClientID(context.Background(), "synthetic-old", "", "")
			var operation *service.CredentialOperationError
			require.ErrorAs(t, err, &operation)
			require.Equal(t, sample.Category, operation.Code)
			require.Equal(t, "token refresh failed: status 400, code "+sample.Marker, infraerrors.Message(err))
			require.NotContains(t, err.Error(), "synthetic-private")
		})
	}
}

type credentialRefreshHTTPRaceRepo struct {
	service.AccountRepository
	old, fresh     *service.Account
	reads, updates int
}

func (r *credentialRefreshHTTPRaceRepo) GetByID(context.Context, int64) (*service.Account, error) {
	r.reads++
	if r.reads == 1 {
		return r.old, nil
	}
	return r.fresh, nil
}
func (r *credentialRefreshHTTPRaceRepo) UpdateCredentials(context.Context, int64, map[string]any) error {
	r.updates++
	return nil
}

func TestCredentialOperationRefreshPolicyHTTPTypeInvalidGrantRace(t *testing.T) {
	var calls atomic.Int32
	requestTokens := make(chan string, 1)
	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := r.ParseForm(); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestTokens <- r.PostForm.Get("refresh_token")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"type":"invalid_grant","message":"opaque","token":"synthetic-private"}}`))
	}))
	defer server.Close()
	old := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Credentials: map[string]any{"refresh_token": "synthetic-old", "access_token": "synthetic-old-access", "expires_at": time.Now().Add(-time.Hour).Format(time.RFC3339)}}
	fresh := &service.Account{ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive,
		Credentials: map[string]any{"refresh_token": "synthetic-new", "access_token": "synthetic-new-access"}}
	repo := &credentialRefreshHTTPRaceRepo{old: old, fresh: fresh}
	oauth := service.NewOpenAIOAuthService(nil, &openaiOAuthService{tokenURL: server.URL})
	executor := service.NewOpenAITokenRefresher(oauth, repo)
	result, err := service.NewOAuthRefreshAPI(repo, nil).RefreshIfNeeded(context.Background(), old, executor, time.Minute)
	require.NoError(t, err, "the actual HTTP type invalid_grant must reach refresh race recovery")
	require.Equal(t, int32(1), calls.Load())
	require.Equal(t, "synthetic-old", <-requestTokens)
	require.Equal(t, 2, repo.reads)
	require.Zero(t, repo.updates)
	require.False(t, result.Refreshed)
	require.Same(t, fresh, result.Account)
	require.Equal(t, "synthetic-new", result.Account.GetCredential("refresh_token"))
	require.Equal(t, "synthetic-new-access", result.Account.GetCredential("access_token"))
	require.Equal(t, "synthetic-old", old.GetCredential("refresh_token"))
}
