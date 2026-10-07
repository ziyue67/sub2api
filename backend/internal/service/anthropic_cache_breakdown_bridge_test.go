//go:build unit

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/apicompat"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAnthropicBridgePreservesCacheTTLBreakdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-opus-5\",\"content\":[],\"usage\":{\"input_tokens\":2,\"cache_read_input_tokens\":80995,\"cache_creation_input_tokens\":4828,\"cache_creation\":{\"ephemeral_5m_input_tokens\":0,\"ephemeral_1h_input_tokens\":4828}}}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":764}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse))}}
			svc := &GatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
			body := []byte(fmt.Sprintf(`{"model":"claude-opus-5","stream":%v,"messages":[{"role":"user","content":"synthetic test"}]}`, stream))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(string(body)))
			account := &Account{ID: 29270, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_key": "test"}}
			result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
			require.NoError(t, err)
			require.Equal(t, 2, result.Usage.InputTokens)
			require.Equal(t, 764, result.Usage.OutputTokens)
			require.Equal(t, 80995, result.Usage.CacheReadInputTokens)
			require.Equal(t, 4828, result.Usage.CacheCreationInputTokens)
			require.Equal(t, 4828, result.Usage.CacheCreation1hTokens)
			require.Zero(t, result.Usage.CacheCreation5mTokens)
			pricing := &ModelPricing{SupportsCacheBreakdown: true, CacheCreation5mPrice: 6.25e-6, CacheCreation1hPrice: 10e-6}
			cost := (&BillingService{}).computeCacheCreationCost(pricing, UsageTokens{
				CacheCreationTokens:   result.Usage.CacheCreationInputTokens,
				CacheCreation5mTokens: result.Usage.CacheCreation5mTokens,
				CacheCreation1hTokens: result.Usage.CacheCreation1hTokens,
			}, 6.25e-6, 1)
			require.InDelta(t, 0.04828, cost, 1e-10, "use reported 1h classification, not 5m fallback")
		})
	}
}

func TestMergeAnthropicUsageCacheTTLFieldPresence(t *testing.T) {
	for _, tc := range []struct {
		name, patch string
		five, hour  int
	}{
		{"absent", `{"output_tokens":764}`, 12, 4828},
		{"null", `{"cache_creation":null}`, 12, 4828},
		{"empty", `{"cache_creation":{}}`, 12, 4828},
		{"explicit_zero", `{"cache_creation":{"ephemeral_5m_input_tokens":4840,"ephemeral_1h_input_tokens":0}}`, 4840, 0},
		{"partial", `{"cache_creation":{"ephemeral_5m_input_tokens":0}}`, 0, 4828},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dst := &ClaudeUsage{CacheCreationInputTokens: 4840, CacheCreation5mTokens: 12, CacheCreation1hTokens: 4828}
			var src apicompat.AnthropicUsage
			require.NoError(t, json.Unmarshal([]byte(tc.patch), &src))
			mergeAnthropicUsage(dst, src)
			require.Equal(t, tc.five, dst.CacheCreation5mTokens)
			require.Equal(t, tc.hour, dst.CacheCreation1hTokens)
		})
	}
}
