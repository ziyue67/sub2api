//go:build integration

package repository_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type issue594OfflineInference struct {
	t        *testing.T
	endpoint string
	mu       sync.Mutex
	calls    []string
}

func (u *issue594OfflineInference) Do(r *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.mu.Lock()
	u.calls = append(u.calls, r.Header.Get("Authorization")+" "+r.URL.String())
	u.mu.Unlock()
	require.Contains(u.t, []string{"api.openai.com", "chatgpt.com", "bps.openai.com"}, r.URL.Host)
	clone := r.Clone(r.Context())
	target := *clone.URL
	target.Scheme = "http"
	target.Host = u.endpoint
	clone.URL = &target
	clone.Host = ""
	return http.DefaultClient.Do(clone)
}
func (u *issue594OfflineInference) DoWithTLS(r *http.Request, p string, id int64, n int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(r, p, id, n)
}

func TestIssue594ProductionGatewayConnectionRouteAndAccountSafety(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()
	first := f.seedHistoricalAPIKey(t, "First route", "synthetic-route-one")
	second := f.seedHistoricalAPIKey(t, "Second route", "synthetic-route-two")
	connectionResponse(t, f.request("PATCH", "/"+first.ID, fmt.Sprintf(`{"action":"select","revision":%d}`, first.Revision)))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/responses", r.URL.Path)
		require.Contains(t, []string{"Bearer synthetic-route-one", "Bearer synthetic-route-two"}, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":3,\"output_tokens\":1,\"total_tokens\":4}}}\n\n"))
	}))
	defer upstream.Close()
	transport := &issue594OfflineInference{t: t, endpoint: strings.TrimPrefix(upstream.URL, "http://")}
	cfg := &config.Config{}
	cfg.Default.RateMultiplier = 1
	gateway := service.NewOpenAIGatewayService(f.accounts, nil, nil, nil, nil, nil, nil, nil, cfg, nil, nil, service.NewBillingService(cfg, nil), nil, nil, transport, &service.DeferredService{}, nil, nil, nil, nil, nil, nil, nil, service.WithOpenAIAuthConnections(f.svc))
	forward := func(session string) (*service.OpenAIForwardResult, error) {
		a, e := f.accounts.GetByID(ctx, f.accountID)
		require.NoError(t, e)
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		body := []byte(`{"model":"synthetic-model","stream":false,"input":"synthetic input"}`)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(string(body)))
		c.Request.Header.Set("Session-Id", session)
		gateway.GenerateSessionHash(c, body)
		return gateway.Forward(ctx, c, a, body)
	}
	cachedLegacy := &service.Account{ID: f.accountID, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Credentials: map[string]any{"access_token": "synthetic-legacy-token"}}
	leaked, _, policyErr := gateway.GetAccessToken(ctx, cachedLegacy)
	require.Error(t, policyErr)
	require.Empty(t, leaked, "old account snapshots cannot return Codex tokens after managed selection")
	result, err := forward("session-one")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, first.ID, result.AuthConnectionID)
	connectionResponse(t, f.request("PATCH", "/"+second.ID, fmt.Sprintf(`{"action":"select","revision":%d}`, second.Revision)))
	result, err = forward("session-one")
	require.NoError(t, err)
	require.Equal(t, first.ID, result.AuthConnectionID, "saved conversation stays on its authorization connection")
	result, err = forward("session-two")
	require.NoError(t, err)
	require.Equal(t, second.ID, result.AuthConnectionID)
	// A persisted API key parent remains an inference route after management is
	// closed. Exercise the actual repository and gateway, then resume OAuth
	// lifecycle checks below without any automatic production data conversion.
	_, err = f.db.Exec(`UPDATE accounts SET type='apikey' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	deniedManagement := f.request("GET", "", "")
	require.Equal(t, 400, deniedManagement.Code, deniedManagement.Body.String())
	result, err = forward("historical-apikey-parent")
	require.NoError(t, err)
	require.Equal(t, second.ID, result.AuthConnectionID)
	_, err = f.db.Exec(`UPDATE accounts SET type='oauth' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	rows, err := f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	for _, c := range rows {
		if c.ID == first.ID {
			first = c
		}
	}
	connectionResponse(t, f.request("PATCH", "/"+first.ID, fmt.Sprintf(`{"action":"disable","revision":%d}`, first.Revision)))
	_, err = forward("session-one")
	require.Error(t, err, "disabled sticky grant cannot silently switch to peer or Codex")
	transport.mu.Lock()
	callsBefore := len(transport.calls)
	transport.mu.Unlock()
	_, err = f.db.Exec(`UPDATE accounts SET status='error',error_message='synthetic account ban' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	_, err = forward("session-three")
	require.Error(t, err)
	transport.mu.Lock()
	require.Len(t, transport.calls, callsBefore, "account-wide security cannot be bypassed by another connection")
	transport.mu.Unlock()
	// Capability and security checks do not give a new allowance to a peer.
	_, err = f.db.Exec(`UPDATE accounts SET status='active',rate_limited_at=NOW(),rate_limit_reset_at=NOW()+INTERVAL '5 minutes' WHERE id=$1`, f.accountID)
	require.NoError(t, err)
	_, err = forward("session-quota")
	require.Error(t, err)
	transport.mu.Lock()
	require.Len(t, transport.calls, callsBefore)
	transport.mu.Unlock()
	usageRepo := repository.NewOpenAIOAuthReauthRepository(f.db).(interface {
		RecordAuthConnectionUsage(context.Context, int64, int64, string, string) error
	})
	require.NoError(t, usageRepo.RecordAuthConnectionUsage(ctx, f.accountID, 900, second.ID, "synthetic-request"))
	var attributed string
	require.NoError(t, f.db.QueryRow(`SELECT connection_id::text FROM openai_auth_connection_usage WHERE account_id=$1 AND request_id='synthetic-request'`, f.accountID).Scan(&attributed))
	require.Equal(t, second.ID, attributed)
}
