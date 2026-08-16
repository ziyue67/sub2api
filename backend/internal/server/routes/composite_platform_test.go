package routes

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type compositeRouteRepoStub struct {
	routes []service.CompositeModelRoute
}

func (s compositeRouteRepoStub) ListByGroup(ctx context.Context, groupID int64, includeDisabled bool) ([]service.CompositeModelRoute, error) {
	routes := make([]service.CompositeModelRoute, 0, len(s.routes))
	for _, route := range s.routes {
		if route.GroupID != groupID {
			continue
		}
		if !includeDisabled && !route.Enabled {
			continue
		}
		routes = append(routes, route)
	}
	return routes, nil
}

func (s compositeRouteRepoStub) Create(ctx context.Context, route *service.CompositeModelRoute) error {
	return nil
}

func (s compositeRouteRepoStub) Update(ctx context.Context, route *service.CompositeModelRoute) error {
	return nil
}

func (s compositeRouteRepoStub) Delete(ctx context.Context, id int64) error {
	return nil
}

func (s compositeRouteRepoStub) DeleteByGroup(ctx context.Context, groupID int64) error {
	return nil
}

func TestCompositeTargetPlatformMiddlewareResolvesModelAndRestoresBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{Platform: service.PlatformComposite},
		})
		c.Next()
	})))
	router.Use(compositeTargetPlatformMiddleware(nil))
	router.POST("/", func(c *gin.Context) {
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, service.PlatformOpenAI, platform)

		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"gpt-5"}`, string(body))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"model":"gpt-5"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestCompositeTargetPlatformMiddlewareUsesExplicitRouteAndRewritesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []service.CompositeModelRoute{
			{
				ID:             1,
				GroupID:        1,
				PublicModel:    "openrouter/gpt-5",
				MatchType:      service.CompositeRouteMatchExact,
				TargetPlatform: service.PlatformOpenAI,
				UpstreamModel:  "gpt-5",
				Endpoint:       service.CompositeRouteEndpointAny,
				Priority:       100,
				Enabled:        true,
			},
		},
	})
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
		})
		c.Next()
	})))
	router.Use(compositeTargetPlatformMiddleware(resolver))
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, service.PlatformOpenAI, platform)

		upstreamModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, "gpt-5", upstreamModel)

		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.JSONEq(t, `{"model":"gpt-5","messages":[]}`, string(body))
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"openrouter/gpt-5","messages":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestCompositeDeepSeekDispatchesEachProtocolToItsNativeHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []service.CompositeModelRoute{
			{ID: 1, GroupID: 1, PublicModel: "deepseek-alias", MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: service.CompositeRouteEndpointChatCompletions, Priority: 100, Enabled: true},
			{ID: 2, GroupID: 1, PublicModel: "deepseek-alias", MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: service.CompositeRouteEndpointResponses, Priority: 100, Enabled: true},
			{ID: 3, GroupID: 1, PublicModel: "deepseek-alias", MatchType: service.CompositeRouteMatchExact, TargetPlatform: service.PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: service.CompositeRouteEndpointMessages, Priority: 100, Enabled: true},
		},
	})
	router := gin.New()
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
		})
		c.Next()
	})))
	router.Use(compositeTargetPlatformMiddleware(resolver))

	mark := func(name, endpoint string) gin.HandlerFunc {
		return func(c *gin.Context) {
			platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, service.PlatformDeepSeek, platform)
			upstreamModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, "deepseek-v4-pro", upstreamModel)
			publicModel, ok := service.RequestedPublicModelFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, "deepseek-alias", publicModel)
			resolvedEndpoint, ok := service.CompositeRouteEndpointFromContext(c.Request.Context())
			require.True(t, ok)
			require.Equal(t, endpoint, resolvedEndpoint)
			body, err := io.ReadAll(c.Request.Body)
			require.NoError(t, err)
			require.Equal(t, "deepseek-v4-pro", gjson.GetBytes(body, "model").String())
			c.String(http.StatusOK, name)
		}
	}
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		dispatchChatResponsesGateway(c, mark("openai-native-chat", service.CompositeRouteEndpointChatCompletions), mark("generic-chat", service.CompositeRouteEndpointChatCompletions))
	})
	router.POST("/v1/responses", func(c *gin.Context) {
		dispatchChatResponsesGateway(c, mark("openai-native-responses", service.CompositeRouteEndpointResponses), mark("generic-responses", service.CompositeRouteEndpointResponses))
	})
	router.POST("/v1/messages", func(c *gin.Context) {
		dispatchMessagesGateway(c, mark("openai-messages-bridge", service.CompositeRouteEndpointMessages), mark("deepseek-native-messages", service.CompositeRouteEndpointMessages))
	})

	for _, tc := range []struct {
		path string
		want string
	}{
		{path: "/v1/chat/completions", want: "openai-native-chat"},
		{path: "/v1/responses", want: "openai-native-responses"},
		{path: "/v1/messages", want: "deepseek-native-messages"},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(`{"model":"deepseek-alias"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "path=%s", tc.path)
		require.Equal(t, tc.want, w.Body.String(), "path=%s", tc.path)
	}
}

func TestCompositeTargetPlatformMiddlewareUsesExplicitRouteForMultipartImages(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []service.CompositeModelRoute{
			{
				ID:             1,
				GroupID:        1,
				PublicModel:    "image-alias",
				MatchType:      service.CompositeRouteMatchExact,
				TargetPlatform: service.PlatformOpenAI,
				UpstreamModel:  "gpt-image-1",
				Endpoint:       service.CompositeRouteEndpointImages,
				Priority:       100,
				Enabled:        true,
			},
		},
	})
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
		})
		c.Next()
	})))
	router.Use(compositeTargetPlatformMiddleware(resolver))
	router.POST("/v1/images/edits", func(c *gin.Context) {
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, service.PlatformOpenAI, platform)

		upstreamModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, "gpt-image-1", upstreamModel)

		publicModel, ok := service.RequestedPublicModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, "image-alias", publicModel)

		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "image-alias")
		c.Status(http.StatusNoContent)
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("model", "image-alias"))
	require.NoError(t, writer.WriteField("prompt", "draw"))
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestCompositeGeminiTargetPlatformMiddlewareUsesPathRoute(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	resolver := service.NewCompositeRouteResolver(compositeRouteRepoStub{
		routes: []service.CompositeModelRoute{
			{
				ID:             1,
				GroupID:        1,
				PublicModel:    "openrouter/gemini-pro",
				MatchType:      service.CompositeRouteMatchExact,
				TargetPlatform: service.PlatformGemini,
				UpstreamModel:  "gemini-2.5-pro",
				Endpoint:       service.CompositeRouteEndpointGemini,
				Priority:       100,
				Enabled:        true,
			},
		},
	})
	router.Use(gin.HandlerFunc(servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		groupID := int64(1)
		c.Set(string(servermiddleware.ContextKeyAPIKey), &service.APIKey{
			GroupID: &groupID,
			Group:   &service.Group{ID: groupID, Platform: service.PlatformComposite},
		})
		c.Next()
	})))
	router.Use(compositeGeminiTargetPlatformMiddleware(resolver))
	router.POST("/v1beta/models/*modelAction", func(c *gin.Context) {
		platform, ok := service.ResolvedTargetPlatformFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, service.PlatformGemini, platform)

		upstreamModel, ok := service.ResolvedUpstreamModelFromContext(c.Request.Context())
		require.True(t, ok)
		require.Equal(t, "gemini-2.5-pro", upstreamModel)
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1beta/models/openrouter/gemini-pro:generateContent", strings.NewReader(`{"contents":[]}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNoContent, w.Code)
}
