package routes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type bpsImageAdmissionRouteRepo struct{ service.SettingRepository }

func (*bpsImageAdmissionRouteRepo) GetMultiple(context.Context, []string) (map[string]string, error) {
	return map[string]string{service.SettingKeyExcelBPSImageRelayEnabled: "true", service.SettingKeyExcelBPSImageBaseURL: "https://images.example"}, nil
}

func (*bpsImageAdmissionRouteRepo) GetValue(context.Context, string) (string, error) {
	return "", service.ErrSettingNotFound
}

type bpsImageUnreadBody struct{ read bool }

func (b *bpsImageUnreadBody) Read([]byte) (int, error) { b.read = true; return 0, io.EOF }
func (*bpsImageUnreadBody) Close() error               { return nil }

func TestExcelBPSImageAdmissionMarksGatewayAliases(t *testing.T) {
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 256 << 20, TextMaxBodySize: 32 << 20}}
	settings := service.NewSettingService(&bpsImageAdmissionRouteRepo{}, cfg)
	r := gin.New()
	var marked bool
	// The key has no group, so RequireGroupAssignment stops each request before
	// a handler runs; the marker is read after the chain returns.
	RegisterGatewayRoutes(r, &handler.Handlers{OpenAIGateway: &handler.OpenAIGatewayHandler{}, Gateway: &handler.GatewayHandler{}}, func(c *gin.Context) {
		c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: service.PlatformOpenAI}})
		c.Next()
		marked = service.IsExcelBPSImageAdmissionMarked(c)
	}, nil, nil, nil, settings, nil, cfg)
	for _, path := range []string{"/responses", "/responses/compact", "/v1/responses", "/v1/responses/compact", "/backend-api/codex/responses", "/backend-api/codex/responses/compact", "/v1/chat/completions", "/chat/completions", "/v1/messages"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-6-astra","input":"hello"}`))
		marked = false
		r.ServeHTTP(httptest.NewRecorder(), req)
		require.True(t, marked, path)
	}

	// The image relay body limit only applies on the BPS route, not at the
	// gateway entrance.
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	req.Body = &bpsImageUnreadBody{}
	req.ContentLength = 65 << 20
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.NotEqual(t, http.StatusRequestEntityTooLarge, w.Code)
	require.NotContains(t, w.Body.String(), "basispoints_image_body_too_large")
}
