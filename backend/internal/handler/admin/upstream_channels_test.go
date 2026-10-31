package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUpstreamChannelEntityObserverCannotBindThroughAccountCreation(t *testing.T) {
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithObserverScope(c.Request.Context(), []int64{1}))
	})
	r.POST("/accounts", (&AccountHandler{}).Create)
	req := httptest.NewRequest("POST", "/accounts", strings.NewReader(`{"name":"test","platform":"anthropic","type":"apikey","credentials":{},"upstream_channel_id":1,"confirm_upstream_channel_binding":true}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	require.Equal(t, 403, w.Code)
	require.Contains(t, w.Body.String(), "OBSERVER_SCOPE_FORBIDDEN")
}
