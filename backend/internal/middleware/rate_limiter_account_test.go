package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestTKSecurityAccountRateLimitCountsNetFailuresAcrossIPs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := NewRateLimiter(rdb)
	router := gin.New()
	router.POST("/login", limiter.LimitByAccount("auth-login", "email", 2, time.Minute, RateLimitOptions{FailureMode: RateLimitFailClose}), func(c *gin.Context) {
		raw, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Contains(t, string(raw), `"password"`)
		if strings.Contains(string(raw), `"password":"correct"`) {
			c.JSON(http.StatusOK, gin.H{"ok": true})
			return
		}
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid"})
	})

	request := func(email, password, remote string) *httptest.ResponseRecorder {
		body := `{"email":"` + email + `","password":"` + password + `"}`
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	require.Equal(t, http.StatusUnauthorized, request(" Victim@Example.com ", "wrong", "192.0.2.1:1").Code)
	require.Equal(t, http.StatusUnauthorized, request("victim@example.com", "wrong", "198.51.100.2:2").Code)
	require.Equal(t, http.StatusTooManyRequests, request("VICTIM@example.com", "wrong", "203.0.113.3:3").Code)
	require.Equal(t, http.StatusUnauthorized, request("other@example.com", "wrong", "203.0.113.3:3").Code)

	keys := mr.Keys()
	require.Len(t, keys, 2)
	for _, key := range keys {
		require.NotContains(t, key, "victim@example.com")
		require.NotContains(t, key, "other@example.com")
	}

	require.NoError(t, rdb.FlushDB(context.Background()).Err())
	require.Equal(t, http.StatusOK, request("victim@example.com", "correct", "192.0.2.1:1").Code)
	require.Empty(t, mr.Keys(), "successful authentication must refund its own counter")
}

func TestTKSecurityAccountRateLimitSkipsUnusableIdentityAndHonorsDisable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = rdb.Close() })
	limiter := NewRateLimiter(rdb)
	enabled := false
	router := gin.New()
	router.POST("/login", limiter.LimitByAccount("auth-login", "email", 1, time.Minute, RateLimitOptions{
		FailureMode: RateLimitFailClose,
		Enabled:     func(*gin.Context) bool { return enabled },
	}), func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	for _, body := range []string{`{"email":"victim@example.com"}`, `{"email":"victim@example.com"}`, `{bad json`, `{}`} {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	require.Empty(t, mr.Keys())

	enabled = true
	for _, body := range []string{`{bad json`, `{}`, `{"email":123}`} {
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(body))
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	}
	require.Empty(t, mr.Keys())
}
