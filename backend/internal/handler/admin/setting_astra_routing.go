package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"strings"
)

func (h *SettingHandler) GetAstraRouting(c *gin.Context) {
	value, err := h.settingService.GetAstraRouting(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, value)
}
func (h *SettingHandler) UpdateAstraRouting(c *gin.Context) {
	var value config.AstraRoutingSettings
	if err := c.ShouldBindJSON(&value); err != nil {
		response.BadRequest(c, "Invalid Astra routing settings")
		return
	}
	var resolveErr error
	value, resolveErr = config.ResolveAstraDependencies(value)
	if resolveErr != nil {
		response.BadRequest(c, resolveErr.Error())
		return
	}
	saved, err := h.settingService.SetAstraRouting(c.Request.Context(), value)
	if err != nil {
		if strings.HasPrefix(err.Error(), "astra_") {
			response.BadRequest(c, err.Error())
		} else {
			response.ErrorFrom(c, err)
		}
		return
	}
	response.Success(c, saved)
}
