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
	"github.com/tidwall/gjson"
)

func TestBuildDeepSeekNativeEndpointURLsDoNotInsertV1(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		root  string
		build func(string) string
		want  string
	}{
		{
			name:  "chat from API root",
			root:  "https://api.deepseek.com",
			build: buildDeepSeekChatCompletionsURL,
			want:  "https://api.deepseek.com/chat/completions",
		},
		{
			name:  "chat from API root with trailing slash",
			root:  "https://api.deepseek.com/",
			build: buildDeepSeekChatCompletionsURL,
			want:  "https://api.deepseek.com/chat/completions",
		},
		{
			name:  "responses from API root",
			root:  "https://api.deepseek.com",
			build: buildDeepSeekResponsesURL,
			want:  "https://api.deepseek.com/responses",
		},
		{
			name:  "responses keeps configured root prefix",
			root:  "https://relay.example/deepseek",
			build: buildDeepSeekResponsesURL,
			want:  "https://relay.example/deepseek/responses",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, tt.build(tt.root))
			require.NotContains(t, tt.build(tt.root), "/v1/")
		})
	}
}

func TestNormalizeOpenAICompatiblePlatformPreservesDeepSeek(t *testing.T) {
	t.Parallel()
	require.Equal(t, PlatformDeepSeek, normalizeOpenAICompatiblePlatform(PlatformDeepSeek))
	require.Equal(t, PlatformGrok, normalizeOpenAICompatiblePlatform(PlatformGrok))
	require.Equal(t, PlatformOpenAI, normalizeOpenAICompatiblePlatform(PlatformOpenAI))
}

func TestForwardAsChatCompletionsDeepSeekUsesNativeRawEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max","service_tier":"fast","stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"chatcmpl_ds","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}
	account := deepSeekForwardTestAccount()

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://deepseek.example/chat/completions", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-deepseek-test", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, string(body), string(upstream.lastBody), "DeepSeek raw Chat body must bypass OpenAI fast/Responses transforms")
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
	require.Equal(t, deepSeekChatCompletionsEndpoint, result.UpstreamEndpoint)
}

func TestForwardDeepSeekResponsesUsesNativeEndpointAndOpaqueBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","input":"hello","reasoning":{"effort":"max"},"service_tier":"fast","previous_response_id":"resp_previous","stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}, "X-Deepseek-Request-Id": []string{"ds_req_1"}},
		Body: io.NopCloser(strings.NewReader(
			`{"id":"resp_ds","object":"response","model":"deepseek-v4-pro","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
		)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}
	account := deepSeekForwardTestAccount()

	result, err := svc.Forward(context.Background(), c, account, body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "http://deepseek.example/responses", upstream.lastReq.URL.String())
	require.Equal(t, "Bearer sk-deepseek-test", upstream.lastReq.Header.Get("Authorization"))
	require.JSONEq(t, string(body), string(upstream.lastBody), "DeepSeek Responses body must bypass Codex, fast, image, and CC bridge transforms")
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
	require.Equal(t, "ds_req_1", result.RequestID)
	require.Equal(t, 5, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, deepSeekResponsesEndpoint, result.UpstreamEndpoint)
}

func TestForwardAsChatCompletionsDeepSeekStreamCompletesWithDoneAndUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"reasoning_effort":"max","stream":true}`)
	upstreamSSE := strings.Join([]string{
		`data: {"id":"chatcmpl_ds","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_ds","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4,"prompt_cache_hit_tokens":2,"prompt_cache_miss_tokens":1}}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n") + "\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/event-stream"},
			"X-Deepseek-Request-Id": []string{"ds-chat-stream-ok"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, deepSeekForwardTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
	require.Equal(t, "ds-chat-stream-ok", result.RequestID)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
	require.Contains(t, recorder.Body.String(), `"usage":{"prompt_tokens":3`)
}

func TestForwardAsChatCompletionsDeepSeekStreamRejectsMissingDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	upstreamSSE := strings.Join([]string{
		`data: {"id":"chatcmpl_ds","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"partial"},"finish_reason":null}]}`,
		``,
		`data: {"id":"chatcmpl_ds","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`,
		``,
	}, "\n")
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, deepSeekForwardTestAccount(), body, "", "")
	require.Error(t, err)
	require.NotNil(t, result, "already relayed output must retain collected usage for accounting")
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.NotContains(t, recorder.Body.String(), "[DONE]")
}

func TestForwardAsChatCompletionsDeepSeekStreamRejectsDoneWithoutUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	upstreamSSE := strings.Join([]string{
		`data: {"id":"chatcmpl_ds","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"unbillable"},"finish_reason":"stop"}]}`,
		``,
		`data: [DONE]`,
		``,
	}, "\n") + "\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, deepSeekForwardTestAccount(), body, "", "")
	require.Error(t, err)
	require.NotNil(t, result, "the relayed terminal marker must not discard the attempt result")
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), `data: [DONE]`)
}

