package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const deepSeekSSEGuardTestAPIKey = "sk-deepseek-sse-canary-0123456789"

func deepSeekSSEGuardTestAccount() *Account {
	return &Account{
		ID:          9191,
		Name:        "deepseek-sse-sensitive-guard",
		Platform:    PlatformDeepSeek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  deepSeekSSEGuardTestAPIKey,
			"base_url": DefaultDeepSeekBaseURL,
		},
	}
}

func deepSeekSSEGuardJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	return string(encoded)
}

func deepSeekSSEGuardDeltaEvent(
	t *testing.T,
	protocol deepSeekSSESensitiveProtocol,
	value string,
	streamIndex int,
	metadata string,
) string {
	t.Helper()
	switch protocol {
	case deepSeekSSESensitiveProtocolChat:
		payload := map[string]any{
			"id": metadata,
			"choices": []any{map[string]any{
				"index": streamIndex,
				"delta": map[string]any{"content": value},
			}},
		}
		return "data: " + deepSeekSSEGuardJSON(t, payload) + "\n\n"
	case deepSeekSSESensitiveProtocolResponses:
		payload := map[string]any{
			"type":          "response.output_text.delta",
			"item_id":       fmt.Sprintf("item_%d_%s", streamIndex, metadata),
			"output_index":  streamIndex,
			"content_index": 0,
			"delta":         value,
		}
		return "event: response.output_text.delta\ndata: " + deepSeekSSEGuardJSON(t, payload) + "\n\n"
	case deepSeekSSESensitiveProtocolAnthropic:
		payload := map[string]any{
			"type":  "content_block_delta",
			"index": streamIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": value,
			},
			"trace": metadata,
		}
		return "event: content_block_delta\ndata: " + deepSeekSSEGuardJSON(t, payload) + "\n\n"
	default:
		t.Fatalf("unsupported test protocol %d", protocol)
		return ""
	}
}

func deepSeekSSEGuardResponsesEvent(
	t *testing.T,
	eventType string,
	itemID string,
	outputIndex int,
	contentIndex int,
	summaryIndex int,
	delta string,
) string {
	t.Helper()
	payload := map[string]any{
		"type":          eventType,
		"item_id":       itemID,
		"output_index":  outputIndex,
		"content_index": contentIndex,
		"summary_index": summaryIndex,
	}
	if strings.HasSuffix(eventType, ".delta") {
		payload["delta"] = delta
	}
	return "event: " + eventType + "\ndata: " + deepSeekSSEGuardJSON(t, payload) + "\n\n"
}

func deepSeekSSEGuardTerminal(t *testing.T, protocol deepSeekSSESensitiveProtocol) string {
	t.Helper()
	switch protocol {
	case deepSeekSSESensitiveProtocolChat:
		usage := map[string]any{
			"id":      "chat_guard_terminal",
			"choices": []any{},
			"usage": map[string]any{
				"prompt_tokens":     2,
				"completion_tokens": 1,
				"total_tokens":      3,
			},
		}
		return "data: " + deepSeekSSEGuardJSON(t, usage) + "\n\ndata: [DONE]\n\n"
	case deepSeekSSESensitiveProtocolResponses:
		terminal := map[string]any{
			"type": "response.completed",
			"response": map[string]any{
				"id":     "resp_guard_terminal",
				"status": "completed",
				"usage": map[string]any{
					"input_tokens":  2,
					"output_tokens": 1,
				},
			},
		}
		return "event: response.completed\ndata: " + deepSeekSSEGuardJSON(t, terminal) + "\n\n"
	case deepSeekSSESensitiveProtocolAnthropic:
		terminal := map[string]any{"type": "message_stop"}
		return "event: message_stop\ndata: " + deepSeekSSEGuardJSON(t, terminal) + "\n\n"
	default:
		t.Fatalf("unsupported test protocol %d", protocol)
		return ""
	}
}

