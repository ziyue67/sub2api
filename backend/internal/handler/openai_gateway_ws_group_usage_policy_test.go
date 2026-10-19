package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAIResponsesWebSocketGroupSubagentPolicy(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeDedicated} {
		for _, first := range []bool{true, false} {
			t.Run(mode+map[bool]string{true: "/first", false: "/followup"}[first], func(t *testing.T) {
				group := wsAllowlistGroup(false)
				group.BlockSubagents = true
				marked := `{"type":"response.create","model":"gpt-5.4","client_metadata":{"x-openai-subagent":"worker"}}`
				tc := openAIResponsesWSUsageLogCase{group: group, ingressMode: mode,
					firstPayload: `{"type":"response.create","model":"gpt-5.4"}`,
					closeReason:  "Subagent requests are disabled for this group",
				}
				if first {
					tc.firstPayload, tc.firstFrameCloseExpected = marked, true
				} else {
					tc.secondPayload, tc.secondTurnCloseExpected = marked, true
				}
				runOpenAIResponsesWebSocketUsageLogCase(t, tc)
			})
		}
	}
}

func TestOpenAIResponsesWebSocketGroupUltraPolicy(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeDedicated} {
		t.Run(mode, func(t *testing.T) {
			group := wsAllowlistGroup(false)
			group.MaxReasoningEffort = "high"
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				group: group, ingressMode: mode,
				firstPayload:  `{"type":"response.create","model":"gpt-5.4","reasoning":{"effort":"ultra"}}`,
				secondPayload: `{"type":"response.create","model":"gpt-5.4","reasoning":{"effort":"ultra"}}`,
			})
			require.Len(t, got.upstreamPayloads, 2)
			for _, payload := range got.upstreamPayloads {
				require.Equal(t, "high", gjson.GetBytes(payload, "reasoning.effort").String())
			}
		})
	}
}
