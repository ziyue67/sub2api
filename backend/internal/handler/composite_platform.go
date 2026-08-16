package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

const responsesWebSocketCompositeResolverContextKey = "responses_websocket_composite_resolver"

var errResponsesWebSocketCompositeTargetUnresolved = errors.New("responses websocket composite target could not be resolved")

// AttachResponsesWebSocketCompositeResolver makes the route resolver available
// after the WebSocket upgrade. GET requests do not carry the model until the
// first response.create frame, so the ordinary body middleware cannot resolve
// an explicit composite route before entering the handler.
func AttachResponsesWebSocketCompositeResolver(c *gin.Context, resolver *service.CompositeRouteResolver) {
	if c != nil && resolver != nil {
		c.Set(responsesWebSocketCompositeResolverContextKey, resolver)
	}
}

func resolveResponsesWebSocketTarget(
	c *gin.Context,
	apiKey *service.APIKey,
	ctx context.Context,
	model string,
) (service.CompositeRouteDecision, string, error) {
	if apiKey == nil {
		return service.CompositeRouteDecision{}, "", errResponsesWebSocketCompositeTargetUnresolved
	}
	// Preserve the legacy OpenAI default for tests and internal callers whose
	// API key context predates hydrated group attachment.
	if apiKey.Group == nil {
		return service.CompositeRouteDecision{}, service.PlatformOpenAI, nil
	}
	if apiKey.Group.Platform != service.PlatformComposite {
		return service.CompositeRouteDecision{}, apiKey.Group.Platform, nil
	}
	if c == nil || c.Request == nil {
		return service.CompositeRouteDecision{}, "", errResponsesWebSocketCompositeTargetUnresolved
	}
	resolverValue, ok := c.Get(responsesWebSocketCompositeResolverContextKey)
	resolver, _ := resolverValue.(*service.CompositeRouteResolver)
	if !ok || resolver == nil {
		resolver = service.NewCompositeRouteResolver(nil)
	}
	decision, err := resolver.Resolve(ctx, apiKey.Group.ID, model, service.CompositeRouteEndpointResponses)
	if err != nil {
		return decision, "", err
	}
	if !decision.Matched || strings.TrimSpace(decision.TargetPlatform) == "" {
		return decision, "", errResponsesWebSocketCompositeTargetUnresolved
	}
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), decision))
	return decision, decision.TargetPlatform, nil
}

func ensureCompositeTargetPlatform(c *gin.Context, apiKey *service.APIKey, model string) {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return
	}
	if _, ok := service.CompositeRouteEndpointFromContext(c.Request.Context()); !ok {
		c.Request = c.Request.WithContext(service.WithCompositeRouteEndpoint(c.Request.Context(), compositeRouteEndpointForPath(c.Request.URL.Path)))
	}
	if _, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
		return
	}
	if platform, ok := service.DetectModelPlatform(model); ok {
		c.Request = c.Request.WithContext(service.WithResolvedTargetPlatform(c.Request.Context(), platform))
	}
}

func compositeRouteEndpointForPath(path string) string {
	path = strings.ToLower(strings.TrimSpace(path))
	switch {
	case strings.Contains(path, "/messages/count_tokens"):
		return service.CompositeRouteEndpointCountTokens
	case strings.Contains(path, "/messages"):
		return service.CompositeRouteEndpointMessages
	case strings.Contains(path, "/responses"):
		return service.CompositeRouteEndpointResponses
	case strings.Contains(path, "/chat/completions"):
		return service.CompositeRouteEndpointChatCompletions
	case strings.Contains(path, "/embeddings"):
		return service.CompositeRouteEndpointEmbeddings
	case strings.Contains(path, "/images/"):
		return service.CompositeRouteEndpointImages
	case strings.Contains(path, "/v1beta/"):
		return service.CompositeRouteEndpointGemini
	default:
		return service.CompositeRouteEndpointAny
	}
}

func compositeTargetPlatformAllowed(c *gin.Context, apiKey *service.APIKey, model string, allowed ...string) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	ensureCompositeTargetPlatform(c, apiKey, model)
	platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	if !ok {
		return false
	}
	for _, allowedPlatform := range allowed {
		if platform == allowedPlatform {
			return true
		}
	}
	return false
}

func compositeTargetPlatformResolved(c *gin.Context, apiKey *service.APIKey, model string) bool {
	if c == nil || c.Request == nil || apiKey == nil || apiKey.Group == nil || apiKey.Group.Platform != service.PlatformComposite {
		return true
	}
	ensureCompositeTargetPlatform(c, apiKey, model)
	_, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	return ok
}

func effectiveAPIKeyPlatform(c *gin.Context, apiKey *service.APIKey) string {
	if c != nil && c.Request != nil {
		if platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context()); ok {
			return platform
		}
	}
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return apiKey.Group.Platform
}

func openAIReasoningEffortPolicyForRequest(c *gin.Context, apiKey *service.APIKey) (string, []service.ReasoningEffortMapping, bool) {
	if apiKey == nil || apiKey.Group == nil {
		return "", nil, false
	}
	if apiKey.Group.Platform != service.PlatformOpenAI && apiKey.Group.Platform != service.PlatformComposite {
		return "", nil, false
	}
	if effectiveAPIKeyPlatform(c, apiKey) != service.PlatformOpenAI {
		return "", nil, false
	}
	return apiKey.Group.MaxReasoningEffort, apiKey.Group.ReasoningEffortMappings, true
}

func applyOpenAIReasoningEffortPolicyForRequest(c *gin.Context, apiKey *service.APIKey, body []byte) ([]byte, bool) {
	maxEffort, mappings, ok := openAIReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return body, false
	}
	return service.ApplyOpenAIReasoningEffortPolicy(body, maxEffort, mappings)
}

func bindOpenAIReasoningEffortPolicyForMessagesRequest(c *gin.Context, apiKey *service.APIKey, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	// The Messages bridge synthesizes a default OpenAI effort when
	// output_config.effort is omitted. Bind the group policy only for an
	// explicit client value so the ceiling does not alter that default.
	effort := gjson.GetBytes(body, "output_config.effort")
	if !effort.Exists() || effort.Type != gjson.String || strings.TrimSpace(effort.String()) == "" {
		return
	}
	maxEffort, mappings, ok := openAIReasoningEffortPolicyForRequest(c, apiKey)
	if !ok {
		return
	}
	c.Request = c.Request.WithContext(service.WithOpenAIReasoningEffortPolicy(c.Request.Context(), maxEffort, mappings))
}