func runDeepSeekSSEGuardProtocol(
	t *testing.T,
	protocol deepSeekSSESensitiveProtocol,
	wire string,
) (string, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	account := deepSeekSSEGuardTestAccount()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(wire)),
	}

	switch protocol {
	case deepSeekSSESensitiveProtocolChat:
		svc := &OpenAIGatewayService{cfg: &config.Config{}}
		_, err := svc.streamRawChatCompletions(
			c, resp, account, "deepseek-chat", "deepseek-chat", "deepseek-chat", nil, nil, time.Now(), 1,
		)
		return recorder.Body.String(), err
	case deepSeekSSESensitiveProtocolResponses:
		svc := &OpenAIGatewayService{cfg: &config.Config{}}
		_, err := svc.handleDeepSeekResponsesStream(context.Background(), resp, c, account, time.Now())
		return recorder.Body.String(), err
	case deepSeekSSESensitiveProtocolAnthropic:
		svc := &GatewayService{cfg: &config.Config{}}
		_, err := svc.handleStreamingResponseAnthropicAPIKeyPassthrough(
			context.Background(), resp, c, account, time.Now(), "deepseek-chat",
		)
		return recorder.Body.String(), err
	default:
		t.Fatalf("unsupported test protocol %d", protocol)
		return "", nil
	}
}

func TestDeepSeekHTTPSSESensitiveGuardRejectsSplitSecretsAcrossNativeProtocols(t *testing.T) {
	foreignDerivedID := deepSeekUserIDPrefix + strings.Repeat("A", deepSeekUserIDEncodedDigestBytes)
	protocols := []struct {
		name     string
		protocol deepSeekSSESensitiveProtocol
	}{
		{name: "chat", protocol: deepSeekSSESensitiveProtocolChat},
		{name: "responses", protocol: deepSeekSSESensitiveProtocolResponses},
		{name: "anthropic", protocol: deepSeekSSESensitiveProtocolAnthropic},
	}
	for _, tt := range protocols {
		t.Run(tt.name+"/api_key_two_pieces_with_metadata_change", func(t *testing.T) {
			split := len(deepSeekSSEGuardTestAPIKey) / 2
			wire := deepSeekSSEGuardDeltaEvent(t, tt.protocol, deepSeekSSEGuardTestAPIKey[:split], 0, "first") +
				deepSeekSSEGuardDeltaEvent(t, tt.protocol, deepSeekSSEGuardTestAPIKey[split:], 0, "changed") +
				deepSeekSSEGuardTerminal(t, tt.protocol)
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire)
			require.ErrorIs(t, err, errDeepSeekSSESensitiveData)
			require.NotContains(t, output, deepSeekSSEGuardTestAPIKey)
			require.NotContains(t, output, deepSeekSSEGuardTestAPIKey[:split])
		})

		t.Run(tt.name+"/derived_id_byte_by_byte", func(t *testing.T) {
			var wire strings.Builder
			for index := range foreignDerivedID {
				_, _ = wire.WriteString(deepSeekSSEGuardDeltaEvent(t, tt.protocol, foreignDerivedID[index:index+1], 0, fmt.Sprintf("m%d", index)))
			}
			_, _ = wire.WriteString(deepSeekSSEGuardTerminal(t, tt.protocol))
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire.String())
			require.ErrorIs(t, err, errDeepSeekSSESensitiveData)
			require.NotContains(t, output, foreignDerivedID)
			require.NotContains(t, output, deepSeekUserIDPrefix)
		})

		t.Run(tt.name+"/interleaved_stream", func(t *testing.T) {
			split := len(deepSeekSSEGuardTestAPIKey) / 2
			wire := deepSeekSSEGuardDeltaEvent(t, tt.protocol, deepSeekSSEGuardTestAPIKey[:split], 0, "stable") +
				deepSeekSSEGuardDeltaEvent(t, tt.protocol, "benign-other-stream", 1, "interleaved") +
				deepSeekSSEGuardDeltaEvent(t, tt.protocol, deepSeekSSEGuardTestAPIKey[split:], 0, "stable") +
				deepSeekSSEGuardTerminal(t, tt.protocol)
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire)
			require.ErrorIs(t, err, errDeepSeekSSESensitiveData)
			require.NotContains(t, output, deepSeekSSEGuardTestAPIKey)
			require.NotContains(t, output, deepSeekSSEGuardTestAPIKey[:split])
		})
	}
}