func TestForwardAsChatCompletionsDeepSeekStreamRejectsDoneBeforeBlankDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	upstreamSSE := "data: {\"id\":\"chatcmpl_half\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n" +
		"data: {\"id\":\"chatcmpl_half\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\n\n" +
		"data: [DONE]\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, deepSeekForwardTestAccount(), body, "", "")
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
}

func TestForwardAsChatCompletionsDeepSeekStreamAcceptsCRLFBlankDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","messages":[{"role":"user","content":"hello"}],"stream":true}`)
	upstreamSSE := "data: {\"id\":\"chatcmpl_crlf\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\r\n\r\n" +
		"data: {\"id\":\"chatcmpl_crlf\",\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1,\"total_tokens\":4}}\r\n\r\n" +
		"data: [DONE]\r\n\r\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.ForwardAsChatCompletions(context.Background(), c, deepSeekForwardTestAccount(), body, "", "")
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 3, result.Usage.InputTokens)
	require.Equal(t, 1, result.Usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "data: [DONE]")
}

func TestForwardDeepSeekResponsesStreamPreservesRawWireAndCompletesWithoutDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","input":"hello","stream":true}`)
	upstreamSSE := strings.Join([]string{
		`event: response.function_call_arguments.done`,
		`data: {"type":"response.function_call_arguments.done","item_id":"fc_1","output_index":0,"arguments":"{\"session\":\"cc_sess_keep\"}{\"session\":\"cc_sess_keep\"}"}`,
		``,
		`event: response.output_item.done`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"id":"fc_1","type":"function_call","name":"cc_sess_resume","arguments":"{\"session\":\"cc_sess_keep\"}{\"session\":\"cc_sess_keep\"}"}}`,
		``,
		`event: response.completed`,
		`data: {"type":"response.completed","response":{"id":"resp_ds_stream","object":"response","model":"deepseek-v4-pro","status":"completed","output":[],"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens_details":{"reasoning_tokens":1}}}}`,
		``,
	}, "\n") + "\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/event-stream"},
			"X-Deepseek-Request-Id": []string{"ds-responses-stream-ok"},
		},
		Body: io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.OutputTokens)
	require.Equal(t, 1, result.Usage.ReasoningTokens)
	require.Equal(t, upstreamSSE, recorder.Body.String(), "DeepSeek Responses SSE must remain byte-for-byte opaque")
	require.NotContains(t, recorder.Body.String(), "sessions_resume")
}

func TestForwardDeepSeekResponsesStreamRejectsBareDone(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","stream":true}`)
	upstreamSSE := "data: [DONE]\n\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err)
	require.NotNil(t, result, "the already relayed wire still belongs to the failed attempt")
	require.Empty(t, result.UpstreamTerminalEvent)
	require.Equal(t, upstreamSSE, recorder.Body.String())
}

func TestForwardDeepSeekResponsesStreamRejectsTerminalDataBeforeBlankDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","input":"hello","stream":true}`)
	upstreamSSE := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_half\",\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.UpstreamTerminalEvent)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, upstreamSSE, recorder.Body.String())
}

func TestForwardDeepSeekResponsesStreamAcceptsCRLFBlankDispatch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","input":"hello","stream":true}`)
	upstreamSSE := "event: response.completed\r\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_crlf\",\"status\":\"completed\",\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\r\n\r\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, 4, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.Equal(t, upstreamSSE, recorder.Body.String())
}

