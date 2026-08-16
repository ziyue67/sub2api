package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const deepSeekRemoteCompactTestSummary = "Implemented the gateway bridge; next run the focused regression tests."

func TestForwardDeepSeekResponsesRemoteCompactionV2UsesHarnessChatWireAndReturnsOneCompactionItem(t *testing.T) {
	body := deepSeekRemoteCompactDetailedTestRequestBody(t)
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactChatResponse(
		deepSeekRemoteCompactTestSummary,
		"private reasoning must not become the checkpoint",
		"stop",
	)}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.True(t, result.Stream)
	require.Equal(t, deepSeekChatCompletionsEndpoint, result.UpstreamEndpoint)
	require.Equal(t, 31, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.ReasoningTokens)

	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "/chat/completions", upstream.lastReq.URL.Path)
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.Equal(t, "1", upstream.lastReq.Header.Get("X-DeepSeek-Harness-Compact"))
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream_options.include_usage").Bool())
	require.Equal(t, int64(deepSeekCompactSummaryMaxTokens), gjson.GetBytes(upstream.lastBody, "max_tokens").Int())
	require.Equal(t, "enabled", gjson.GetBytes(upstream.lastBody, "thinking.type").String())
	require.Equal(t, deepSeekCompactReasoningEffort, gjson.GetBytes(upstream.lastBody, "reasoning_effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, deepSeekCompactReasoningEffort, *result.ReasoningEffort)
	require.False(t, gjson.GetBytes(upstream.lastBody, "temperature").Exists())
	require.Equal(t, "function", gjson.GetBytes(upstream.lastBody, "tools.0.type").String())
	require.Equal(t, "shell", gjson.GetBytes(upstream.lastBody, "tools.0.function.name").String())
	require.Equal(t, "Run a command", gjson.GetBytes(upstream.lastBody, "tools.0.function.description").String())
	require.Equal(t, "object", gjson.GetBytes(upstream.lastBody, "tools.0.function.parameters.type").String())

	messages := gjson.GetBytes(upstream.lastBody, "messages").Array()
	require.GreaterOrEqual(t, len(messages), 6)
	require.Equal(t, "system", messages[0].Get("role").String())
	require.Equal(t, "You are Codex. Preserve the current engineering task.", messages[0].Get("content").String())
	require.Equal(t, "system", messages[1].Get("role").String())
	require.Equal(t, "assistant", messages[len(messages)-3].Get("role").String())
	require.Equal(t, "shell", messages[len(messages)-3].Get("tool_calls.0.function.name").String())
	require.Equal(t, "tool", messages[len(messages)-2].Get("role").String())
	require.Equal(t, "user", messages[len(messages)-1].Get("role").String())
	require.Equal(t, deepSeekCompactInstruction, messages[len(messages)-1].Get("content").String())

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "text/event-stream", recorder.Header().Get("Content-Type"))
	events := parseCompactBridgeSSE(t, recorder.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "compaction", gjson.Get(events[0][1], "item.type").String())
	require.NotEmpty(t, gjson.Get(events[0][1], "item.encrypted_content").String())
	require.NotContains(t, events[0][1], deepSeekRemoteCompactTestSummary)
	require.NotContains(t, events[0][1], "private reasoning")
	require.Equal(t, "response.completed", events[1][0])
	require.Len(t, gjson.Get(events[1][1], "response.output").Array(), 1)
	require.Equal(t, "compaction", gjson.Get(events[1][1], "response.output.0.type").String())
	require.Equal(t, int64(38), gjson.Get(events[1][1], "response.usage.total_tokens").Int())
}

