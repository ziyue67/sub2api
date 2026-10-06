package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type astraHandlerRepo struct {
	service.SettingRepository
	value string
}

func (r *astraHandlerRepo) GetValue(context.Context, string) (string, error) {
	if r.value == "" {
		return "", service.ErrSettingNotFound
	}
	return r.value, nil
}
func (r *astraHandlerRepo) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}
func TestAstraGatewayHandlerRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &astraHandlerRepo{}
	cfg := &config.Config{}
	h := &SettingHandler{settingService: service.NewSettingService(repo, cfg)}
	router := gin.New()
	router.GET("/settings", h.GetAstraRouting)
	router.PUT("/settings", h.UpdateAstraRouting)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/settings", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"enabled":false`)
	req := httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"cookie_pool":{"enabled":true,"source_account_ids":[299],"target_account_ids":[300]},"ws_session":{"enabled":false,"account_ids":[]}}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.True(t, cfg.AstraRouting(t.Context()).CookiePool.Enabled)
	before := repo.value
	req = httptest.NewRequest(http.MethodPut, "/settings", strings.NewReader(`{"cookie_pool":{"enabled":true,"source_account_ids":[299],"target_account_ids":[299]}}`))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, 400, w.Code)
	require.Equal(t, before, repo.value)
}
