package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type moderationEngineSettings struct {
	service.SettingRepository
	value string
}

func (r *moderationEngineSettings) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}
func (r *moderationEngineSettings) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

func TestContentModerationEngineHandlerRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &moderationEngineSettings{value: `{"api_keys":["legacy-secret"],"model":"omni-moderation-latest"}`}
	svc := service.NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil)
	h := NewContentModerationHandler(svc)
	router := gin.New()
	router.PUT("/config", h.UpdateConfig)
	router.GET("/config", h.GetConfig)
	body := `{"engine":"typesafe","engine_configs":{"typesafe":{"base_url":"https://relay.example/typesafe/v1/","model":"jev-latest","api_keys":["new-secret"],"thresholds":{"sexual":0.91}}}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"engine":"typesafe"`)
	require.Contains(t, w.Body.String(), `"sexual":0.91`)
	require.Contains(t, w.Body.String(), `"base_url":"https://relay.example/typesafe/v1"`)
	require.NotContains(t, w.Body.String(), "new-secret")
	require.NotContains(t, w.Body.String(), "legacy-secret")
	w = httptest.NewRecorder()
	// Reload through a fresh service so the GET cannot rely on update-time state.
	reloaded := NewContentModerationHandler(service.NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil))
	router = gin.New()
	router.GET("/config", reloaded.GetConfig)
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/config", nil))
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"engine_configs"`)
	require.Contains(t, w.Body.String(), `"model":"jev-latest"`)
	require.Contains(t, w.Body.String(), `"base_url":"https://relay.example/typesafe/v1"`)
	require.Contains(t, settings.value, "legacy-secret")
	require.Contains(t, settings.value, "new-secret")
}

func TestContentModerationPerKeyURLHandlerRoundTrip(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settings := &moderationEngineSettings{value: `{"engine":"typesafe","typesafe":{"base_url":"https://default.example","api_keys":["fixture-one-shared","fixture-two-shared"]}}`}
	newHandler := func() *ContentModerationHandler {
		return NewContentModerationHandler(service.NewContentModerationService(settings, nil, nil, nil, nil, nil, nil, nil))
	}
	h := newHandler()
	router := gin.New()
	router.GET("/config", h.GetConfig)
	router.PUT("/config", h.UpdateConfig)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/config", nil))
	var initial struct {
		Data service.ContentModerationConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &initial))
	require.Len(t, initial.Data.APIKeyStatuses, 2)
	for i, base := range []string{"https://one.example/v1/", "https://two.example/v1"} {
		payload, _ := json.Marshal(map[string]any{"engine_configs": map[string]any{"typesafe": map[string]any{"key_url_updates": []map[string]string{{"key_hash": initial.Data.APIKeyStatuses[i].KeyHash, "base_url": base, "expected_base_url": "https://default.example"}}}}})
		w = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
	}
	fresh := gin.New()
	fresh.GET("/config", newHandler().GetConfig)
	w = httptest.NewRecorder()
	fresh.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/config", nil))
	var got struct {
		Data service.ContentModerationConfigView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "https://one.example/v1", got.Data.APIKeyStatuses[0].BaseURL)
	require.Equal(t, "https://two.example/v1", got.Data.APIKeyStatuses[1].BaseURL)
	for _, key := range []string{"fixture-one-shared", "fixture-two-shared"} {
		require.False(t, strings.Contains(w.Body.String(), key))
	}
}
