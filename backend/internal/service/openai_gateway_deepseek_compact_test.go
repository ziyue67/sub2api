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
)

const deepSeekRemoteCompactTestSummary = "Implemented the gateway bridge; next run the focused regression tests."

func TestDeepSeekCompactResponsesRequestPreservesNativeHistoryAndEffort(t *testing.T) {
	history := []any{
		map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "keep this verbatim"}}},
		map[string]any{"type": "function_call", "call_id": "call_1", "name": "shell", "arguments": `{"cmd":"go test ./..."}`, "opaque": map[string]any{"keep": true}},
		map[string]any{"type": "function_call_output", "call_id": "call_1", "output": []any{map[string]any{"type": "input_text", "text": "passed"}}},
		map[string]any{"type": "opaque_future_item", "payload": map[string]any{"keep": true}},
	}
	for _, effort := range []string{"max", "high", ""} {
		t.Run("effort_"+effort, func(t *testing.T) {
			input := append(append([]any(nil), history...), map[string]any{"type": "compaction_trigger"})
			request := map[string]any{
				"model": "client-model", "instructions": "preserve these instructions", "input": input,
				"tools": []any{map[string]any{"type": "function", "name": "shell"}}, "tool_choice": "auto",
				"parallel_tool_calls": true, "store": true, "include": []string{"reasoning.encrypted_content"},
				"temperature": 0.1, "top_p": 0.2, "service_tier": "priority", "prompt_cache_key": "cache-key",
			}
			if effort != "" {
				request["reasoning"] = map[string]any{"effort": effort, "summary": "detailed"}
			}
			body := mustMarshalDeepSeekCompactTestJSON(t, request)
			upstreamBody, err := deepSeekCompactResponsesRequest(body, "mapped-model")
			require.NoError(t, err)

			var root map[string]json.RawMessage
			require.NoError(t, json.Unmarshal(upstreamBody, &root))
			wantKeys := []string{"model", "instructions", "input", "stream", "max_output_tokens"}
			if effort != "" {
				wantKeys = append(wantKeys, "reasoning")
			}
			gotKeys := make([]string, 0, len(root))
			for key := range root {
				gotKeys = append(gotKeys, key)
			}
			require.ElementsMatch(t, wantKeys, gotKeys)
			require.Equal(t, "mapped-model", gjson.GetBytes(upstreamBody, "model").String())
			require.Equal(t, "preserve these instructions", gjson.GetBytes(upstreamBody, "instructions").String())
			require.True(t, gjson.GetBytes(upstreamBody, "stream").Bool())
			require.Equal(t, int64(deepSeekCompactSummaryMaxTokens), gjson.GetBytes(upstreamBody, "max_output_tokens").Int())
			if effort == "" {
				require.False(t, gjson.GetBytes(upstreamBody, "reasoning").Exists())
			} else {
				require.Equal(t, effort, gjson.GetBytes(upstreamBody, "reasoning.effort").String())
				require.False(t, gjson.GetBytes(upstreamBody, "reasoning.summary").Exists())
			}
			items := gjson.GetBytes(upstreamBody, "input").Array()
			require.Len(t, items, len(history)+1)
			for index, original := range history {
				require.JSONEq(t, mustMarshalDeepSeekCompactTestJSONString(t, original), items[index].Raw)
			}
			require.Equal(t, "message", items[len(items)-1].Get("type").String())
			require.Equal(t, "user", items[len(items)-1].Get("role").String())
			require.Equal(t, deepSeekCompactInstruction, items[len(items)-1].Get("content.0.text").String())
		})
	}
}

