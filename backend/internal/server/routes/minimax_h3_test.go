package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestMiniMaxH3RoutesAreScopedToMiniMax(t *testing.T) {
	for _, platform := range []string{service.PlatformMiniMax, service.PlatformGrok, service.PlatformComposite, service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			router := newGatewayRoutesTestRouter(platform)
			for _, path := range []string{"/v1/videos", "/videos", "/v1/videos/task", "/v1/videos/task/content"} {
				method := http.MethodGet
				if path == "/v1/videos" || path == "/videos" {
					method = http.MethodPost
				}
				req := httptest.NewRequest(method, path, strings.NewReader(`{"model":"MiniMax-H3","prompt":"waves","seconds":"1"}`))
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if platform == service.PlatformMiniMax || platform == service.PlatformGrok || platform == service.PlatformComposite {
					require.NotContains(t, w.Body.String(), "not supported for this platform", path)
				} else {
					require.Equal(t, http.StatusNotFound, w.Code, path)
					require.Contains(t, w.Body.String(), "Videos API is not supported for this platform", path)
				}
			}
		})
	}
}
