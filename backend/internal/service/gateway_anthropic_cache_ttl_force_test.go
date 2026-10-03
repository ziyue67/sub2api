package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestAccountIsAnthropicAPIKeyForceCacheTTL1hEnabled(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    bool
	}{
		{name: "enabled Anthropic API Key", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"force_anthropic_cache_ttl_1h": true}}, want: true},
		{name: "disabled", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"force_anthropic_cache_ttl_1h": false}}},
		{name: "wrong value type", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Extra: map[string]any{"force_anthropic_cache_ttl_1h": "true"}}},
		{name: "Anthropic OAuth", account: &Account{Platform: PlatformAnthropic, Type: AccountTypeOAuth, Extra: map[string]any{"force_anthropic_cache_ttl_1h": true}}},
		{name: "OpenAI API Key", account: &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{"force_anthropic_cache_ttl_1h": true}}},
		{name: "nil account"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.account.IsAnthropicAPIKeyForceCacheTTL1hEnabled())
		})
	}
}

func TestForceAnthropicAPIKeyCacheTTL1hOnlyUpgradesExistingEphemeralBreakpoints(t *testing.T) {
	account := forcedAnthropicCacheTTL1hTestAccount(false)
	body := []byte(`{
		"model":"claude-sonnet-4-5",
		"system":[
			{"type":"text","text":"system","cache_control":{"type":"ephemeral"}},
			{"type":"text","text":"persistent","cache_control":{"type":"persistent","ttl":"5m"}}
		],
		"messages":[{"role":"user","content":[
			{"type":"text","text":"five","cache_control":{"type":"ephemeral","ttl":"5m"}},
			{"type":"text","text":"already","cache_control":{"type":"ephemeral","ttl":"1h"}},
			{"type":"text","text":"plain"}
		]}],
		"tools":[{"name":"lookup","cache_control":{"type":"ephemeral","ttl":"5m"}}]
	}`)

	got := forceAnthropicAPIKeyCacheTTL1h(account, body)
	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(got, "system.0.cache_control.ttl").String())
	require.Equal(t, "5m", gjson.GetBytes(got, "system.1.cache_control.ttl").String(), "persistent cache control must remain unchanged")
	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(got, "messages.0.content.0.cache_control.ttl").String())
	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(got, "messages.0.content.1.cache_control.ttl").String())
	require.False(t, gjson.GetBytes(got, "messages.0.content.2.cache_control").Exists(), "no new cache breakpoint may be created")
	require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(got, "tools.0.cache_control.ttl").String())
	require.Equal(t, bytes.Count(body, []byte(`"cache_control"`)), bytes.Count(got, []byte(`"cache_control"`)), "the transformation must not add or remove cache breakpoints")

	disabled := *account
	disabled.Extra = map[string]any{}
	require.Equal(t, body, forceAnthropicAPIKeyCacheTTL1h(&disabled, body))
}

func TestApplyForcedAnthropicCacheTTL1hBetaMergesAndDeduplicates(t *testing.T) {
	account := forcedAnthropicCacheTTL1hTestAccount(false)
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"1h"}}]}]}`)
	header := http.Header{}
	setHeaderRaw(header, "anthropic-beta", "custom-beta,"+claude.BetaExtendedCacheTTL+",custom-beta")

	applyForcedAnthropicCacheTTL1hBeta(header, account, body)
	got := getHeaderRaw(header, "anthropic-beta")
	require.Contains(t, got, "custom-beta")
	require.Contains(t, got, claude.BetaExtendedCacheTTL)
	require.Equal(t, 1, strings.Count(got, "custom-beta"))
	require.Equal(t, 1, strings.Count(got, claude.BetaExtendedCacheTTL))

	withoutBreakpoint := http.Header{"Anthropic-Beta": []string{"custom-beta"}}
	applyForcedAnthropicCacheTTL1hBeta(withoutBreakpoint, account, []byte(`{"messages":[]}`))
	require.Equal(t, "custom-beta", getHeaderRaw(withoutBreakpoint, "anthropic-beta"))

	account.Credentials[credKeyHeaderOverrideEnabled] = true
	account.Credentials[credKeyHeaderOverrides] = map[string]any{
		"anthropic-beta": "account-override-beta," + claude.BetaExtendedCacheTTL,
	}
	overridden := http.Header{"Anthropic-Beta": []string{"client-beta"}}
	account.ApplyHeaderOverrides(overridden)
	applyForcedAnthropicCacheTTL1hBeta(overridden, account, body)
	require.Equal(t, 1, strings.Count(getHeaderRaw(overridden, "anthropic-beta"), claude.BetaExtendedCacheTTL))
	require.Contains(t, getHeaderRaw(overridden, "anthropic-beta"), "account-override-beta")
	require.NotContains(t, getHeaderRaw(overridden, "anthropic-beta"), "client-beta")
}

func TestGatewayAnthropicAPIKeyForceCacheTTL1hMessagesPaths(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "passthrough"}[passthrough], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Request.Header.Set("Anthropic-Beta", "client-beta")

			body := []byte(`{"model":"claude-sonnet-4-5","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral","ttl":"5m"}}]}]}`)
			parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-5"}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"id":"msg_1","type":"message","model":"claude-sonnet-4-5","content":[],"usage":{"input_tokens":1,"output_tokens":1}}`)),
			}}
			svc := forcedAnthropicCacheTTL1hGateway(upstream)
			account := forcedAnthropicCacheTTL1hTestAccount(passthrough)

			result, err := svc.Forward(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.cache_control.ttl").String())
			require.Contains(t, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), claude.BetaExtendedCacheTTL)
			require.Contains(t, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "client-beta")
		})
	}
}

func TestGatewayAnthropicAPIKeyForceCacheTTL1hCountTokensPaths(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "passthrough"}[passthrough], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", nil)
			c.Request.Header.Set("Anthropic-Beta", "client-beta")

			body := []byte(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":[{"type":"text","text":"hello","cache_control":{"type":"ephemeral"}}]}]}`)
			parsed := &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-5"}
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"application/json"}},
				Body:       io.NopCloser(strings.NewReader(`{"input_tokens":42}`)),
			}}
			svc := forcedAnthropicCacheTTL1hGateway(upstream)
			account := forcedAnthropicCacheTTL1hTestAccount(passthrough)

			err := svc.ForwardCountTokens(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.Equal(t, cacheTTLTarget1h, gjson.GetBytes(upstream.lastBody, "messages.0.content.0.cache_control.ttl").String())
			require.Contains(t, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), claude.BetaExtendedCacheTTL)
			require.Contains(t, getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"), "client-beta")
		})
	}
}

func forcedAnthropicCacheTTL1hTestAccount(passthrough bool) *Account {
	extra := map[string]any{"force_anthropic_cache_ttl_1h": true}
	if passthrough {
		extra["anthropic_passthrough"] = true
	}
	return &Account{
		ID:          29644,
		Name:        "cache-ttl-1h-test",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{"api_key": "upstream-key", "base_url": "https://api.anthropic.com"},
		Extra:       extra,
		Status:      StatusActive,
		Schedulable: true,
	}
}

func forcedAnthropicCacheTTL1hGateway(upstream *anthropicHTTPUpstreamRecorder) *GatewayService {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
	return &GatewayService{
		cfg:                  cfg,
		responseHeaderFilter: compileResponseHeaderFilter(cfg),
		httpUpstream:         upstream,
		rateLimitService:     &RateLimitService{},
		deferredService:      &DeferredService{},
	}
}
