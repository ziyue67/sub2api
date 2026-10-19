package routes

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGatewayRoutesEnforceCodexGroupAcrossAliasesAndTransports(t *testing.T) {
	router := newGatewayRoutesTestRouterWithGroup(&service.Group{Platform: service.PlatformOpenAI, CodexCLIOnly: true})
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		paths := []string{"/v1/responses", "/responses", "/backend-api/codex/responses"}
		if method == http.MethodPost {
			paths = append(paths, "/v1/responses/compact", "/responses/compact", "/backend-api/codex/responses/compact", "/v1/chat/completions", "/chat/completions", "/v1/messages")
		}
		for _, path := range paths {
			req := httptest.NewRequest(method, path, strings.NewReader(`{"model":"gpt-test","input":"test"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "OpenAI/Python 2.0.0")
			if method == http.MethodGet {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			require.Equal(t, http.StatusForbidden, w.Code, "%s %s: %s", method, path, w.Body.String())
			require.Contains(t, w.Body.String(), "codex_client_required")
		}
	}
}
