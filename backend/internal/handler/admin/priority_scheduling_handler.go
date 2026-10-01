package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *SettingHandler) GetPriorityScheduling(c *gin.Context) {
	cfg, err := h.settingService.GetPrioritySchedulingConfig(c.Request.Context())
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Priority scheduling configuration unavailable")
		return
	}
	response.Success(c, cfg)
}
func (h *SettingHandler) SavePriorityScheduling(c *gin.Context) {
	var request struct {
		service.PrioritySchedulingConfig
		BalanceProtocols *bool `json:"balance_protocols"`
	}
	if c.ShouldBindJSON(&request) != nil {
		response.BadRequest(c, "Invalid scheduling configuration")
		return
	}
	cfg := request.PrioritySchedulingConfig
	if request.BalanceProtocols != nil {
		cfg.BalanceProtocols = *request.BalanceProtocols
	} else {
		current, err := h.settingService.GetPrioritySchedulingConfig(c.Request.Context())
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "Priority scheduling configuration unavailable")
			return
		}
		cfg.BalanceProtocols = current.BalanceProtocols
	}
	if err := service.ValidatePrioritySchedulingConfig(cfg); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.settingService.SavePrioritySchedulingConfig(c.Request.Context(), cfg); err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Could not save scheduling configuration")
		return
	}
	response.Success(c, cfg)
}
func (h *AccountHandler) PrioritySchedulingSnapshot(c *gin.Context) {
	response.Success(c, gin.H{"snapshot": h.openAIGatewayService.PrioritySchedulingSnapshot()})
}