func TestForwardDeepSeekResponsesLegacyCompactReturnsUnaryJSON(t *testing.T) {
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model":            "deepseek-v4-flash",
		"stream":           true,
		"store":            true,
		"prompt_cache_key": "legacy-compact-session",
		"instructions":     "You are Codex.",
		"input": []any{map[string]any{
			"type": "message", "role": "user",
			"content": strings.Repeat("Preserve this legacy Codex context. ", 100),
		}},
	})
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactChatResponse(
		deepSeekRemoteCompactTestSummary,
		"private reasoning must not become the checkpoint",
		"stop",
	)}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	c.Request.URL.Path = "/v1/responses/compact"

	normalized, err := svc.NormalizeDeepSeekLegacyCompactRequest(c, body)
	require.NoError(t, err)
	require.False(t, gjson.GetBytes(normalized, "stream").Exists())
	require.False(t, gjson.GetBytes(normalized, "store").Exists())
	require.False(t, gjson.GetBytes(normalized, "prompt_cache_key").Exists())
	legacyInput := gjson.GetBytes(normalized, "input").Array()
	require.NotEmpty(t, legacyInput)
	require.Equal(t, "compaction_trigger", legacyInput[len(legacyInput)-1].Get("type").String())
	MarkDeepSeekCompaction(c, DeepSeekCompactionModeLegacyUnary)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), normalized)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)
	require.Equal(t, deepSeekChatCompletionsEndpoint, result.UpstreamEndpoint)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "application/json", recorder.Header().Get("Content-Type"))
	require.Equal(t, "response", gjson.GetBytes(recorder.Body.Bytes(), "object").String())
	require.Equal(t, "completed", gjson.GetBytes(recorder.Body.Bytes(), "status").String())
	require.Len(t, gjson.GetBytes(recorder.Body.Bytes(), "output").Array(), 1)
	require.Equal(t, "compaction", gjson.GetBytes(recorder.Body.Bytes(), "output.0.type").String())
	require.NotEmpty(t, gjson.GetBytes(recorder.Body.Bytes(), "output.0.encrypted_content").String())
	require.NotContains(t, recorder.Body.String(), "event: response.completed")
	require.NotNil(t, upstream.lastReq)
	require.Equal(t, "/chat/completions", upstream.lastReq.URL.Path)
}

func TestNormalizeDeepSeekLegacyCompactRequestRejectsInvalidTriggers(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	for _, input := range [][]any{
		{
			map[string]any{"type": "compaction_trigger"},
			map[string]any{"type": "message", "role": "user", "content": "after trigger"},
		},
		{
			map[string]any{"type": "message", "role": "user", "content": "context"},
			map[string]any{"type": "compaction_trigger"},
			map[string]any{"type": "compaction_trigger"},
		},
	} {
		body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
			"model": "deepseek-v4-flash",
			"input": input,
		})
		c, _ := newDeepSeekResponsesTestContext(t, body)
		_, err := svc.NormalizeDeepSeekLegacyCompactRequest(c, body)
		require.Error(t, err)
	}
}

func TestNormalizeDeepSeekLegacyCompactRequestConvertsStringInput(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash",
		"input": "legacy string history",
	})
	c, _ := newDeepSeekResponsesTestContext(t, body)

	normalized, err := svc.NormalizeDeepSeekLegacyCompactRequest(c, body)
	require.NoError(t, err)
	items := gjson.GetBytes(normalized, "input").Array()
	require.Len(t, items, 2)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "user", items[0].Get("role").String())
	require.Equal(t, "legacy string history", items[0].Get("content.0.text").String())
	require.Equal(t, "compaction_trigger", items[1].Get("type").String())
}

func TestDeepSeekCompactChatRequestMatchesHarnessHistorySemantics(t *testing.T) {
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": true,
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "alpha"},
				map[string]any{"type": "input_text", "text": "beta"},
			}},
			map[string]any{"type": "reasoning", "summary": []any{
				map[string]any{"type": "summary_text", "text": "reason-a"},
				map[string]any{"type": "summary_text", "text": "reason-b"},
			}},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": ""},
			map[string]any{"type": "reasoning", "summary": []any{map[string]any{"type": "summary_text", "text": "standalone"}}},
			map[string]any{"type": "message", "role": "user", "content": []any{
				map[string]any{"type": "input_text", "text": "gamma"},
				map[string]any{"type": "input_text", "text": "delta"},
			}},
			map[string]any{"type": "compaction_trigger"},
		},
	})
	chatBody, _, err := deepSeekCompactChatRequest(body, "deepseek-v4-flash")
	require.NoError(t, err)
	messages := gjson.GetBytes(chatBody, "messages").Array()
	require.Equal(t, "alphabeta", messages[0].Get("content").String())
	require.Equal(t, "assistant", messages[1].Get("role").String())
	require.Equal(t, "", messages[1].Get("content").String())
	require.True(t, messages[1].Get("content").Exists())
	require.Equal(t, "reason-areason-b", messages[1].Get("reasoning_content").String())
	require.Equal(t, "tool", messages[2].Get("role").String())
	require.Equal(t, "(no output)", messages[2].Get("content").String())
	require.Equal(t, "assistant", messages[3].Get("role").String())
	require.True(t, messages[3].Get("content").Exists())
	require.Equal(t, "", messages[3].Get("content").String())
	require.Equal(t, "gammadelta", messages[4].Get("content").String())
	require.Equal(t, deepSeekCompactInstruction, messages[len(messages)-1].Get("content").String())
}

