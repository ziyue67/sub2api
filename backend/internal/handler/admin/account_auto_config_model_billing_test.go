//go:build unit

package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type modelBillingHandlerGroups struct{ service.GroupRepository }

func TestAutoConfigModelBillingLegacySaveAndClear(t *testing.T) {
	gin.SetMode(gin.TestMode)
	original := service.DefaultOAuthAutoConfig()
	original.ModelBilling = service.ModelBillingConfig{Enabled: true, Rules: []service.ModelBillingRule{
		{Model: "luna", Multiplier: 10, GroupIDs: []int64{5}},
		{Model: "gpt-*", Multiplier: 2, GroupIDs: []int64{6}},
	}}
	raw, err := json.Marshal(original)
	require.NoError(t, err)
	repo := &opsHandlerWritableSettings{raw: string(raw)}
	cfg := &config.Config{}
	cfg.Runtime.Role = config.RuntimeRoleGateway
	svc := service.ProvideAccountOpsService(repo, opsHandlerQueue{}, nil, nil, modelBillingHandlerGroups{}, cfg, nil, nil, nil, nil, nil)
	svc.Stop() // No background work participates in this offline handler fixture.
	handler := NewAccountOpsHandler(svc, nil)
	router := gin.New()
	router.PUT("/config", handler.SaveAutoConfig)
	router.GET("/config", handler.GetAutoConfig)
	for _, tc := range []struct {
		body   string
		status int
		scopes [][]int64
	}{
		{`{"concurrency":9,"model_billing":{"rules":[{"model":"gpt-*","multiplier":3},{"model":" LUNA ","multiplier":10}]}}`, 200, [][]int64{{6}, {5}}},
		{`{"model_billing":{"enabled":false}}`, 200, [][]int64{{6}, {5}}},
		{`{"model_billing":{"rules":[{"model":"renamed","multiplier":10}]}}`, 400, nil},
		{`{"model_billing":{"rules":[{"model":"gpt-*","multiplier":3,"group_ids":[]},{"model":"luna","multiplier":10}]}}`, 200, [][]int64{{}, {5}}},
		{`{"model_billing":{"rules":[{"model":"luna","multiplier":10,"group_ids":[0]}]}}`, 400, nil},
		{`{"model_billing":{"rules":[{"model":"luna","multiplier":10,"group_ids":[1.5]}]}}`, 400, nil},
	} {
		before := repo.raw
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/config", strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, tc.status, w.Code, w.Body.String())
		if tc.status != 200 {
			require.Equal(t, before, repo.raw)
			continue
		}
		var saved service.OAuthAutoConfig
		require.NoError(t, json.Unmarshal([]byte(repo.raw), &saved))
		require.Equal(t, 9, saved.Concurrency)
		require.Equal(t, original.OpenAITwoFAAdmission, saved.OpenAITwoFAAdmission)
		require.Equal(t, original.ExcelBPS, saved.ExcelBPS)
		require.Equal(t, original.ModelMappings, saved.ModelMappings)
		for i, ids := range tc.scopes {
			require.Equal(t, ids, saved.ModelBilling.Rules[i].GroupIDs)
		}
		read := httptest.NewRecorder()
		router.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/config", nil))
		require.Equal(t, 200, read.Code)
		var response struct {
			Data service.OAuthAutoConfig `json:"data"`
		}
		require.NoError(t, json.Unmarshal(read.Body.Bytes(), &response))
		require.Equal(t, saved.ModelBilling, response.Data.ModelBilling)
	}
}
