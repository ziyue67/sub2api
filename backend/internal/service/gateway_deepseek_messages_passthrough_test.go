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
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newDeepSeekAPIKeyAccountForMessagesTest(baseURL string) *Account {
	credentials := map[string]any{
		"api_key": "upstream-deepseek-key",
	}
	if baseURL != "" {
		credentials["base_url"] = baseURL
	}
	return &Account{
		ID:          5201,
		Name:        "deepseek-messages-native-test",
		Platform:    PlatformDeepSeek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: credentials,
		Status:      StatusActive,
		Schedulable: true,
	}
}

func newDeepSeekMessagesGatewayForTest(upstream HTTPUpstream) *GatewayService {
	return &GatewayService{
		cfg: &config.Config{
			Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize},
			Security: config.SecurityConfig{
				URLAllowlist: config.URLAllowlistConfig{Enabled: false},
			},
		},
		httpUpstream: upstream,
	}
}

func TestGatewayService_DeepSeekMessagesNativePassthrough_Stream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")
	c.Request.Header.Set("Anthropic-Beta", "interleaved-thinking-2025-05-14")

	body := []byte(`{"model":"deepseek-v4-pro","stream":true,"thinking":{"type":"enabled","budget_tokens":4096},"output_config":{"effort":"max"},"context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"need search","signature":"sig_1"},{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"weather"}},{"type":"text","text":""}]},{"role":"user","content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[{"type":"web_search_result","url":"https://example.com","title":"Weather","encrypted_content":"opaque"}]}]}]}`)
	parsed := &ParsedRequest{
		Body:   NewRequestBodyRef(body),
		Model:  "deepseek-v4-pro",
		Stream: true,
	}

	upstreamSSE := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_ds_stream","model":"deepseek-v4-pro","usage":{"input_tokens":12,"cache_read_input_tokens":5}}}`,
		``,
		`event: content_block_start`,
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"reasoning"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":7}}`,
		``,
		`event: message_stop`,
		`data: {"type":"message_stop"}`,
		``,
	}, "\n") + "\n"
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/event-stream"},
				"x-request-id": []string{"rid-deepseek-stream"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamSSE)),
		},
	}
	svc := newDeepSeekMessagesGatewayForTest(upstream)

	result, err := svc.Forward(context.Background(), c, newDeepSeekAPIKeyAccountForMessagesTest(""), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 12, result.Usage.InputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)

	require.Equal(t, "https://api.deepseek.com/anthropic/v1/messages", upstream.lastReq.URL.String())
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	require.Empty(t, upstream.lastReq.URL.RawQuery, "DeepSeek native Messages URL must not append beta=true")
	require.Equal(t, "upstream-deepseek-key", getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "authorization"))
	require.Equal(t, "interleaved-thinking-2025-05-14", getHeaderRaw(upstream.lastReq.Header, "anthropic-beta"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "x-stainless-lang"), "DeepSeek API Key must not receive Anthropic OAuth mimic headers")
	require.True(t, bytes.Equal(body, upstream.lastBody), "thinking/server_tool/web_search request body must remain byte-for-byte unchanged")
	require.Equal(t, upstreamSSE, rec.Body.String(), "Anthropic SSE must be relayed without protocol conversion")
}

func TestGatewayService_DeepSeekMessagesNativePassthrough_NonStream(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("Authorization", "Bearer inbound-token")
	c.Request.Header.Set("X-Api-Key", "inbound-api-key")

	body := []byte(`{"model":"deepseek-v4-flash","stream":false,"thinking":{"type":"enabled","budget_tokens":2048},"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"use tool","signature":"sig_2"},{"type":"server_tool_use","id":"srvtoolu_2","name":"web_search","input":{"query":"docs"}}]},{"role":"user","content":[{"type":"web_search_tool_result","tool_use_id":"srvtoolu_2","content":[]}]}]}`)
	parsed := &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: "deepseek-v4-flash",
	}
	upstreamJSON := `{"id":"msg_ds_json","type":"message","role":"assistant","model":"deepseek-v4-flash","content":[{"type":"thinking","thinking":"checked","signature":"sig_out"},{"type":"text","text":"done"}],"stop_reason":"end_turn","usage":{"input_tokens":21,"output_tokens":8,"cache_read_input_tokens":6}}`
	upstream := &anthropicHTTPUpstreamRecorder{
		resp: &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
				"x-request-id": []string{"rid-deepseek-json"},
			},
			Body: io.NopCloser(strings.NewReader(upstreamJSON)),
		},
	}
	svc := newDeepSeekMessagesGatewayForTest(upstream)
	account := newDeepSeekAPIKeyAccountForMessagesTest("https://relay.example/deepseek/")

	result, err := svc.Forward(context.Background(), c, account, parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, 21, result.Usage.InputTokens)
	require.Equal(t, 6, result.Usage.CacheReadInputTokens)
	require.Equal(t, 8, result.Usage.OutputTokens)

	require.Equal(t, "https://relay.example/deepseek/anthropic/v1/messages", upstream.lastReq.URL.String())
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.lastReq.Context()))
	require.Empty(t, upstream.lastReq.URL.RawQuery, "DeepSeek native Messages URL must not append beta=true")
	require.Equal(t, "upstream-deepseek-key", getHeaderRaw(upstream.lastReq.Header, "x-api-key"))
	require.Empty(t, getHeaderRaw(upstream.lastReq.Header, "authorization"))
	require.True(t, bytes.Equal(body, upstream.lastBody), "non-streaming request body must remain byte-for-byte unchanged")
	require.Equal(t, upstreamJSON, rec.Body.String(), "non-streaming JSON must be relayed without protocol conversion")
}

