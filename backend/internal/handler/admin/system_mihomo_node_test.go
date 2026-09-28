package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubMihomoNodeChecker struct {
	kernel service.MihomoNodeProber
	names  []string
	err    error
}

func (s *stubMihomoNodeChecker) TestMihomoNode(_ context.Context, kernel service.MihomoNodeProber, name string) (*service.ProxyTestResult, error) {
	s.kernel = kernel
	s.names = append(s.names, name)
	if s.err != nil {
		return nil, s.err
	}
	return &service.ProxyTestResult{Success: true, Message: "Proxy is accessible", LatencyMs: 12, IPAddress: "203.0.113.9"}, nil
}

func (s *stubMihomoNodeChecker) CheckMihomoNodeQuality(_ context.Context, kernel service.MihomoNodeProber, name string) (*service.ProxyQualityCheckResult, error) {
	s.kernel = kernel
	s.names = append(s.names, name)
	if s.err != nil {
		return nil, s.err
	}
	return &service.ProxyQualityCheckResult{Score: 78, Grade: "B", Items: []service.ProxyQualityCheckItem{{Target: "base_connectivity", Status: "pass"}}}, nil
}

func mihomoNodeCheckRouter(h *SystemHandler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/api/v1/admin/system/mihomo/nodes/:name/test", h.TestMihomoNode)
	router.POST("/api/v1/admin/system/mihomo/nodes/:name/quality-check", h.CheckMihomoNodeQuality)
	return router
}

func postMihomoNodeCheck(t *testing.T, router *gin.Engine, path string) (int, map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, nil))
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return rec.Code, body
}

func TestMihomoNodeChecksRunThroughTheManagedKernel(t *testing.T) {
	kernel := mihomo.New(t.TempDir())
	t.Cleanup(kernel.Close)
	checker := &stubMihomoNodeChecker{}
	h := &SystemHandler{kernel: kernel}
	h.SetMihomoNodeChecker(checker)
	router := mihomoNodeCheckRouter(h)

	code, body := postMihomoNodeCheck(t, router, "/api/v1/admin/system/mihomo/nodes/DYNAMIC-0123456789abcdef/test")
	require.Equal(t, http.StatusOK, code)
	data, ok := body["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, data["success"])
	require.EqualValues(t, 12, data["latency_ms"])
	manager, ok := checker.kernel.(*mihomo.Manager)
	require.True(t, ok)
	require.Same(t, kernel, manager, "checks use the handler's kernel manager")

	code, body = postMihomoNodeCheck(t, router, "/api/v1/admin/system/mihomo/nodes/node-0123456789abcdef/quality-check")
	require.Equal(t, http.StatusOK, code)
	data, ok = body["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "B", data["grade"])
	require.Equal(t, []string{"DYNAMIC-0123456789abcdef", "node-0123456789abcdef"}, checker.names)
}

func TestMihomoNodeCheckErrors(t *testing.T) {
	kernel := mihomo.New(t.TempDir())
	t.Cleanup(kernel.Close)
	checker := &stubMihomoNodeChecker{err: infraerrors.NotFound("MIHOMO_NODE_NOT_FOUND", "node no longer exists; refresh the list")}
	h := &SystemHandler{kernel: kernel}
	h.SetMihomoNodeChecker(checker)
	router := mihomoNodeCheckRouter(h)
	for _, path := range []string{"/api/v1/admin/system/mihomo/nodes/node-gone/test", "/api/v1/admin/system/mihomo/nodes/node-gone/quality-check"} {
		code, body := postMihomoNodeCheck(t, router, path)
		require.Equal(t, http.StatusNotFound, code)
		require.Equal(t, "MIHOMO_NODE_NOT_FOUND", body["reason"])
	}

	checker.err = infraerrors.Conflict("MIHOMO_NODE_CHECK_UNAVAILABLE", "install the kernel first")
	code, body := postMihomoNodeCheck(t, router, "/api/v1/admin/system/mihomo/nodes/node-one/test")
	require.Equal(t, http.StatusConflict, code)
	require.Equal(t, "install the kernel first", body["message"])

	unconfigured := mihomoNodeCheckRouter(&SystemHandler{kernel: kernel})
	for _, path := range []string{"/api/v1/admin/system/mihomo/nodes/node-one/test", "/api/v1/admin/system/mihomo/nodes/node-one/quality-check"} {
		code, _ = postMihomoNodeCheck(t, unconfigured, path)
		require.Equal(t, http.StatusServiceUnavailable, code)
	}
}