func TestDeepSeekHTTPResponsesSSESensitiveGuardKeepsStreamsDistinct(t *testing.T) {
	split := len(deepSeekSSEGuardTestAPIKey) / 2
	first := deepSeekSSEGuardTestAPIKey[:split]
	second := deepSeekSSEGuardTestAPIKey[split:]
	tests := []struct {
		name       string
		streamItem string
		middle     string
	}{
		{
			name:       "same item different content index",
			streamItem: "item_shared",
			middle: deepSeekSSEGuardResponsesEvent(
				t, "response.output_text.delta", "item_shared", 0, 1, 0, "benign interleaving",
			),
		},
		{
			name:       "same item and indexes different event family",
			streamItem: "item_shared",
			middle: deepSeekSSEGuardResponsesEvent(
				t, "response.reasoning_text.delta", "item_shared", 0, 0, 0, "benign interleaving",
			),
		},
		{
			name:       "done item id is not a prefix match",
			streamItem: "item_10",
			middle: deepSeekSSEGuardResponsesEvent(
				t, "response.output_text.delta", "item_1", 0, 0, 0, "benign interleaving",
			) + deepSeekSSEGuardResponsesEvent(
				t, "response.output_text.done", "item_1", 0, 0, 0, "",
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire := deepSeekSSEGuardResponsesEvent(
				t, "response.output_text.delta", tt.streamItem, 0, 0, 0, first,
			) + tt.middle + deepSeekSSEGuardResponsesEvent(
				t, "response.output_text.delta", tt.streamItem, 0, 0, 0, second,
			) + deepSeekSSEGuardTerminal(t, deepSeekSSESensitiveProtocolResponses)

			output, err := runDeepSeekSSEGuardProtocol(t, deepSeekSSESensitiveProtocolResponses, wire)
			require.ErrorIs(t, err, errDeepSeekSSESensitiveData)
			require.NotContains(t, output, deepSeekSSEGuardTestAPIKey)
			require.NotContains(t, output, first)
		})
	}
}

func TestDeepSeekResponsesSensitiveStreamKeyIncludesIdentityFamilyAndIndexes(t *testing.T) {
	base := map[string]any{
		"item_id":       "item_1",
		"output_index":  0,
		"content_index": 0,
		"summary_index": 0,
	}
	key := func(eventType string, overrides map[string]any) string {
		payload := make(map[string]any, len(base))
		for name, value := range base {
			payload[name] = value
		}
		for name, value := range overrides {
			payload[name] = value
		}
		return deepSeekResponsesSSESensitiveStreamKey(eventType, []byte(deepSeekSSEGuardJSON(t, payload)))
	}

	baseline := key("response.output_text.delta", nil)
	for _, tt := range []struct {
		name      string
		eventType string
		overrides map[string]any
	}{
		{name: "family", eventType: "response.reasoning_text.delta"},
		{name: "item id", eventType: "response.output_text.delta", overrides: map[string]any{"item_id": "item_2"}},
		{name: "call id fallback", eventType: "response.output_text.delta", overrides: map[string]any{"item_id": "", "call_id": "call_1"}},
		{name: "output index", eventType: "response.output_text.delta", overrides: map[string]any{"output_index": 1}},
		{name: "content index", eventType: "response.output_text.delta", overrides: map[string]any{"content_index": 1}},
		{name: "summary index", eventType: "response.output_text.delta", overrides: map[string]any{"summary_index": 1}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.NotEqual(t, baseline, key(tt.eventType, tt.overrides))
		})
	}
	require.Equal(
		t,
		baseline,
		key("response.output_text.done", nil),
		"matching delta and done events must address the same stream",
	)
}

