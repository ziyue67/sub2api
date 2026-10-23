package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type safetyPromptHandlerSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *safetyPromptHandlerSettingsRepo) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	out := map[string]string{}
	for _, key := range keys {
		if value, ok := r.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (r *safetyPromptHandlerSettingsRepo) SetMultiple(_ context.Context, values map[string]string) error {
	if r.values == nil {
		r.values = map[string]string{}
	}
	for key, value := range values {
		r.values[key] = value
	}
	return nil
}

func newSafetyPromptHandlerRouter(t *testing.T, admin bool) (*gin.Engine, *service.SafetyPromptStore, *safetyPromptHandlerSettingsRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	t.Setenv("DATA_DIR", t.TempDir())
	repo := &safetyPromptHandlerSettingsRepo{values: map[string]string{}}
	settings := service.NewSettingService(repo, &config.Config{})
	store := service.NewSafetyPromptStore()
	h := NewContentModerationHandlerWithSafetyPrompt(nil, store, settings)

	router := gin.New()
	if admin {
		router.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
			c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
			c.Next()
		})
	}
	router.GET("/admin/risk-control/safety-prompt/config", h.GetSafetyPromptConfig)
	router.PUT("/admin/risk-control/safety-prompt/config", h.UpdateSafetyPromptConfig)
	router.GET("/admin/risk-control/safety-prompt/events", h.ListSafetyPromptEvents)
	return router, store, repo
}

func TestSafetyPromptHandlerConfigRoundTrip(t *testing.T) {
	router, _, repo := newSafetyPromptHandlerRouter(t, true)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/risk-control/safety-prompt/config", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"enabled":false`)
	require.Contains(t, recorder.Body.String(), `"persistent":true`)

	recorder = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/admin/risk-control/safety-prompt/config", strings.NewReader(`{"enabled":true,"signal_enabled":true,"retention_days":0}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	recorder = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/admin/risk-control/safety-prompt/config", strings.NewReader(`{"enabled":true,"signal_enabled":true,"retention_days":7}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "true", repo.values[service.SettingKeySafetyPromptEnabled])
	require.Equal(t, "7", repo.values[service.SettingKeySafetyPromptRetentionDays])

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/risk-control/safety-prompt/config", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"enabled":true`)
	require.Contains(t, recorder.Body.String(), `"retention_days":7`)
}

func TestSafetyPromptHandlerEventsFilterAndBadRange(t *testing.T) {
	router, store, _ := newSafetyPromptHandlerRouter(t, true)
	store.Record(service.SafetyPromptEvent{
		Time: time.Now().UTC(), ID: "sp-list", Model: "gpt-5.1",
		Action: "suspected_policy_refusal", Summary: "redacted",
	}, 30)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/risk-control/safety-prompt/events", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"sp-list"`)
	require.Contains(t, recorder.Body.String(), `"events"`)

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/risk-control/safety-prompt/events?from=not-a-date", nil))
	require.Equal(t, http.StatusBadRequest, recorder.Code)
}

func TestSafetyPromptHandlerRequiresAdministrator(t *testing.T) {
	router, _, _ := newSafetyPromptHandlerRouter(t, false)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/admin/risk-control/safety-prompt/config", nil))
	require.Equal(t, http.StatusForbidden, recorder.Code)
}
