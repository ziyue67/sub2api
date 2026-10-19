package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestStripAnthropicClaudeCodeIdentityFromBody(t *testing.T) {
	t.Run("array system removes billing header, rewrites CLI and SDK banners, preserves cache_control and instructions, removes metadata.user_id", func(t *testing.T) {
		raw := []byte(`{
			"model": "claude-sonnet-5-5",
			"system": [
				{
					"type": "text",
					"text": "x-anthropic-billing-header: cc_version=2.1.302.abc; cc_entrypoint=cli; cch=00000;"
				},
				{
					"type": "text",
					"text": "You are Claude Code, Anthropic's official CLI for Claude.",
					"cache_control": {"type": "ephemeral", "ttl": "5m"}
				},
				{
					"type": "text",
					"text": "You are a Claude agent, built on Anthropic's Claude Agent SDK.\nFollow repo rules.",
					"cache_control": {"type": "ephemeral"}
				}
			],
			"metadata": {
				"user_id": "{\"device_id\":\"0123\",\"account_uuid\":\"\",\"session_id\":\"4567\"}"
			},
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		got := stripAnthropicClaudeCodeIdentityFromBody(raw)
		sys := gjson.GetBytes(got, "system")
		require.True(t, sys.IsArray())
		blocks := sys.Array()
		require.Len(t, blocks, 2)
		require.Equal(t, neutralCodingAssistantSystemPrompt, blocks[0].Get("text").String())
		require.Equal(t, "ephemeral", blocks[0].Get("cache_control.type").String())
		require.Equal(t, "5m", blocks[0].Get("cache_control.ttl").String())
		require.Equal(t, neutralCodingAssistantSystemPrompt+"\nFollow repo rules.", blocks[1].Get("text").String())
		require.Equal(t, "ephemeral", blocks[1].Get("cache_control.type").String())
		require.False(t, gjson.GetBytes(got, "metadata").Exists())
	})

	t.Run("array system with only billing header removes system key", func(t *testing.T) {
		raw := []byte(`{
			"model": "claude-sonnet-5-5",
			"system": [
				{
					"type": "text",
					"text": "x-anthropic-billing-header: cc_version=2.1.302.abc; cc_entrypoint=cli;"
				}
			],
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		got := stripAnthropicClaudeCodeIdentityFromBody(raw)
		require.False(t, gjson.GetBytes(got, "system").Exists())
	})

	t.Run("string system rewrites compact Agent SDK and file search banners", func(t *testing.T) {
		raw := []byte(`{
			"model": "claude-sonnet-5-5",
			"system": "x-anthropic-billing-header: cc_version=2.1.302.abc;\nYou are Claude Code, Anthropic's official CLI for Claude, running within the Claude Agent SDK.\nYou are a file search specialist for Claude Code, Anthropic's official CLI for Claude.",
			"metadata": {
				"user_id": "user_abc",
				"custom_tag": "keep-me"
			},
			"messages": [{"role": "user", "content": "hi"}]
		}`)

		got := stripAnthropicClaudeCodeIdentityFromBody(raw)
		require.Equal(t,
			neutralCodingAssistantSystemPrompt+"\n"+neutralFileSearchSystemPrompt,
			gjson.GetBytes(got, "system").String(),
		)
		require.False(t, gjson.GetBytes(got, "metadata.user_id").Exists())
		require.Equal(t, "keep-me", gjson.GetBytes(got, "metadata.custom_tag").String())
	})
}

func TestBuildUpstreamRequest_AnthropicStripClaudeCodeIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	clientBody := []byte(`{
		"model": "claude-sonnet-5-5",
		"max_tokens": 128,
		"system": [
			{"type": "text", "text": "x-anthropic-billing-header: cc_version=2.1.302.abc; cc_entrypoint=cli;"},
			{"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude.", "cache_control": {"type": "ephemeral"}}
		],
		"metadata": {"user_id": "user_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef_account__session_11111111-2222-3333-4444-555555555555"},
		"messages": [{"role": "user", "content": "hi"}]
	}`)

	newClientContext := func() *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(clientBody))
		req.Header.Set("User-Agent", "claude-cli/2.1.302 (external, cli)")
		req.Header.Set("anthropic-beta", "claude-code-20250219,oauth-2025-04-20,interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14")
		req.Header.Set("x-app", "cli")
		req.Header.Set("X-Claude-Code-Session-Id", "11111111-2222-3333-4444-555555555555")
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
		c.Request = req
		return c
	}

	svc := &GatewayService{cfg: &config.Config{}}

	t.Run("passthrough with strip enabled", func(t *testing.T) {
		account := &Account{
			ID:       2298,
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://api.anthropic.com",
			},
			Extra: map[string]any{
				"anthropic_passthrough":                true,
				"anthropic_strip_claude_code_identity": true,
			},
		}
		c := newClientContext()
		req, wireBody, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, clientBody, "sk-ant-usr-test")
		require.NoError(t, err)

		require.Equal(t, defaultAnthropicAPIKeyNeutralUserAgent, getHeaderRaw(req.Header, "User-Agent"))
		require.Equal(t, "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14", getHeaderRaw(req.Header, "anthropic-beta"))
		require.Empty(t, getHeaderRaw(req.Header, "x-app"))
		require.Empty(t, getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
		require.Empty(t, getHeaderRaw(req.Header, "anthropic-dangerous-direct-browser-access"))

		blocks := gjson.GetBytes(wireBody, "system").Array()
		require.Len(t, blocks, 1)
		require.Equal(t, neutralCodingAssistantSystemPrompt, blocks[0].Get("text").String())
		require.Equal(t, "ephemeral", blocks[0].Get("cache_control.type").String())
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
	})

	t.Run("non-passthrough with strip enabled", func(t *testing.T) {
		account := &Account{
			ID:       2322,
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url":                "https://api.anthropic.com",
				"header_override_enabled": true,
				"header_overrides": map[string]any{
					"anthropic-workspace-id": "wrkspc_test",
				},
			},
			Extra: map[string]any{
				"anthropic_strip_claude_code_identity": true,
			},
		}
		c := newClientContext()
		req, wireBody, err := svc.buildUpstreamRequest(context.Background(), c, account, clientBody, "sk-ant-usr-test", "apikey", "claude-sonnet-5-5", true, false)
		require.NoError(t, err)

		require.Equal(t, defaultAnthropicAPIKeyNeutralUserAgent, getHeaderRaw(req.Header, "User-Agent"))
		require.Equal(t, "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14", getHeaderRaw(req.Header, "anthropic-beta"))
		require.Equal(t, "wrkspc_test", getHeaderRaw(req.Header, "anthropic-workspace-id"))
		require.Empty(t, getHeaderRaw(req.Header, "x-app"))
		require.Empty(t, getHeaderRaw(req.Header, "X-Claude-Code-Session-Id"))
		require.Empty(t, getHeaderRaw(req.Header, "anthropic-dangerous-direct-browser-access"))

		blocks := gjson.GetBytes(wireBody, "system").Array()
		require.Len(t, blocks, 1)
		require.Equal(t, neutralCodingAssistantSystemPrompt, blocks[0].Get("text").String())
		require.False(t, gjson.GetBytes(wireBody, "metadata").Exists())
	})

	t.Run("count_tokens passthrough and standard with strip enabled", func(t *testing.T) {
		account := &Account{
			ID:       2298,
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://api.anthropic.com",
			},
			Extra: map[string]any{
				"anthropic_passthrough":                true,
				"anthropic_strip_claude_code_identity": true,
			},
		}
		c := newClientContext()
		ptReq, err := svc.buildCountTokensRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, clientBody, "sk-ant-usr-test")
		require.NoError(t, err)
		ptBody, err := io.ReadAll(ptReq.Body)
		require.NoError(t, err)
		require.Equal(t, defaultAnthropicAPIKeyNeutralUserAgent, getHeaderRaw(ptReq.Header, "User-Agent"))
		require.Equal(t, "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14", getHeaderRaw(ptReq.Header, "anthropic-beta"))
		require.Len(t, gjson.GetBytes(ptBody, "system").Array(), 1)
		require.False(t, gjson.GetBytes(ptBody, "metadata").Exists())

		c2 := newClientContext()
		stdReq, stdWireBody, err := svc.buildCountTokensRequest(context.Background(), c2, account, clientBody, "sk-ant-usr-test", "apikey", "claude-sonnet-5-5", false)
		require.NoError(t, err)
		require.Equal(t, defaultAnthropicAPIKeyNeutralUserAgent, getHeaderRaw(stdReq.Header, "User-Agent"))
		require.Equal(t, "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14", getHeaderRaw(stdReq.Header, "anthropic-beta"))
		require.Len(t, gjson.GetBytes(stdWireBody, "system").Array(), 1)
		require.False(t, gjson.GetBytes(stdWireBody, "metadata").Exists())
	})

	t.Run("strip disabled keeps original Claude Code headers and body", func(t *testing.T) {
		account := &Account{
			ID:       2298,
			Platform: PlatformAnthropic,
			Type:     AccountTypeAPIKey,
			Credentials: map[string]any{
				"base_url": "https://api.anthropic.com",
			},
			Extra: map[string]any{
				"anthropic_passthrough": true,
			},
		}
		c := newClientContext()
		req, wireBody, err := svc.buildUpstreamRequestAnthropicAPIKeyPassthrough(context.Background(), c, account, clientBody, "sk-ant-usr-test")
		require.NoError(t, err)

		require.Equal(t, "claude-cli/2.1.302 (external, cli)", getHeaderRaw(req.Header, "User-Agent"))
		require.Contains(t, getHeaderRaw(req.Header, "anthropic-beta"), "claude-code-20250219")
		require.Equal(t, "cli", getHeaderRaw(req.Header, "x-app"))
		require.Len(t, gjson.GetBytes(wireBody, "system").Array(), 2)
		require.True(t, gjson.GetBytes(wireBody, "metadata.user_id").Exists())
	})
}

