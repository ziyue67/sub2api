package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

func (h *OpenAIOAuthReauthHandler) SIWCStatus(c *gin.Context) {
	id, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	status, err := h.service.SIWCStatus(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
func (h *OpenAIOAuthReauthHandler) BeginSIWC(c *gin.Context) {
	id, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	auth, err := h.service.BeginSIWCAuthorization(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, auth)
}
func (h *OpenAIOAuthReauthHandler) CompleteSIWC(c *gin.Context) {
	id, ok := parseReauthAccountID(c)
	if !ok {
		return
	}
	var input struct {
		SessionID   string `json:"session_id" binding:"required"`
		CallbackURL string `json:"callback_url" binding:"required"`
	}
	if c.ShouldBindJSON(&input) != nil {
		response.BadRequest(c, "Session and callback URL are required")
		return
	}
	if err := h.service.CompleteSIWCAuthorization(c.Request.Context(), id, input.SessionID, input.CallbackURL, nil); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"bound": true})
}
