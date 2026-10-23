package service

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newSafetyPromptTestGateway(t *testing.T, enabled, signal bool, retentionDays int) (*OpenAIGatewayService, *SafetyPromptStore) {
	t.Helper()
	repo := &contentModerationTestSettingRepo{values: map[string]string{
		SettingKeySafetyPromptEnabled:       boolString(enabled),
		SettingKeySafetyPromptSignalEnabled: boolString(signal),
		SettingKeySafetyPromptRetentionDays: intString(retentionDays),
	}}
	settings := &SettingService{settingRepo: repo}
	store := &SafetyPromptStore{path: filepath.Join(t.TempDir(), "safety-prompt", "events.json")}
	return &OpenAIGatewayService{settingService: settings, safetyPromptStore: store}, store
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func intString(value int) string {
	return strconv.Itoa(value)
}

func TestInjectSafetyPromptResponsesAppendsPolicyAndMarker(t *testing.T) {
	body := []byte(`{"model":"gpt-5.1","instructions":"be concise","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`)
	next, err := injectSafetyPrompt(body, safetyPromptProtocolResponses, true, "sp-deadbeef")
	require.NoError(t, err)

	require.Contains(t, safetyPromptTestString(next, "instructions"), safetyPromptPolicy)
	require.Contains(t, safetyPromptTestString(next, "instructions"), safetyPromptConvention)
	require.True(t, strings.HasSuffix(safetyPromptTestString(next, "instructions"), "be concise"))

	var input []map[string]any
	require.NoError(t, json.Unmarshal(safetyPromptTestRaw(next, "input"), &input))
	require.Len(t, input, 2)
	require.Equal(t, "user", input[0]["role"])
	require.Equal(t, "developer", input[1]["role"])
	content, ok := input[1]["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, content)
	part, ok := content[0].(map[string]any)
	require.True(t, ok)
	markerText, ok := part["text"].(string)
	require.True(t, ok)
	require.Contains(t, markerText, "sp-deadbeef")
	require.True(t, strings.HasPrefix(markerText, safetyPromptMarkerPrefix))
	require.Equal(t, "gpt-5.1", safetyPromptTestString(next, "model"))
}

func TestInjectSafetyPromptResponsesConvertsStringInput(t *testing.T) {
	next, err := injectSafetyPrompt([]byte(`{"input":"hello"}`), safetyPromptProtocolResponses, true, "sp-1")
	require.NoError(t, err)
	var input []map[string]any
	require.NoError(t, json.Unmarshal(safetyPromptTestRaw(next, "input"), &input))
	require.Len(t, input, 2)
	require.Equal(t, "user", input[0]["role"])
	parts, ok := input[0]["content"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, parts)
	part, ok := parts[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "hello", part["text"])
	require.Equal(t, "developer", input[1]["role"])
}

func TestInjectSafetyPromptCompactSkipsMarker(t *testing.T) {
	next, err := injectSafetyPrompt([]byte(`{"input":[{"role":"user","content":"x"}]}`), safetyPromptProtocolResponsesCompact, true, "sp-1")
	require.NoError(t, err)
	require.Contains(t, safetyPromptTestString(next, "instructions"), safetyPromptPolicy)
	var input []map[string]any
	require.NoError(t, json.Unmarshal(safetyPromptTestRaw(next, "input"), &input))
	require.Len(t, input, 1)
	require.Equal(t, "user", input[0]["role"])
}

func TestInjectSafetyPromptChat(t *testing.T) {
	next, err := injectSafetyPrompt([]byte(`{"messages":[{"role":"user","content":"hi"}]}`), safetyPromptProtocolChat, true, "sp-chat")
	require.NoError(t, err)
	var messages []map[string]any
	require.NoError(t, json.Unmarshal(safetyPromptTestRaw(next, "messages"), &messages))
	require.Len(t, messages, 3)
	require.Equal(t, "system", messages[0]["role"])
	require.Contains(t, messages[0]["content"], safetyPromptPolicy)
	require.Equal(t, "user", messages[1]["role"])
	require.Equal(t, "developer", messages[2]["role"])
	require.Contains(t, messages[2]["content"], "sp-chat")
}

func TestInjectSafetyPromptChatWithoutMessagesStillAddsMarker(t *testing.T) {
	next, err := injectSafetyPrompt([]byte(`{"model":"gpt-5.1"}`), safetyPromptProtocolChat, true, "sp-empty")
	require.NoError(t, err)
	var messages []map[string]any
	require.NoError(t, json.Unmarshal(safetyPromptTestRaw(next, "messages"), &messages))
	require.Len(t, messages, 2)
	require.Equal(t, "system", messages[0]["role"])
	require.Equal(t, "developer", messages[1]["role"])
	require.Contains(t, messages[1]["content"], "sp-empty")
}

func TestInjectSafetyPromptAnthropicPolicyOnly(t *testing.T) {
	next, err := injectSafetyPrompt([]byte(`{"system":"legacy","messages":[]}`), safetyPromptProtocolAnthropic, true, "sp-anth")
	require.NoError(t, err)
	require.Contains(t, safetyPromptTestString(next, "system"), safetyPromptPolicy)
	require.True(t, strings.HasSuffix(safetyPromptTestString(next, "system"), "legacy"))
	require.NotContains(t, string(next), "sp-anth")
}

func TestRewriteSafetyPromptJSONResponses(t *testing.T) {
	raw := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"sp-abc"}]}]}`)
	next, matched := rewriteSafetyPromptJSON(raw, &safetyPromptRequestInfo{Marker: "sp-abc", Protocol: safetyPromptProtocolResponses})
	require.True(t, matched)
	require.Equal(t, safetyPromptRefusal+"sp-abc", safetyPromptTestString(next, "output.0.content.0.text"))

	extra := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"sp-abc extra"}]}]}`)
	_, matched = rewriteSafetyPromptJSON(extra, &safetyPromptRequestInfo{Marker: "sp-abc", Protocol: safetyPromptProtocolResponses})
	require.False(t, matched)

	withTool := []byte(`{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"sp-abc"}]},{"type":"function_call","name":"shell"}]}`)
	_, matched = rewriteSafetyPromptJSON(withTool, &safetyPromptRequestInfo{Marker: "sp-abc", Protocol: safetyPromptProtocolResponses})
	require.False(t, matched)
}