type stripIdentityCaptureUpstream struct {
	lastReq  *http.Request
	lastBody []byte
}

func (u *stripIdentityCaptureUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	u.lastReq = req.Clone(req.Context())
	if req.Body != nil {
		u.lastBody, _ = io.ReadAll(req.Body)
	}
	sse := "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\ndata: [DONE]\n\n"
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(bytes.NewReader([]byte(sse))),
	}, nil
}

func (u *stripIdentityCaptureUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

func TestAccountTestService_AnthropicStripClaudeCodeIdentity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	account := &Account{
		ID:       2298,
		Name:     "claude-vm-key",
		Platform: PlatformAnthropic,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":  "sk-ant-usr-test",
			"base_url": "https://api.anthropic.com",
		},
		Extra: map[string]any{
			"anthropic_strip_claude_code_identity": true,
		},
	}

	repo := &stripIdentityTestAccountRepo{account: account}
	upstream := &stripIdentityCaptureUpstream{}
	svc := NewAccountTestService(repo, nil, nil, nil, nil, upstream, &config.Config{}, nil)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/2298/test", nil)

	err := svc.TestAccountConnection(c, 2298, "claude-sonnet-5-5", "", "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)

	require.Equal(t, defaultAnthropicAPIKeyNeutralUserAgent, getHeaderRaw(upstream.lastReq.Header, "User-Agent"))
	require.Equal(t, "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14", getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "X-App"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "Anthropic-Dangerous-Direct-Browser-Access"))

	blocks := gjson.GetBytes(upstream.lastBody, "system").Array()
	require.Len(t, blocks, 1)
	require.Equal(t, neutralCodingAssistantSystemPrompt, blocks[0].Get("text").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "metadata").Exists())
}

type stripIdentityTestAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *stripIdentityTestAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}
