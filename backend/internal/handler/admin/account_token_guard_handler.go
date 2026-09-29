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

// StartRun 接受后台巡检并立即返回。保留 Run 以兼容旧客户端。
func (h *AccountTokenGuardHandler) StartRun(c *gin.Context) {
	job, err := h.svc.StartRun(true)
	if err != nil && job == nil {
		response.Error(c, http.StatusServiceUnavailable, "巡检任务创建失败: "+err.Error())
		return
	}
	response.Accepted(c, job)
}

func (h *AccountTokenGuardHandler) Job(c *gin.Context) {
	job, ok := h.svc.Job(c.Param("id"))
	if !ok {
		response.NotFound(c, "巡检任务不存在")
		return
	}
	response.Success(c, job)
}

func (h *AccountTokenGuardHandler) Cancel(c *gin.Context) {
	job, err := h.svc.CancelRun(c.Param("id"))
	if err != nil {
		response.NotFound(c, err.Error())
		return
	}
	response.Success(c, job)
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

func (h *AccountTokenGuardHandler) StartTwoFALogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var input struct {
		service.AccountTokenGuardReloginAccount
		CredentialTarget string `json:"credential_target"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "登录凭据格式不正确")
		return
	}
	entry := input.AccountTokenGuardReloginAccount
	if err := service.ValidateOpenAITwoFALogin(entry); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	var job *service.OpenAITwoFALoginJob
	var err error
	switch input.CredentialTarget {
	case "operations":
		job, err = h.svc.StartTwoFALoginForOperations(c.Request.Context(), entry)
	case "", "guard": // Preserve the contract for older clients.
		job, err = h.svc.StartTwoFALogin(c.Request.Context(), entry)
	default:
		response.BadRequest(c, "登录凭据目标不合法")
		return
	}
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, err.Error())
		return
	}
	response.Accepted(c, job)
}

func (h *AccountTokenGuardHandler) TwoFALogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	job, ok := h.svc.TwoFALogin(c.Param("id"))
	if !ok {
		response.NotFound(c, "登录任务不存在或已过期")
		return
	}
	response.Success(c, job)
}

func (h *AccountTokenGuardHandler) DeleteTwoFALogin(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	h.svc.DeleteTwoFALogin(c.Param("id"))
	response.Success(c, gin.H{"deleted": true})
}
