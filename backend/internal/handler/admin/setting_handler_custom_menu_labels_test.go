//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCustomMenuLocalizedLabelsRoundTripThroughSettingsAPI(t *testing.T) {
	h, repo := newStepUpSwitchTestHandler(t, map[string]string{})

	items := []map[string]any{
		{"id": "help", "label_zh": "帮助中心", "label_en": "Help center", "url": "https://example.com/help", "visibility": "user"},
		{"id": "legacy", "label": "Legacy page", "url": "https://example.com/legacy", "visibility": "user"},
	}
	update := doUpdateSettings(t, h, map[string]any{"custom_menu_items": items}, nil)
	require.Equal(t, http.StatusOK, update.Code, update.Body.String())

	var stored []map[string]any
	require.NoError(t, json.Unmarshal([]byte(repo.values[service.SettingKeyCustomMenuItems]), &stored))
	require.Equal(t, "帮助中心", stored[0]["label_zh"])
	require.Equal(t, "Help center", stored[0]["label_en"])
	require.Equal(t, "帮助中心", stored[0]["label"])
	require.Equal(t, "Legacy page", stored[1]["label"])

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	h.GetSettings(c)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var response struct {
		Data struct {
			CustomMenuItems []map[string]any `json:"custom_menu_items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
	require.Len(t, response.Data.CustomMenuItems, 2)
	require.Equal(t, "帮助中心", response.Data.CustomMenuItems[0]["label_zh"])
	require.Equal(t, "Help center", response.Data.CustomMenuItems[0]["label_en"])
	require.Equal(t, "帮助中心", response.Data.CustomMenuItems[0]["label"])
	require.Equal(t, "Legacy page", response.Data.CustomMenuItems[1]["label"])
}

func TestCustomMenuLocalizedLabelsUseCharacterLimit(t *testing.T) {
	h, _ := newStepUpSwitchTestHandler(t, map[string]string{})
	menuItem := func(label string) []map[string]any {
		return []map[string]any{{
			"id": "help", "label_zh": label, "url": "https://example.com/help", "visibility": "user",
		}}
	}

	accepted := doUpdateSettings(t, h, map[string]any{"custom_menu_items": menuItem(strings.Repeat("界", 50))}, nil)
	require.Equal(t, http.StatusOK, accepted.Code, accepted.Body.String())

	rejected := doUpdateSettings(t, h, map[string]any{"custom_menu_items": menuItem(strings.Repeat("界", 51))}, nil)
	require.Equal(t, http.StatusBadRequest, rejected.Code, rejected.Body.String())
}
