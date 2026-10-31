package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func enforceAccessSession(c *gin.Context, auth *service.AuthService, claims *service.JWTClaims) bool {
	revoked, err := auth.IsAccessSessionRevoked(c.Request.Context(), claims)
	if err != nil {
		AbortWithError(c, 503, "SESSION_CHECK_UNAVAILABLE", "Session verification unavailable")
		return false
	}
	if revoked {
		AbortWithError(c, 401, "TOKEN_REVOKED", "Session has been revoked")
		return false
	}
	return true
}