func TestRewriteSafetyPromptJSONChatAndAnthropic(t *testing.T) {
	chat := []byte(`{"choices":[{"message":{"role":"assistant","content":"sp-chat"}}]}`)
	next, matched := rewriteSafetyPromptJSON(chat, &safetyPromptRequestInfo{Marker: "sp-chat", Protocol: safetyPromptProtocolChat})
	require.True(t, matched)
	require.Equal(t, safetyPromptRefusal+"sp-chat", safetyPromptTestString(next, "choices.0.message.content"))

	anth := []byte(`{"content":[{"type":"text","text":"sp-anth"}]}`)
	next, matched = rewriteSafetyPromptJSON(anth, &safetyPromptRequestInfo{Marker: "sp-anth", Protocol: safetyPromptProtocolAnthropic})
	require.True(t, matched)
	require.Equal(t, safetyPromptRefusal+"sp-anth", safetyPromptTestString(next, "content.0.text"))
}

func TestRewriteSafetyPromptJSONRejectsToolOutputs(t *testing.T) {
	chat := []byte(`{"choices":[{"message":{"role":"assistant","content":"sp-chat","tool_calls":[{"id":"call-1"}]}}]}`)
	_, matched := rewriteSafetyPromptJSON(chat, &safetyPromptRequestInfo{Marker: "sp-chat", Protocol: safetyPromptProtocolChat})
	require.False(t, matched)

	anth := []byte(`{"content":[{"type":"text","text":"sp-anth"},{"type":"tool_use","id":"tool-1"}]}`)
	_, matched = rewriteSafetyPromptJSON(anth, &safetyPromptRequestInfo{Marker: "sp-anth", Protocol: safetyPromptProtocolAnthropic})
	require.False(t, matched)
}