func TestDeepSeekCompactChatRequestFlattensToolOutputTextParts(t *testing.T) {
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": true,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "run the tool"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": []any{
				map[string]any{"type": "input_text", "text": "alpha"},
				map[string]any{"type": "input_text", "text": "beta"},
			}},
			map[string]any{"type": "compaction_trigger"},
		},
	})
	chatBody, _, err := deepSeekCompactChatRequest(body, "deepseek-v4-flash")
	require.NoError(t, err)
	messages := gjson.GetBytes(chatBody, "messages").Array()
	require.Equal(t, "tool", messages[2].Get("role").String())
	require.Equal(t, "alphabeta", messages[2].Get("content").String())
}

func TestDeepSeekCompactChatRequestRejectsUnbalancedToolHistory(t *testing.T) {
	for _, history := range [][]any{
		{
			map[string]any{"type": "message", "role": "user", "content": "run the tool"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{}`},
		},
		{
			map[string]any{"type": "message", "role": "user", "content": "orphan result"},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "done"},
		},
	} {
		body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
			"model": "deepseek-v4-flash", "stream": true,
			"input": append(history, map[string]any{"type": "compaction_trigger"}),
		})
		_, _, err := deepSeekCompactChatRequest(body, "deepseek-v4-flash")
		require.Error(t, err)
	}
}

func TestDeepSeekCompactChatRequestRejectsPreviousResponseID(t *testing.T) {
	body := deepSeekRemoteCompactTestRequestBody()
	body, err := sjson.SetBytes(body, "previous_response_id", "resp_server_state")
	require.NoError(t, err)
	_, _, err = deepSeekCompactChatRequest(body, "deepseek-v4-flash")
	require.ErrorContains(t, err, "does not support previous_response_id")
}

func TestForwardDeepSeekResponsesRestoresRemoteCompactionAsFramedUserCheckpoint(t *testing.T) {
	compactItem := runDeepSeekRemoteCompactTestTurn(t)
	nextUser := map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "Continue with the regression test."}},
	}
	nextBody := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": false, "instructions": "You are Codex.",
		"input": []any{compactItem, nextUser},
	})
	nextResponse := `{
		"id":"resp_ds_continue","object":"response","model":"deepseek-v4-flash","status":"completed",
		"output":[{"id":"msg_continue","type":"message","role":"assistant","content":[{"type":"output_text","text":"continued"}]}],
		"usage":{"input_tokens":9,"output_tokens":2,"total_tokens":11}
	}`
	c, recorder := newDeepSeekResponsesTestContext(t, nextBody)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(nextResponse)),
	}}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), nextBody)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Stream)

	restoredInput := gjson.GetBytes(upstream.lastBody, "input").Array()
	require.Len(t, restoredInput, 2)
	require.Equal(t, "message", restoredInput[0].Get("type").String())
	require.Equal(t, "user", restoredInput[0].Get("role").String())
	checkpoint := restoredInput[0].Get("content.0.text").String()
	require.Equal(t, frameDeepSeekCompactSummary(deepSeekRemoteCompactTestSummary), checkpoint)
	require.JSONEq(t, mustMarshalDeepSeekCompactTestJSONString(t, nextUser), restoredInput[1].Raw)
	require.False(t, gjson.GetBytes(upstream.lastBody, `input.#(type=="compaction")`).Exists())
	require.Equal(t, nextResponse, recorder.Body.String())
}

func TestForwardDeepSeekResponsesRemoteCompactionFailsClosedBeforeClientOutput(t *testing.T) {
	tests := []struct {
		name string
		resp *http.Response
	}{
		{name: "empty_text", resp: deepSeekRemoteCompactChatResponse("   ", "", "stop")},
		{name: "reasoning_only", resp: deepSeekRemoteCompactChatResponse("", "private reasoning is not a checkpoint", "stop")},
		{name: "incomplete", resp: deepSeekRemoteCompactChatResponse("partial checkpoint", "", "length")},
		{name: "image_output", resp: deepSeekRemoteCompactRawChatResponse(
			`{"id":"chatcmpl_compact","choices":[{"index":0,"delta":{"content":[{"type":"image_url","image_url":{"url":"data:image/png;base64,aW1hZ2U="}}]},"finish_reason":"stop"}]}`,
		)},
		{name: "error_event_before_text", resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body: io.NopCloser(strings.NewReader(
				"event: error\n" +
					"data: {\"type\":\"error\",\"error\":{\"message\":\"provider failed\"}}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"must not be accepted\"},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n" +
					"data: [DONE]\n\n",
			)),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := deepSeekRemoteCompactTestRequestBody()
			c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: tt.resp}
			svc := newDeepSeekRemoteCompactTestService(upstream)

			result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
			require.Error(t, err)
			require.NotNil(t, result, "a completed billable summary call must not be retried")
			require.True(t, result.HasBillableTokenUsage())
			var failoverErr *UpstreamFailoverError
			require.False(t, errors.As(err, &failoverErr))
			if tt.name == "error_event_before_text" {
				require.Empty(t, result.UpstreamTerminalEvent)
			}
			require.NotNil(t, upstream.lastReq)
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.String())
		})
	}
}