func TestDeepSeekHTTPSSESensitiveGuardPreservesSafeStreamingSemantics(t *testing.T) {
	protocols := []struct {
		name     string
		protocol deepSeekSSESensitiveProtocol
	}{
		{name: "chat", protocol: deepSeekSSESensitiveProtocolChat},
		{name: "responses", protocol: deepSeekSSESensitiveProtocolResponses},
		{name: "anthropic", protocol: deepSeekSSESensitiveProtocolAnthropic},
	}
	for _, tt := range protocols {
		t.Run(tt.name+"/complete_secret_is_redacted", func(t *testing.T) {
			wire := deepSeekSSEGuardDeltaEvent(t, tt.protocol, "echo "+deepSeekSSEGuardTestAPIKey, 0, "single") +
				deepSeekSSEGuardTerminal(t, tt.protocol)
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire)
			require.NoError(t, err)
			require.NotContains(t, output, deepSeekSSEGuardTestAPIKey)
			require.Contains(t, output, deepSeekCredentialRedaction)
		})

		t.Run(tt.name+"/benign_prefix_rolls_back_in_order", func(t *testing.T) {
			prefix := deepSeekSSEGuardTestAPIKey[:8]
			wire := deepSeekSSEGuardDeltaEvent(t, tt.protocol, prefix, 0, "prefix") +
				deepSeekSSEGuardDeltaEvent(t, tt.protocol, "! definitely benign", 0, "rollback") +
				deepSeekSSEGuardTerminal(t, tt.protocol)
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire)
			require.NoError(t, err)
			prefixAt := strings.Index(output, prefix)
			rollbackAt := strings.Index(output, "! definitely benign")
			require.GreaterOrEqual(t, prefixAt, 0)
			require.Greater(t, rollbackAt, prefixAt)
		})

		t.Run(tt.name+"/terminal_flushes_incomplete_prefix", func(t *testing.T) {
			partial := deepSeekUserIDPrefix + "AAAA"
			wire := deepSeekSSEGuardDeltaEvent(t, tt.protocol, partial, 0, "partial") +
				deepSeekSSEGuardTerminal(t, tt.protocol)
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire)
			require.NoError(t, err)
			require.Contains(t, output, partial)
		})
	}
}

func TestDeepSeekHTTPSSESensitiveGuardCurrentEventLimitFailsClosedAcrossNativeProtocols(t *testing.T) {
	line := ": " + strings.Repeat("x", 4096) + "\n"
	wire := strings.Repeat(line, deepSeekSSESensitiveHoldMaxBytes/len(line)+2)
	protocols := []struct {
		name     string
		protocol deepSeekSSESensitiveProtocol
	}{
		{name: "chat", protocol: deepSeekSSESensitiveProtocolChat},
		{name: "responses", protocol: deepSeekSSESensitiveProtocolResponses},
		{name: "anthropic", protocol: deepSeekSSESensitiveProtocolAnthropic},
	}

	for _, tt := range protocols {
		t.Run(tt.name, func(t *testing.T) {
			output, err := runDeepSeekSSEGuardProtocol(t, tt.protocol, wire)
			require.ErrorIs(t, err, errDeepSeekSSESensitiveData)
			require.ErrorContains(t, err, "current event limit exceeded")
			require.Empty(t, output)
		})
	}
}

func TestDeepSeekSSESensitiveGuardPendingLimitFailsClosed(t *testing.T) {
	guard := newDeepSeekSSESensitiveEventGuard(deepSeekSSEGuardTestAccount(), deepSeekSSESensitiveProtocolResponses)
	emitted := 0
	emit := func([]byte) error {
		emitted++
		return nil
	}
	for index := 0; index < deepSeekSSESensitiveHoldMaxEvents; index++ {
		wire := deepSeekSSEGuardDeltaEvent(t, deepSeekSSESensitiveProtocolResponses, "s", index, fmt.Sprintf("hold%d", index))
		for _, line := range strings.SplitAfter(wire, "\n") {
			if line == "" {
				continue
			}
			require.NoError(t, guard.PushWireLine([]byte(line), emit))
		}
	}
	wire := deepSeekSSEGuardDeltaEvent(t, deepSeekSSESensitiveProtocolResponses, "s", deepSeekSSESensitiveHoldMaxEvents, "overflow")
	var guardErr error
	for _, line := range strings.SplitAfter(wire, "\n") {
		if line == "" {
			continue
		}
		guardErr = guard.PushWireLine([]byte(line), emit)
		if guardErr != nil {
			break
		}
	}
	require.ErrorIs(t, guardErr, errDeepSeekSSESensitiveData)
	require.Zero(t, emitted)
}

func TestDeepSeekSSESensitiveGuardPropagatesEmitterError(t *testing.T) {
	want := errors.New("writer closed")
	guard := newDeepSeekSSESensitiveEventGuard(deepSeekSSEGuardTestAccount(), deepSeekSSESensitiveProtocolChat)
	err := guard.PushWireLine([]byte("data: [DONE]\n"), func([]byte) error { return want })
	require.NoError(t, err)
	err = guard.PushWireLine([]byte("\n"), func([]byte) error { return want })
	require.ErrorIs(t, err, want)
}
