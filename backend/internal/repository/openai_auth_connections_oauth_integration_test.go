//go:build integration

package repository_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestIssue594ActualOAuthAdapterHTTPDatabaseRotationAndReauthorization(t *testing.T) {
	f := newConnectionHTTPFixture(t)
	ctx := context.Background()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks, err := json.Marshal(map[string]any{"keys": []any{map[string]any{"kty": "RSA", "kid": "issue594", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	require.NoError(t, err)
	sign := func(client, subject, nonce string) string {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"sub": subject, "iss": "https://auth.openai.com", "aud": client, "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce, "email": "same-recognition-label@example.test", "https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "synthetic-workspace", "chatgpt_user_id": "synthetic-user", "chatgpt_plan_type": "plus"}})
		token.Header["kid"] = "issue594"
		raw, e := token.SignedString(key)
		require.NoError(t, e)
		return raw
	}
	var mu sync.Mutex
	codes := map[string]openai.TokenResponse{}
	refreshClients := map[string]string{}
	refreshCalls := map[string]int{}
	revoked := []string{}
	repository.Issue594InstallOfflineOAuthTransport(t, func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		payload := string(jwks)
		switch r.URL.String() {
		case "https://auth.openai.com/.well-known/jwks.json":
		case "https://auth.openai.com/.well-known/openid-configuration":
			payload = `{"revocation_endpoint":"https://auth.openai.com/oauth/revoke"}`
		case "https://auth.openai.com/oauth/revoke":
			require.NoError(t, r.ParseForm())
			require.Equal(t, refreshClients[r.Form.Get("token")], r.Form.Get("client_id"))
			revoked = append(revoked, r.Form.Get("token"))
			payload = ""
		default:
			require.Equal(t, "auth.openai.com", r.URL.Host, "No real remote endpoint is allowed")
			require.Contains(t, []string{"/oauth/token", "/api/accounts/oauth/token"}, r.URL.Path)
			require.NoError(t, r.ParseForm())
			client := r.Form.Get("client_id")
			var token openai.TokenResponse
			if r.Form.Get("grant_type") == "authorization_code" {
				token = codes[r.Form.Get("code")]
				require.NotEmpty(t, token.AccessToken)
				require.NotEmpty(t, r.Form.Get("code_verifier"))
				refreshClients[token.RefreshToken] = client
			} else {
				old := r.Form.Get("refresh_token")
				require.Equal(t, refreshClients[old], client, "refresh cannot splice registration clients")
				refreshCalls[old]++
				require.Equal(t, 1, refreshCalls[old], "rotating tokens must only be consumed once")
				token = openai.TokenResponse{AccessToken: "synthetic-access-rotated-" + client, RefreshToken: old + "-next", ExpiresIn: 3600, TokenType: "Bearer"}
				refreshClients[token.RefreshToken] = client
			}
			if strings.Contains(client, "issued-") {
				require.Equal(t, openai.SIWCResource, r.Form.Get("resource"))
				require.Empty(t, r.Form.Get("scope"))
			}
			raw, e := json.Marshal(token)
			require.NoError(t, e)
			payload = string(raw)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(payload)), Request: r}, nil
	})
	authorize := func(c *service.OpenAIAuthConnection, client, subject, scope, code string, want ...int) {
		t.Helper()
		w := f.request("POST", "/"+c.ID+"/authorize", `{}`)
		require.Equal(t, 200, w.Code, w.Body.String())
		var result struct {
			Data service.OpenAIAuthURLResult `json:"data"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
		parsed, e := url.Parse(result.Data.AuthURL)
		require.NoError(t, e)
		q := parsed.Query()
		if c.Kind == "siwc" && c.ClientID == "" {
			require.Equal(t, openai.SIWCDynamicClientID, q.Get("client_id"))
		} else {
			require.Equal(t, client, q.Get("client_id"))
		}
		mu.Lock()
		codes[code] = openai.TokenResponse{AccessToken: "synthetic-access-" + code, RefreshToken: "synthetic-refresh-" + code, IDToken: sign(client, subject, q.Get("nonce")), Scope: scope, ExpiresIn: 3600, TokenType: "Bearer"}
		mu.Unlock()
		redirect := q.Get("redirect_uri")
		callback := redirect + "?" + url.Values{"code": {code}, "state": {q.Get("state")}, "client_id": {client}}.Encode()
		input, _ := json.Marshal(map[string]string{"session_id": result.Data.SessionID, "callback_url": callback})
		w = f.request("POST", "/"+c.ID+"/callback", string(input))
		expected := 200
		if len(want) > 0 {
			expected = want[0]
		}
		require.Equal(t, expected, w.Code, w.Body.String())
		replay := f.request("POST", "/"+c.ID+"/callback", string(input))
		require.NotEqual(t, 200, replay.Code)
	}
	first := connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"Registration one"}`))
	second := connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"Registration two"}`))
	identity := connectionResponse(t, f.request("POST", "", `{"kind":"siwc","label":"Identity sign-in"}`))
	authorize(first, "issued-one", "pairwise-one", openai.SIWCScopes, "one")
	authorize(second, "issued-two", "pairwise-two", openai.SIWCScopes, "two")
	authorize(identity, "issued-identity", "pairwise-identity", "openid email", "identity")
	codex := connectionResponse(t, f.request("POST", "", `{"kind":"codex","label":"Codex registration"}`))
	bps := connectionResponse(t, f.request("POST", "", `{"kind":"bps","label":"BPS registration"}`))
	authorize(codex, openai.ClientID, "codex-subject", "openid offline_access", "codex")
	authorize(bps, openai.ExcelClientID, "bps-subject", "openid offline_access", "bps")
	codexPeer := connectionResponse(t, f.request("POST", "", `{"kind":"codex","label":"Second Codex registration"}`))
	bpsPeer := connectionResponse(t, f.request("POST", "", `{"kind":"bps","label":"Second BPS registration"}`))
	authorize(codexPeer, openai.ClientID, "codex-subject", "openid offline_access", "codex-peer")
	authorize(bpsPeer, openai.ExcelClientID, "bps-subject", "openid offline_access", "bps-peer")
	for id, expected := range map[string]string{codex.ID: "synthetic-access-codex", codexPeer.ID: "synthetic-access-codex-peer", bps.ID: "synthetic-access-bps", bpsPeer.ID: "synthetic-access-bps-peer"} {
		got, tokenErr := f.svc.ConnectionAccessToken(ctx, f.accountID, id)
		require.NoError(t, tokenErr)
		require.Equal(t, expected, got)
	}

	_, err = f.svc.ConnectionAccessToken(ctx, f.accountID, identity.ID)
	require.Error(t, err)
	rows, err := f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	require.Len(t, rows, 7)
	for _, row := range rows {
		if row.ID != first.ID {
			continue
		}
		plain, e := f.encryptor.Decrypt(row.CredentialsCiphertext)
		require.NoError(t, e)
		var creds map[string]any
		require.NoError(t, json.Unmarshal([]byte(plain), &creds))
		creds["expires_at"] = "0"
		raw, e := json.Marshal(creds)
		require.NoError(t, e)
		row.CredentialsCiphertext, e = f.encryptor.Encrypt(string(raw))
		require.NoError(t, e)
		changed, e := f.repo.CASAuthConnection(ctx, row, row.Revision)
		require.NoError(t, e)
		require.True(t, changed)
	}
	// Two service instances and the actual adapter contend on real PostgreSQL.
	peer := f.restart()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, svc := range []*service.OpenAIOAuthReauthService{f.svc, peer} {
		wg.Add(1)
		go func(svc *service.OpenAIOAuthReauthService) {
			defer wg.Done()
			_, e := svc.ConnectionAccessToken(ctx, f.accountID, first.ID)
			results <- e
		}(svc)
	}
	wg.Wait()
	close(results)
	successes := 0
	for e := range results {
		if e == nil {
			successes++
		}
	}
	require.GreaterOrEqual(t, successes, 1)
	mu.Lock()
	require.Equal(t, 1, refreshCalls["synthetic-refresh-one"])
	mu.Unlock()
	token, err := peer.ConnectionAccessToken(ctx, f.accountID, first.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-access-rotated-issued-one", token)
	token, err = f.svc.ConnectionAccessToken(ctx, f.accountID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-access-two", token)
	rows, err = f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	for _, row := range rows {
		if row.ID == second.ID {
			second = row
		}
	}
	// Re-login reuses registration/host, pins subject, and changes no peer token.
	authorize(second, "issued-two", "pairwise-two", openai.SIWCScopes, "two-again")
	rows, err = f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	for _, row := range rows {
		if row.ID == second.ID {
			second = row
		}
	}
	authorize(second, "issued-two", "different-subject", openai.SIWCScopes, "two-mismatch", 409)
	require.NoError(t, f.svc.FailAuthConnection(ctx, &service.Account{ID: f.accountID, AuthConnectionID: second.ID, AuthConnectionRevision: second.Revision}, 401))
	healed, healErr := peer.ConnectionAccessToken(ctx, f.accountID, second.ID)
	require.NoError(t, healErr)
	require.Equal(t, "synthetic-access-rotated-issued-two", healed, "resource 401 expires only this access token and keeps the rotating refresh grant")

	rows, err = f.repo.ListAuthConnections(ctx, f.accountID)
	require.NoError(t, err)
	for _, row := range rows {
		if row.ID == first.ID {
			first = row
		}
	}
	w := f.request("POST", "/"+first.ID+"/revoke", fmt.Sprintf(`{"revision":%d}`, first.Revision))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"remote_revocation_confirmed":true`)
	mu.Lock()
	require.Equal(t, []string{"synthetic-refresh-one-next"}, revoked)
	mu.Unlock()
	token, err = peer.ConnectionAccessToken(ctx, f.accountID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "synthetic-access-rotated-issued-two", token)
	_, err = peer.ConnectionAccessToken(ctx, f.accountID, first.ID)
	require.Error(t, err)
	var legacy string
	require.NoError(t, f.db.QueryRow(`SELECT credentials->>'refresh_token' FROM accounts WHERE id=$1`, f.accountID).Scan(&legacy))
	require.Equal(t, "synthetic-legacy-refresh", legacy)
	list := f.request("GET", "", "")
	require.Equal(t, 200, list.Code)
	require.NotContains(t, list.Body.String(), "synthetic-access")
	require.NotContains(t, list.Body.String(), "synthetic-refresh")
}
