package middleware

import (
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRegistrationIPLimitDoesNotDisableAuthProtection(t *testing.T) {
	for _, tc := range []struct {
		name      string
		policy    func(*gin.Context) bool
		wantQuota bool
	}{
		{"default", nil, true},
		{"enabled", func(*gin.Context) bool { return true }, true},
		{"disabled", func(*gin.Context) bool { return false }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, client := newAbuseTestRedis(t)
			router := gin.New()
			require.NoError(t, router.SetTrustedProxies(nil))
			router.Use(AuthAbuseGuardWithOptions(client, AuthAbuseGuardOptions{RegistrationIPLimitEnabled: tc.policy}))
			for _, path := range []string{"/auth/register", "/auth/oauth/google/callback", "/auth/oauth/google/complete-registration", "/auth/oauth/github/complete-registration", "/auth/oauth/pending/create-account"} {
				router.POST(path, func(c *gin.Context) {
					_, enabled := authguard.RegistrationIP(c.Request.Context())
					require.Equal(t, tc.wantQuota, enabled, c.FullPath())
					c.Status(http.StatusNoContent)
				})
				for i := 0; i < 3; i++ {
					require.Equal(t, http.StatusNoContent, abuseRequest(router, "192.0.2.30:1234", path).Code)
				}
			}
		})
	}
	_, client := newAbuseTestRedis(t)
	router := gin.New()
	require.NoError(t, router.SetTrustedProxies(nil))
	router.Use(AuthAbuseGuardWithOptions(client, AuthAbuseGuardOptions{RegistrationIPLimitEnabled: func(*gin.Context) bool { return false }}))
	router.POST("/auth/login", func(c *gin.Context) {
		response.ErrorWithDetails(c, http.StatusUnauthorized, "test credentials", "INVALID_CREDENTIALS", nil)
	})
	for i := 0; i < authFailureLimit; i++ {
		require.Equal(t, http.StatusUnauthorized, abuseRequest(router, "192.0.2.31:1234", "/auth/login").Code)
	}
	require.Equal(t, http.StatusTooManyRequests, abuseRequest(router, "192.0.2.31:1234", "/auth/login").Code)
}
