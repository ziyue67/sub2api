//go:build integration

package repository_test

import (
	"context"
	"encoding/json"
	"fmt"
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

func TestIssue594StagedRefreshSurvivesFinishFailureAndServiceRestart(t *testing.T) {
	for _, disable := range []bool{false, true} {
		t.Run(fmt.Sprintf("disable=%t", disable), func(t *testing.T) {
			f := newConnectionHTTPFixture(t)
			ctx := context.Background()
			c := connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"recover candidate"}`))
			c.ClientID = "issued-recovery"
			c.Subject = "verified-recovery"
			c.IdentityVerified = true
			c.Status = "active"
			c.Capabilities = []string{"responses"}
			raw, e := json.Marshal(map[string]any{"_connection_id": c.ID, "_account_id": strconv.FormatInt(f.accountID, 10), "_connection_kind": c.Kind, "_connection_subject": c.Subject, "client_id": c.ClientID, "access_token": "synthetic-expired", "refresh_token": "synthetic-old-recovery", "expires_at": "0"})
			require.NoError(t, e)
			c.CredentialsCiphertext, e = f.encryptor.Encrypt(string(raw))
			require.NoError(t, e)
			changed, e := f.repo.CASAuthConnection(ctx, c, c.Revision)
			require.NoError(t, e)
			require.True(t, changed)
			var posts atomic.Int32
			repository.Issue594InstallOfflineOAuthTransport(t, func(r *http.Request) (*http.Response, error) {
				body := `{"keys":[]}`
				if r.URL.Path != "/.well-known/jwks.json" {
					require.Equal(t, "/api/accounts/oauth/token", r.URL.Path)
					require.NoError(t, r.ParseForm())
					require.Equal(t, "synthetic-old-recovery", r.Form.Get("refresh_token"))
					require.Equal(t, "issued-recovery", r.Form.Get("client_id"))
					posts.Add(1)
					body = `{"access_token":"synthetic-new-recovery","refresh_token":"synthetic-new-rotating-recovery","expires_in":3600,"token_type":"Bearer"}`
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})
			// Fail the actual DB finalization, after the adapter's rotating response and
			// encrypted journal have committed. This does not replace repository methods.
			trigger := fmt.Sprintf("issue594_finish_%d", f.accountID)
			_, e = f.db.Exec(fmt.Sprintf(`CREATE FUNCTION %s() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN IF OLD.id='%s'::uuid AND OLD.refresh_candidate_ciphertext<>'' AND NEW.refresh_state='idle' AND NEW.credentials_ciphertext<>OLD.credentials_ciphertext THEN RAISE EXCEPTION 'isolated finish failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER %s BEFORE UPDATE ON openai_auth_connections FOR EACH ROW EXECUTE FUNCTION %s()`, trigger, c.ID, trigger, trigger))
			require.NoError(t, e)
			t.Cleanup(func() {
				_, _ = f.db.Exec(fmt.Sprintf(`DROP TRIGGER IF EXISTS %s ON openai_auth_connections; DROP FUNCTION IF EXISTS %s()`, trigger, trigger))
			})
			token, e := f.svc.ConnectionAccessToken(ctx, f.accountID, c.ID)
			require.Error(t, e)
			require.Empty(t, token)
			require.Equal(t, int32(1), posts.Load())
			records, e := f.repo.ListAuthConnections(ctx, f.accountID)
			require.NoError(t, e)
			require.Len(t, records, 1)
			saved := records[0]
			require.NotEmpty(t, saved.RefreshCandidateCiphertext)
			require.NotEqual(t, "idle", saved.RefreshState)
			require.NotContains(t, saved.RefreshCandidateCiphertext, "synthetic-new-rotating-recovery")
			plain, e := f.encryptor.Decrypt(saved.CredentialsCiphertext)
			require.NoError(t, e)
			require.Contains(t, plain, "synthetic-old-recovery", "live token is not replaced before finish commits")
			_, e = f.db.Exec(fmt.Sprintf(`DROP TRIGGER %s ON openai_auth_connections`, trigger))
			require.NoError(t, e)
			if disable {
				disabled, e := f.svc.UpdateAuthConnection(ctx, f.accountID, c.ID, service.UpdateOpenAIAuthConnectionInput{Action: "disable", Revision: saved.Revision})
				require.NoError(t, e)
				require.Equal(t, "disabled", disabled.Status)
				_, e = f.restart().ConnectionAccessToken(ctx, f.accountID, c.ID)
				require.Error(t, e, "recovery cannot revive disabled credentials")
				_, e = f.restart().UpdateAuthConnection(ctx, f.accountID, c.ID, service.UpdateOpenAIAuthConnectionInput{Action: "enable", Revision: disabled.Revision})
				require.Error(t, e, "disabled uncertain grant needs fresh authorization")
				rows, e := f.repo.ListAuthConnections(ctx, f.accountID)
				require.NoError(t, e)
				require.Equal(t, "disabled", rows[0].Status)
				require.NotEmpty(t, rows[0].RefreshCandidateCiphertext, "retain encrypted journal for diagnosis until explicit reauthorization/unlink")
				unlinked, e := f.restart().UpdateAuthConnection(ctx, f.accountID, c.ID, service.UpdateOpenAIAuthConnectionInput{Action: "unlink", Revision: rows[0].Revision})
				require.NoError(t, e)
				require.Equal(t, "unlinked", unlinked.Status)
				rows, e = f.repo.ListAuthConnections(ctx, f.accountID)
				require.NoError(t, e)
				require.Empty(t, rows[0].RefreshCandidateCiphertext)
				require.Empty(t, rows[0].CredentialsCiphertext)
			} else {
				token, e = f.restart().ConnectionAccessToken(ctx, f.accountID, c.ID)
				require.NoError(t, e)
				require.Equal(t, "synthetic-new-recovery", token)
				rows, e := f.repo.ListAuthConnections(ctx, f.accountID)
				require.NoError(t, e)
				require.Equal(t, "active", rows[0].Status)
				require.Equal(t, "idle", rows[0].RefreshState)
				require.Empty(t, rows[0].RefreshCandidateCiphertext)
				plain, e = f.encryptor.Decrypt(rows[0].CredentialsCiphertext)
				require.NoError(t, e)
				require.Contains(t, plain, "synthetic-new-rotating-recovery")
			}
			require.Equal(t, int32(1), posts.Load(), "service restart/recovery/disable must never repeat the rotating POST")
		})
	}
}
