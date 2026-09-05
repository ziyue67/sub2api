package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newCompositeAPIKeyForTest() *service.APIKey {
	return &service.APIKey{Group: &service.Group{ID: 1, Platform: service.PlatformComposite}}
}

func newCompositeGateContextForTest(path string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, path, nil)
	c.Request = req.WithContext(context.Background())
	return c
}

// Scenario: a composite group must resolve Antigravity-only Gemini Flash models
// to the antigravity platform. Resolving them to gemini (the pre-fix behavior)
// made every antigravity account ineligible during scheduling, so the client
// only ever saw "model is not supported" (#6523).
func TestCompositeResolvesAntigravityOnlyGeminiModelsToAntigravity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, model := range []string{
		"gemini-3.6-flash",
		"gemini-3.6-flash-medium",
		"gemini-3.7-flash",
		"gemini-3.8-flash",
		"gemini-3.8-flash-high",
		"gemini-3.8-flash-low",
		"gemini-3.8-flash-medium",
		"gemini-3.8-flash-tiered",
	} {
		for _, path := range []string{"/v1/chat/completions", "/v1/messages", "/v1/responses"} {
			t.Run(model+path, func(t *testing.T) {
				c := newCompositeGateContextForTest(path)

				require.True(t, compositeTargetPlatformResolved(c, newCompositeAPIKeyForTest(), model),
					"composite gate must resolve a target platform for %s", model)

				platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
				require.True(t, ok)
				require.Equal(t, service.PlatformAntigravity, platform)
			})
		}
	}
}

// Scenario: models served by the public Gemini channel keep the historical
// gemini target platform so existing deployments are unaffected.
func TestCompositeKeepsPublicGeminiModelsOnGeminiPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, model := range []string{
		"gemini-2.5-flash",
		"gemini-3-flash",
		"gemini-3.5-flash",
		"gemini-3.1-pro-high",
		"gemini-3.1-flash-image",
	} {
		t.Run(model, func(t *testing.T) {
			c := newCompositeGateContextForTest("/v1/chat/completions")

			require.True(t, compositeTargetPlatformResolved(c, newCompositeAPIKeyForTest(), model))

			platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, service.PlatformGemini, platform)
		})
	}
}

// Scenario: OpenAI-only composite endpoints (embeddings/images) must still
// reject antigravity targets — the platform-detection change must not widen
// those gates.
func TestOpenAIOnlyCompositeGatesStillRejectAntigravityTargets(t *testing.T) {
	gin.SetMode(gin.TestMode)

	c := newCompositeGateContextForTest("/v1/embeddings")
	require.False(t, compositeTargetPlatformAllowed(c, newCompositeAPIKeyForTest(), "gemini-3.8-flash", service.PlatformOpenAI))

	platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, service.PlatformAntigravity, platform)
}

// Scenario: a Gemini group mixing in Antigravity OAuth accounts must keep those
// accounts eligible on /v1/chat/completions. The pre-fix platform guard dropped
// every non-Gemini account before the antigravity compat branch could run, so
// Antigravity-only models had an empty candidate pool.
func TestGeminiGroupKeepsAntigravityOAuthAccountsForChatCompletions(t *testing.T) {
	antigravityOAuth := &service.Account{Platform: service.PlatformAntigravity, Type: service.AccountTypeOAuth}
	require.True(t, shouldUseAntigravityCompat(antigravityOAuth),
		"antigravity OAuth accounts must stay eligible for gemini groups")

	// API-key antigravity accounts have no compat forwarder, so the guard must
	// still drop them for gemini groups.
	antigravityAPIKey := &service.Account{Platform: service.PlatformAntigravity, Type: service.AccountTypeAPIKey}
	require.False(t, shouldUseAntigravityCompat(antigravityAPIKey))

	openAIAccount := &service.Account{Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
	require.False(t, shouldUseAntigravityCompat(openAIAccount))
}