func TestForwardDeepSeekResponsesRemoteCompactionRequiresDispatchedDoneAndUsage(t *testing.T) {
	tests := []struct {
		name         string
		body         string
		wantBillable bool
	}{
		{
			name:         "done_without_blank_dispatch",
			wantBillable: true,
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"summary\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n" +
				"data: [DONE]\n",
		},
		{
			name: "missing_usage",
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"summary\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: [DONE]\n\n",
		},
		{
			name:         "done_payload_on_error_event",
			wantBillable: true,
			body: "data: {\"choices\":[{\"delta\":{\"content\":\"must not be accepted\"},\"finish_reason\":\"stop\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n" +
				"event: error\n" +
				"data: [DONE]\n\n",
		},
		{
			name:         "malformed_event_then_usage",
			wantBillable: true,
			body: "data: {malformed-json\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":1}}\n\n" +
				"data: [DONE]\n\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := deepSeekRemoteCompactTestRequestBody()
			c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}}
			svc := newDeepSeekRemoteCompactTestService(upstream)
			result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
			require.Error(t, err)
			require.Empty(t, recorder.Body.String())
			var failoverErr *UpstreamFailoverError
			if tt.wantBillable {
				require.NotNil(t, result)
				require.True(t, result.HasBillableTokenUsage())
				require.False(t, errors.As(err, &failoverErr))
			} else {
				require.Nil(t, result)
				require.True(t, errors.As(err, &failoverErr))
			}
		})
	}
}