func TestDeepSeekCompactResponsesRequestRejectsInvalidStateBeforeUpstream(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{name: "previous response id", body: map[string]any{"model": "m", "previous_response_id": "resp_1", "input": []any{map[string]any{"type": "message"}, map[string]any{"type": "compaction_trigger"}}}},
		{name: "missing trigger", body: map[string]any{"model": "m", "input": []any{map[string]any{"type": "message"}}}},
		{name: "non-final trigger", body: map[string]any{"model": "m", "input": []any{map[string]any{"type": "compaction_trigger"}, map[string]any{"type": "message"}}}},
		{name: "unpaired tool call", body: map[string]any{"model": "m", "input": []any{map[string]any{"type": "function_call", "call_id": "call_1"}, map[string]any{"type": "compaction_trigger"}}}},
		{name: "image", body: map[string]any{"model": "m", "input": []any{map[string]any{"type": "message", "content": []any{map[string]any{"type": "input_image", "image_url": "data:image/png;base64,aQ=="}}}, map[string]any{"type": "compaction_trigger"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := deepSeekCompactResponsesRequest(mustMarshalDeepSeekCompactTestJSON(t, tt.body), "mapped")
			require.Error(t, err)
		})
	}
}

func TestForwardDeepSeekRemoteCompactionUsesNativeResponsesAndSynthesizesOneItem(t *testing.T) {
	body := deepSeekRemoteCompactDetailedTestRequestBody(t)
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactResponsesResponse(deepSeekRemoteCompactTestSummary)}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, deepSeekResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, UsageRequestKindCompact, result.RequestKind)
	require.Equal(t, "/responses", upstream.lastReq.URL.Path)
	require.Equal(t, "text/event-stream", upstream.lastReq.Header.Get("Accept"))
	require.True(t, gjson.GetBytes(upstream.lastBody, "stream").Bool())
	require.False(t, gjson.GetBytes(upstream.lastBody, "tools").Exists())
	require.Equal(t, "max", gjson.GetBytes(upstream.lastBody, "reasoning.effort").String())
	require.NotNil(t, result.ReasoningEffort)
	require.Equal(t, "max", *result.ReasoningEffort)
	require.Equal(t, 31, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 5, result.Usage.CacheReadInputTokens)
	require.Equal(t, 3, result.Usage.ReasoningTokens)

	events := parseCompactBridgeSSE(t, recorder.Body.String())
	require.Len(t, events, 2)
	require.Equal(t, "response.output_item.done", events[0][0])
	require.Equal(t, "response.completed", events[1][0])
	require.Len(t, gjson.Get(events[1][1], "response.output").Array(), 1)
	envelope := gjson.Get(events[0][1], "item.encrypted_content").String()
	checkpoint, err := svc.openDeepSeekCompactCheckpoint(deepSeekCompactTestContext(42), envelope)
	require.NoError(t, err)
	require.Contains(t, checkpoint, deepSeekRemoteCompactTestSummary)
	require.NotContains(t, checkpoint, "private chain of thought")
}

func TestForwardDeepSeekLegacyCompactReturnsUnaryResponsesJSON(t *testing.T) {
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "deepseek-v4-flash", "instructions": "You are Codex.",
		"input": strings.Repeat("legacy context ", 100),
	})
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	c.Request.URL.Path = "/v1/responses/compact"
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{resp: deepSeekRemoteCompactResponsesResponse(deepSeekRemoteCompactTestSummary)})
	normalized, err := svc.NormalizeDeepSeekLegacyCompactRequest(c, body)
	require.NoError(t, err)
	MarkDeepSeekCompaction(c, DeepSeekCompactionModeLegacyUnary)
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), normalized)
	require.NoError(t, err)
	require.False(t, result.Stream)
	require.Equal(t, UsageRequestKindCompact, result.RequestKind)
	require.Equal(t, deepSeekResponsesEndpoint, result.UpstreamEndpoint)
	require.Equal(t, "response", gjson.GetBytes(recorder.Body.Bytes(), "object").String())
	require.Equal(t, "compaction", gjson.GetBytes(recorder.Body.Bytes(), "output.0.type").String())
	require.NotEmpty(t, gjson.GetBytes(recorder.Body.Bytes(), "output.0.encrypted_content").String())
}