func TestForwardDeepSeekResponsesFailedTerminalPreservesWireUsageAndError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-pro","input":"hello","stream":true}`)
	upstreamSSE := strings.Join([]string{
		`event: response.failed`,
		`data: {"type":"response.failed","response":{"id":"resp_ds_failed","object":"response","model":"deepseek-v4-pro","status":"failed","output":[],"error":{"code":"upstream_rejected","message":"rejected"},"usage":{"input_tokens":11,"output_tokens":4,"total_tokens":15,"input_tokens_details":{"cached_tokens":3},"output_tokens_details":{"reasoning_tokens":2}}}}`,
		``,
	}, "\n") + "\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err, "response.failed is a protocol terminal failure, not a successful attempt")
	require.NotNil(t, result, "usage from a failed terminal event must remain available for accounting")
	require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Equal(t, 3, result.Usage.CacheReadInputTokens)
	require.Equal(t, 4, result.Usage.OutputTokens)
	require.Equal(t, 2, result.Usage.ReasoningTokens)
	require.Equal(t, upstreamSSE, recorder.Body.String(), "the upstream failure event must not be replaced or followed by a synthetic event")
}

func TestForwardDeepSeekResponsesRejectsCompletedWithoutUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"id":"resp_missing_usage","object":"response","status":"completed","output":[]}`)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, deepSeekMissingUsageCode, gjson.GetBytes(failoverErr.ResponseBody, "error.code").String())
	require.False(t, c.Writer.Written(), "a non-stream response without usage must fail over before client output")
}

func TestForwardDeepSeekResponsesFailedWithoutUsagePreservesTerminalError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","stream":false}`)
	upstreamBody := `{"id":"resp_failed_no_usage","object":"response","status":"failed","output":[],"error":{"code":"rejected","message":"request rejected"}}`
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(upstreamBody)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
	require.Zero(t, result.Usage.InputTokens)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, upstreamBody, recorder.Body.String())
}

func TestForwardDeepSeekResponsesRejectsNonStreamWithoutTerminalBeforeWrite(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"application/json"},
			"X-Deepseek-Request-Id": []string{"ds-missing-terminal"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"resp_missing_terminal","object":"response","output":[],"usage":{"input_tokens":3,"output_tokens":1}}`)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, PlatformDeepSeek, failoverErr.Platform)
	require.Equal(t, "ds-missing-terminal", failoverErr.ResponseHeaders.Get("X-Deepseek-Request-Id"))
	require.False(t, c.Writer.Written(), "a non-stream response without terminal status must fail over before client output")
}

func TestForwardDeepSeekResponsesRejectsInvalidNonStreamJSONBeforeWriteAndRedactsKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","stream":false}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	account := deepSeekForwardTestAccount()
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/plain"},
			"X-Deepseek-Request-Id": []string{"ds-invalid-json"},
		},
		Body: io.NopCloser(strings.NewReader("invalid JSON echoed " + account.GetDeepSeekAPIKey())),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, account, body)
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, PlatformDeepSeek, failoverErr.Platform)
	require.Equal(t, "ds-invalid-json", failoverErr.ResponseHeaders.Get("X-Deepseek-Request-Id"))
	require.NotContains(t, string(failoverErr.ResponseBody), account.GetDeepSeekAPIKey())
	require.False(t, c.Writer.Written(), "invalid non-stream JSON must fail over before client output")
}

func TestForwardDeepSeekResponsesStreamRejectsCompletedWithoutUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4-flash","input":"hello","stream":true}`)
	upstreamSSE := "event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_missing_usage\",\"status\":\"completed\",\"output\":[]}}\n\n"
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(upstreamSSE)),
	}}
	svc := &OpenAIGatewayService{cfg: deepSeekForwardTestConfig(), httpUpstream: upstream}

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Equal(t, "response.completed", result.UpstreamTerminalEvent)
	require.Equal(t, upstreamSSE, recorder.Body.String())
}

func deepSeekForwardTestAccount() *Account {
	return &Account{
		ID:          901,
		Name:        "deepseek-api-key",
		Platform:    PlatformDeepSeek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "sk-deepseek-test",
			"base_url": "http://deepseek.example",
		},
	}
}

func deepSeekForwardTestConfig() *config.Config {
	return &config.Config{
		Security: config.SecurityConfig{
			URLAllowlist: config.URLAllowlistConfig{
				Enabled:           false,
				AllowInsecureHTTP: true,
			},
		},
	}
}
