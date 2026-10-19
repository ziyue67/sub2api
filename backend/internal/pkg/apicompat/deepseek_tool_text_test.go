package apicompat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func dsTextTools() []ChatTool {
	return []ChatTool{{Type: "function", Function: &ChatFunction{Name: "functions__exec"}}, {Type: "function", Function: &ChatFunction{Name: "functions__wait"}}}
}

const dsTextCall = `<｜DSML｜tool_calls>
<｜DSML｜invoke name="functions__exec">
<｜DSML｜parameter name="input" string="true">if (1 < 2 && true) text("原样 &amp;");</｜DSML｜parameter>
</｜DSML｜invoke>
<｜DSML｜invoke name="functions__wait">
<｜DSML｜parameter name="cell_id" string="true">21631</｜DSML｜parameter>
<｜DSML｜parameter name="yield_time_ms" string="false">1000</｜DSML｜parameter>
</｜DSML｜invoke>
</｜DSML｜tool_calls>`

func TestDeepSeekToolTextStreamEveryByteBoundary(t *testing.T) {
	// Splitting in the middle of UTF-8 DSML delimiters must not leak protocol text
	// or change the exact command string. The normal namespace/custom lifecycle
	// still routes calls to the original Codex tools.
	for split := 0; split <= len(dsTextCall); split++ {
		d := NewDeepSeekToolTextDecoder(dsTextTools())
		state := NewChatCompletionsToResponsesStreamState("gpt-6.1-sol")
		state.NamespaceTools = map[string]NamespacedToolName{
			"functions__exec": {Namespace: "functions", Name: "exec", Custom: true},
			"functions__wait": {Namespace: "functions", Name: "wait"},
		}
		for _, text := range []string{"Working.\n", dsTextCall[:split], dsTextCall[split:]} {
			chunk := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &text}}}}
			ChatCompletionsChunkToResponsesEvents(d.Push(chunk), state)
		}
		chunk, err := d.Finish("stop")
		require.NoError(t, err, "split=%d", split)
		ChatCompletionsChunkToResponsesEvents(chunk, state)
		require.NoError(t, state.ValidateToolCallArguments())
		events := FinalizeChatCompletionsResponsesStream(state)
		final := events[len(events)-1].Response
		require.Equal(t, "completed", final.Status)
		require.Equal(t, "Working.\n", final.Output[0].Content[0].Text)
		require.Equal(t, "custom_tool_call", final.Output[1].Type)
		require.Equal(t, "exec", final.Output[1].Name)
		require.Equal(t, "functions", final.Output[1].Namespace)
		require.Equal(t, `if (1 < 2 && true) text("原样 &amp;");`, final.Output[1].Input)
		require.Equal(t, "wait", final.Output[2].Name)
		require.JSONEq(t, `{"cell_id":"21631","yield_time_ms":1000}`, final.Output[2].Arguments)
	}
}

func TestDeepSeekToolTextRejectsIncompleteAndAmbiguousCalls(t *testing.T) {
	cases := []string{
		`<parameter name="input">{"cell_id":"21631"}</parameter>`,
		`</｜｜DSML｜｜ calls>`,
		strings.TrimSuffix(dsTextCall, `</｜DSML｜tool_calls>`),
		strings.Replace(dsTextCall, "functions__exec", "undeclared_tool", 1),
		strings.Replace(dsTextCall, `string="true"`, `string="unknown"`, 1),
		strings.Replace(dsTextCall, `>1000<`, `>broken-json<`, 1),
		strings.Replace(dsTextCall, `name="yield_time_ms"`, `name="cell_id"`, 1),
		dsTextCall + " extra prose",
	}
	for _, text := range cases {
		d := NewDeepSeekToolTextDecoder(dsTextTools())
		message := ChatMessage{Content: json.RawMessage(`""`)}
		message.Content, _ = json.Marshal(text)
		require.Error(t, d.RecoverMessage(&message, "stop"), "%s", text)
		require.Empty(t, message.ToolCalls)
	}
	for _, kind := range []string{"length", "content_filter", "aborted", "", "native", "overflow"} {
		d := NewDeepSeekToolTextDecoder(dsTextTools())
		if kind == "native" {
			d.native = true
		}
		text := dsTextCall
		if kind == "overflow" {
			text += strings.Repeat("x", deepSeekToolTextLimit)
		}
		d.appendPending(text)
		_, err := d.Finish(kind)
		require.Error(t, err, kind)
	}
}

func TestDeepSeekToolTextPreservesExamplesAndNativeCalls(t *testing.T) {
	for _, text := range []string{
		"normal answer", "Use a < b & c.",
		"```xml\n" + dsTextCall + "\n```", "~~~xml\n" + dsTextCall + "\n~~~",
		"The literal tag <parameter name=\"input\"> is incomplete.",
		"````xml\n```\n" + dsTextCall + "\n````",
		"    " + strings.ReplaceAll(dsTextCall, "\n", "\n    "),
		"```xml\n~~~\n" + dsTextCall + "\n```",
	} {
		d := NewDeepSeekToolTextDecoder(dsTextTools())
		var emitted strings.Builder
		// A tag may arrive in any chunk, including split Markdown fences.
		for _, part := range strings.Split(text, "") {
			chunk := d.Push(&ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{Content: &part}}}})
			_, _ = emitted.WriteString(*chunk.Choices[0].Delta.Content)
		}
		chunk, err := d.Finish("stop")
		require.NoError(t, err)
		if chunk != nil {
			_, _ = emitted.WriteString(*chunk.Choices[0].Delta.Content)
			require.Empty(t, chunk.Choices[0].Delta.ToolCalls)
		}
		require.Equal(t, text, emitted.String())
	}
	d := NewDeepSeekToolTextDecoder(dsTextTools())
	native := &ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{ID: "call_native", Function: ChatFunctionCall{Name: "functions__wait", Arguments: `{}`}}}}}}}
	require.Equal(t, native, d.Push(native))
	chunk, err := d.Finish("tool_calls")
	require.NoError(t, err)
	require.Nil(t, chunk)
}

func TestDeepSeekToolTextFailureNeverCompletesATool(t *testing.T) {
	state := NewChatCompletionsToResponsesStreamState("gpt-6.1-sol")
	ChatCompletionsChunkToResponsesEvents(&ChatCompletionsChunk{Choices: []ChatChunkChoice{{Delta: ChatDelta{ToolCalls: []ChatToolCall{{ID: "call_native", Function: ChatFunctionCall{Name: "functions__wait", Arguments: `{}`}}}}}}}, state)
	state.UpstreamError = &ResponsesError{Code: "upstream_tool_protocol_error", Message: "invalid tool markup"}
	events := FinalizeChatCompletionsResponsesStream(state)
	require.Len(t, events, 1)
	require.Equal(t, "response.failed", events[0].Type)
	require.Empty(t, events[0].Response.Output)
	require.Equal(t, "upstream_tool_protocol_error", events[0].Response.Error.Code)
	require.Empty(t, FinalizeChatCompletionsResponsesStream(state))
}

func TestDeepSeekToolTextLegacyAndASCIIDelimiters(t *testing.T) {
	for _, marker := range []string{"", "|DSML|"} {
		d := NewDeepSeekToolTextDecoder(dsTextTools())
		text := strings.ReplaceAll(dsTextCall, "｜DSML｜", marker)
		message := ChatMessage{}
		message.Content, _ = json.Marshal(text)
		require.NoError(t, d.RecoverMessage(&message, "stop"))
		require.Len(t, message.ToolCalls, 2)
		require.Empty(t, chatMessageContentText(message.Content))
	}
}
