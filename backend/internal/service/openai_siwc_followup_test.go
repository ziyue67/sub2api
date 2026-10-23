//go:build unit

package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/Wei-Shaw/sub2api/internal/pkg/redissession"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSIWCHeadersUseIsolatedSessionAndLocalClientIdentity(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"chatgpt_account_id": "account-a"}}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Set(codexAccountIdentitySourceContextKey, account)
	for k, v := range map[string]string{"Session-Id": "client-session", "X-Client-Request-Id": "private-request", "User-Agent": "private-agent", "Cookie": "private-cookie", "Authorization": "private-token", "ChatGPT-Account-ID": "private-account", "X-Codex-Turn-State": "private-state"} {
		c.Request.Header.Set(k, v)
	}
	svc := &OpenAIGatewayService{}
	req, err := svc.buildSIWCRequest(context.Background(), c, []byte(`{"stream":false}`), "siwc-access")
	require.NoError(t, err)
	require.Equal(t, "Sub2API ("+runtime.GOOS+"; "+runtime.GOARCH+")", req.Header.Get("User-Agent"))
	require.Equal(t, "text/event-stream", req.Header.Get("Accept"))
	require.Equal(t, generateSessionUUID(isolateOpenAIUpstreamSessionID(0, account, "client-session")), req.Header.Get("session_id"))
	require.NotEmpty(t, req.Header.Get("X-Client-Request-Id"))
	for _, v := range []string{"private-request", "private-agent", "private-cookie", "private-token", "private-account", "private-state", "client-session"} {
		require.NotContains(t, fmt.Sprint(req.Header), v)
	}
	next, err := svc.buildSIWCRequest(context.Background(), c, []byte(`{}`), "siwc-access")
	require.NoError(t, err)
	require.NotEqual(t, req.Header.Get("X-Client-Request-Id"), next.Header.Get("X-Client-Request-Id"))
	require.Equal(t, req.Header.Get("session_id"), next.Header.Get("session_id"))
	c.Set("api_key", &APIKey{ID: 99})
	userReq, err := svc.buildSIWCRequest(context.Background(), c, []byte(`{}`), "siwc-access")
	require.NoError(t, err)
	require.NotEqual(t, req.Header.Get("session_id"), userReq.Header.Get("session_id"))
	c.Set("api_key", &APIKey{ID: 0})
	// Exercise the final allowlist after compatibility adapters mutate headers.
	req.Header.Set("Cookie", "adapter-secret")
	req.Header.Set("ChatGPT-Account-ID", "adapter-account")
	account.Extra = map[string]any{OpenAISIWCEnabledKey: true}
	capture := &httpUpstreamRecorder{err: fmt.Errorf("offline transport stop")}
	sender := openAIClientToolsTestService(capture)
	_, err = sender.doOpenAIUpstream(req, "", account)
	require.Error(t, err)
	require.NotNil(t, capture.lastReq)
	require.Equal(t, "Sub2API ("+runtime.GOOS+"; "+runtime.GOARCH+")", capture.lastReq.Header.Get("User-Agent"))
	require.Equal(t, next.Header.Get("session_id"), capture.lastReq.Header.Get("session_id"))
	require.Empty(t, capture.lastReq.Header.Get("Cookie"))
	require.Empty(t, capture.lastReq.Header.Get("ChatGPT-Account-ID"))
	account.Credentials["chatgpt_account_id"] = "account-b"
	other, err := svc.buildSIWCRequest(context.Background(), c, []byte(`{}`), "siwc-access")
	require.NoError(t, err)
	require.NotEqual(t, req.Header.Get("session_id"), other.Header.Get("session_id"))
	c.Request.Header.Set("Session-Id", "client-session")
	c.Request.Header.Set("session_id", "conflicting-session")
	ambiguous, err := svc.buildSIWCRequest(context.Background(), c, []byte(`{}`), "siwc-access")
	require.NoError(t, err)
	require.Empty(t, ambiguous.Header.Get("session_id"))
	c.Request.Header.Del("session_id")
	for _, bad := range []string{strings.Repeat("a", 257), "bad\r\nheader", "bad session"} {
		c.Request.Header.Set("Session-Id", bad)
		invalid, err := svc.buildSIWCRequest(context.Background(), c, []byte(`{}`), "siwc-access")
		require.NoError(t, err)
		require.Empty(t, invalid.Header.Get("session_id"))
	}
}

