package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesBlockSubagentsBeforeCompositeRouting(t *testing.T) {
	for _, platform := range []string{service.PlatformOpenAI, service.PlatformComposite} {
		router := newGatewayRoutesTestRouterWithGroup(&service.Group{Platform: platform, BlockSubagents: true})
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			paths := []string{"/v1/responses", "/responses", "/backend-api/codex/responses"}
			if method == http.MethodPost {
				paths = append(paths, "/v1/chat/completions", "/chat/completions", "/v1/messages", "/messages/count_tokens", "/antigravity/v1/messages", "/v1/responses/compact")
			}
			for _, path := range paths {
				req := httptest.NewRequest(method, path, strings.NewReader(`{"model":"public-alias"}`))
				req.Header.Set("X-OpenAI-Subagent", "worker")
				req.Header.Set("Content-Type", "application/json")
				if method == http.MethodGet {
					req.Header.Set("Connection", "Upgrade")
					req.Header.Set("Upgrade", "websocket")
				}
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				require.Equal(t, http.StatusForbidden, w.Code, "%s %s: %s", method, path, w.Body.String())
				require.Contains(t, w.Body.String(), "subagents_blocked")
			}
		}
	}
}