func TestReadDeepSeekCompactResponsesStreamStrictTerminalContract(t *testing.T) {
	completed := deepSeekRemoteCompactCompletedPayload(deepSeekRemoteCompactTestSummary, true, nil)
	completedNoUsage := deepSeekRemoteCompactCompletedPayload(deepSeekRemoteCompactTestSummary, false, nil)
	completedNoText := deepSeekRemoteCompactCompletedPayload("", true, nil)
	completedToolOutput := deepSeekRemoteCompactCompletedPayload("", true, []any{map[string]any{"type": "function_call", "status": "completed"}})
	completedWithError := deepSeekRemoteCompactCompletedWithErrorPayload(deepSeekRemoteCompactTestSummary)
	usageBeforeTerminal := `{"type":"response.in_progress","response":{"id":"resp_usage_only","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`
	incomplete := deepSeekRemoteCompactTerminalPayload("response.incomplete", "incomplete", true)
	failed := deepSeekRemoteCompactTerminalPayload("response.failed", "failed", true)
	tests := []struct {
		name      string
		wire      string
		wantErr   bool
		wantDone  bool
		wantUsage bool
		wantFirst bool
	}{
		{name: "completed", wire: deepSeekRemoteCompactSSEWire(`{"type":"response.output_text.delta","delta":"first"}`, completed), wantDone: true, wantUsage: true, wantFirst: true},
		{name: "error null", wire: deepSeekRemoteCompactSSEWire(strings.TrimSuffix(completed, "}") + `,"error":null}`), wantDone: true, wantUsage: true},
		{name: "nested response error", wire: deepSeekRemoteCompactSSEWire(completedWithError), wantErr: true, wantDone: true, wantUsage: true},
		{name: "done forbidden after completed", wire: deepSeekRemoteCompactSSEWire(completed, "[DONE]"), wantErr: true, wantDone: true, wantUsage: true},
		{name: "duplicate terminal", wire: deepSeekRemoteCompactSSEWire(completed, completed), wantErr: true, wantUsage: true},
		{name: "incomplete", wire: deepSeekRemoteCompactSSEWire(incomplete), wantErr: true, wantUsage: true},
		{name: "failed", wire: deepSeekRemoteCompactSSEWire(failed), wantErr: true, wantUsage: true},
		{name: "missing usage", wire: deepSeekRemoteCompactSSEWire(completedNoUsage), wantErr: true, wantDone: true},
		{name: "no visible text", wire: deepSeekRemoteCompactSSEWire(completedNoText), wantErr: true, wantDone: true, wantUsage: true},
		{name: "tool output", wire: deepSeekRemoteCompactSSEWire(completedToolOutput), wantErr: true, wantDone: true, wantUsage: true},
		{name: "undispatched terminal", wire: strings.TrimSuffix(deepSeekRemoteCompactSSEWire(completed), "\n"), wantErr: true},
		{name: "usage before truncated stream", wire: deepSeekRemoteCompactSSEWire(usageBeforeTerminal), wantErr: true, wantUsage: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newDeepSeekRemoteCompactTestContext(t, deepSeekRemoteCompactTestRequestBody())
			svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
			resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(tt.wire))}
			result, err := svc.readDeepSeekCompactResponsesStream(c, resp, deepSeekForwardTestAccount(), time.Now())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, deepSeekRemoteCompactTestSummary, result.Summary)
			}
			require.Equal(t, tt.wantDone, result.Completed)
			require.Equal(t, tt.wantUsage, hasBillableOpenAIUsage(result.Usage))
			require.Equal(t, tt.wantFirst, result.FirstTokenMs != nil)
		})
	}
}

func TestReadDeepSeekCompactResponsesStreamRejectsMalformedWireAndImageOnlyTerminal(t *testing.T) {
	completed := deepSeekRemoteCompactCompletedPayload(deepSeekRemoteCompactTestSummary, true, nil)
	imageOnly := deepSeekRemoteCompactCompletedPayload("", true, []any{map[string]any{
		"id": "msg_image_only", "type": "message", "role": "assistant", "status": "completed",
		"content": []any{map[string]any{"type": "output_image", "image_url": "data:image/png;base64,aW1hZ2U="}},
	}})
	tests := []struct {
		name          string
		wire          string
		wantCompleted bool
		wantUsage     bool
	}{
		{
			name: "malformed JSON data",
			wire: "event: response.completed\n" +
				"data: {\"type\":\"response.completed\",\"response\":\n\n",
		},
		{
			name: "malformed SSE data field",
			wire: "event: response.completed\n" +
				"data " + completed + "\n\n",
		},
		{
			name:          "image-only completed terminal",
			wire:          deepSeekRemoteCompactSSEWire(imageOnly),
			wantCompleted: true,
			wantUsage:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newDeepSeekRemoteCompactTestContext(t, deepSeekRemoteCompactTestRequestBody())
			svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(tt.wire)),
			}

			result, err := svc.readDeepSeekCompactResponsesStream(c, resp, deepSeekForwardTestAccount(), time.Now())

			require.Error(t, err)
			require.True(t, result.UpstreamFailed)
			require.Equal(t, tt.wantCompleted, result.Completed)
			require.Equal(t, tt.wantUsage, hasBillableOpenAIUsage(result.Usage))
			require.Empty(t, result.Summary)
		})
	}
}

