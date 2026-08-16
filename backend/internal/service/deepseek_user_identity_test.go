package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func deepSeekIdentityTestContext(userID int64) context.Context {
	return context.WithValue(context.Background(), ctxkey.UserID, userID)
}

func deepSeekIdentityTestAccount(mode string) *Account {
	extra := map[string]any{}
	if mode != "" {
		extra[DeepSeekUserIsolationModeKey] = mode
	}
	return &Account{
		ID:       11,
		Platform: PlatformDeepSeek,
		Type:     AccountTypeAPIKey,
		Extra:    extra,
	}
}

func TestDeriveDeepSeekAuthenticatedUserIDStableAndAnonymous(t *testing.T) {
	firstCfg := &config.Config{}
	firstCfg.DeepSeek.UserIDSecret = "persistent-deepseek-user-secret"
	firstCfg.JWT.Secret = "first-jwt-secret-does-not-affect-explicit-deepseek-secret"
	restartedCfg := &config.Config{}
	restartedCfg.DeepSeek.UserIDSecret = firstCfg.DeepSeek.UserIDSecret
	restartedCfg.JWT.Secret = "different-jwt-secret-does-not-affect-explicit-deepseek-secret"
	rotatedCfg := &config.Config{}
	rotatedCfg.DeepSeek.UserIDSecret = "rotated-persistent-deepseek-user-secret"

	first, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(42), firstCfg)
	require.NoError(t, err)
	restarted, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(42), restartedCfg)
	require.NoError(t, err)
	rotated, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(42), rotatedCfg)
	require.NoError(t, err)
	other, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(43), firstCfg)
	require.NoError(t, err)

	require.Equal(t, first, restarted, "the same explicit secret must survive process restart and unrelated JWT rotation")
	require.NotEqual(t, first, rotated, "rotating deepseek.user_id_secret must rotate every upstream identity")
	require.NotEqual(t, first, other)
	require.Len(t, first, len(deepSeekUserIDPrefix)+deepSeekUserIDEncodedDigestBytes)
	require.True(t, strings.HasPrefix(first, deepSeekUserIDPrefix))
	require.LessOrEqual(t, len(first), 512)
	require.True(t, regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`).MatchString(first))
	require.NotContains(t, first, "42")
}

func TestDeepSeekAuthenticatedUserIDStableAcrossRoutingCredentialsAndClientAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const userID int64 = 4242
	cfg := &config.Config{}
	cfg.DeepSeek.UserIDSecret = "scope-independent-deepseek-user-secret"
	want, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(userID), cfg)
	require.NoError(t, err)

	tests := []struct {
		name            string
		apiKey          string
		sessionID       string
		group           *Group
		accountID       int64
		accountAPIKey   string
		harnessUserID   string
		composite       bool
		accountSwitches int
	}{
		{
			name:          "direct first api key session group and account",
			apiKey:        "sub2api-key-one",
			sessionID:     "session-one",
			group:         &Group{ID: 101, Platform: PlatformDeepSeek},
			accountID:     1001,
			accountAPIKey: "upstream-key-one",
			harnessUserID: "harness-spoof-one",
		},
		{
			name:          "direct second api key session group and account",
			apiKey:        "sub2api-key-two",
			sessionID:     "session-two",
			group:         &Group{ID: 202, Platform: PlatformDeepSeek},
			accountID:     2002,
			accountAPIKey: "upstream-key-two",
			harnessUserID: "harness-spoof-two",
		},
		{
			name:            "composite failover account with unrelated harness identity",
			apiKey:          "sub2api-key-three",
			sessionID:       "session-three",
			group:           &Group{ID: 303, Platform: PlatformComposite},
			accountID:       3003,
			accountAPIKey:   "upstream-key-three",
			harnessUserID:   "harness-spoof-three",
			composite:       true,
			accountSwitches: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := deepSeekIdentityTestContext(userID)
			ctx = context.WithValue(ctx, ctxkey.Group, tt.group)
			ctx = context.WithValue(ctx, ctxkey.AccountID, tt.accountID)
			ctx = context.WithValue(ctx, ctxkey.AccountSwitchCount, tt.accountSwitches)
			if tt.composite {
				ctx = WithResolvedTargetPlatform(ctx, PlatformDeepSeek)
			}

			account := deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser)
			account.ID = tt.accountID
			account.Credentials = map[string]any{"api_key": tt.accountAPIKey, "base_url": DefaultDeepSeekBaseURL}
			account.Concurrency = 1
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil).WithContext(ctx)
			c.Request.Header.Set("Authorization", "Bearer "+tt.apiKey)
			c.Request.Header.Set("Session-Id", tt.sessionID)
			c.Request.Header.Set("X-DeepSeek-Harness-User-ID", tt.harnessUserID)

			resp, sendErr := svc.sendCCUpstreamRequest(
				ctx,
				c,
				account,
				"https://api.deepseek.com/chat/completions",
				[]byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hi"}],"user_id":"client-spoof"}`),
				true,
				tt.accountAPIKey,
				"",
				"",
			)
			require.NoError(t, sendErr)
			require.NotNil(t, resp)
			require.NoError(t, resp.Body.Close())
			require.Equal(t, want, gjson.GetBytes(upstream.lastBody, "user_id").String())
			require.Equal(t, tt.harnessUserID, upstream.lastReq.Header.Get("X-DeepSeek-Harness-User-ID"))
			require.Equal(t, "Bearer "+tt.accountAPIKey, upstream.lastReq.Header.Get("Authorization"))
		})
	}
}