func TestSIWCEligibilityRejectionHasNeutralSafeDiagnostic(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"code", 403, `{"error":{"code":"subscription_sharing_user_not_eligible","message":"private upstream text"}}`},
		{"type", 403, `{"error":{"type":"subscription_sharing_user_not_eligible","message":"private upstream text"}}`},
		{"wrapped", 502, `{"error":{"message":"Upstream access denied","upstream_error":{"code":"subscription_sharing_user_not_eligible","message":"private upstream text"}}}`},
		{"semantic", 403, `{"error":{"message":"User is not eligible for subscription sharing."}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			a := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAISIWCEnabledKey: true}, Status: StatusActive, Schedulable: true}
			svc := &OpenAIGatewayService{cfg: &config.Config{}}
			svc.cfg.Gateway.LogUpstreamErrorBody = true
			err := svc.handleErrorResponsePassthrough(context.Background(), &http.Response{StatusCode: tc.status, Header: make(http.Header)}, c, a, []byte(`{"input":"private prompt"}`), []byte(tc.body))
			require.ErrorContains(t, err, "subscription_sharing_user_not_eligible")
			require.Equal(t, 502, recorder.Code)
			require.Equal(t, "subscription_sharing_user_not_eligible", gjsonString(recorder.Body.Bytes(), "error", "code"))
			raw, _ := c.Get(OpsUpstreamErrorsKey)
			encoded, err := json.Marshal(raw)
			require.NoError(t, err)
			for _, text := range []string{recorder.Body.String(), string(encoded)} {
				require.NotContains(t, text, "private")
				require.NotContains(t, text, "风控")
				require.NotContains(t, text, "risk")
			}
			require.Equal(t, StatusActive, a.Status)
			require.True(t, a.Schedulable)
			require.False(t, shouldFailoverOpenAIPassthroughResponse(a, tc.status, []byte(tc.body)))
		})
	}
}
func gjsonString(raw []byte, keys ...string) string {
	var v map[string]any
	_ = json.Unmarshal(raw, &v)
	for _, k := range keys[:len(keys)-1] {
		v, _ = v[k].(map[string]any)
	}
	s, _ := v[keys[len(keys)-1]].(string)
	return s
}

func TestSIWCSharedAuthorizationSurvivesReplicaAndIsSingleUse(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	first, _, repo, _, oldClient, _ := newReauthTestService("acct-1")
	defer first.oauth.Stop()
	first.repo = &siwcTestRepo{reauthTestRepo: repo}
	first.siwcSessionStore = redissession.New(rdb, siwcSessionPrefix, openai.SessionTTL)
	first.encryptor = newSIWCSessionTestEncryptor(t)
	auth, err := first.BeginSIWCAuthorization(context.Background(), 42)
	require.NoError(t, err)
	u, err := url.Parse(auth.AuthURL)
	require.NoError(t, err)
	require.NotEmpty(t, mr.Keys(), "pending sessions must be shared")
	for _, key := range mr.Keys() {
		raw, e := mr.Get(key)
		require.NoError(t, e)
		for _, secret := range []string{u.Query().Get("state"), u.Query().Get("nonce"), "old-access", "user@example.com"} {
			require.NotContains(t, raw, secret)
		}
	}
	second, _, _, _, _, _ := newReauthTestService("acct-1")
	defer second.oauth.Stop()
	second.repo = first.repo
	second.siwcSessionStore = redissession.New(rdb, siwcSessionPrefix, openai.SessionTTL)
	second.encryptor = first.encryptor
	client := &siwcTestClient{reauthTestOAuthClient: oldClient}
	second.oauth.oauthClient = client
	callback := "http://127.0.0.1:1455/auth/callback?code=code&state=" + url.QueryEscape(u.Query().Get("state")) + "&client_id=issued"
	require.NoError(t, second.CompleteSIWCAuthorization(context.Background(), 42, auth.SessionID, callback, nil))
	require.Error(t, first.CompleteSIWCAuthorization(context.Background(), 42, auth.SessionID, callback, nil))
	require.Equal(t, 1, client.calls)
}

func TestSIWCSharedAuthorizationFailsClosedOnRedisLoss(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1, DialTimeout: time.Millisecond * 20})
	defer func() { _ = rdb.Close() }()
	svc, _, repo, _, oldClient, _ := newReauthTestService("acct-1")
	defer svc.oauth.Stop()
	svc.siwcSessionStore = redissession.New(rdb, siwcSessionPrefix, openai.SessionTTL)
	svc.repo = &siwcTestRepo{reauthTestRepo: repo}
	client := &siwcTestClient{reauthTestOAuthClient: oldClient}
	svc.oauth.oauthClient = client
	auth, err := svc.BeginSIWCAuthorization(context.Background(), 42)
	require.NoError(t, err)
	u, err := url.Parse(auth.AuthURL)
	require.NoError(t, err)
	mr.Close()
	callback := "http://127.0.0.1:1455/auth/callback?code=code&state=" + url.QueryEscape(u.Query().Get("state")) + "&client_id=issued"
	require.Error(t, svc.CompleteSIWCAuthorization(context.Background(), 42, auth.SessionID, callback, nil))
	require.Zero(t, client.calls)
	_, err = svc.BeginSIWCAuthorization(context.Background(), 42)
	require.Error(t, err)
}

func TestSIWCSharedAuthorizationConcurrentConsumption(t *testing.T) {
	// Each replica must claim the same session only once, before a token exchange.
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer func() { _ = rdb.Close() }()
	svc, _, repo, _, _, _ := newReauthTestService("acct-1")
	defer svc.oauth.Stop()
	svc.siwcSessionStore = redissession.New(rdb, siwcSessionPrefix, openai.SessionTTL)
	svc.repo = &siwcTestRepo{reauthTestRepo: repo}
	auth, err := svc.BeginSIWCAuthorization(context.Background(), 42)
	require.NoError(t, err)
	// GETDEL is exercised below through callbacks with no available token client.
	u, err := url.Parse(auth.AuthURL)
	require.NoError(t, err)
	svc.oauth.oauthClient = nil
	callback := "http://127.0.0.1:1455/auth/callback?code=code&state=" + url.QueryEscape(u.Query().Get("state")) + "&client_id=issued"
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- svc.CompleteSIWCAuthorization(context.Background(), 42, auth.SessionID, callback, nil)
		}()
	}
	wg.Wait()
	close(results)
	claimed := 0
	for err := range results {
		require.Error(t, err)
		if err.Error() == "SIWC token client is unavailable" {
			claimed++
		}
	}
	require.Equal(t, 1, claimed)
	require.Empty(t, mr.Keys())
}

// A real authenticated cipher checks that Redis never receives the grant or PKCE
// in plaintext; the repository mock otherwise uses a deliberately plain fixture.
type siwcSessionTestEncryptor struct{ cipher.AEAD }

func newSIWCSessionTestEncryptor(t *testing.T) siwcSessionTestEncryptor {
	t.Helper()
	block, err := aes.NewCipher(make([]byte, 32))
	require.NoError(t, err)
	aead, err := cipher.NewGCM(block)
	require.NoError(t, err)
	return siwcSessionTestEncryptor{aead}
}
func (e siwcSessionTestEncryptor) Encrypt(s string) (string, error) {
	n := make([]byte, e.NonceSize())
	if _, err := rand.Read(n); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(e.Seal(n, n, []byte(s), nil)), nil
}
func (e siwcSessionTestEncryptor) Decrypt(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	p, err := e.Open(nil, b[:e.NonceSize()], b[e.NonceSize():], nil)
	return string(p), err
}

func TestSIWCEligibilityDoesNotInferFromStatusOrArbitraryText(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{403, `{"error":{"message":"Forbidden"}}`},
		{502, `{"error":{"message":"Upstream access denied"}}`},
		{403, `{"input":"subscription_sharing_user_not_eligible"}`},
		{403, `{"error":{"message":"unrelated quoted subscription_sharing_user_not_eligible text"}}`},
		{403, `{"error":{"code":"subscription_sharing_user_not_eligible_suffix"}}`},
		{403, `<html>subscription_sharing_user_not_eligible</html>`},
		{403, `{"error":"subscription_sharing_user_not_eligible"}`},
		{403, `{"error":{"code":123}}`},
		{400, `{"error":{"code":"subscription_sharing_user_not_eligible"}}`},
		{401, `{"error":{"code":"subscription_sharing_user_not_eligible"}}`},
	} {
		t.Run(tc.body, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			a := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{OpenAISIWCEnabledKey: true}}
			err := (&OpenAIGatewayService{}).handleErrorResponsePassthrough(context.Background(), &http.Response{StatusCode: tc.status, Header: make(http.Header)}, c, a, nil, []byte(tc.body))
			require.Error(t, err)
			require.Empty(t, gjsonString(recorder.Body.Bytes(), "error", "code"))
		})
	}
}

func TestSIWCStoredIdentityCannotDisappearBeforeRefresh(t *testing.T) {
	for _, scenario := range []string{"removed_email", "removed_workspace", "changed_workspace"} {
		t.Run(scenario, func(t *testing.T) {
			svc, reader, repo, _, oldClient, _ := newReauthTestService("acct-1")
			defer svc.oauth.Stop()
			store := &siwcTestRepo{reauthTestRepo: repo}
			svc.repo = store
			client := &siwcTestClient{reauthTestOAuthClient: oldClient, omitRefreshIdentity: true}
			svc.oauth.oauthClient = client
			raw, err := json.Marshal(siwcGrant{AccessToken: "expired", RefreshToken: "old-refresh", ClientID: "issued", Subject: "subscriber", Email: "user@example.com", Workspace: "acct-1", Scope: openai.SIWCDirectScope})
			require.NoError(t, err)
			store.ciphertext = "encrypted:" + string(raw)
			before := store.ciphertext
			switch scenario {
			case "removed_email":
				delete(reader.account.Credentials, "email")
			case "removed_workspace":
				delete(reader.account.Credentials, "chatgpt_account_id")
			case "changed_workspace":
				reader.account.Credentials["chatgpt_account_id"] = "acct-2"
			}
			token, err := svc.SIWCAccessToken(context.Background(), reader.account, nil)
			require.Error(t, err)
			require.Empty(t, token)
			require.Zero(t, client.calls)
			require.Equal(t, before, store.ciphertext)
		})
	}
}

func TestSIWCSharedAuthorizationValidationAndTTL(t *testing.T) {
	for _, scenario := range []string{"wrong_state", "wrong_account", "wrong_client", "expired", "swapped_ciphertext", "tampered_ciphertext"} {
		t.Run(scenario, func(t *testing.T) {
			mr := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
			defer func() { _ = rdb.Close() }()
			svc, _, repo, _, oldClient, _ := newReauthTestService("acct-1")
			defer svc.oauth.Stop()
			svc.siwcSessionStore = redissession.New(rdb, siwcSessionPrefix, openai.SessionTTL)
			svc.repo = &siwcTestRepo{reauthTestRepo: repo}
			svc.encryptor = newSIWCSessionTestEncryptor(t)
			client := &siwcTestClient{reauthTestOAuthClient: oldClient}
			svc.oauth.oauthClient = client
			auth, err := svc.BeginSIWCAuthorization(context.Background(), 42)
			require.NoError(t, err)
			u, err := url.Parse(auth.AuthURL)
			require.NoError(t, err)
			require.Equal(t, openai.SessionTTL, mr.TTL(siwcSessionPrefix+auth.SessionID))
			callback := "http://127.0.0.1:1455/auth/callback?code=code&state=" + url.QueryEscape(u.Query().Get("state")) + "&client_id=issued"
			id := int64(42)
			switch scenario {
			case "wrong_state":
				callback = strings.Replace(callback, u.Query().Get("state"), "wrong", 1)
			case "wrong_account":
				id = 43
			case "wrong_client":
				// Returning clients must use exactly the issued ID from the session.
				session, ok := svc.readSIWCSession(context.Background(), auth.SessionID, false)
				require.True(t, ok)
				session.ClientID = "original-issued"
				require.NoError(t, svc.setSIWCSession(context.Background(), auth.SessionID, session))
			case "expired":
				mr.FastForward(openai.SessionTTL + time.Second)
			case "swapped_ciphertext":
				other, err := svc.BeginSIWCAuthorization(context.Background(), 42)
				require.NoError(t, err)
				raw, err := mr.Get(siwcSessionPrefix + other.SessionID)
				require.NoError(t, err)
				mr.Set(siwcSessionPrefix+auth.SessionID, raw)
			case "tampered_ciphertext":
				mr.Set(siwcSessionPrefix+auth.SessionID, "invalid ciphertext")
			}
			require.Error(t, svc.CompleteSIWCAuthorization(context.Background(), id, auth.SessionID, callback, nil))
			require.Zero(t, client.calls)
			if scenario == "wrong_state" || scenario == "wrong_account" || scenario == "wrong_client" {
				_, ok := svc.readSIWCSession(context.Background(), auth.SessionID, false)
				require.True(t, ok, "invalid callback must not consume a valid authorization")
			}
		})
	}
}