func TestForwardDeepSeekRemoteCompactionMissingUsageAllowsFailover(t *testing.T) {
	body := deepSeekRemoteCompactDetailedTestRequestBody(t)
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	completedWithoutUsage := deepSeekRemoteCompactCompletedPayload(deepSeekRemoteCompactTestSummary, false, nil)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(deepSeekRemoteCompactSSEWire(completedWithoutUsage))),
	}}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)

	require.Nil(t, result)
	require.Error(t, err)
	var failoverErr *UpstreamFailoverError
	require.True(t, errors.As(err, &failoverErr))
	require.Empty(t, recorder.Body.Bytes())
}

func TestForwardDeepSeekRemoteCompactionUsageBeforeTruncationDoesNotFailover(t *testing.T) {
	body := deepSeekRemoteCompactDetailedTestRequestBody(t)
	c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
	usageBeforeTerminal := `{"type":"response.in_progress","response":{"id":"resp_usage_only","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(deepSeekRemoteCompactSSEWire(usageBeforeTerminal))),
	}}
	svc := newDeepSeekRemoteCompactTestService(upstream)

	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)

	require.NotNil(t, result)
	require.Error(t, err)
	require.True(t, result.HasBillableTokenUsage())
	require.Equal(t, UsageRequestKindCompact, result.RequestKind)
	var failoverErr *UpstreamFailoverError
	require.False(t, errors.As(err, &failoverErr))
	require.Empty(t, recorder.Body.Bytes())
}

type scriptedDeepSeekCompactHTTPUpstream struct {
	HTTPUpstream
	responses []*http.Response
	calls     int
}

func (u *scriptedDeepSeekCompactHTTPUpstream) Do(*http.Request, string, int64, int) (*http.Response, error) {
	if u.calls >= len(u.responses) {
		return nil, errors.New("unexpected DeepSeek compact upstream call")
	}
	resp := u.responses[u.calls]
	u.calls++
	return resp, nil
}

func TestForwardDeepSeekRemoteCompactionHTTPFailureAllowsFailoverBeforeSemanticOutput(t *testing.T) {
	for _, statusCode := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(statusCode), func(t *testing.T) {
			body := deepSeekRemoteCompactDetailedTestRequestBody(t)
			c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
			upstream := &scriptedDeepSeekCompactHTTPUpstream{responses: []*http.Response{
				deepSeekCompactHTTPErrorResponse(statusCode, "first-attempt"),
			}}
			svc := newDeepSeekRemoteCompactScriptedTestService(upstream)

			result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)

			require.Nil(t, result, "an HTTP failure before a semantic event must not expose billable usage")
			var failoverErr *UpstreamFailoverError
			require.ErrorAs(t, err, &failoverErr)
			require.Equal(t, statusCode, failoverErr.StatusCode)
			require.True(t, failoverErr.ShouldRetryNextAccount())
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.Bytes())
			require.Equal(t, 1, upstream.calls)
		})
	}
}

func TestForwardDeepSeekRemoteCompactionFailoverUsageCardinalityAndExhaustion(t *testing.T) {
	for _, firstStatus := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(firstStatus)+" then success", func(t *testing.T) {
			body := deepSeekRemoteCompactDetailedTestRequestBody(t)
			c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
			upstream := &scriptedDeepSeekCompactHTTPUpstream{responses: []*http.Response{
				deepSeekCompactHTTPErrorResponse(firstStatus, "failed-account"),
				deepSeekRemoteCompactResponsesResponse(deepSeekRemoteCompactTestSummary),
			}}
			svc := newDeepSeekRemoteCompactScriptedTestService(upstream)
			billableResults := 0

			for attempt := 0; attempt < 2; attempt++ {
				account := deepSeekForwardTestAccount()
				account.ID += int64(attempt)
				result, err := svc.Forward(deepSeekCompactTestContext(42), c, account, body)
				if attempt == 0 {
					require.Nil(t, result)
					var failoverErr *UpstreamFailoverError
					require.ErrorAs(t, err, &failoverErr)
					require.Equal(t, firstStatus, failoverErr.StatusCode)
					continue
				}
				require.NoError(t, err)
				require.NotNil(t, result)
				if result.HasBillableTokenUsage() {
					billableResults++
				}
			}

			require.Equal(t, 1, billableResults, "only the successful account attempt may be recorded")
			require.Equal(t, 2, upstream.calls)
			require.Len(t, parseCompactBridgeSSE(t, recorder.Body.String()), 2)
		})
	}

	t.Run("429 then 500 exhausts without usage", func(t *testing.T) {
		body := deepSeekRemoteCompactDetailedTestRequestBody(t)
		c, recorder := newDeepSeekRemoteCompactTestContext(t, body)
		upstream := &scriptedDeepSeekCompactHTTPUpstream{responses: []*http.Response{
			deepSeekCompactHTTPErrorResponse(http.StatusTooManyRequests, "first-account"),
			deepSeekCompactHTTPErrorResponse(http.StatusInternalServerError, "last-account"),
		}}
		svc := newDeepSeekRemoteCompactScriptedTestService(upstream)
		billableResults := 0
		var lastFailoverErr *UpstreamFailoverError

		for attempt := 0; attempt < 2; attempt++ {
			account := deepSeekForwardTestAccount()
			account.ID += int64(attempt)
			result, err := svc.Forward(deepSeekCompactTestContext(42), c, account, body)
			if result != nil && result.HasBillableTokenUsage() {
				billableResults++
			}
			require.Nil(t, result)
			require.ErrorAs(t, err, &lastFailoverErr)
		}

		require.NotNil(t, lastFailoverErr)
		require.Equal(t, http.StatusInternalServerError, lastFailoverErr.StatusCode)
		require.Equal(t, 0, billableResults)
		require.Equal(t, 2, upstream.calls)
		require.False(t, c.Writer.Written(), "the caller can render the final exhaustion error without prior semantic output")
		require.Empty(t, recorder.Body.Bytes())
	})
}

func TestReadDeepSeekCompactResponsesStreamRecomputesTotalFromLatestUsage(t *testing.T) {
	c, _ := newDeepSeekRemoteCompactTestContext(t, deepSeekRemoteCompactTestRequestBody())
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	earlyUsage := `{"type":"response.in_progress","response":{"id":"resp_usage_only","usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}`
	completedWithoutTotal := deepSeekRemoteCompactCompletedWithoutTotalPayload(deepSeekRemoteCompactTestSummary)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(deepSeekRemoteCompactSSEWire(earlyUsage, completedWithoutTotal))),
	}

	result, err := svc.readDeepSeekCompactResponsesStream(c, resp, deepSeekForwardTestAccount(), time.Now())

	require.NoError(t, err)
	require.Equal(t, 31, result.Usage.InputTokens)
	require.Equal(t, 7, result.Usage.OutputTokens)
	require.Equal(t, 38, result.TotalTokens)
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
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactResponsesResponse("unused")}
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
		[]byte(`{"model":"deepseek-v4-flash","Input":[{"type":"compaction","encrypted_content":"foreign"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"Type":"compaction","encrypted_content":"foreign"}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"compaction","Encrypted_Content":"foreign"}]}`),
	} {
		restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), body)
		require.ErrorIs(t, err, ErrDeepSeekResponsesNonCanonicalJSONKey)
		require.Nil(t, restored)
		require.False(t, changed)
	}
}

