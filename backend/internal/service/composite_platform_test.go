package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestDetectModelPlatform(t *testing.T) {
	tests := []struct {
		name     string
		model    string
		platform string
		ok       bool
	}{
		{name: "claude", model: "claude-sonnet-4-5", platform: PlatformAnthropic, ok: true},
		{name: "anthropic prefix", model: "anthropic/claude-opus-4-5", platform: PlatformAnthropic, ok: true},
		{name: "gpt", model: "gpt-5.1", platform: PlatformOpenAI, ok: true},
		{name: "o series", model: "o3-mini", platform: PlatformOpenAI, ok: true},
		{name: "embedding", model: "text-embedding-3-large", platform: PlatformOpenAI, ok: true},
		{name: "gemini", model: "gemini-3-pro", platform: PlatformGemini, ok: true},
		{name: "gemini models prefix", model: "models/gemini-2.5-flash", platform: PlatformGemini, ok: true},
		{name: "learnlm", model: "learnlm-2.0-flash-experimental", platform: PlatformGemini, ok: true},
		{name: "grok", model: "grok-4", platform: PlatformGrok, ok: true},
		{name: "xai prefix", model: "xai/grok-4", platform: PlatformGrok, ok: true},
		{name: "deepseek", model: "deepseek-v4-pro", platform: PlatformDeepSeek, ok: true},
		{name: "deepseek prefix", model: "deepseek/deepseek-v4-flash", platform: PlatformDeepSeek, ok: true},
		{name: "unknown", model: "llama-4-maverick", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			platform, ok := DetectModelPlatform(tt.model)
			require.Equal(t, tt.ok, ok)
			require.Equal(t, tt.platform, platform)
		})
	}
}

func TestQuotaPlatformCompositeUsesResolvedOrForceOnly(t *testing.T) {
	apiKey := &APIKey{Group: &Group{Platform: PlatformComposite}}

	require.Equal(t, "", QuotaPlatform(context.Background(), apiKey))
	require.Equal(t, PlatformGemini, QuotaPlatform(WithResolvedTargetPlatform(context.Background(), PlatformGemini), apiKey))
	require.Equal(t, PlatformAntigravity, QuotaPlatform(context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAntigravity), apiKey))

	ctx := WithResolvedTargetPlatform(context.Background(), PlatformAnthropic)
	ctx = context.WithValue(ctx, ctxkey.ForcePlatform, PlatformAntigravity)
	require.Equal(t, PlatformAntigravity, QuotaPlatform(ctx, apiKey))
}

func TestCompositeGroupSchedulerHasAllCanonicalPlatformBuckets(t *testing.T) {
	seen := make(map[string]struct{})
	for _, bucket := range schedulerCanonicalBuckets(99) {
		seen[bucket.Platform] = struct{}{}
	}
	platforms := make([]string, 0, len(seen))
	for platform := range seen {
		platforms = append(platforms, platform)
	}
	require.ElementsMatch(t,
		[]string{PlatformAnthropic, PlatformGemini, PlatformOpenAI, PlatformAntigravity, PlatformGrok, PlatformDeepSeek},
		platforms,
	)
}

func TestResolveCompositeRouteDecisionExplicitRouteOverridesDetectorContext(t *testing.T) {
	group := &Group{ID: 7, Platform: PlatformComposite}
	routes := []CompositeModelRoute{
		{ID: 1, GroupID: group.ID, PublicModel: "gpt-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-flash", Endpoint: CompositeRouteEndpointChatCompletions, Priority: 100, Enabled: true},
		{ID: 2, GroupID: group.ID, PublicModel: "grok-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: CompositeRouteEndpointResponses, Priority: 100, Enabled: true},
		{ID: 3, GroupID: group.ID, PublicModel: "claude-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: CompositeRouteEndpointMessages, Priority: 100, Enabled: true},
	}
	svc := &GatewayService{compositeResolver: NewCompositeRouteResolver(compositeRouteRepoStub{routes: routes})}

	for _, route := range routes {
		t.Run(route.Endpoint, func(t *testing.T) {
			detectedPlatform, ok := DetectModelPlatform(route.PublicModel)
			require.True(t, ok)
			require.NotEqual(t, PlatformDeepSeek, detectedPlatform)
			ctx := WithResolvedTargetPlatform(context.Background(), detectedPlatform)
			ctx = WithCompositeRouteEndpoint(ctx, route.Endpoint)

			decision, matched, err := svc.resolveCompositeRouteDecision(ctx, group, route.PublicModel, CompositeRouteEndpointAny)

			require.NoError(t, err)
			require.True(t, matched)
			require.Equal(t, CompositeRouteSourceExplicit, decision.Source)
			require.Equal(t, PlatformDeepSeek, decision.TargetPlatform)
			require.Equal(t, route.UpstreamModel, decision.UpstreamModel)
			require.Equal(t, route.Endpoint, decision.Endpoint)
		})
	}
}