func TestForwardDeepSeekResponsesRejectsTamperedCompactEnvelopeBeforeUpstream(t *testing.T) {
	compactItem := runDeepSeekRemoteCompactTestTurn(t)
	envelope, _ := compactItem["encrypted_content"].(string)
	require.NotEmpty(t, envelope)
	compactItem["encrypted_content"] = tamperDeepSeekCompactTestEnvelope(envelope)
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": false,
		"input": []any{compactItem, map[string]any{
			"type": "message", "role": "user",
			"content": []any{map[string]any{"type": "input_text", "text": "Continue."}},
		}},
	})
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactChatResponse("unused", "", "stop")}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.ErrorIs(t, err, ErrDeepSeekCompactInvalidEncryptedContent)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestDeepSeekCompactEnvelopeIsBoundToAuthenticatedUser(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	checkpoint := frameDeepSeekCompactSummary("tenant-bound summary")
	envelope, err := svc.sealDeepSeekCompactCheckpoint(deepSeekCompactTestContext(42), checkpoint)
	require.NoError(t, err)

	restored, err := svc.openDeepSeekCompactCheckpoint(deepSeekCompactTestContext(42), envelope)
	require.NoError(t, err)
	require.Equal(t, checkpoint, restored)
	_, err = svc.openDeepSeekCompactCheckpoint(deepSeekCompactTestContext(43), envelope)
	require.ErrorIs(t, err, ErrDeepSeekCompactInvalidEncryptedContent)
}

func TestForwardDeepSeekResponsesRemoteCompactionRedactsSplitAPIKeyFromCheckpoint(t *testing.T) {
	body := deepSeekRemoteCompactTestRequestBody()
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactRawChatResponse(
		`{"id":"chatcmpl_compact","choices":[{"delta":{"content":"sk-deep"}}]}`,
		`{"id":"chatcmpl_compact","choices":[{"delta":{"content":"seek-test safe summary"},"finish_reason":"stop"}]}`,
	)}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	events := parseCompactBridgeSSE(t, recorder.Body.String())
	envelope := gjson.Get(events[0][1], "item.encrypted_content").String()
	checkpoint, err := svc.openDeepSeekCompactCheckpoint(deepSeekCompactTestContext(42), envelope)
	require.NoError(t, err)
	require.NotContains(t, checkpoint, "sk-deepseek-test")
	require.Contains(t, checkpoint, "[redacted]")
}

func TestRestoreDeepSeekCompactInputRejectsNonStringEncryptedContent(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	for _, encryptedContent := range []string{`null`, `123`, `[]`, `{}`} {
		body := []byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","encrypted_content":` + encryptedContent + `}]}`)
		restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), body)
		require.ErrorIs(t, err, ErrDeepSeekCompactInvalidEncryptedContent)
		require.Nil(t, restored)
		require.False(t, changed)
	}
	duplicateInput := []byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":"first"}],"input":[{"type":"compaction","encrypted_content":{}}]}`)
	restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), duplicateInput)
	require.ErrorIs(t, err, ErrDeepSeekResponsesDuplicateJSONKey)
	require.Nil(t, restored)
	require.False(t, changed)
	nonArrayInput := []byte(`{"model":"deepseek-v4-flash","input":{"type":"compaction","encrypted_content":"foreign"}}`)
	restored, changed, err = svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), nonArrayInput)
	require.ErrorIs(t, err, ErrDeepSeekCompactInvalidEncryptedContent)
	require.Nil(t, restored)
	require.False(t, changed)
}

func TestRestoreDeepSeekCompactInputRejectsDuplicateJSONKeys(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	tests := [][]byte{
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","encrypted_content":"foreign"}],"input":[{"type":"message","role":"user","content":"benign-last"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":"benign-first"}],"input":[{"type":"compaction","encrypted_content":"foreign"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","encrypted_content":"foreign","type":"message","role":"user","content":"benign-last"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","encrypted_content":"foreign","encrypted_content":"also-foreign"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"benign","text":"malicious"}]}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"benign","text":"malicious"}]}]}`),
	}
	for _, body := range tests {
		restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), body)
		require.ErrorIs(t, err, ErrDeepSeekResponsesDuplicateJSONKey)
		require.Nil(t, restored)
		require.False(t, changed)
	}
}

func TestRestoreDeepSeekCompactInputRejectsNonCanonicalStructuralKeys(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	for _, body := range [][]byte{
		[]byte(`{"model":"deepseek-v4-flash","Input":[{"type":"message","role":"user","content":"hidden"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"Type":"compaction","encrypted_content":"foreign"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","Text":"hidden"}]}]}`),
	} {
		restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), body)
		require.ErrorIs(t, err, ErrDeepSeekResponsesNonCanonicalJSONKey)
		require.Nil(t, restored)
		require.False(t, changed)
	}
}

