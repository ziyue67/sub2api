package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type AccountTokenGuardV2Handler struct {
	service *service.AccountTokenGuardV2Service
}

func NewAccountTokenGuardV2Handler(guardService *service.AccountTokenGuardV2Service) *AccountTokenGuardV2Handler {
	return &AccountTokenGuardV2Handler{service: guardService}
}

type accountTokenGuardV2SaveRequest struct {
	AccountID          int64  `json:"account_id"`
	LoginEmail         string `json:"login_email" binding:"required"`
	CredentialMode     string `json:"credential_mode" binding:"required"`
	Engine             string `json:"engine"`
	ProxySource        string `json:"proxy_source"`
	ProxyID            *int64 `json:"proxy_id"`
	Password           string `json:"password"`
	TOTPSecret         string `json:"totp_secret"`
	OTPURL             string `json:"otp_url"`
	ClearPassword      bool   `json:"clear_password"`
	ClearTOTP          bool   `json:"clear_totp"`
	Enabled            *bool  `json:"enabled"`
	AutoReloginEnabled *bool  `json:"auto_relogin_enabled"`
}

func (r accountTokenGuardV2SaveRequest) input() service.AccountTokenGuardV2AccountInput {
	enabled := true
	autoRelogin := true
	if r.Enabled != nil {
		enabled = *r.Enabled
	}
	if r.AutoReloginEnabled != nil {
		autoRelogin = *r.AutoReloginEnabled
	}
	return service.AccountTokenGuardV2AccountInput{
		PreserveEnabled: r.Enabled == nil, PreserveAutoRelogin: r.AutoReloginEnabled == nil,
		LoginEmail: r.LoginEmail, CredentialMode: r.CredentialMode,
		Engine:      r.Engine,
		ProxySource: r.ProxySource, ProxyID: r.ProxyID,
		Password: r.Password, TOTPSecret: r.TOTPSecret, OTPURL: r.OTPURL,
		ClearPassword: r.ClearPassword, ClearTOTP: r.ClearTOTP,
		Enabled: enabled, AutoReloginEnabled: autoRelogin,
	}
}

func parseTokenGuardV2AccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

func (h *AccountTokenGuardV2Handler) List(c *gin.Context) {
	rules, err := h.service.GetRules(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	accounts, err := h.service.ListAccounts(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	runtimeSettings, err := h.service.GetRuntimeSettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{
		"runtime_settings":         runtimeSettings,
		"accounts":                 accounts,
		"worker":                   h.service.WorkerStatus(),
		"probe_interval_seconds":   rules.ProbeIntervalSeconds,
		"retry_interval_seconds":   rules.RetryIntervalSeconds,
		"relogin_cooldown_seconds": rules.ReloginCooldownSeconds,
		"fail_streak_threshold":    rules.FailStreakThreshold,
	})
}

func (h *AccountTokenGuardV2Handler) SaveRuntime(c *gin.Context) {
	var cfg service.OpenAIOAuthReauthRuntimeSettings
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid re-login settings")
		return
	}
	saved, err := h.service.SaveRuntimeSettings(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardV2Handler) UpdateSwitches(c *gin.Context) {
	id, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	var req struct {
		Enabled            *bool `json:"enabled"`
		AutoReloginEnabled *bool `json:"auto_relogin_enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid automation switches")
		return
	}
	saved, err := h.service.UpdateSwitches(c.Request.Context(), id, req.Enabled, req.AutoReloginEnabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardV2Handler) SaveRules(c *gin.Context) {
	var rules service.AccountTokenGuardV2Rules
	if err := c.ShouldBindJSON(&rules); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	saved, err := h.service.SaveRules(c.Request.Context(), rules)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}

func (h *AccountTokenGuardV2Handler) Create(c *gin.Context) {
	var req accountTokenGuardV2SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.AccountID <= 0 {
		response.BadRequest(c, "Invalid request")
		return
	}
	account, err := h.service.SaveAccount(c.Request.Context(), req.AccountID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, account)
}

func (h *AccountTokenGuardV2Handler) Update(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	var req accountTokenGuardV2SaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	account, err := h.service.SaveAccount(c.Request.Context(), accountID, req.input())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

func (h *AccountTokenGuardV2Handler) Delete(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	if err := h.service.RemoveAccount(c.Request.Context(), accountID); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *AccountTokenGuardV2Handler) Probe(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	account, err := h.service.ProbeNow(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, account)
}

func (h *AccountTokenGuardV2Handler) Relogin(c *gin.Context) {
	accountID, ok := parseTokenGuardV2AccountID(c)
	if !ok {
		return
	}
	task, err := h.service.ReloginNow(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}
