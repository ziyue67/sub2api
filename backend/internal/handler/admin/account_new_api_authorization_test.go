package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewAPIConfigHandlerRejectsInvalidIDAndMalformedSecretBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &AccountHandler{}
	h.SetUpstreamBillingProbeService(service.NewUpstreamBillingProbeService(nil, nil, nil))
	r := gin.New()
	r.GET("/:id/config", h.GetNewAPIConfig)
	r.PUT("/:id/config", h.SaveNewAPIConfig)
	r.POST("/:id/config/preview", h.PreviewNewAPIConfig)
	r.DELETE("/:id/config", h.DeleteNewAPIConfig)
	for _, tt := range []struct {
		method, path, body string
		code               int
	}{{"GET", "/invalid/config", "", 400}, {"PUT", "/1/config", `{"access_token":"private-test-secret","user_id":"bad"}`, 400}, {"POST", "/1/config/preview", `{"access_token":"private-test-secret","user_id":"bad"}`, 400}, {"DELETE", "/invalid/config", "", 400}} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(tt.body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		require.Equal(t, tt.code, w.Code)
		require.NotContains(t, w.Body.String(), "private-test-secret")
	}
}

var _ = http.MethodGet