func TestPrepareDeepSeekRemoteCompactionRequestUsesGatewayMaxBodySize(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	body := deepSeekRemoteCompactTestRequestBody()
	svc.cfg.Gateway.MaxBodySize = int64(len(body))
	require.NoError(t, svc.PrepareDeepSeekRemoteCompactionRequest(nil, body))
	require.ErrorIs(t, svc.PrepareDeepSeekRemoteCompactionRequest(nil, append(body, ' ')), ErrDeepSeekCompactRequestTooLarge)
}

func TestRestoreDeepSeekCompactInputUsesGatewayMaxBodySize(t *testing.T) {
	const maxBodySize = 1024
	ordinaryPrefix := []byte(`{"model":"deepseek-v4-pro","input":[{"type":"message","role":"user","content":"`)
	ordinarySuffix := []byte(`"}]}`)
	ordinaryBody := make([]byte, 0, len(ordinaryPrefix)+maxBodySize+len(ordinarySuffix))
	ordinaryBody = append(ordinaryBody, ordinaryPrefix...)
	ordinaryBody = append(ordinaryBody, bytes.Repeat([]byte("a"), maxBodySize)...)
	ordinaryBody = append(ordinaryBody, ordinarySuffix...)
	require.Greater(t, len(ordinaryBody), maxBodySize)

	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	svc.cfg.Gateway.MaxBodySize = maxBodySize
	restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), ordinaryBody)
	require.ErrorIs(t, err, ErrDeepSeekCompactRequestTooLarge)
	require.False(t, changed)
	require.Nil(t, restored)

	compactPrefix := []byte(`{"model":"deepseek-v4-pro","padding":"`)
	compactSuffix := []byte(`","input":[{"type":"compaction","encrypted_content":"foreign"}]}`)
	compactBody := make([]byte, 0, len(compactPrefix)+maxBodySize+len(compactSuffix))
	compactBody = append(compactBody, compactPrefix...)
	compactBody = append(compactBody, bytes.Repeat([]byte("a"), maxBodySize)...)
	compactBody = append(compactBody, compactSuffix...)
	require.Greater(t, len(compactBody), maxBodySize)

	restored, changed, err = svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), compactBody)
	require.ErrorIs(t, err, ErrDeepSeekCompactRequestTooLarge)
	require.Nil(t, restored)
	require.False(t, changed)
}

func TestForwardDeepSeekResponsesRemoteCompactionPreservesEarlierBillableUsage(t *testing.T) {
	body := deepSeekRemoteCompactTestRequestBody()
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	stream := "data: {\"id\":\"chat_compact\",\"choices\":[{\"delta\":{\"content\":\"short summary\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":31,\"completion_tokens\":7,\"total_tokens\":38}}\n\n" +
		"data: {\"choices\":[],\"usage\":{}}\n\n" +
		"data: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.Equal(t, 31, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Contains(t, recorder.Body.String(), "response.completed")
}

func TestForwardDeepSeekResponsesRemoteCompactionMarksSSECyberPolicyWithUsage(t *testing.T) {
	body := deepSeekRemoteCompactTestRequestBody()
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	stream := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"code\":\"cyber_policy\",\"message\":\"blocked by policy\"}}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":17,\"completion_tokens\":2,\"total_tokens\":19}}\n\n" +
		"data: [DONE]\n\n"
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err)
	require.NotNil(t, result)
	require.Empty(t, result.UpstreamTerminalEvent)
	require.Equal(t, 17, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	mark := GetOpsCyberPolicy(c)
	require.NotNil(t, mark)
	require.Equal(t, "cyber_policy", mark.Code)
	require.Equal(t, 17, mark.UpstreamInTok)
	require.Equal(t, 2, mark.UpstreamOutTok)
	require.Empty(t, recorder.Body.String())
}

