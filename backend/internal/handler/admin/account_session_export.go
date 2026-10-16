package admin

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ExportOpenAISession returns the upstream payload only after verifying account
// identity. Cookie input is request-scoped and is not written to the account.
func (h *AccountHandler) ExportOpenAISession(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 32<<10)
	var input struct {
		Cookie string `json:"cookie"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Invalid Session export request")
		return
	}
	account, err := h.adminService.GetAccount(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	session, err := h.openaiOAuthService.ExportChatGPTSession(c.Request.Context(), account, input.Cookie)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, session)
}