func TestDeriveDeepSeekAuthenticatedUserIDJWTSubkeyFallbackAndRotation(t *testing.T) {
	firstCfg := &config.Config{}
	firstCfg.JWT.Secret = "persistent-jwt-secret-with-at-least-32-bytes"
	secondCfg := &config.Config{}
	secondCfg.JWT.Secret = firstCfg.JWT.Secret
	rotatedCfg := &config.Config{}
	rotatedCfg.JWT.Secret = "rotated-jwt-secret-with-at-least-32-bytes"

	first, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(7), firstCfg)
	require.NoError(t, err)
	restarted, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(7), secondCfg)
	require.NoError(t, err)
	rotated, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(7), rotatedCfg)
	require.NoError(t, err)

	require.Equal(t, first, restarted)
	require.NotEqual(t, first, rotated)
}

func TestApplyDeepSeekAuthenticatedUserIDByProtocol(t *testing.T) {
	cfg := &config.Config{}
	cfg.DeepSeek.UserIDSecret = "identity-secret"
	ctx := deepSeekIdentityTestContext(99)
	account := deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser)
	want, err := DeriveDeepSeekAuthenticatedUserID(ctx, cfg)
	require.NoError(t, err)

	tests := []struct {
		name     string
		protocol DeepSeekUserIdentityProtocol
		body     string
		path     string
	}{
		{
			name:     "chat completions",
			protocol: DeepSeekUserIdentityChatCompletions,
			body:     `{"model":"deepseek-v4-pro","user_id":"spoofed","extension":{"keep":true}}`,
			path:     "user_id",
		},
		{
			name:     "responses",
			protocol: DeepSeekUserIdentityResponses,
			body:     `{"model":"deepseek-v4-pro","user":"spoofed","extension":{"keep":true}}`,
			path:     "user",
		},
		{
			name:     "anthropic messages",
			protocol: DeepSeekUserIdentityMessages,
			body:     `{"model":"deepseek-v4-pro","metadata":{"user_id":"spoofed","trace":"keep"},"extension":{"keep":true}}`,
			path:     "metadata.user_id",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := applyDeepSeekAuthenticatedUserID(ctx, cfg, account, tt.protocol, []byte(tt.body))
			require.NoError(t, err)
			require.Equal(t, want, gjson.GetBytes(got, tt.path).String())
			require.True(t, gjson.GetBytes(got, "extension.keep").Bool())
			if tt.protocol == DeepSeekUserIdentityMessages {
				require.Equal(t, "keep", gjson.GetBytes(got, "metadata.trace").String())
			}
		})
	}
}

func TestApplyDeepSeekAuthenticatedUserIDModeOffPreservesWire(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-flash","user":"client-owned","unknown":1}`)
	account := deepSeekIdentityTestAccount(DeepSeekUserIsolationModeOff)
	got, err := applyDeepSeekAuthenticatedUserID(context.Background(), &config.Config{}, account, DeepSeekUserIdentityResponses, body)
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestApplyDeepSeekAuthenticatedUserIDPreservesUnknownDuplicateFields(t *testing.T) {
	cfg := &config.Config{}
	cfg.DeepSeek.UserIDSecret = "identity-secret"
	body := []byte(`{"model":"deepseek-v4-pro","Model":"provider-native-case-variant","include":[],"Include":{"future":true},"extension":"first","extension":"second"}`)
	got, err := applyDeepSeekAuthenticatedUserID(
		deepSeekIdentityTestContext(9),
		cfg,
		deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser),
		DeepSeekUserIdentityResponses,
		body,
	)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(got), `"extension"`))
	require.Contains(t, string(got), `"Model":"provider-native-case-variant"`)
	require.Contains(t, string(got), `"Include":{"future":true}`)
}

