package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPrismBrowserModelScope(t *testing.T) {
	for _, tc := range []struct {
		name       string
		scope      any
		configured bool
		model      string
		want       bool
	}{
		{"legacy Sol", nil, false, "gpt-6.1-sol", true},
		{"legacy audio stays native", nil, false, "gpt-4o-audio-preview", false},
		{"selected", []any{"gpt-6.1-sol"}, true, "gpt-6.1-sol", true},
		{"unselected", []any{"gpt-6.1-sol"}, true, "gpt-5.6-sol", false},
		{"empty", []string{}, true, "gpt-6.1-sol", false},
		{"null", nil, true, "gpt-6.1-sol", false},
		{"malformed", "gpt-6.1-sol", true, "gpt-6.1-sol", false},
		{"wildcard never widens", []string{"*"}, true, "gpt-6.1-sol", false},
		{"unsupported cannot be selected", []string{"gpt-4o-audio-preview"}, true, "gpt-4o-audio-preview", false},
		{"explicit alias", []string{"gpt-6.1-sol"}, true, "my-sol", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, a := prismTestService("")
			a.Credentials["model_mapping"] = map[string]any{"my-sol": "gpt-6.1-sol"}
			if tc.configured {
				a.Extra[PrismBrowserModelsKey] = tc.scope
			}
			require.Equal(t, tc.want, a.IsPrismBrowserEnabledForModel(tc.model))
			a.Extra["openai_prism_browser"] = false
			require.False(t, a.IsPrismBrowserEnabledForModel(tc.model))
		})
	}
	_, a := prismTestService("")
	for _, model := range PrismBrowserSupportedModels() {
		require.True(t, a.IsPrismBrowserEnabledForModel(model))
	}
}

func TestPrismScopePreservesNativeWebSocketModels(t *testing.T) {
	s, a := prismTestService("")
	s.cfg = newSchedulerTestOpenAIWSV2Config()
	a.Extra["openai_oauth_responses_websockets_v2_enabled"] = true
	a.Extra[PrismBrowserModelsKey] = []string{"gpt-6.1-sol"}
	for _, model := range []string{"gpt-4o-audio-preview", "gpt-5.6-sol"} {
		require.True(t, s.isOpenAIAccountTransportCompatible(a, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, model))
	}
	require.False(t, s.isOpenAIAccountTransportCompatible(a, OpenAIUpstreamTransportResponsesWebsocketV2Ingress, "gpt-6.1-sol"))
	before := openAITurnRouteFingerprint(a)
	a.Extra[PrismBrowserModelsKey] = []string{}
	require.NotEqual(t, before, openAITurnRouteFingerprint(a))
}