func TestRestoreDeepSeekCompactInputLeavesOrdinaryProviderFieldsOpaque(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	for _, body := range [][]byte{
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"benign","text":"provider-owned"}]}]}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"function_call_output","call_id":"call_1","output":[{"type":"input_text","text":"benign","text":"provider-owned"}]}]}`),
		[]byte(`{"model":"deepseek-v4-flash","include":[],"Include":{"future":true},"provider_extension":"first","provider_extension":"second","input":"provider-owned"}`),
		[]byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","Text":"provider-owned"}]}]}`),
	} {
		restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), body)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, body, restored)
	}
}

func TestRestoreDeepSeekCompactInputRejectsOrdinaryRootModelAndInputAmbiguity(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	tests := []struct {
		name    string
		body    []byte
		wantErr error
	}{
		{
			name:    "duplicate model",
			body:    []byte(`{"model":"deepseek-v4-flash","model":"gpt-5.6-sol","input":"hello"}`),
			wantErr: ErrDeepSeekResponsesDuplicateJSONKey,
		},
		{
			name:    "non-canonical model",
			body:    []byte(`{"model":"deepseek-v4-flash","Model":"gpt-5.6-sol","input":"hello"}`),
			wantErr: ErrDeepSeekResponsesNonCanonicalJSONKey,
		},
		{
			name:    "duplicate input",
			body:    []byte(`{"model":"deepseek-v4-flash","input":"audited","input":"forwarded"}`),
			wantErr: ErrDeepSeekResponsesDuplicateJSONKey,
		},
		{
			name:    "non-canonical input",
			body:    []byte(`{"model":"deepseek-v4-flash","Input":"hidden","input":"visible"}`),
			wantErr: ErrDeepSeekResponsesNonCanonicalJSONKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), tt.body)
			require.ErrorIs(t, err, tt.wantErr)
			require.Nil(t, restored)
			require.False(t, changed)
		})
	}
}