func TestValidateDeepSeekUserIdentityRequestRejectsAmbiguousKeys(t *testing.T) {
	tests := []struct {
		name     string
		protocol DeepSeekUserIdentityProtocol
		body     string
	}{
		{"chat duplicate", DeepSeekUserIdentityChatCompletions, `{"user_id":"a","user_id":"b"}`},
		{"chat case", DeepSeekUserIdentityChatCompletions, `{"User_ID":"a"}`},
		{"responses duplicate", DeepSeekUserIdentityResponses, `{"user":"a","user":"b"}`},
		{"responses case", DeepSeekUserIdentityResponses, `{"User":"a"}`},
		{"messages metadata duplicate", DeepSeekUserIdentityMessages, `{"metadata":{},"metadata":{}}`},
		{"messages metadata case", DeepSeekUserIdentityMessages, `{"Metadata":{}}`},
		{"messages user duplicate", DeepSeekUserIdentityMessages, `{"metadata":{"user_id":"a","user_id":"b"}}`},
		{"messages user case", DeepSeekUserIdentityMessages, `{"metadata":{"User_ID":"a"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, ValidateDeepSeekUserIdentityRequest([]byte(tt.body), tt.protocol))
		})
	}
}

func TestApplyDeepSeekAuthenticatedUserIDRequiresTrustedContext(t *testing.T) {
	cfg := &config.Config{}
	cfg.DeepSeek.UserIDSecret = "identity-secret"
	account := deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser)
	_, err := applyDeepSeekAuthenticatedUserID(context.Background(), cfg, account, DeepSeekUserIdentityResponses, []byte(`{"model":"deepseek-v4-pro"}`))
	require.EqualError(t, err, deepSeekUserIdentityMissingError)
}

func TestResolveDeepSeekUserIsolationModeKeepsLegacyAccountsOff(t *testing.T) {
	require.Equal(t, DeepSeekUserIsolationModeOff, deepSeekIdentityTestAccount("").ResolveDeepSeekUserIsolationMode())
	require.Equal(t, DeepSeekUserIsolationModeAuthenticatedUser, deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser).ResolveDeepSeekUserIsolationMode())
}

func TestDeepSeekUserIdentityInjectedAtNativeTransportBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.DeepSeek.UserIDSecret = "identity-secret"
	ctx := deepSeekIdentityTestContext(101)
	want, err := DeriveDeepSeekAuthenticatedUserID(ctx, cfg)
	require.NoError(t, err)
	account := deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser)
	account.Credentials = map[string]any{"api_key": "secret", "base_url": DefaultDeepSeekBaseURL}
	account.Concurrency = 1

	for _, tt := range []struct {
		name string
		url  string
		body string
		path string
	}{
		{"chat", "https://api.deepseek.com/chat/completions", `{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hi"}]}`, "user_id"},
		{"responses", "https://api.deepseek.com/responses", `{"model":"deepseek-v4-pro","input":"hi"}`, "user"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}}
			svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			resp, err := svc.sendCCUpstreamRequest(ctx, c, account, tt.url, []byte(tt.body), true, "secret", "", "")
			require.NoError(t, err)
			require.NotNil(t, resp)
			require.Equal(t, want, gjson.GetBytes(upstream.lastBody, tt.path).String())
		})
	}

	gateway := &GatewayService{cfg: cfg}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	_, sentBody, err := gateway.buildUpstreamRequestAnthropicAPIKeyPassthrough(
		ctx,
		c,
		account,
		[]byte(`{"model":"deepseek-v4-pro","metadata":{"trace":"keep"},"messages":[{"role":"user","content":"hi"}],"max_tokens":32}`),
		"secret",
	)
	require.NoError(t, err)
	require.Equal(t, want, gjson.GetBytes(sentBody, "metadata.user_id").String())
	require.Equal(t, "keep", gjson.GetBytes(sentBody, "metadata.trace").String())
}
