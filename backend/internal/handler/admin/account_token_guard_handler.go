package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountTokenGuardHandler 提供智能运维 → 凭证守护的状态、配置、日志与手动操作接口。
type AccountTokenGuardHandler struct {
	svc *service.AccountTokenGuardService
}

func NewAccountTokenGuardHandler(svc *service.AccountTokenGuardService) *AccountTokenGuardHandler {
	return &AccountTokenGuardHandler{svc: svc}
}

func (h *AccountTokenGuardHandler) Status(c *gin.Context) {
	status, err := h.svc.Status(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "凭证守护状态读取失败: "+err.Error())
		return
	}
	response.Success(c, status)
}

func (h *AccountTokenGuardHandler) SaveConfig(c *gin.Context) {
	var cfg service.AccountTokenGuardConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "配置格式不正确")
		return
	}
	if err := service.ValidateAccountTokenGuardConfig(cfg); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	saved, err := h.svc.SaveConfig(c.Request.Context(), cfg)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "配置保存失败: "+err.Error())
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardHandler) Run(c *gin.Context) {
	stats, err := h.svc.RunCycle(c.Request.Context(), true)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "巡检未完成: "+err.Error())
		return
	}
	response.Success(c, stats)
}

func (h *AccountTokenGuardHandler) Events(c *gin.Context) {
	offset, err := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if err != nil || offset < 0 {
		response.BadRequest(c, "offset 不合法")
		return
	}
	limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if err != nil || limit < 1 || limit > 200 {
		response.BadRequest(c, "limit 不合法")
		return
	}
	status, err := h.svc.Status(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "日志读取失败")
		return
	}
	events := status.Events
	if offset >= len(events) {
		events = nil
	} else {
		events = events[offset:]
	}
	if len(events) > limit {
		events = events[:limit]
	}
	response.Success(c, gin.H{"items": events, "has_more": len(status.Events) > offset+len(events)})
}

func (h *AccountTokenGuardHandler) Relogin(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "账号 ID 不合法")
		return
	}
	action, err := h.svc.ReloginAccount(c.Request.Context(), accountID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "重登失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"account_id": accountID, "action": action})
}
