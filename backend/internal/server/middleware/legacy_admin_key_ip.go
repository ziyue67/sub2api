package middleware

import (
	"log/slog"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

var legacyAdminIPLogLimiter = rate.NewLimiter(1, 10)
var legacyAdminIPLogSuppressed atomic.Uint64

func enforceLegacyAdminKeyIP(c *gin.Context, settings *service.SettingService) bool {
	policy, err := settings.GetLegacyAdminKeyIPPolicy(c.Request.Context())
	code, reason, status := "ADMIN_KEY_IP_DENIED", "source_not_allowed", 403
	if err != nil {
		code, reason, status = "ADMIN_KEY_IP_POLICY_UNAVAILABLE", "policy_unavailable", 503
	} else if policy.Allows(ip.GetTrustedClientIP(c)) {
		return true
	}
	AbortWithError(c, status, code, "旧版管理密钥来源不被允许或策略不可用")
	if legacyAdminIPLogLimiter.Allow() {
		slog.Warn("legacy admin key IP rejected", "audit", true, "auth_method", "admin_api_key", "client_ip", ip.GetTrustedClientIP(c), "method", c.Request.Method, "path", c.FullPath(), "status", status, "reason", reason, "suppressed", legacyAdminIPLogSuppressed.Swap(0))
	} else {
		legacyAdminIPLogSuppressed.Add(1)
	}
	return false
}
