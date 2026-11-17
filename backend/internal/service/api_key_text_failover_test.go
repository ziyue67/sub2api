package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAITextGroupFailoverBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		allowed    bool
	}{
		{"chat", `{"messages":[{"role":"user","content":"hello"}]}`, true},
		{"responses", `{"input":"hello"}`, true},
		{"tool_results", `{"input":[{"type":"function_call_output","call_id":"call_1","output":"ok"}]}`, true},
		{"previous_response", `{"previous_response_id":"resp_1","input":"hello"}`, false},
		{"conversation", `{"conversation":{"id":"conv_1"}}`, false},
		{"thread", `{"thread_id":"thread_1"}`, false},
		{"item_reference", `{"input":[{"type":"item_reference","id":"item_1"}]}`, false},
		{"encrypted_reasoning", `{"input":[{"type":"reasoning","encrypted_content":"opaque"}]}`, false},
		{"encrypted_agent", `{"input":[{"type":"message","content":[{"type":"text","encrypted_content":"opaque"}]}]}`, false},
		{"escaped_encrypted", `{"input":[{"type":"reasoning","encrypted\u005fcontent":"opaque"}]}`, false},
		{"compact", `{"input":[{"type":"compaction","id":"cmp_1"}]}`, false},
		{"malformed", `{`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := WithOpenAITextGroupFailover(context.Background(), []byte(tc.body))
			require.Equal(t, tc.allowed, compatibleAPIKeyGroupFailover(ctx, PlatformDeepseek, PlatformOpenAI))
			require.Equal(t, tc.allowed, compatibleAPIKeyGroupFailover(ctx, PlatformOpenAI, PlatformDeepseek))
			require.False(t, compatibleAPIKeyGroupFailover(ctx, PlatformAnthropic, PlatformOpenAI))
			require.False(t, compatibleAPIKeyGroupFailover(ctx, PlatformOpenAI, PlatformGemini))
			require.False(t, compatibleAPIKeyGroupFailover(ctx, PlatformComposite, PlatformDeepseek))
			require.False(t, compatibleAPIKeyGroupFailover(ctx, "unknown", PlatformOpenAI))
			require.True(t, compatibleAPIKeyGroupFailover(ctx, PlatformOpenAI, PlatformOpenAI), "existing same-platform retries remain unchanged")
		})
	}
	require.False(t, compatibleAPIKeyGroupFailover(context.Background(), PlatformDeepseek, PlatformOpenAI), "handlers must explicitly opt in")
	ctx := WithOpenAITextGroupFailover(context.Background(), []byte(`{"input":"hello"}`))
	ctx = WithOpenAITextGroupFailover(ctx, []byte(`{"previous_response_id":"resp_1"}`))
	require.False(t, compatibleAPIKeyGroupFailover(ctx, PlatformDeepseek, PlatformOpenAI), "a later opaque payload revokes the opt-in")
}
