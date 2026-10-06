//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercise the real builders, including their header copy/override ordering.
// No parallel subtests: the canonical version resolver is process-wide.
func TestOpenAIAPIKeyOutboundIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	SetCodexCanonicalUserAgentResolver(func() string {
		return openai.CodexDefaultOriginator + "/0.200.1" + codexCLIUserAgentSuffix
	})
	t.Cleanup(func() { SetCodexCanonicalUserAgentResolver(nil) })
	svc := &OpenAIGatewayService{}
	body := []byte(`{"model":"gpt-6-sol","input":[],"stream":true}`)
	builders := map[string]func(*gin.Context, *Account) (http.Header, error){
		"responses": func(c *gin.Context, a *Account) (http.Header, error) {
			r, err := svc.buildUpstreamRequest(c.Request.Context(), c, a, body, "test-token", true, "", false)
			if err != nil {
				return nil, err
			}
			return r.Header, nil
		},
		"passthrough": func(c *gin.Context, a *Account) (http.Header, error) {
			r, err := svc.buildUpstreamRequestOpenAIPassthrough(c.Request.Context(), c, a, body, "test-token")
			if err != nil {
				return nil, err
			}
			return r.Header, nil
		},
		"input_tokens": func(c *gin.Context, a *Account) (http.Header, error) {
			r, err := svc.buildInputTokensUpstreamRequest(c.Request.Context(), c, a, body, "test-token")
			if err != nil {
				return nil, err
			}
			return r.Header, nil
		},
		"images": func(c *gin.Context, a *Account) (http.Header, error) {
			r, err := svc.buildOpenAIImagesRequest(c.Request.Context(), c, a, body, "application/json", "test-token", openAIImagesGenerationsEndpoint)
			if err != nil {
				return nil, err
			}
			return r.Header, nil
		},
		"websocket": func(c *gin.Context, a *Account) (http.Header, error) {
			h, _, err := svc.buildOpenAIWSHeaders(c.Request.Context(), c, a, "test-token",
				OpenAIWSProtocolDecision{Transport: OpenAIUpstreamTransportResponsesWebsocketV2},
				false, "", "", "", "gpt-6-sol", "")
			return h, err
		},
	}
	for name, build := range builders {
		for _, ua := range []string{"", "Go-http-client/2.0", "python-httpx/0.28.0", "Mozilla/5.0", "codex_cli_rs/0.125.0"} {
			t.Run(name+"/"+ua, func(t *testing.T) {
				c, _ := gin.CreateTestContext(httptest.NewRecorder())
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				c.Request.Header.Set("User-Agent", ua)
				c.Request.Header.Set("Originator", "untrusted-client")
				c.Request.Header.Set("Version", "0.1.0")
				before := c.Request.Header.Clone()
				h, err := build(c, &Account{ID: 991, Platform: PlatformOpenAI, Type: AccountTypeAPIKey})
				require.NoError(t, err)
				require.Equal(t, openai.CodexDefaultOriginator+"/0.200.1"+codexCLIUserAgentSuffix, h.Get("User-Agent"))
				require.Equal(t, openai.CodexDefaultOriginator, h.Get("Originator"))
				require.Equal(t, "0.200.1", h.Get("Version"))
				require.Equal(t, "Bearer test-token", h.Get("Authorization"))
				require.Equal(t, before, c.Request.Header, "inbound audit headers must remain intact")
			})
		}
	}
}

func TestOpenAIAPIKeyChatTestOutboundIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/pelican-test", nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n")),
	}}
	svc := &AccountTestService{httpUpstream: upstream}
	account := &Account{ID: 992, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.NoError(t, svc.testOpenAIChatCompletionsConnection(c, account, "gpt-6-sol", "hi", "https://upstream.example/v1", "test-token"))
	require.Equal(t, CodexCanonicalUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, openai.CodexDefaultOriginator, upstream.lastReq.Header.Get("Originator"))
	require.Equal(t, CodexCanonicalClientVersion(), upstream.lastReq.Header.Get("Version"))
}

func TestOpenAIAPIKeyChatForwardOutboundIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("User-Agent", "Go-http-client/2.0")
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{}"))}}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	account := &Account{ID: 993, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	resp, err := svc.sendCCUpstreamRequest(context.Background(), c, account, "https://upstream.example/v1/chat/completions", []byte(`{"model":"gpt-6-sol","messages":[]}`), false, "test-token", "", "")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, CodexCanonicalUserAgent(), upstream.lastReq.Header.Get("User-Agent"))
	require.Equal(t, openai.CodexDefaultOriginator, upstream.lastReq.Header.Get("Originator"))
	require.Equal(t, CodexCanonicalClientVersion(), upstream.lastReq.Header.Get("Version"))
}

func TestOpenAIAPIKeyIdentityPreservesOverridesAndOptOut(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "Go-http-client/2.0")
	for _, tc := range []struct {
		name     string
		account  *Account
		force    bool
		disabled bool
		wantUA   string
	}{
		{"account_codex_version_refreshed", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"user_agent": "codex_vscode/0.125.0 (Linux; x86_64) vscode"}}, false, false, "codex_vscode/" + codexCLIVersion + " (Linux; x86_64) vscode"},
		{"force_cli", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"user_agent": "codex_vscode/0.125.0 (Linux; x86_64) vscode"}}, true, false, codexCLIUserAgent},
		{"explicit_override", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{credKeyHeaderOverrideEnabled: true, credKeyHeaderOverrides: map[string]any{"user-agent": "custom-client/1.0", "originator": "custom-client", "version": "1.0"}}}, false, false, "custom-client/1.0"},
		{"disabled", &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, false, true, "Go-http-client/2.0"},
		{"other_platform", &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey}, false, false, "Go-http-client/2.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			SetCodexIdentityEnforcementEnabled(!tc.disabled)
			t.Cleanup(func() { SetCodexIdentityEnforcementEnabled(true) })
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{ForceCodexCLI: tc.force}}}
			r, err := svc.buildUpstreamRequest(c.Request.Context(), c, tc.account, []byte(`{"model":"gpt-6-sol"}`), "test-token", true, "", false)
			require.NoError(t, err)
			require.Equal(t, tc.wantUA, r.Header.Get("User-Agent"))
			if tc.name == "explicit_override" {
				require.Equal(t, "custom-client", getHeaderRaw(r.Header, "originator"))
				require.Equal(t, "1.0", getHeaderRaw(r.Header, "version"))
			}
		})
	}
}
