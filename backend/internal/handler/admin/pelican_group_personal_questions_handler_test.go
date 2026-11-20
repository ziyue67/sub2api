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

func TestPelicanGroupPersonalHandlerBindsSessionOwner(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{}
	cfg.JWT.Secret = "unit-test-only"
	settings := service.NewSettingService(&personalBankHTTPRepo{values: map[string]string{}}, cfg)
	_, err := settings.SavePelicanPersonalQuestions(context.Background(), 71, []service.PelicanPersonalQuestion{{Title: "Rain", Prompt: "Draw rain", Enabled: true}})
	require.NoError(t, err)
	repo := &groupTestHandlerRepo{plans: map[int64]*service.PelicanGroupTestPlan{}}
	h := NewPelicanGroupTestHandler(service.NewPelicanGroupTestService(repo, groupTestHandlerGroups{}, nil, nil, nil, nil, nil, nil, settings))
	request := func(uid int64, role, method, route, target, auth string) *httptest.ResponseRecorder {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: uid})
			c.Set(string(middleware.ContextKeyUserRole), role)
			c.Set("auth_method", auth)
		})
		handler := h.CreatePlan
		if method == http.MethodPut {
			handler = h.UpdatePlan
		}
		r.Handle(method, route, handler)
		body := `{"group_id":4,"model_id":"fixture","prompt_mode":"personal_random","prompt":"","reasoning_effort":"high","enabled":true,"personal_bank_owner_id":999,"use_my_question_bank":true}`
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		return w
	}
	for _, access := range []struct{ role, auth string }{{"observer", "jwt"}, {"user", "jwt"}, {"admin", "admin_api_key"}, {"admin", ""}} {
		for _, method := range []string{http.MethodPost, http.MethodPut} {
			route, target := "/plans", "/plans"
			if method == http.MethodPut {
				route, target = "/plans/:id", "/plans/9"
			}
			w := request(71, access.role, method, route, target, access.auth)
			require.Equal(t, http.StatusForbidden, w.Code, w.Body.String())
		}
	}
	w := request(72, "admin", http.MethodPost, "/plans", "/plans", "jwt")
	require.Equal(t, http.StatusBadRequest, w.Code, "another admin does not inherit the seeded bank")
	w = request(71, "admin", http.MethodPost, "/plans", "/plans", "jwt")
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var data struct {
		Data service.PelicanGroupTestPlan `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &data))
	require.Equal(t, int64(71), data.Data.PelicanConfig.PersonalBankOwnerID)
	require.Empty(t, data.Data.PelicanConfig.Prompt)
	require.Len(t, repo.plans, 1)
}