func TestDeepSeekCompactImageDetectionIgnoresTextToolDataFields(t *testing.T) {
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": true,
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": "inspect metadata"},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "lookup", "arguments": `{}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": map[string]any{"image_url": "record identifier"}},
			map[string]any{"type": "compaction_trigger"},
		},
	})
	_, _, err := deepSeekCompactChatRequest(body, "deepseek-v4-flash")
	require.NoError(t, err)
}

func TestForwardDeepSeekResponsesRemoteCompactionRejectsImageInputBeforeUpstream(t *testing.T) {
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": true,
		"input": []any{
			map[string]any{
				"type": "message", "role": "user",
				"content": []any{map[string]any{
					"type": "input_image", "image_url": "data:image/png;base64,aW1hZ2U=",
				}},
			},
			map[string]any{"type": "compaction_trigger"},
		},
	})
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactChatResponse("unused", "", "stop")}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.ErrorContains(t, err, "does not support image content")
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Empty(t, recorder.Body.String())
}

func TestForwardDeepSeekResponsesOrdinaryWireRemainsByteForByteUnchanged(t *testing.T) {
	body := []byte("{\n  \"model\": \"deepseek-v4-pro\",\n  \"stream\": false,\n  \"input\": [{\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"ordinary request\"}]}]\n}")
	responseBody := "{\n  \"id\": \"resp_ds_opaque\",\n  \"object\": \"response\",\n  \"model\": \"deepseek-v4-pro\",\n  \"status\": \"completed\",\n  \"output\": [{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]}],\n  \"usage\": {\"input_tokens\": 4, \"output_tokens\": 1, \"total_tokens\": 5}\n}"
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	c.Request.Header.Set("X-DeepSeek-Harness-Compact", "1")
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(responseBody)),
	}}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, body, upstream.lastBody)
	require.Equal(t, responseBody, recorder.Body.String())
}

func TestForwardDeepSeekResponsesRejectsForeignCompactItemBeforeUpstream(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","stream":false,"input":[{"type":"compaction","encrypted_content":"foreign-provider-state"},{"type":"message","role":"user","content":"continue"}]}`)
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactChatResponse("unused", "", "stop")}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.ErrorIs(t, err, ErrDeepSeekCompactInvalidEncryptedContent)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Empty(t, recorder.Body.String())
}

type delayedDeepSeekCompactHTTPUpstream struct {
	HTTPUpstream
	delay time.Duration
	resp  *http.Response
}

func (u *delayedDeepSeekCompactHTTPUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	time.Sleep(u.delay)
	return u.resp, nil
}

func TestForwardDeepSeekRemoteCompactionCommittedKeepaliveUsesResponsesFailureEvent(t *testing.T) {
	body := deepSeekRemoteCompactTestRequestBody()
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	stop := StartOpenAICompactSSEKeepalive(c, time.Millisecond)
	t.Cleanup(stop)
	svc := newDeepSeekRemoteCompactTestService(nil)
	svc.httpUpstream = &delayedDeepSeekCompactHTTPUpstream{
		delay: 10 * time.Millisecond,
		resp: &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"bad compact request"}}`)),
		},
	}
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.Error(t, err)
	require.Nil(t, result)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), ": keepalive\n\n")
	require.Contains(t, recorder.Body.String(), "event: response.failed")
	require.NotContains(t, recorder.Body.String(), `"type":"compaction"`)
}

func runDeepSeekRemoteCompactTestTurn(t *testing.T) map[string]any {
	t.Helper()
	body := deepSeekRemoteCompactTestRequestBody()
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactChatResponse(deepSeekRemoteCompactTestSummary, "", "stop")}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	events := parseCompactBridgeSSE(t, recorder.Body.String())
	require.Len(t, events, 2)
	itemJSON := gjson.Get(events[0][1], "item").Raw
	require.NotEmpty(t, itemJSON)
	var item map[string]any
	require.NoError(t, json.Unmarshal([]byte(itemJSON), &item))
	return item
}

func deepSeekRemoteCompactTestRequestBody() []byte {
	request := map[string]any{
		"model": "deepseek-v4-flash", "stream": true, "instructions": "You are Codex.",
		"tools": []any{map[string]any{"type": "function", "name": "shell", "parameters": map[string]any{"type": "object"}}},
		"input": []any{
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{
				"type": "input_text", "text": strings.Repeat("important engineering context ", 80),
			}}},
			map[string]any{"type": "compaction_trigger"},
		},
	}
	encoded, _ := json.Marshal(request)
	return encoded
}

func deepSeekRemoteCompactDetailedTestRequestBody(t *testing.T) []byte {
	t.Helper()
	return mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "stream": true, "store": true,
		"instructions": "You are Codex. Preserve the current engineering task.",
		"tools":        []any{map[string]any{"type": "function", "name": "shell", "description": "Run a command", "parameters": map[string]any{"type": "object"}}},
		"tool_choice":  "auto",
		"input": []any{
			map[string]any{"type": "message", "role": "system", "content": []any{map[string]any{"type": "input_text", "text": "Repository rules apply."}}},
			map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": strings.Repeat("Implement the compact bridge with full contract coverage. ", 80)}}},
			map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{"cmd":"go test ./..."}`},
			map[string]any{"type": "function_call_output", "call_id": "call_1", "output": "one focused test is failing"},
			map[string]any{"type": "compaction_trigger"},
		},
	})
}

func newDeepSeekRemoteCompactTestContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	c.Request.Header.Set("Accept", "text/event-stream")
	c.Request.Header.Set("X-Codex-Beta-Features", "responses_websockets_v2, remote_compaction_v2")
	MarkDeepSeekRemoteCompactionV2(c)
	return c, recorder
}

func newDeepSeekResponsesTestContext(t *testing.T, body []byte) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	return c, recorder
}

func deepSeekRemoteCompactChatResponse(content, reasoning, finishReason string) *http.Response {
	contentJSON, _ := json.Marshal(content)
	reasoningJSON, _ := json.Marshal(reasoning)
	payload := `{"id":"chatcmpl_compact","model":"deepseek-v4-flash","choices":[{"index":0,"delta":{"content":` + string(contentJSON) + `,"reasoning_content":` + string(reasoningJSON) + `},"finish_reason":"` + finishReason + `"}]}`
	return deepSeekRemoteCompactRawChatResponse(payload)
}

func deepSeekRemoteCompactRawChatResponse(payloads ...string) *http.Response {
	var stream strings.Builder
	for _, payload := range payloads {
		_, _ = stream.WriteString("data: ")
		_, _ = stream.WriteString(payload)
		_, _ = stream.WriteString("\n\n")
	}
	_, _ = stream.WriteString(`data: {"id":"chatcmpl_compact","model":"deepseek-v4-flash","choices":[],"usage":{"prompt_tokens":31,"completion_tokens":7,"total_tokens":38,"prompt_tokens_details":{"cached_tokens":5},"completion_tokens_details":{"reasoning_tokens":3}}}`)
	_, _ = stream.WriteString("\n\n")
	_, _ = stream.WriteString("data: [DONE]\n\n")
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/event-stream"},
			"X-Deepseek-Request-Id": []string{"ds-compact-test"},
		},
		Body: io.NopCloser(strings.NewReader(stream.String())),
	}
}

func newDeepSeekRemoteCompactTestService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	cfg := deepSeekForwardTestConfig()
	cfg.JWT = config.JWTConfig{Secret: "deepseek-compact-test-jwt-secret-32-bytes"}
	return &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
}

func deepSeekCompactTestContext(userID int64) context.Context {
	return context.WithValue(context.Background(), ctxkey.UserID, userID)
}

func mustMarshalDeepSeekCompactTestJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return encoded
}

func mustMarshalDeepSeekCompactTestJSONString(t *testing.T, value any) string {
	t.Helper()
	return string(mustMarshalDeepSeekCompactTestJSON(t, value))
}

func tamperDeepSeekCompactTestEnvelope(value string) string {
	bytesValue := []byte(value)
	if len(bytesValue) == 0 {
		return "tampered"
	}
	index := len(bytesValue) / 2
	if bytesValue[index] == 'A' {
		bytesValue[index] = 'B'
	} else {
		bytesValue[index] = 'A'
	}
	return string(bytesValue)
}
