package middleware

import (
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Runs inside both API-key authentication paths, before group policy, balance,
// subscription and account admission. Panel/JWT login is unaffected.
func applySecurityPenalty(c *gin.Context, keys *service.APIKeyService, key *service.APIKey) (*service.APIKey, error) {
	cfg, err := keys.SecurityPenaltyConfig(c.Request.Context())
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("security_penalty_unavailable", "请求权限检查暂时不可用")
	}
	if key == nil || !cfg.Applies(key.UserID) || cfg.ObserveOnly {
		return key, nil
	}
	// Existing admin access must never be disabled by automated punishment.
	if key.User != nil && key.User.IsAdmin() {
		return key, nil
	}
	var body []byte
	if c.Request.Method == http.MethodPost || c.Request.Method == http.MethodPut || c.Request.Method == http.MethodPatch {
		contentType := strings.ToLower(c.GetHeader("Content-Type"))
		if contentType == "" || strings.Contains(contentType, "json") {
			var ok bool
			body, ok = readAdmissionRequestBody(c)
			if !ok {
				return nil, nil
			}
		}
	}
	identity := service.ResolveCyberSessionIdentity(key.ID, c, body)
	admission, err := keys.SecurityPenaltyAdmission(c.Request.Context(), key.UserID, identity.BlockKey)
	if err != nil {
		return nil, infraerrors.ServiceUnavailable("security_penalty_unavailable", "请求权限检查暂时不可用")
	}
	if admission.Blocked {
		return nil, infraerrors.Forbidden("security_penalty_blocked", "请求权限已受限，请登录个人资料查看安全记录")
	}
	if admission.TargetGroupID == nil {
		return key, nil
	}
	original := key.GroupID
	if original != nil {
		c.Set("security_penalty.original_group_id", *original)
	}
	if key.Group != nil {
		c.Set("security_penalty.original_group_name", key.Group.Name)
	}
	target, err := keys.PinAPIKeyGroup(c.Request.Context(), key, *admission.TargetGroupID)
	if err != nil {
		return nil, infraerrors.Forbidden("security_penalty_target_unavailable", "受限分组未绑定到此 API Key、未授权或不可用，请联系管理员")
	}
	if target.Group == nil || key.Group == nil || target.Group.Platform != key.Group.Platform {
		return nil, infraerrors.Forbidden("security_penalty_target_platform_mismatch", "受限分组与原分组的平台必须一致")
	}
	// Normal auth below validates target subscription, model and billing limits.
	return target, nil
}
