package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *AccountOpsHandler) GetAutoConfig(c *gin.Context) {
	cfg, err := h.svc.GetOAuthAutoConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Automatic configuration unavailable")
		return
	}
	response.Success(c, struct {
		service.OAuthAutoConfig
		RuntimeBlocked bool `json:"runtime_blocked"`
	}{cfg, h.svc.AutoConfigBlocked()})
}
func (h *AccountOpsHandler) SaveAutoConfig(c *gin.Context) {
	var cfg service.OAuthAutoConfig
	if c.ShouldBindJSON(&cfg) != nil {
		response.BadRequest(c, "Invalid automatic configuration")
		return
	}
	saved, err := h.svc.SaveOAuthAutoConfig(c.Request.Context(), cfg)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, saved)
}