func TestGatewayService_DeepSeekMessagesPreservesStaticToolNamesAndForceCacheWire(t *testing.T) {
	gin.SetMode(gin.TestMode)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"deepseek-v4-pro","stream":false,"messages":[{"role":"user","content":"resume"}]}`)
	parsed := &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: "deepseek-v4-pro",
	}
	upstreamJSON := `{"id":"msg_ds_tool","type":"message","role":"assistant","model":"deepseek-v4-pro","content":[{"type":"tool_use","id":"toolu_1","name":"cc_sess_resume","input":{"session_id":"cc_sess_keep"}}],"stop_reason":"tool_use","usage":{"input_tokens":13,"output_tokens":4,"cache_read_input_tokens":2}}`
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamJSON)),
	}}
	svc := newDeepSeekMessagesGatewayForTest(upstream)
	ctx := WithForceCacheBilling(context.Background())

	result, err := svc.Forward(ctx, c, newDeepSeekAPIKeyAccountForMessagesTest(""), parsed)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 13, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, upstreamJSON, rec.Body.String(), "DeepSeek wire usage and static cc_sess_ tool names must ignore gateway cache/tool rewrites")
	require.Contains(t, rec.Body.String(), `"name":"cc_sess_resume"`)
	require.Contains(t, rec.Body.String(), `"session_id":"cc_sess_keep"`)
	require.NotContains(t, rec.Body.String(), `"name":"sessions_resume"`)
}

func TestGatewayService_DeepSeekMessagesRejectsNonStreamWithoutUsageBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"messages":[]}`)
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"msg_missing_usage","type":"message","role":"assistant","content":[]}`)),
	}}
	svc := newDeepSeekMessagesGatewayForTest(upstream)

	result, err := svc.Forward(context.Background(), c, newDeepSeekAPIKeyAccountForMessagesTest(""), &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: "deepseek-v4-flash",
	})
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.False(t, c.Writer.Written())
}

func TestGatewayService_DeepSeekMessagesRequiresBlankLineTerminalDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	prefix := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_partial\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\n\n"
	tests := []struct {
		name string
		tail string
	}{
		{name: "EOF after terminal event line", tail: "event: message_stop\n"},
		{name: "EOF after terminal data line", tail: "event: message_stop\ndata: {\"type\":\"message_stop\"}\n"},
		{name: "blank frame without terminal data", tail: "event: message_stop\n\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			upstreamWire := prefix + tt.tail
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamWire)),
			}}
			svc := newDeepSeekMessagesGatewayForTest(upstream)
			body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"stream":true,"messages":[]}`)

			result, err := svc.Forward(context.Background(), c, newDeepSeekAPIKeyAccountForMessagesTest(""), &ParsedRequest{
				Body:   NewRequestBodyRef(body),
				Model:  "deepseek-v4-flash",
				Stream: true,
			})
			require.Error(t, err)
			require.NotNil(t, result, "usage observed before truncation must remain available")
			require.Equal(t, 2, result.Usage.InputTokens)
			require.Equal(t, 1, result.Usage.OutputTokens)
			require.Equal(t, upstreamWire, recorder.Body.String())
		})
	}
}

func TestGatewayService_DeepSeekMessagesAcceptsCRLFBlankLineTerminalDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	upstreamWire := "event: message_start\r\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_crlf\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\r\n\r\n" +
		"event: message_delta\r\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\r\n\r\n" +
		"event: message_stop\r\n" +
		"data: {\"type\":\"message_stop\"}\r\n\r\n"
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamWire)),
	}}
	svc := newDeepSeekMessagesGatewayForTest(upstream)
	body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"stream":true,"messages":[]}`)

	result, err := svc.Forward(context.Background(), c, newDeepSeekAPIKeyAccountForMessagesTest(""), &ParsedRequest{
		Body:   NewRequestBodyRef(body),
		Model:  "deepseek-v4-flash",
		Stream: true,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 2, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.Equal(t, upstreamWire, recorder.Body.String())
}

func TestGatewayService_DeepSeekMessagesInvalidJSONFailoverRedactsCredentialAndCopiesHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := newDeepSeekAPIKeyAccountForMessagesTest("")
	upstreamBody := "invalid response echoed " + account.GetDeepSeekAPIKey()
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/plain"},
			"X-Deepseek-Request-Id": []string{"ds-invalid-json"},
			"Retry-After":           []string{"7"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := newDeepSeekMessagesGatewayForTest(upstream)
	body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"messages":[]}`)

	result, err := svc.Forward(context.Background(), c, account, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: "deepseek-v4-flash",
	})
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.NotContains(t, string(failoverErr.ResponseBody), account.GetDeepSeekAPIKey())
	require.Contains(t, string(failoverErr.ResponseBody), deepSeekCredentialRedaction)
	require.Equal(t, "ds-invalid-json", failoverErr.ResponseHeaders.Get("X-Deepseek-Request-Id"))
	require.Equal(t, "7", failoverErr.ResponseHeaders.Get("Retry-After"))
	require.False(t, c.Writer.Written())
}

func TestGatewayService_DeepSeekMessagesHTTPFailoverCopiesHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	account := newDeepSeekAPIKeyAccountForMessagesTest("")
	upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header: http.Header{
			"Content-Type":          []string{"application/json"},
			"X-Deepseek-Request-Id": []string{"ds-http-failover"},
			"Retry-After":           []string{"11"},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"unavailable"}}`)),
	}}
	svc := newDeepSeekMessagesGatewayForTest(upstream)
	body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"messages":[]}`)

	result, err := svc.Forward(context.Background(), c, account, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: "deepseek-v4-flash",
	})
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, "ds-http-failover", failoverErr.ResponseHeaders.Get("X-Deepseek-Request-Id"))
	require.Equal(t, "11", failoverErr.ResponseHeaders.Get("Retry-After"))
}
