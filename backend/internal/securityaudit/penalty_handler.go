package securityaudit

import (
	"context"
	"strconv"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func ProvidePenaltyAdminHandler(prompt PromptAdminService, penalties *service.SecurityPenaltyService) *PromptAdminHandler {
	h := NewPromptAdminHandler(prompt)
	h.penalties = penalties
	return h
}

func (h *PromptAdminHandler) GetPenaltyConfig(c *gin.Context) {
	result, err := h.penalties.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *PromptAdminHandler) SavePenaltyConfig(c *gin.Context) {
	var cfg service.SecurityPenaltyConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("security_penalty_invalid_config", "处罚配置无效"))
		return
	}
	result, err := h.penalties.SaveConfig(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *PromptAdminHandler) ReleasePenalty(c *gin.Context) {
	userID, err := strconv.ParseInt(c.Param("user_id"), 10, 64)
	var req struct {
		Reason string `json:"reason"`
	}
	if err != nil || userID <= 0 || c.ShouldBindJSON(&req) != nil {
		response.ErrorFrom(c, infraerrors.BadRequest("security_penalty_invalid_release", "解除请求无效"))
		return
	}
	if err = h.penalties.Release(c.Request.Context(), userID, adminID(c), req.Reason); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"released": true})
}

func penaltyOwner(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "请先登录")
		return 0, false
	}
	return subject.UserID, true
}

func (h *PromptAdminHandler) MyPenaltySummary(c *gin.Context) {
	id, ok := penaltyOwner(c)
	if !ok {
		return
	}
	result, err := h.penalties.Summary(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *PromptAdminHandler) MyPenaltyEvents(c *gin.Context) {
	id, ok := penaltyOwner(c)
	if !ok {
		return
	}
	h.listPenaltyEvents(c, id)
}
func (h *PromptAdminHandler) AdminPenaltyEvents(c *gin.Context) {
	var id int64
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		var err error
		id, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			response.ErrorFrom(c, infraerrors.BadRequest("security_penalty_invalid_user", "用户 ID 无效"))
			return
		}
	}
	h.listPenaltyEvents(c, id)
}
func (h *PromptAdminHandler) listPenaltyEvents(c *gin.Context, id int64) {
	page, err := positiveIntQuery(c, "page", 1, 100000)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	size, err := positiveIntQuery(c, "page_size", 20, 100)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result, err := h.penalties.ListEvents(c.Request.Context(), id, page, size)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *PromptAdminHandler) MyPenaltyEvent(c *gin.Context) {
	id, ok := penaltyOwner(c)
	if !ok {
		return
	}
	h.getPenaltyEvent(c, id)
}
func (h *PromptAdminHandler) AdminPenaltyEvent(c *gin.Context) { h.getPenaltyEvent(c, 0) }
func (h *PromptAdminHandler) getPenaltyEvent(c *gin.Context, owner int64) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.ErrorFrom(c, infraerrors.BadRequest("security_penalty_invalid_event", "安全记录 ID 无效"))
		return
	}
	result, err := h.penalties.GetEvent(c.Request.Context(), owner, id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// IPAuditPenaltySummary exposes only existing user/session state. The caller
// must supply an explicit authorized user ID; no IP-to-user inference is made.
func (h *PromptAdminHandler) IPAuditPenaltySummary(ctx context.Context, id int64) (service.SecurityPenaltySummary, error) {
	if h.penalties == nil {
		return service.SecurityPenaltySummary{}, infraerrors.ServiceUnavailable("security_penalty_unavailable", "Security penalty source unavailable")
	}
	return h.penalties.Summary(ctx, id)
}

// ReleaseIPAuditPenalty uses the existing explicitly user-wide recovery action.
// It does not infer a user from an IP or modify users.status/API-key ACLs.
func (h *PromptAdminHandler) ReleaseIPAuditPenalty(ctx context.Context, userID, actorID int64, reason string) (service.SecurityPenaltySummary, error) {
	if h.penalties == nil {
		return service.SecurityPenaltySummary{}, infraerrors.ServiceUnavailable("security_penalty_unavailable", "Security penalty source unavailable")
	}
	if err := h.penalties.Release(ctx, userID, actorID, reason); err != nil {
		return service.SecurityPenaltySummary{}, err
	}
	return h.penalties.Summary(ctx, userID)
}