func TestPrepareDeepSeekRemoteCompactionRequestUsesGatewayMaxBodySize(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	body := deepSeekRemoteCompactTestRequestBody()
	svc.cfg.Gateway.MaxBodySize = int64(len(body))
	require.NoError(t, svc.PrepareDeepSeekRemoteCompactionRequest(nil, body))
	require.ErrorIs(t, svc.PrepareDeepSeekRemoteCompactionRequest(nil, append(body, ' ')), ErrDeepSeekCompactRequestTooLarge)
}

func TestPrepareDeepSeekRemoteCompactionRequestStrictlyScansTriggerHistory(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	tests := []struct {
		name           string
		body           []byte
		wantErr        error
		restoreRejects bool
	}{
		{
			name:    "duplicate content text",
			body:    []byte(`{"model":"deepseek-v4-flash","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"first","text":"second"}]},{"type":"compaction_trigger"}]}`),
			wantErr: ErrDeepSeekResponsesDuplicateJSONKey,
		},
		{
			name:           "non-canonical root Input",
			body:           []byte(`{"model":"deepseek-v4-flash","Input":[{"type":"message","role":"user","content":"history"},{"type":"compaction_trigger"}]}`),
			wantErr:        ErrDeepSeekResponsesNonCanonicalJSONKey,
			restoreRejects: true,
		},
		{
			name:    "non-canonical history Type",
			body:    []byte(`{"model":"deepseek-v4-flash","input":[{"Type":"message","role":"user","content":"history"},{"type":"compaction_trigger"}]}`),
			wantErr: ErrDeepSeekResponsesNonCanonicalJSONKey,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restored, changed, err := svc.RestoreDeepSeekCompactInput(deepSeekCompactTestContext(42), tt.body)
			if tt.restoreRejects {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, restored)
			} else {
				require.NoError(t, err, "compaction_trigger is not persisted checkpoint state")
				require.Equal(t, tt.body, restored)
			}
			require.False(t, changed)

			c, _ := newDeepSeekRemoteCompactTestContext(t, tt.body)
			err = svc.PrepareDeepSeekRemoteCompactionRequest(c, tt.body)
			require.ErrorIs(t, err, tt.wantErr)
		})
	}
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

