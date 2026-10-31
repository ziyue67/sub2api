//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// These use the actual service, OAuth adapter and PostgreSQL. Only the final
// HTTP exchange is synthetic. A sequence of requests must not replay a grant
// after the first request could already have consumed its rotating token.
func TestIssue594ReviewUncertainRefreshNeverReplaysOldGrant(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()
	c := connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"review rotation"}`))
	c.ClientID = "issued-review"
	c.Subject = "verified-review"
	c.IdentityVerified = true
	c.Status = "active"
	c.Capabilities = []string{"responses"}
	raw, err := json.Marshal(map[string]any{"_connection_id": c.ID, "_account_id": strconv.FormatInt(f.accountID, 10), "_connection_kind": c.Kind, "_connection_subject": c.Subject, "client_id": c.ClientID, "access_token": "synthetic-expired", "refresh_token": "synthetic-review-old-refresh", "expires_at": "0"})
	require.NoError(t, err)
	c.CredentialsCiphertext, err = f.encryptor.Encrypt(string(raw))
	require.NoError(t, err)
	ok, err := f.repo.CASAuthConnection(ctx, c, c.Revision)
	require.NoError(t, err)
	require.True(t, ok)
	var spent atomic.Int32
	repository.Issue594InstallOfflineOAuthTransport(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/.well-known/jwks.json" {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"keys":[]}`)), Request: r}, nil
		}
		require.Equal(t, "/api/accounts/oauth/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "synthetic-review-old-refresh", r.Form.Get("refresh_token"))
		spent.Add(1)
		return nil, io.ErrUnexpectedEOF
	})
	for _, svc := range []*service.OpenAIOAuthReauthService{f.svc, f.restart(), f.restart()} {
		token, e := svc.ConnectionAccessToken(ctx, f.accountID, c.ID)
		require.Error(t, e)
		require.Empty(t, token)
	}
	require.Equal(t, int32(1), spent.Load(), "an uncertain rotating refresh must be quarantined durably before another process can replay the old grant")
}

func TestIssue594ReviewExpiredLeaseCannotCommitOldCredentials(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()
	c := f.seedHistoricalAPIKey(t, "lease fence", "synthetic-key")
	acquired, err := f.repo.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "expired-worker")
	require.NoError(t, err)
	require.True(t, acquired)
	rows, err := f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	stale := *rows[0]
	_, err = f.db.Exec(`UPDATE openai_auth_connections SET lease_until=NOW()-INTERVAL '1 second' WHERE id=$1`, c.ID)
	require.NoError(t, err)
	acquired, err = f.repo.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "current-worker")
	require.NoError(t, err)
	require.True(t, acquired)
	stale.Status = "active"
	changed, err := f.repo.CASAuthConnection(ctx, &stale, stale.Revision)
	require.NoError(t, err)
	require.False(t, changed, "an expired worker cannot commit its stale credential snapshot after a new lease owner starts")
}