func TestApplySafetyPromptInjectsAndRestoresRequest(t *testing.T) {
	gateway, _ := newSafetyPromptTestGateway(t, true, true, 30)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	original := `{"model":"gpt-5.1","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(original))
	req.Header.Set("Content-Type", "application/json")

	got, info := gateway.applySafetyPromptToRequest(req, account)
	require.NotNil(t, info)
	require.True(t, strings.HasPrefix(info.Marker, "sp-"))
	require.Equal(t, int64(7), info.AccountID)
	require.NotEmpty(t, info.RequestSHA256)
	require.Equal(t, "hello", info.Summary)

	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	require.Contains(t, string(body), safetyPromptPolicy)
	require.Contains(t, string(body), info.Marker)
	require.Equal(t, int64(len(body)), got.ContentLength)
	replay, err := got.GetBody()
	require.NoError(t, err)
	replayed, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, body, replayed)
}

func TestApplySafetyPromptRestoresBodyWhenInjectionFails(t *testing.T) {
	gateway, _ := newSafetyPromptTestGateway(t, true, true, 30)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	original := `{"model":"gpt-5.1","input":42}`
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(original))
	req.Header.Set("Content-Type", "application/json")

	got, info := gateway.applySafetyPromptToRequest(req, account)
	require.Nil(t, info)
	body, err := io.ReadAll(got.Body)
	require.NoError(t, err)
	require.Equal(t, original, string(body))
	require.Equal(t, int64(len(original)), got.ContentLength)
	replay, err := got.GetBody()
	require.NoError(t, err)
	replayed, err := io.ReadAll(replay)
	require.NoError(t, err)
	require.Equal(t, original, string(replayed))
}

func TestApplySafetyPromptLeavesUnsupportedPathsUntouched(t *testing.T) {
	gateway, _ := newSafetyPromptTestGateway(t, true, true, 30)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/embeddings", strings.NewReader(`{"input":"x"}`))
	got, info := gateway.applySafetyPromptToRequest(req, account)
	require.Nil(t, info)
	require.Same(t, req, got)
}

func TestApplySafetyPromptDisabledLeavesRequestUntouched(t *testing.T) {
	gateway, _ := newSafetyPromptTestGateway(t, false, true, 30)
	account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	req := httptest.NewRequest(http.MethodPost, "https://api.openai.com/v1/responses", strings.NewReader(`{"input":"x"}`))
	got, info := gateway.applySafetyPromptToRequest(req, account)
	require.Nil(t, info)
	require.Same(t, req, got)
}

func TestSafetyPromptJSONBodyLargeResponsePassesThroughUntruncated(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), safetyPromptMaxJSONResponse+2)
	body := newSafetyPromptJSONBody(io.NopCloser(bytes.NewReader(payload)), nil, &safetyPromptRequestInfo{Marker: "sp-never"})
	output, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Len(t, output, len(payload))
	require.Equal(t, payload, output)
}

func TestSafetyPromptSSEExactMarkerIsRewritten(t *testing.T) {
	_, store := newSafetyPromptTestGateway(t, true, true, 30)
	marker := "sp-abc123"
	input := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"sp-"}`) +
		sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"abc123"}`) +
		sseFrame("response.completed", `{"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"sp-abc123"}]}]}}`)
	body := newSafetyPromptSSEBody(io.NopCloser(strings.NewReader(input)), store, &safetyPromptRequestInfo{Marker: marker, RetentionDays: 30})
	output, err := io.ReadAll(body)
	require.NoError(t, err)

	text := string(output)
	require.Contains(t, text, safetyPromptRefusal+marker)
	require.NotContains(t, text, `"delta":"sp-abc123"`)
	require.Contains(t, text, `"delta":""`)
	require.Equal(t, safetyPromptRefusal+marker, safetyPromptTestString(gjsonFramePayload(t, text, "response.completed"), "response.output.0.content.0.text"))
	require.Len(t, store.List(time.Time{}, time.Time{}, "", 30), 1)
}

func TestSafetyPromptSSENormalTextPassesThrough(t *testing.T) {
	_, store := newSafetyPromptTestGateway(t, true, true, 30)
	input := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"hello"}`) +
		sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":" world"}`) +
		sseFrame("response.completed", `{"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello world"}]}]}}`)
	body := newSafetyPromptSSEBody(io.NopCloser(strings.NewReader(input)), store, &safetyPromptRequestInfo{Marker: "sp-never", RetentionDays: 30})
	output, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, input, string(output))
	require.Empty(t, store.List(time.Time{}, time.Time{}, "", 30))
}

func TestSafetyPromptSSEToolOutputIsNotRewritten(t *testing.T) {
	_, store := newSafetyPromptTestGateway(t, true, true, 30)
	input := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"sp-abc"}`) +
		sseFrame("response.completed", `{"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"sp-abc"}]},{"type":"function_call","name":"shell"}]}}`)
	body := newSafetyPromptSSEBody(io.NopCloser(strings.NewReader(input)), store, &safetyPromptRequestInfo{Marker: "sp-abc", RetentionDays: 30})
	output, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, input, string(output))
	require.Empty(t, store.List(time.Time{}, time.Time{}, "", 30))
}

