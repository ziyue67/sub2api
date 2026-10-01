package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type priorityCompatSettings struct {
	service.SettingRepository
	value string
}

func (r *priorityCompatSettings) GetValue(context.Context, string) (string, error) {
	return r.value, nil
}
func (r *priorityCompatSettings) Set(_ context.Context, _ string, value string) error {
	r.value = value
	return nil
}

func TestPrioritySavePreservesOmittedProtocolPreference(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		cfg := service.DefaultPrioritySchedulingConfig()
		cfg.BalanceProtocols = false
		raw, err := json.Marshal(cfg)
		require.NoError(t, err)
		repo := &priorityCompatSettings{value: string(raw)}
		h := &SettingHandler{settingService: service.NewSettingService(repo, nil)}
		var body map[string]any
		require.NoError(t, json.Unmarshal(raw, &body))
		delete(body, "balance_protocols")
		if explicit {
			body["balance_protocols"] = true
		}
		raw, err = json.Marshal(body)
		require.NoError(t, err)
		router := gin.New()
		router.PUT("/config", h.SavePriorityScheduling)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, "/config", bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
		var saved service.PrioritySchedulingConfig
		require.NoError(t, json.Unmarshal([]byte(repo.value), &saved))
		require.Equal(t, explicit, saved.BalanceProtocols)
	}
}