func TestForwardDeepSeekResponsesOrdinaryWireRemainsByteForByteUnchanged(t *testing.T) {
	body := []byte("{\n  \"model\": \"deepseek-v4-pro\",\n  \"model_hint\": \"first\",\n  \"model_hint\": \"second\",\n  \"include\": [],\n  \"Include\": {\"future\": true},\n  \"stream\": false,\n  \"input\": [{\"Type\":\"provider-native-message\",\"type\":\"message\",\"role\":\"user\",\"content\":[{\"type\":\"input_text\",\"text\":\"ordinary request\"}]}]\n}")
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

func TestRestoreDeepSeekCompactInputForOpenAITargetRestoresEscapedOwnedEnvelope(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	ctx := deepSeekCompactTestContext(42)
	checkpoint := frameDeepSeekCompactSummary("resume escaped checkpoint")
	envelope, err := svc.sealDeepSeekCompactCheckpoint(ctx, checkpoint)
	require.NoError(t, err)
	escapedEnvelope := strings.ReplaceAll(envelope, ".", `\u002e`)
	body := []byte(`{"model":"gpt-5.6-sol","input":[{"type":"compaction","encrypted_content":"` + escapedEnvelope + `"},{"type":"message","role":"user","content":"continue"}]}`)
	require.NotContains(t, string(body), deepSeekCompactEnvelopePrefix)

	restored, changed, err := svc.RestoreDeepSeekCompactInputForTarget(ctx, body, PlatformOpenAI)
	require.NoError(t, err)
	require.True(t, changed)
	items := gjson.GetBytes(restored, "input").Array()
	require.Len(t, items, 2)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "user", items[0].Get("role").String())
	require.Equal(t, checkpoint, items[0].Get("content.0.text").String())
}

func TestForwardDeepSeekResponsesRejectsForeignCompactItemBeforeUpstream(t *testing.T) {
	body := []byte(`{"model":"deepseek-v4-pro","stream":false,"input":[{"type":"compaction","encrypted_content":"foreign-provider-state"},{"type":"message","role":"user","content":"continue"}]}`)
	c, recorder := newDeepSeekResponsesTestContext(t, body)
	upstream := &httpUpstreamRecorder{resp: deepSeekRemoteCompactResponsesResponse("unused")}
	svc := newDeepSeekRemoteCompactTestService(upstream)
	result, err := svc.Forward(context.Background(), c, deepSeekForwardTestAccount(), body)
	require.ErrorIs(t, err, ErrDeepSeekCompactInvalidEncryptedContent)
	require.Nil(t, result)
	require.Nil(t, upstream.lastReq)
	require.Empty(t, recorder.Body.String())
}

func TestRestoreDeepSeekCompactInputForOpenAITargetRestoresOwnedAndPreservesForeignState(t *testing.T) {
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{})
	ctx := deepSeekCompactTestContext(42)
	checkpoint := frameDeepSeekCompactSummary("resume this task")
	envelope, err := svc.sealDeepSeekCompactCheckpoint(ctx, checkpoint)
	require.NoError(t, err)
	body := mustMarshalDeepSeekCompactTestJSON(t, map[string]any{
		"model": "gpt-5.6-sol",
		"input": []any{
			map[string]any{"type": "compaction", "encrypted_content": "openai-native-opaque"},
			map[string]any{"type": "compaction", "encrypted_content": envelope},
			map[string]any{"type": "message", "role": "user", "content": "continue"},
		},
	})

	restored, changed, err := svc.RestoreDeepSeekCompactInputForTarget(ctx, body, PlatformOpenAI)
	require.NoError(t, err)
	require.True(t, changed)
	items := gjson.GetBytes(restored, "input").Array()
	require.Len(t, items, 3)
	require.Equal(t, "compaction", items[0].Get("type").String())
	require.Equal(t, "openai-native-opaque", items[0].Get("encrypted_content").String())
	require.Equal(t, "message", items[1].Get("type").String())
	require.Equal(t, "user", items[1].Get("role").String())
	require.Equal(t, checkpoint, items[1].Get("content.0.text").String())
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
	svc := newDeepSeekRemoteCompactTestService(&httpUpstreamRecorder{resp: deepSeekRemoteCompactResponsesResponse(deepSeekRemoteCompactTestSummary)})
	result, err := svc.Forward(deepSeekCompactTestContext(42), c, deepSeekForwardTestAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	events := parseCompactBridgeSSE(t, recorder.Body.String())
	require.Len(t, events, 2)
	var item map[string]any
	require.NoError(t, json.Unmarshal([]byte(gjson.Get(events[0][1], "item").Raw), &item))
	return item
}

