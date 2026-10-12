// Adapted from MACOS-DO/sub4api at 254e7ec4932e65ab8830e7d10e66f184140041bd.
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetOpenAIRequestTimezones(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := &AccountHandler{}
	router.GET("/api/v1/admin/accounts/openai-request-timezones", handler.GetOpenAIRequestTimezones)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/openai-request-timezones", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	var payload struct {
		Data struct {
			Default   string   `json:"default"`
			Timezones []string `json:"timezones"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	require.Equal(t, service.DefaultOpenAIRequestTimezone, payload.Data.Default)
	require.Greater(t, len(payload.Data.Timezones), 30)
	require.Equal(t, service.OpenAIRequestTimezoneOptions(), payload.Data.Timezones)
	require.Contains(t, payload.Data.Timezones, "Europe/London")
	require.Contains(t, payload.Data.Timezones, "Europe/Oslo")
	require.Contains(t, payload.Data.Timezones, "Asia/Shanghai")
	require.Contains(t, payload.Data.Timezones, "Asia/Hong_Kong")
	require.Contains(t, payload.Data.Timezones, "Asia/Taipei")
	require.NotContains(t, payload.Data.Timezones, "Invalid/Timezone")
}
