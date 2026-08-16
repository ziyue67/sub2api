//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDeepSeekChatUsageParsesCacheAndReasoningDetails(t *testing.T) {
	t.Parallel()

	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{
		"choices":[],
		"usage":{
			"prompt_tokens":283,
			"completion_tokens":69,
			"prompt_cache_hit_tokens":256,
			"prompt_cache_miss_tokens":27,
			"completion_tokens_details":{"reasoning_tokens":24}
		}
	}`))

	require.True(t, ok)
	require.Equal(t, 283, usage.InputTokens)
	require.Equal(t, 256, usage.CacheReadInputTokens)
	require.Zero(t, usage.CacheCreationInputTokens, "a cache miss is ordinary input, not a cache write")
	require.Equal(t, 69, usage.OutputTokens, "completion_tokens already includes reasoning tokens")
	require.Equal(t, 24, usage.ReasoningTokens)
}

func TestDeepSeekChatUsageFallsBackToCacheComponentsWithoutPromptTotal(t *testing.T) {
	t.Parallel()

	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{
		"usage":{
			"completion_tokens":5,
			"prompt_cache_hit_tokens":8,
			"prompt_cache_miss_tokens":2
		}
	}`))

	require.True(t, ok)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 8, usage.CacheReadInputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}

func TestResponsesUsageStillParsesCanonicalDetails(t *testing.T) {
	t.Parallel()

	usage, ok := extractOpenAIUsageFromJSONBytes([]byte(`{
		"type":"response.completed",
		"response":{"usage":{
			"input_tokens":10,
			"output_tokens":5,
			"input_tokens_details":{"cached_tokens":3},
			"output_tokens_details":{"reasoning_tokens":4}
		}}
	}`))

	require.True(t, ok)
	require.Equal(t, 10, usage.InputTokens)
	require.Equal(t, 3, usage.CacheReadInputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 4, usage.ReasoningTokens)
}

func TestDeepSeekHarnessHeadersUseExactClientPassthroughWithoutGeneration(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const (
		userID    = "anonymous-user"
		sessionID = "session-123"
	)
	body := []byte(`{"model":"deepseek-v4-flash","messages":[],"stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c.Request.Header.Set("X-DeepSeek-Harness-User-ID", userID)
	c.Request.Header.Set("x-deepseek-harness-session-id", sessionID)
	c.Request.Header.Set("X-DeepSeek-Harness-Compact", "1")
	c.Request.Header.Set("X-DeepSeek-Harness-Untrusted", "drop-me")
	c.Request.Header.Add("X-DeepSeek-Harness-User-ID", strings.Repeat("x", deepSeekHarnessForwardHeaderMaxBytes+1))

	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header)}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	account := rawChatCompletionsTestAccount()
	account.Platform = PlatformDeepSeek

	_, err := svc.sendCCUpstreamRequest(context.Background(), c, account, "https://api.deepseek.com/chat/completions", body, false, "secret", "", "")
	require.NoError(t, err)
	require.NotNil(t, upstream.lastReq)
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	require.Equal(t, userID, upstream.lastReq.Header.Get("X-DeepSeek-Harness-User-ID"))
	require.Equal(t, []string{userID}, upstream.lastReq.Header.Values("X-DeepSeek-Harness-User-ID"))
	require.Equal(t, sessionID, upstream.lastReq.Header.Get("X-DeepSeek-Harness-Session-ID"))
	require.Equal(t, "1", upstream.lastReq.Header.Get("X-DeepSeek-Harness-Compact"))
	require.Empty(t, upstream.lastReq.Header.Get("X-DeepSeek-Harness-Untrusted"))

	withoutHeaders := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(withoutHeaders)
	c2.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)

	_, err = svc.sendCCUpstreamRequest(context.Background(), c2, account, "https://api.deepseek.com/chat/completions", body, false, "secret", "", "")
	require.NoError(t, err)
	require.Empty(t, upstream.lastReq.Header.Get("X-DeepSeek-Harness-User-ID"))
	require.Empty(t, upstream.lastReq.Header.Get("X-DeepSeek-Harness-Session-ID"))
	require.Empty(t, upstream.lastReq.Header.Get("X-DeepSeek-Harness-Compact"))

	nonDeepSeekRecorder := httptest.NewRecorder()
	c3, _ := gin.CreateTestContext(nonDeepSeekRecorder)
	c3.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	c3.Request.Header.Set("X-DeepSeek-Harness-User-ID", userID)
	_, err = svc.sendCCUpstreamRequest(context.Background(), c3, rawChatCompletionsTestAccount(), "https://api.openai.com/v1/chat/completions", body, false, "secret", "", "")
	require.NoError(t, err)
	require.False(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	require.Empty(t, upstream.lastReq.Header.Get("X-DeepSeek-Harness-User-ID"))
}

func TestDeepSeekHTTPErrorStatusesTriggerFailoverAndKeepVendorRequestID(t *testing.T) {
	t.Parallel()

	account := deepSeekAccountTestFixture("")
	svc := &OpenAIGatewayService{}
	for _, status := range []int{
		http.StatusUnauthorized,
		http.StatusPaymentRequired,
		http.StatusForbidden,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusServiceUnavailable,
	} {
		status := status
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			requestID := "deepseek-request-" + http.StatusText(status)
			resp := &http.Response{
				StatusCode: status,
				Header:     http.Header{"X-Deepseek-Request-Id": []string{requestID}},
				Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"upstream failed"}}`)),
			}
			body, message := svc.readOpenAIUpstreamError(resp)

			failoverErr := svc.failoverOpenAIUpstreamHTTPError(
				context.Background(), c, account, resp, body, message, "deepseek-v4-flash",
			)
			require.NotNil(t, failoverErr)
			require.Equal(t, PlatformDeepSeek, failoverErr.Platform)
			require.Equal(t, status, failoverErr.StatusCode)
			require.Equal(t, requestID, failoverErr.ResponseHeaders.Get("X-DeepSeek-Request-Id"))

			rawEvents, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			events, ok := rawEvents.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, events, 1)
			require.Equal(t, PlatformDeepSeek, events[0].Platform)
			require.Equal(t, requestID, events[0].UpstreamRequestID)
			require.Equal(t, "failover", events[0].Kind)
		})
	}

	require.False(t, svc.shouldFailoverUpstreamError(http.StatusBadRequest))
	require.False(t, svc.shouldFailoverUpstreamError(http.StatusNotFound))
}

func TestDeepSeekRequestIDPrefersGenericThenVendorHeader(t *testing.T) {
	t.Parallel()

	header := http.Header{
		"X-Request-Id":          []string{"generic-id"},
		"X-Deepseek-Request-Id": []string{"deepseek-id"},
		"Xai-Request-Id":        []string{"xai-id"},
	}
	require.Equal(t, "generic-id", openAICompatibleUpstreamRequestID(header))
	header.Del("X-Request-Id")
	require.Equal(t, "deepseek-id", openAICompatibleUpstreamRequestID(header))
	header.Del("X-Deepseek-Request-Id")
	require.Equal(t, "xai-id", openAICompatibleUpstreamRequestID(header))
}

func TestDeepSeekUsesCompatibleRuntimeBlockWithoutOAuthOnlyBehavior(t *testing.T) {
	t.Parallel()

	account := deepSeekAccountTestFixture("")
	svc := &OpenAIGatewayService{}
	svc.BlockAccountScheduling(account, time.Now().Add(time.Minute), "test")
	require.True(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.False(t, isOpenAIOAuthAccount(account), "DeepSeek API keys must not enter OpenAI OAuth cooldown logic")
}
