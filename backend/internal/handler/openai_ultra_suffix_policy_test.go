package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the handlers, not just the policy function: Messages used to omit
// its policy binding, and composite routing removes the public model suffix.
func TestOpenAIUltraSuffixRejectedBeforeScheduling(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, composite := range []bool{false, true} {
		for _, mappingDeny := range []bool{false, true} {
			for _, path := range []string{"/v1/responses", "/v1/chat/completions", "/v1/messages"} {
				name := path
				if composite {
					name += "/composite"
				}
				if mappingDeny {
					name += "/mapping-deny"
				}
				t.Run(name, func(t *testing.T) {
					publicModel, bodyModel := "gpt-test-ultra", "gpt-test-ultra"
					group := &service.Group{Platform: service.PlatformOpenAI, AllowMessagesDispatch: true,
						MaxReasoningEffort: "max", MaxReasoningEffortOverLimit: service.ReasoningEffortOverLimitDeny}
					if composite {
						group.Platform = service.PlatformComposite
						publicModel, bodyModel = "staff-ultra", "gpt-6.1-sol"
					}
					if mappingDeny {
						group.MaxReasoningEffort = ""
						group.ReasoningEffortMappings = []service.ReasoningEffortMapping{
							{From: "ultra", To: "deny", Model: publicModel, MatchType: "exact"},
						}
					}
					h := newServiceTierHandlerTest(t)
					t.Cleanup(h.billingCacheService.Stop)
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ID: 1, Group: group,
							User: &service.User{ID: 2, Status: service.StatusActive}})
						c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 2, Concurrency: 1})
						if composite {
							// The exact context and body produced by composite routing.
							c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
								Matched: true, PublicModel: publicModel, UpstreamModel: bodyModel, TargetPlatform: service.PlatformOpenAI,
							}))
						}
					})
					switch path {
					case "/v1/responses":
						router.POST(path, h.Responses)
					case "/v1/chat/completions":
						router.POST(path, h.ChatCompletions)
					case "/v1/messages":
						router.POST(path, h.Messages)
					}
					body := `{"model":"` + bodyModel + `","input":"test","messages":[{"role":"user","content":"test"}],"max_tokens":16}`
					req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
					req.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					router.ServeHTTP(rec, req)
					require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
					require.Contains(t, rec.Body.String(), "ultra")
					require.Contains(t, rec.Body.String(), "permission_error")
				})
			}
		}
	}
}

func TestMessagesUltraSuffixDowngradeAppliesOneMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request = c.Request.WithContext(service.WithCompositeRouteDecision(c.Request.Context(), service.CompositeRouteDecision{
		Matched: true, PublicModel: "staff-ultra", UpstreamModel: "gpt-6.1-sol", TargetPlatform: service.PlatformOpenAI,
	}))
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformComposite,
		ReasoningEffortMappings: []service.ReasoningEffortMapping{
			{From: "ultra", To: "high", Model: "staff-ultra", MatchType: "exact"},
			{From: "high", To: "low"},
		},
	}}
	body, err := prepareOpenAIReasoningEffortPolicyForMessagesRequest(c, key, []byte(`{"model":"gpt-6.1-sol","messages":[]}`))
	require.NoError(t, err)
	require.Equal(t, "high", gjson.GetBytes(body, "output_config.effort").String())
	require.Equal(t, "gpt-6.1-sol", gjson.GetBytes(body, "model").String())
	requested := service.RequestedReasoningEffortFromContext(c.Request.Context())
	require.NotNil(t, requested)
	require.Equal(t, "ultra", *requested)
	// Conversion must not apply high -> low a second time.
	converted := []byte(`{"model":"gpt-6.1-sol","reasoning":{"effort":"high"}}`)
	forwarded, changed, err := service.ApplyOpenAIReasoningEffortPolicyFromContext(c.Request.Context(), converted)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, converted, forwarded)
}

func TestMessagesExplicitEffortOverridesUltraSuffix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	key := &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI,
		ReasoningEffortMappings: []service.ReasoningEffortMapping{{From: "ultra", To: "deny"}},
	}}
	body := []byte(`{"model":"gpt-test-ultra","output_config":{"effort":"high"}}`)
	prepared, err := prepareOpenAIReasoningEffortPolicyForMessagesRequest(c, key, body)
	require.NoError(t, err)
	require.Equal(t, body, prepared)
	converted := []byte(`{"model":"gpt-test","reasoning":{"effort":"high"}}`)
	forwarded, changed, err := service.ApplyOpenAIReasoningEffortPolicyFromContext(c.Request.Context(), converted)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, converted, forwarded)
}
