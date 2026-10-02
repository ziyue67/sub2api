package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCORSPublicPelicanReadAndConditionalPreflight(t *testing.T) {
	for _, path := range []string{"/api/v1/public/pelican-showcase", "/api/v1/public/pelican-showcase/items/7"} {
		for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
			router := gin.New()
			router.Use(CORS(config.CORSConfig{AllowedOrigins: []string{"https://panel.example"}, AllowCredentials: true}))
			router.Any(path, func(c *gin.Context) { c.Status(http.StatusOK) })
			request := httptest.NewRequest(method, path, nil)
			request.Header.Set("Origin", "https://downstream.example")
			request.Header.Set("Access-Control-Request-Method", "GET")
			request.Header.Set("Access-Control-Request-Headers", "authorization, if-none-match")
			w := httptest.NewRecorder()
			router.ServeHTTP(w, request)
			if method == http.MethodOptions {
				require.Equal(t, http.StatusNoContent, w.Code)
			} else {
				require.Equal(t, http.StatusOK, w.Code)
			}
			require.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
			require.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
			require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "If-None-Match")
			require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Authorization")
			require.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-API-Key")
			require.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "ETag")
			require.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "Retry-After")
		}
	}
}

func TestCORSPublicPelicanExceptionIsNarrow(t *testing.T) {
	for _, tc := range []struct{ path, method string }{
		{"/api/v1/pelican-showcase", http.MethodGet},
		{"/api/v1/admin/pelican-showcase/settings", http.MethodGet},
		{"/api/v1/public/pelican-showcase-private", http.MethodGet},
		{"/api/v1/public/pelican-showcase/items/7/secret", http.MethodGet},
		{"/api/v1/public/pelican-showcase/items/0", http.MethodGet},
		{"/api/v1/public/pelican-showcase", http.MethodDelete},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodOptions, tc.path, nil)
		c.Request.Header.Set("Origin", "https://downstream.example")
		c.Request.Header.Set("Access-Control-Request-Method", tc.method)
		CORS(config.CORSConfig{})(c)
		require.Equal(t, http.StatusForbidden, w.Code, tc.path)
		require.Empty(t, w.Header().Get("Access-Control-Allow-Origin"), tc.path)
	}
}