func deepSeekRemoteCompactResponsesResponse(summary string) *http.Response {
	completed := deepSeekRemoteCompactCompletedPayload(summary, true, nil)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":          []string{"text/event-stream"},
			"X-Deepseek-Request-Id": []string{"ds-compact-test"},
		},
		Body: io.NopCloser(strings.NewReader(deepSeekRemoteCompactSSEWire(
			`{"type":"response.output_text.delta","delta":"visible"}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_intermediate","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"this intermediate item is not the checkpoint"}]}}`,
			completed,
		))),
	}
}

func deepSeekCompactHTTPErrorResponse(statusCode int, requestID string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type":          []string{"application/json"},
			"X-Deepseek-Request-Id": []string{requestID},
		},
		Body: io.NopCloser(strings.NewReader(`{"error":{"message":"temporary compact failure"}}`)),
	}
}

func deepSeekRemoteCompactCompletedPayload(summary string, withUsage bool, outputOverride []any) string {
	output := outputOverride
	if output == nil {
		output = []any{
			map[string]any{"id": "rs_1", "type": "reasoning", "status": "completed", "summary": []any{map[string]any{"type": "summary_text", "text": "private chain of thought"}}},
			map[string]any{"id": "msg_final", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": summary}}},
		}
	}
	response := map[string]any{
		"id": "resp_ds_compact", "object": "response", "model": "deepseek-v4-flash", "status": "completed", "output": output,
	}
	if withUsage {
		response["usage"] = map[string]any{
			"input_tokens": 31, "output_tokens": 7, "total_tokens": 38,
			"input_tokens_details":  map[string]any{"cached_tokens": 5},
			"output_tokens_details": map[string]any{"reasoning_tokens": 3},
		}
	}
	payload, _ := json.Marshal(map[string]any{"type": "response.completed", "response": response})
	return string(payload)
}

func deepSeekRemoteCompactCompletedWithErrorPayload(summary string) string {
	var payload map[string]any
	_ = json.Unmarshal([]byte(deepSeekRemoteCompactCompletedPayload(summary, true, nil)), &payload)
	response, _ := payload["response"].(map[string]any)
	response["error"] = map[string]any{"code": "upstream_error", "message": "failed despite completed status"}
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func deepSeekRemoteCompactCompletedWithoutTotalPayload(summary string) string {
	var payload map[string]any
	_ = json.Unmarshal([]byte(deepSeekRemoteCompactCompletedPayload(summary, true, nil)), &payload)
	response, _ := payload["response"].(map[string]any)
	usage, _ := response["usage"].(map[string]any)
	delete(usage, "total_tokens")
	encoded, _ := json.Marshal(payload)
	return string(encoded)
}

func deepSeekRemoteCompactTerminalPayload(eventType, status string, withUsage bool) string {
	response := map[string]any{"id": "resp_ds_terminal", "status": status, "output": []any{}}
	if withUsage {
		response["usage"] = map[string]any{"input_tokens": 3, "output_tokens": 1, "total_tokens": 4}
	}
	payload, _ := json.Marshal(map[string]any{"type": eventType, "response": response})
	return string(payload)
}

func deepSeekRemoteCompactSSEWire(payloads ...string) string {
	var wire strings.Builder
	for _, payload := range payloads {
		if payload != "[DONE]" {
			if eventType := strings.TrimSpace(gjson.Get(payload, "type").String()); eventType != "" {
				_, _ = wire.WriteString("event: " + eventType + "\n")
			}
		}
		_, _ = wire.WriteString("data: " + payload + "\n\n")
	}
	return wire.String()
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
		"reasoning":    map[string]any{"effort": "max", "summary": "detailed"},
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

func newDeepSeekRemoteCompactTestService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	cfg := deepSeekForwardTestConfig()
	cfg.JWT = config.JWTConfig{Secret: "deepseek-compact-test-jwt-secret-32-bytes"}
	return &OpenAIGatewayService{cfg: cfg, httpUpstream: upstream}
}

func newDeepSeekRemoteCompactScriptedTestService(upstream HTTPUpstream) *OpenAIGatewayService {
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
