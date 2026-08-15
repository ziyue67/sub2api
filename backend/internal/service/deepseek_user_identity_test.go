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
	cfg := &config.Config{}
	cfg.DeepSeek.UserIDSecret = "persistent-deepseek-user-secret"

	first, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(42), cfg)
	require.NoError(t, err)
	second, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(42), cfg)
	require.NoError(t, err)
	other, err := DeriveDeepSeekAuthenticatedUserID(deepSeekIdentityTestContext(43), cfg)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.NotEqual(t, first, other)
	require.LessOrEqual(t, len(first), 512)
	require.True(t, regexp.MustCompile(`^[a-zA-Z0-9\-_]+$`).MatchString(first))
	require.NotContains(t, first, "42")
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
	body := []byte(`{"model":"deepseek-v4-pro","extension":"first","extension":"second"}`)
	got, err := applyDeepSeekAuthenticatedUserID(
		deepSeekIdentityTestContext(9),
		cfg,
		deepSeekIdentityTestAccount(DeepSeekUserIsolationModeAuthenticatedUser),
		DeepSeekUserIdentityResponses,
		body,
	)
	require.NoError(t, err)
	require.Equal(t, 2, strings.Count(string(got), `"extension"`))
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
