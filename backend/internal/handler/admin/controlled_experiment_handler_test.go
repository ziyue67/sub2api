package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestControlledExperimentFullAdminScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, observer := range []bool{false, true} {
		router := gin.New()
		handler := NewControlledExperimentHandler(nil)
		router.Use(handler.FullAdmin)
		router.GET("/experiment", func(c *gin.Context) { c.Status(http.StatusNoContent) })
		request := httptest.NewRequest(http.MethodGet, "/experiment", nil)
		if observer {
			request = request.WithContext(service.WithObserverScope(context.Background(), []int64{1}))
		}
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		if observer {
			require.Equal(t, http.StatusForbidden, recorder.Code)
		} else {
			require.Equal(t, http.StatusNoContent, recorder.Code)
		}
	}
}
