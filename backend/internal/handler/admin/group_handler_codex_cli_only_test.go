package admin

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupHandlerPassesCodexCLIOnlyToService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	h := NewGroupHandlerWithConfig(svc, nil, nil, &config.Config{})
	r := gin.New()
	r.POST("/groups", h.Create)
	r.PUT("/groups/:id", h.Update)

	res := serveGroupRequest(r, http.MethodPost, "/groups", `{"name":"cc","platform":"anthropic","rate_multiplier":1,"codex_cli_only":true}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Len(t, svc.createdGroups, 1)
	require.True(t, svc.createdGroups[0].CodexCLIOnly)

	res = serveGroupRequest(r, http.MethodPut, "/groups/2", `{"codex_cli_only":false}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	res = serveGroupRequest(r, http.MethodPut, "/groups/2", `{"name":"renamed"}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.Len(t, svc.updatedGroups, 2)
	require.NotNil(t, svc.updatedGroups[0].CodexCLIOnly)
	require.False(t, *svc.updatedGroups[0].CodexCLIOnly)
	require.Nil(t, svc.updatedGroups[1].CodexCLIOnly, "没带 codex_cli_only 时不改")
}

func TestGroupHandlerSimpleModeDropsCodexCLIOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	svc := newStubAdminService()
	r := newSimpleModeGroupRouter(svc)

	res := serveGroupRequest(r, http.MethodPost, "/groups", `{"name":"simple","platform":"anthropic","codex_cli_only":true}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	res = serveGroupRequest(r, http.MethodPut, "/groups/2", `{"name":"simple","codex_cli_only":true}`)
	require.Equal(t, http.StatusOK, res.Code, res.Body.String())
	require.False(t, svc.createdGroups[0].CodexCLIOnly)
	require.Nil(t, svc.updatedGroups[0].CodexCLIOnly)
}
