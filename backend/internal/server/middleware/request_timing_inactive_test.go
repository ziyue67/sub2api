//go:build unit

package middleware

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestRequestTimingInactiveCredentialIsObservationOnly(t *testing.T) {
	cfg := &config.Config{RunMode: config.RunModeSimple}
	group := int64(8)
	repo := &stubApiKeyRepo{getByKey: func(_ context.Context, key string) (*service.APIKey, error) {
		if key != "known" {
			return nil, service.ErrAPIKeyNotFound
		}
		return &service.APIKey{ID: 7, UserID: 9, GroupID: &group, Status: service.StatusActive, User: &service.User{ID: 9, Status: "disabled", Username: "disabled-example"}}, nil
	}}
	svc := service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, cfg)
	for _, key := range []string{"known", "unknown"} {
		recorder := &connectionRecorderTest{}
		r := gin.New()
		require.NoError(t, r.SetTrustedProxies(nil))
		upstream := 0
		r.Use(RequestTiming(recorder), func(c *gin.Context) {
			c.Next()
			_, authenticated := GetAPIKeyFromContext(c)
			require.False(t, authenticated)
			_, user := GetAuthSubjectFromContext(c)
			require.False(t, user)
			require.Nil(t, c.Request.Context().Value(ctxkey.UserID))
		})
		r.POST("/v1/responses", gin.HandlerFunc(NewAPIKeyAuthMiddleware(svc, nil, cfg)), func(c *gin.Context) { upstream++; c.Status(200) })
		req := httptest.NewRequest("POST", "/v1/responses", nil)
		req.RemoteAddr = "8.8.8.8:1234"
		req.Header.Set("x-api-key", key)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 401, w.Code)
		require.Zero(t, upstream)
		require.Len(t, recorder.records, 1)
		got := recorder.records[0]
		if key == "known" {
			require.EqualValues(t, 9, got.UserID)
			require.EqualValues(t, 7, got.APIKeyID)
			require.EqualValues(t, 8, got.GroupID)
			require.Equal(t, "USER_INACTIVE", got.Detail.AuthRejection)
			require.Equal(t, "recognized_credential", got.Detail.IdentitySource)
			require.Empty(t, got.Detail.Attempts)
		} else {
			require.Zero(t, got.UserID)
			require.Zero(t, got.APIKeyID)
		}
	}
}

// Historical/collection-limited traces may have no owner even when the server
// already identified USER_INACTIVE. Neither absence nor HTTP 401 is evidence
// for an anonymous credential failure; do not infer an owner or fill history.
func TestRequestTimingUnattributedUserInactiveNeverTriggersAnonymousBan(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1, ContextTimeoutEnabled: true})
	t.Cleanup(func() { _ = client.Close() })
	settings := iprisk.DefaultConfig()
	settings.AnonymousAPI.Enabled = true
	settings.AnonymousAPI.Threshold = 2
	guard := iprisk.New(&anonymousSettingsStore{settings}, client, nil, false)
	t.Cleanup(guard.Stop)
	recorder := &connectionRecorderTest{}
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(guard.Middleware(), RequestTiming(recorder))
	upstream := 0
	router.POST("/v1/responses", func(c *gin.Context) {
		require.Nil(t, c.Request.Context().Value(ctxkey.UserID))
		MarkIngressRejected(c, IngressRejectUserInactive)
		AbortWithError(c, 401, "USER_INACTIVE", "User account is not active")
	}, func(c *gin.Context) { upstream++; c.Status(200) })
	for i := 0; i < 13; i++ {
		request := httptest.NewRequest("POST", "/v1/responses", nil)
		request.RemoteAddr = "8.8.8.8:1234"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		require.Equal(t, 401, w.Code)
	}
	require.Zero(t, upstream)
	require.Len(t, recorder.records, 13)
	for _, record := range recorder.records {
		require.Zero(t, record.UserID)
		require.Zero(t, record.APIKeyID)
		require.Equal(t, "USER_INACTIVE", record.Detail.AuthRejection)
		require.Empty(t, record.Detail.IdentitySource)
		require.Empty(t, record.Detail.Attempts)
	}
	bans, err := guard.AnonymousBans(context.Background())
	require.NoError(t, err)
	require.Empty(t, bans)
	ban, err := guard.AnonymousFailure(context.Background(), "8.8.8.8", "unknown")
	require.NoError(t, err)
	require.Nil(t, ban, "the 13 USER_INACTIVE observations must not consume even one anonymous failure")
}
