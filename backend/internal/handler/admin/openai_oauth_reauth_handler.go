package admin

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

const openAIOAuthReauthWorkerTokenEnv = "OPENAI_REAUTH_WORKER_TOKEN"

type OpenAIOAuthReauthHandler struct {
	service *service.OpenAIOAuthReauthService
}

func NewOpenAIOAuthReauthHandler(reauthService *service.OpenAIOAuthReauthService) *OpenAIOAuthReauthHandler {
	return &OpenAIOAuthReauthHandler{service: reauthService}
}

type saveOpenAIOAuthReauthConfigRequest struct {
	LoginEmail string `json:"login_email" binding:"required"`
	OTPURL     string `json:"otp_url" binding:"required"`
}

func parseReauthAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

func (h *OpenAIOAuthReauthHandler) SaveConfig(c *gin.Context) {
	accountID, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	var req saveOpenAIOAuthReauthConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	config, err := h.service.SaveConfig(c.Request.Context(), accountID, req.LoginEmail, req.OTPURL)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, config)
}

func (h *OpenAIOAuthReauthHandler) GetStatus(c *gin.Context) {
	accountID, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	status, err := h.service.GetStatus(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *OpenAIOAuthReauthHandler) CreateTask(c *gin.Context) {
	accountID, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	task, err := h.service.CreateTask(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

type reauthWorkerRequest struct {
	WorkerID string `json:"worker_id"`
}

type reauthWorkerProgressRequest struct {
	WorkerID string `json:"worker_id"`
	Stage    string `json:"stage" binding:"required"`
}

type reauthWorkerCallbackRequest struct {
	WorkerID    string `json:"worker_id"`
	CallbackURL string `json:"callback_url" binding:"required"`
}

type reauthWorkerCredentialsRequest struct {
	WorkerID    string         `json:"worker_id"`
	Credentials map[string]any `json:"credentials" binding:"required"`
	Extra       map[string]any `json:"extra"`
}

type reauthWorkerFailureRequest struct {
	WorkerID string `json:"worker_id"`
	Reason   string `json:"reason"`
}

func (h *OpenAIOAuthReauthHandler) requireWorker(c *gin.Context) bool {
	expected := strings.TrimSpace(os.Getenv(openAIOAuthReauthWorkerTokenEnv))
	if len(expected) < 32 {
		response.Error(c, http.StatusServiceUnavailable, "OpenAI re-auth worker is not configured")
		c.Abort()
		return false
	}
	provided := strings.TrimSpace(c.GetHeader("X-OpenAI-Reauth-Worker-Token"))
	if provided == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		response.Unauthorized(c, "invalid OpenAI re-auth worker token")
		c.Abort()
		return false
	}
	return true
}

// Claim is intentionally outside the admin-authenticated route group. It is
// protected by a separate high-entropy worker token and only returns a task
// when one is queued.
func (h *OpenAIOAuthReauthHandler) Claim(c *gin.Context) {
	if !h.requireWorker(c) {
		return
	}
	var req reauthWorkerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	claim, err := h.service.ClaimTask(c.Request.Context(), req.WorkerID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, claim)
}

func (h *OpenAIOAuthReauthHandler) Progress(c *gin.Context) {
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	var req reauthWorkerProgressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	if err := h.service.UpdateStage(c.Request.Context(), taskID, req.WorkerID, req.Stage); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"accepted": true})
}

func (h *OpenAIOAuthReauthHandler) Callback(c *gin.Context) {
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	var req reauthWorkerCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	task, err := h.service.SubmitCallback(c.Request.Context(), taskID, req.WorkerID, req.CallbackURL)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}

func (h *OpenAIOAuthReauthHandler) Credentials(c *gin.Context) {
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	var req reauthWorkerCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	task, err := h.service.SubmitCredentials(c.Request.Context(), taskID, req.WorkerID, req.Credentials, req.Extra)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}

func (h *OpenAIOAuthReauthHandler) Fail(c *gin.Context) {
	if !h.requireWorker(c) {
		return
	}
	taskID, err := strconv.ParseInt(strings.TrimSpace(c.Param("task_id")), 10, 64)
	if err != nil || taskID <= 0 {
		response.BadRequest(c, "Invalid task ID")
		return
	}
	var req reauthWorkerFailureRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid worker request")
		return
	}
	if err := h.service.FailTask(c.Request.Context(), taskID, req.WorkerID, req.Reason); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"accepted": true})
}
