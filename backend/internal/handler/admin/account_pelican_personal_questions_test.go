//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type personalBankHTTPRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *personalBankHTTPRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *personalBankHTTPRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func TestPelicanPersonalQuestionEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.JWT.Secret = "unit-test-only"
	h := &AccountHandler{}
	h.SetPelicanPersonalSettings(service.NewSettingService(&personalBankHTTPRepo{values: map[string]string{}}, cfg))

	request := func(id int64, role, authMethod, method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: id})
			c.Set(string(middleware.ContextKeyUserRole), role)
			c.Set("auth_method", authMethod)
		})
		r.GET("/bank", h.GetPelicanPersonalQuestions)
		r.PUT("/bank", h.SavePelicanPersonalQuestions)
		r.GET("/draw", h.PreparePelicanPersonalQuestion)
		r.POST("/accounts/:id/pelican-test", h.PelicanTest)
		out := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(out, req)
		return out
	}

	body := `{"user_id":22,"items":[{"title":"Rain","prompt":"Draw rain","enabled":true}]}`
	for _, restricted := range []struct{ role, method string }{
		{service.RoleObserver, service.AuditAuthMethodJWT},
		{service.RoleUser, service.AuditAuthMethodJWT},
		{service.RoleAdmin, "admin_api_key"},
	} {
		for _, endpoint := range []struct{ method, path string }{{"GET", "/bank"}, {"PUT", "/bank"}, {"GET", "/draw"}} {
			out := request(11, restricted.role, restricted.method, endpoint.method, endpoint.path, body)
			require.Equal(t, http.StatusForbidden, out.Code)
		}
	}
	save := request(11, service.RoleAdmin, service.AuditAuthMethodJWT, "PUT", "/bank", body)
	require.Equal(t, http.StatusOK, save.Code, save.Body.String())
	require.Equal(t, "private, no-store", save.Header().Get("Cache-Control"))
	other := request(22, service.RoleAdmin, service.AuditAuthMethodJWT, "GET", "/bank", "")
	require.Equal(t, http.StatusOK, other.Code)
	require.Contains(t, other.Body.String(), `"data":[]`)
	empty := request(22, service.RoleAdmin, service.AuditAuthMethodJWT, "GET", "/draw", "")
	require.Equal(t, http.StatusBadRequest, empty.Code)
	require.Contains(t, empty.Body.String(), `"reason":"PELICAN_PERSONAL_EMPTY"`)
	draw := request(11, service.RoleAdmin, service.AuditAuthMethodJWT, "GET", "/draw", "")
	require.Equal(t, http.StatusOK, draw.Code, draw.Body.String())
	var envelope struct {
		Data service.PelicanTestConfig `json:"data"`
	}
	require.NoError(t, json.Unmarshal(draw.Body.Bytes(), &envelope))
	require.NotNil(t, envelope.Data.PelicanQuestion)
	require.Equal(t, "Draw rain", envelope.Data.PelicanQuestion.Prompt)
	require.NotEmpty(t, envelope.Data.PelicanQuestion.Proof)
	testRequest, err := json.Marshal(map[string]any{
		"model_id": "fixture-model", "prompt_mode": "personal_random", "pelican_question": envelope.Data.PelicanQuestion,
	})
	require.NoError(t, err)
	denied := request(22, service.RoleAdmin, service.AuditAuthMethodJWT, "POST", "/accounts/1/pelican-test", string(testRequest))
	require.Equal(t, http.StatusBadRequest, denied.Code)
	require.Contains(t, denied.Body.String(), `"reason":"PELICAN_PERSONAL_ROUND_INVALID"`)
}
