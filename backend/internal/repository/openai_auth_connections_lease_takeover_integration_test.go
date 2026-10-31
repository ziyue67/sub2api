//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Swap leases exactly after A's acquisition and before A's registration reread.
// The real service must keep A's acquisition proof rather than adopting B's.
type takeoverConnectionRepository struct {
	service.OpenAIOAuthReauthRepository
	service.OpenAIAuthConnectionRepository
	takeover func()
	done     atomic.Bool
}

func (r *takeoverConnectionRepository) ListAuthConnections(ctx context.Context, id int64) ([]*service.OpenAIAuthConnection, error) {
	rows, err := r.OpenAIAuthConnectionRepository.ListAuthConnections(ctx, id)
	if err == nil && len(rows) > 0 && rows[0].LeaseOwner != "" && r.done.CompareAndSwap(false, true) {
		r.takeover()
		return r.OpenAIAuthConnectionRepository.ListAuthConnections(ctx, id)
	}
	return rows, err
}
func TestIssue594LeaseTakeoverCannotAdoptAnotherOwnersProof(t *testing.T) {
	for _, action := range []string{"disable", "unlink", "failure", "refresh", "revoke"} {
		t.Run(action, func(t *testing.T) {
			f := newConnectionHTTPFixture(t)
			ctx := context.Background()
			var c *service.OpenAIAuthConnection
			if action == "refresh" {
				c = connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"refresh lease takeover"}`))
			} else {
				c = f.seedHistoricalAPIKey(t, "lease takeover", "synthetic-takeover-key")
			}

			var network atomic.Int32
			if action == "refresh" {
				c.Status = "active"
				c.ClientID = "issued-takeover"
				c.Subject = "verified-takeover"
				c.IdentityVerified = true
				c.Capabilities = []string{"responses"}
				raw, e := json.Marshal(map[string]any{"_connection_id": c.ID, "_account_id": strconv.FormatInt(f.accountID, 10), "_connection_kind": c.Kind, "_connection_subject": c.Subject, "client_id": c.ClientID, "access_token": "synthetic-expired", "refresh_token": "synthetic-takeover-refresh", "expires_at": "0"})
				require.NoError(t, e)
				c.CredentialsCiphertext, e = f.encryptor.Encrypt(string(raw))
				require.NoError(t, e)
				changed, e := f.repo.CASAuthConnection(ctx, c, c.Revision)
				require.NoError(t, e)
				require.True(t, changed)
				rows, e := f.repo.ListAuthConnections(ctx, f.accountID)
				require.NoError(t, e)
				c = rows[0]
				repository.Issue594InstallOfflineOAuthTransport(t, func(*http.Request) (*http.Response, error) { network.Add(1); return nil, io.ErrUnexpectedEOF })
			}
			base := repository.NewOpenAIOAuthReauthRepository(f.db)
			wrapper := &takeoverConnectionRepository{OpenAIOAuthReauthRepository: base, OpenAIAuthConnectionRepository: base.(service.OpenAIAuthConnectionRepository)}
			var ownerA string
			wrapper.takeover = func() {
				require.NoError(t, f.db.QueryRow(`SELECT lease_owner FROM openai_auth_connections WHERE id=$1`, c.ID).Scan(&ownerA))
				require.NotEmpty(t, ownerA)
				_, e := f.db.Exec(`UPDATE openai_auth_connections SET lease_until=clock_timestamp()-INTERVAL '1 second' WHERE id=$1`, c.ID)
				require.NoError(t, e)
				acquired, e := f.repo.AcquireAuthConnectionLease(ctx, f.accountID, c.ID, "worker-B")
				require.NoError(t, e)
				require.True(t, acquired)
			}
			svc := service.NewOpenAIOAuthReauthService(wrapper, connectionDBReader{f.accounts}, f.accounts.(service.OpenAIOAuthReauthCredentialUpdater), service.NewOpenAIOAuthService(nil, repository.NewOpenAIOAuthClient()), f.encryptor, true, nil, nil)
			var err error
			if action == "refresh" {
				_, err = svc.ConnectionAccessToken(ctx, f.accountID, c.ID)
			} else if action == "revoke" {
				_, err = svc.RevokeAuthConnection(ctx, f.accountID, c.ID, c.Revision)
			} else if action == "failure" {
				err = svc.FailAuthConnection(ctx, &service.Account{ID: f.accountID, AuthConnectionID: c.ID, AuthConnectionRevision: c.Revision}, 401)
			} else {
				_, err = svc.UpdateAuthConnection(ctx, f.accountID, c.ID, service.UpdateOpenAIAuthConnectionInput{Action: action, Revision: c.Revision})
			}
			require.Zero(t, network.Load(), "a stolen lease snapshot cannot authorize an OAuth network operation")
			require.True(t, wrapper.done.Load(), "must exercise A lease → expiry → B takeover → A reread")
			require.Error(t, err, "A cannot commit using owner/epoch obtained from B's snapshot")
			rows, e := f.repo.ListAuthConnections(ctx, f.accountID)
			require.NoError(t, e)
			require.Equal(t, "active", rows[0].Status)
			require.Equal(t, c.Revision, rows[0].Revision)
			require.Equal(t, "worker-B", rows[0].LeaseOwner)
			require.NoError(t, f.repo.ReleaseAuthConnectionLease(ctx, f.accountID, c.ID, "worker-B"))
		})
	}
}
