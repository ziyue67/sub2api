package service

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestUltraReasoningEffortGroupPolicy(t *testing.T) {
	for _, field := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		t.Run(field, func(t *testing.T) {
			bodies := map[string]string{
				"reasoning.effort":     `{"model":"gpt-test","reasoning":{"effort":"ultra"}}`,
				"reasoning_effort":     `{"model":"gpt-test","reasoning_effort":"ultra"}`,
				"output_config.effort": `{"model":"gpt-test","output_config":{"effort":"ultra"}}`,
			}
			body := []byte(bodies[field])
			capped, changed, err := ApplyOpenAIReasoningEffortPolicy(body, "max", nil, "downgrade")
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "max", gjson.GetBytes(capped, field).String())
			original, changed, err := ApplyOpenAIReasoningEffortPolicy(body, "max", nil, "deny")
			require.ErrorAs(t, err, new(*ReasoningEffortOverLimitError))
			require.False(t, changed)
			require.Equal(t, body, original)
			_, _, err = ApplyOpenAIReasoningEffortPolicy(body, "", []ReasoningEffortMapping{{From: "ultra", To: "deny"}}, "")
			require.ErrorAs(t, err, new(*ReasoningEffortMappingDeniedError))
		})
	}
}

func TestUltraReasoningEffortPlatformValidation(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformComposite} {
		value, err := normalizeMaxReasoningEffortForPlatform(platform, " ULTRA ")
		require.NoError(t, err)
		require.Equal(t, "ultra", value)
		_, err = NormalizeReasoningEffortMappings(platform, []ReasoningEffortMapping{{From: "ultra", To: "high"}})
		require.NoError(t, err)
	}
	_, err := normalizeMaxReasoningEffortForPlatform(PlatformAnthropic, "ultra")
	require.Error(t, err)
	_, err = NormalizeReasoningEffortMappings(PlatformAnthropic, []ReasoningEffortMapping{{From: "ultra", To: "high"}})
	require.Error(t, err)
}

func TestUltraReasoningEffortWSAndModelSuffix(t *testing.T) {
	for _, body := range []string{
		`{"model":"gpt-test-ultra"}`,
		`{"type":"response.create","model":"gpt-test","reasoning":{"effort":"ultra"}}`,
		`{"type":"response.create","response":{"model":"gpt-test","reasoning":{"effort":"ultra"}},"event_id":"fixture-event"}`,
	} {
		capped, changed, err := ApplyOpenAIReasoningEffortPolicy([]byte(body), "high", nil, "downgrade")
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "high", openAIRequestPayloadView(capped).Get("reasoning.effort").String())
		if gjson.Get(body, "event_id").Exists() {
			require.Equal(t, "fixture-event", gjson.GetBytes(capped, "event_id").String())
		}
		_, _, err = ApplyOpenAIReasoningEffortPolicy([]byte(body), "high", nil, "deny")
		require.True(t, IsReasoningEffortPolicyDenied(err))
		unchanged, changed, err := ApplyOpenAIReasoningEffortPolicy([]byte(body), "", nil, "")
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, []byte(body), unchanged)
	}
}
