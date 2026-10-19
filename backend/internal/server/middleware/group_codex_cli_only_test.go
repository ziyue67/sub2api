package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func codexGroupRequest(method, path, body string, official bool) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if official {
		req.Header.Set("User-Agent", "codex-tui/0.163.0 (Ubuntu 22.4.0; x86_64) xterm-256color")
		req.Header.Set("Originator", "codex-tui")
		req.Header.Set("X-Codex-Window-Id", "test-codex-window")
	} else {
		req.Header.Set("User-Agent", "OpenAI/Python 2.0.0")
	}
	return req
}

func TestGroupCodexCLIOnlyPreservesOfficialRequestsAndRejectsForgedSystemCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/responses/compact", "/v1/chat/completions"} {
		t.Run(path, func(t *testing.T) {
			r := gin.New()
			calls := 0
			r.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{CodexCLIOnly: true}})
			})
			r.Use(GroupCodexCLIOnly(nil))
			body := `{"model":"gpt-test","input":"employee request","stream":true}`
			r.POST(path, func(c *gin.Context) {
				calls++
				actual, err := io.ReadAll(c.Request.Body)
				require.NoError(t, err)
				require.Equal(t, body, string(actual), "classification must preserve the original request")
				c.Status(http.StatusNoContent)
			})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, codexGroupRequest(http.MethodPost, path, body, true))
			require.Equal(t, http.StatusNoContent, w.Code)
			require.Equal(t, 1, calls)

			for _, header := range []string{"X-Sub2API-Internal", "X-Quality-Judge", "X-Admin-Test"} {
				req := codexGroupRequest(http.MethodPost, path, body, false)
				req.Header.Set(header, "true")
				w = httptest.NewRecorder()
				r.ServeHTTP(w, req)
				require.Equal(t, http.StatusForbidden, w.Code)
				require.Contains(t, w.Body.String(), "codex_client_required")
			}
			require.Equal(t, 1, calls, "rejected employees must never reach forwarding")
		})
	}
}

func TestGroupCodexCLIOnlyRejectsMissingFingerprintAndLeavesDisabledGroupUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, enabled := range []bool{false, true} {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{CodexCLIOnly: enabled}})
		})
		r.Use(GroupCodexCLIOnly(nil))
		r.POST("/v1/responses", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		req := codexGroupRequest(http.MethodPost, "/v1/responses", `{}`, true)
		req.Header.Del("X-Codex-Window-Id")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if enabled {
			require.Equal(t, http.StatusForbidden, w.Code)
		} else {
			require.Equal(t, http.StatusNoContent, w.Code)
		}
	}
}

func TestGroupCodexCLIOnlyWSChecksOriginalFramesAfterHandshake(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{CodexCLIOnly: true}})
	})
	r.Use(GroupCodexCLIOnly(nil))
	r.GET("/v1/responses", func(c *gin.Context) {
		// A rewritten composite destination must not remove the employee gate.
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{}})
		// Upstream-generated headers must not supply a missing client fingerprint.
		c.Request.Header.Set("X-Codex-Turn-State", "upstream-generated")
		result := CheckGroupCodexClientFrame(c, []byte(`{"type":"response.create","model":"gpt-test"}`))
		require.True(t, result.Enabled)
		if !result.Matched {
			c.Status(http.StatusForbidden)
			return
		}
		c.Status(http.StatusNoContent)
	})
	for _, fingerprint := range []bool{false, true} {
		req := codexGroupRequest(http.MethodGet, "/v1/responses", "", true)
		if !fingerprint {
			req.Header.Del("X-Codex-Window-Id")
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if fingerprint {
			require.Equal(t, http.StatusNoContent, w.Code)
		} else {
			require.Equal(t, http.StatusForbidden, w.Code, "a header-only WS preflight must not skip the frame fingerprint gate")
		}
	}
}

func TestGroupCodexCLIOnlyDoesNotGateAdminConnectionAndIQTests(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	public := r.Group("/v1")
	public.Use(func(c *gin.Context) {
		c.Set(string(ContextKeyAPIKey), &service.APIKey{Group: &service.Group{CodexCLIOnly: true}})
	})
	public.Use(GroupCodexCLIOnly(nil))
	public.POST("/responses", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	for _, path := range []string{"/api/v1/admin/accounts/:id/test", "/api/v1/admin/accounts/:id/pelican-test"} {
		r.POST(path, func(c *gin.Context) {
			result := CheckGroupCodexClientFrame(c, nil)
			require.False(t, result.Enabled, "management calls cannot inherit employee admission")
			c.Status(http.StatusNoContent)
		})
	}
	for _, path := range []string{"/api/v1/admin/accounts/34/test", "/api/v1/admin/accounts/34/pelican-test"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, codexGroupRequest(http.MethodPost, path, `{}`, false))
		require.Equal(t, http.StatusNoContent, w.Code)
	}
}

func TestCodexGroupPolicyChangesRequireQueuedAndWebSocketRequestsToReenterAdmission(t *testing.T) {
	initial := &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI}}
	identity := captureAPIKeyQueueIdentity(initial)
	latest := &service.APIKey{ID: 1, Group: &service.Group{Platform: service.PlatformOpenAI, CodexCLIOnly: true}}
	require.True(t, apiKeyQueueCoreIdentityChanged(identity, latest), "a connection opened while unrestricted must not bypass a newly enabled gate")
	latest.Group.CodexCLIOnly = false
	require.False(t, apiKeyQueueCoreIdentityChanged(identity, latest))
}
