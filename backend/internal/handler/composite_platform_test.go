package handler

import (
	"context"
	"net/http/httptest"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type responsesWSCompositeRouteRepoStub struct {
	service.CompositeModelRouteRepository
	routes []service.CompositeModelRoute
}

func (s *responsesWSCompositeRouteRepoStub) ListByGroup(context.Context, int64, bool) ([]service.CompositeModelRoute, error) {
	return append([]service.CompositeModelRoute(nil), s.routes...), nil
}

func TestResolveResponsesWebSocketTargetUsesExplicitCompositeRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/backend-api/codex/responses", nil)
	apiKey := &service.APIKey{Group: &service.Group{ID: 42, Platform: service.PlatformComposite}}
	resolver := service.NewCompositeRouteResolver(&responsesWSCompositeRouteRepoStub{routes: []service.CompositeModelRoute{{
		ID:             1,
		GroupID:        42,
		PublicModel:    "company-coding-model",
		MatchType:      service.CompositeRouteMatchExact,
		TargetPlatform: service.PlatformDeepSeek,
		UpstreamModel:  "deepseek-v4-pro",
		Endpoint:       service.CompositeRouteEndpointResponses,
		Enabled:        true,
	}}})
	AttachResponsesWebSocketCompositeResolver(c, resolver)

	decision, platform, err := resolveResponsesWebSocketTarget(c, apiKey, c.Request.Context(), "company-coding-model")

	require.NoError(t, err)
	require.Equal(t, service.PlatformDeepSeek, platform)
	require.Equal(t, "deepseek-v4-pro", decision.UpstreamModel)
	resolved, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, service.PlatformDeepSeek, resolved)
}

func TestCompositeTargetPlatformAllowedResolvesKnownAllowedModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/embeddings", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

	require.True(t, compositeTargetPlatformAllowed(c, apiKey, "text-embedding-3-large", service.PlatformOpenAI))
	platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, service.PlatformOpenAI, platform)
}

func TestEnsureCompositeTargetPlatformBindsInboundEndpoint(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

	for _, tc := range []struct {
		path     string
		model    string
		endpoint string
	}{
		{path: "/v1/chat/completions", model: "gpt-deepseek-alias", endpoint: service.CompositeRouteEndpointChatCompletions},
		{path: "/v1/responses", model: "grok-deepseek-alias", endpoint: service.CompositeRouteEndpointResponses},
		{path: "/v1/messages", model: "claude-deepseek-alias", endpoint: service.CompositeRouteEndpointMessages},
	} {
		t.Run(tc.endpoint, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", tc.path, nil)

			ensureCompositeTargetPlatform(c, apiKey, tc.model)

			endpoint, ok := service.CompositeRouteEndpointFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, tc.endpoint, endpoint)
			_, resolved := service.ResolvedTargetPlatformFromContext(c.Request.Context())
			require.True(t, resolved, "the handler detector should seed a fallback platform")
		})
	}
}

func TestOpenAICompatibleTextTargetAllowsCompositeGrokModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, path := range []string{"/v1/messages", "/v1/chat/completions"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", path, nil)
		apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

		require.True(t, openAICompatibleTextTargetAllowed(c, apiKey, "grok-4.3"), "path=%s", path)
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, ok, "path=%s", path)
		require.Equal(t, service.PlatformGrok, platform, "path=%s", path)
	}
}

func TestAllowOpenAICompatibleMessagesDispatchUsesCompositeTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	apiKey := &service.APIKey{Group: &service.Group{
		Platform:              service.PlatformComposite,
		AllowMessagesDispatch: false,
	}}

	for _, tc := range []struct {
		name     string
		platform string
		resolved bool
		want     bool
	}{
		{name: "openai target", platform: service.PlatformOpenAI, resolved: true, want: true},
		{name: "grok target", platform: service.PlatformGrok, resolved: true, want: true},
		{name: "anthropic target", platform: service.PlatformAnthropic, resolved: true, want: false},
		{name: "deepseek target", platform: service.PlatformDeepSeek, resolved: true, want: false},
		{name: "unresolved target", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			if tc.resolved {
				c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), tc.platform))
			}

			require.Equal(t, tc.want, allowOpenAICompatibleMessagesDispatch(c, apiKey))
		})
	}
}