func TestSafetyPromptSSEPrefixThenDivergenceFlushes(t *testing.T) {
	_, store := newSafetyPromptTestGateway(t, true, true, 30)
	input := sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"sp-"}`) +
		sseFrame("response.output_text.delta", `{"type":"response.output_text.delta","delta":"zzz"}`) +
		sseFrame("response.completed", `{"type":"response.completed","response":{"output":[]}}`)
	body := newSafetyPromptSSEBody(io.NopCloser(strings.NewReader(input)), store, &safetyPromptRequestInfo{Marker: "sp-abc", RetentionDays: 30})
	output, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, input, string(output))
	require.Empty(t, store.List(time.Time{}, time.Time{}, "", 30))
}

func TestSafetyPromptStoreRetentionAndPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "safety-prompt", "events.json")
	store := &SafetyPromptStore{path: path}
	store.Record(SafetyPromptEvent{Time: time.Now().UTC().Add(-48 * time.Hour), ID: "sp-old"}, 1)
	require.Empty(t, store.List(time.Time{}, time.Time{}, "", 1))
	store.Record(SafetyPromptEvent{Time: time.Now().UTC(), ID: "sp-new"}, 1)
	events := store.List(time.Time{}, time.Time{}, "", 1)
	require.Len(t, events, 1)
	require.Equal(t, "sp-new", events[0].ID)

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestSafetyPromptStoreCap(t *testing.T) {
	store := &SafetyPromptStore{path: filepath.Join(t.TempDir(), "events.json")}
	now := time.Now().UTC()
	for i := 0; i < safetyPromptMaxEvents; i++ {
		store.events = append(store.events, SafetyPromptEvent{Time: now, ID: "sp-" + intString(i)})
	}
	store.Record(SafetyPromptEvent{Time: now, ID: "sp-last"}, 30)
	require.Len(t, store.events, safetyPromptMaxEvents)
	require.Equal(t, "sp-last", store.events[len(store.events)-1].ID)
}

func TestSafetyPromptStoreLoadRejectsCorruptFileWithoutOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	require.NoError(t, os.WriteFile(path, []byte("{not-json"), 0o600))
	store := &SafetyPromptStore{path: path}
	store.load()
	require.NotEmpty(t, store.warning)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "{not-json", string(raw))
}

func TestSafetyPromptConfigDefaultsAndUpdate(t *testing.T) {
	repo := &contentModerationTestSettingRepo{values: map[string]string{}}
	settings := &SettingService{settingRepo: repo}
	cfg := settings.GetSafetyPromptConfig(t.Context())
	require.Equal(t, DefaultSafetyPromptConfig(), cfg)

	require.Error(t, settings.UpdateSafetyPromptConfig(t.Context(), SafetyPromptConfig{RetentionDays: 0}))
	require.NoError(t, settings.UpdateSafetyPromptConfig(t.Context(), SafetyPromptConfig{Enabled: true, SignalEnabled: true, RetentionDays: 7}))
	cfg = settings.GetSafetyPromptConfig(t.Context())
	require.True(t, cfg.Enabled)
	require.True(t, cfg.SignalEnabled)
	require.Equal(t, 7, cfg.RetentionDays)
	require.Equal(t, "true", repo.values[SettingKeySafetyPromptEnabled])
}

func sseFrame(event, data string) string {
	return "event: " + event + "\ndata: " + data + "\n\n"
}

func gjsonFramePayload(t *testing.T, text, eventType string) []byte {
	t.Helper()
	for _, frame := range strings.Split(text, "\n\n") {
		for _, line := range strings.Split(frame, "\n") {
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := []byte(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			if safetyPromptTestString(payload, "type") == eventType {
				return payload
			}
		}
	}
	t.Fatalf("frame %q not found", eventType)
	return nil
}

func safetyPromptTestRaw(raw []byte, path string) []byte {
	result := gjson.GetBytes(raw, path)
	if result.Raw != "" {
		return []byte(result.Raw)
	}
	return []byte(result.String())
}

func safetyPromptTestString(raw []byte, path string) string {
	return gjson.GetBytes(raw, path).String()
}