func TestOpenAICompatibleRequestPlatformPreservesDeepSeek(t *testing.T) {
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformDeepSeek}}
	require.Equal(t, service.PlatformDeepSeek, openAICompatibleRequestPlatform(context.Background(), apiKey))

	ctx := service.WithResolvedTargetPlatform(context.Background(), service.PlatformDeepSeek)
	compositeKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}
	require.Equal(t, service.PlatformDeepSeek, openAICompatibleRequestPlatform(ctx, compositeKey))
}

func TestCompositeTargetPlatformAllowedRejectsWrongOrUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name  string
		model string
	}{
		{name: "wrong provider", model: "claude-sonnet-4-5"},
		{name: "unknown provider", model: "llama-4-maverick"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/embeddings", nil)
			apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

			require.False(t, compositeTargetPlatformAllowed(c, apiKey, tc.model, service.PlatformOpenAI))
		})
	}
}

func TestCompositeTargetPlatformResolvedRejectsUnknownModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite}}

	require.False(t, compositeTargetPlatformResolved(c, apiKey, "llama-4-maverick"))
	_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.False(t, ok)
}

func TestCompositeTargetPlatformResolvedAllowsConcreteGroupWithoutResolution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	apiKey := &service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}}

	require.True(t, compositeTargetPlatformResolved(c, apiKey, "llama-4-maverick"))
}

func TestOpenAIReasoningEffortPolicyForCompositeTarget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	group := &service.Group{
		Platform:           service.PlatformComposite,
		MaxReasoningEffort: "medium",
		ReasoningEffortMappings: []service.ReasoningEffortMapping{
			{From: "max", To: "xhigh"},
		},
	}
	apiKey := &service.APIKey{Group: group}
	body := []byte(`{"reasoning":{"effort":"max"}}`)

	openAICtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	openAICtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	openAICtx.Request = openAICtx.Request.WithContext(service.WithResolvedTargetPlatform(openAICtx.Request.Context(), service.PlatformOpenAI))
	got, changed := applyOpenAIReasoningEffortPolicyForRequest(openAICtx, apiKey, body)
	require.True(t, changed)
	require.JSONEq(t, `{"reasoning":{"effort":"medium"}}`, string(got))

	bindOpenAIReasoningEffortPolicyForMessagesRequest(openAICtx, apiKey, []byte(`{"output_config":{"effort":"max"}}`))
	bound, changed := service.ApplyOpenAIReasoningEffortPolicyFromContext(openAICtx.Request.Context(), body)
	require.True(t, changed)
	require.JSONEq(t, `{"reasoning":{"effort":"medium"}}`, string(bound))

	omittedCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	omittedCtx.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	omittedCtx.Request = omittedCtx.Request.WithContext(service.WithResolvedTargetPlatform(omittedCtx.Request.Context(), service.PlatformOpenAI))
	bindOpenAIReasoningEffortPolicyForMessagesRequest(omittedCtx, apiKey, []byte(`{"model":"gpt-5"}`))
	omitted, changed := service.ApplyOpenAIReasoningEffortPolicyFromContext(omittedCtx.Request.Context(), body)
	require.False(t, changed)
	require.Equal(t, body, omitted)

	grokCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	grokCtx.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	grokCtx.Request = grokCtx.Request.WithContext(service.WithResolvedTargetPlatform(grokCtx.Request.Context(), service.PlatformGrok))
	got, changed = applyOpenAIReasoningEffortPolicyForRequest(grokCtx, apiKey, body)
	require.False(t, changed)
	require.Equal(t, body, got)
}

func TestClientRequestedModelUsesCompositePublicModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
		Matched:        true,
		Source:         service.CompositeRouteSourceExplicit,
		PublicModel:    "public-alias",
		TargetPlatform: service.PlatformOpenAI,
		UpstreamModel:  "gpt-5",
	}))

	input := buildContentModerationInput(c, nil, middleware2.AuthSubject{UserID: 42}, service.ContentModerationProtocolOpenAIChat, "gpt-5", nil)
	require.Equal(t, "public-alias", input.Model)
	require.Equal(t, service.PlatformOpenAI, input.Provider)

	fields := clientRequestedUsageFields(c, service.ChannelMappingResult{MappedModel: "gpt-5"}, "gpt-5", "gpt-5")
	require.Equal(t, "public-alias", fields.OriginalModel)
	require.Equal(t, "public-alias", fields.ChannelMappedModel)
	require.Equal(t, "public-alias\u2192gpt-5", fields.ModelMappingChain)
}
