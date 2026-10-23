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
	defaults := service.DefaultPrioritySchedulingConfig()
	var request struct {
		service.PrioritySchedulingConfig
		ErrorRateWeight            *float64 `json:"error_rate_weight"`
		ErrorRateWindowMinutes     *int     `json:"error_rate_window_minutes"`
		ErrorRatePriorSamples      *int     `json:"error_rate_prior_samples"`
		OAuthQuotaPriority         *bool    `json:"oauth_quota_priority"`
		OAuthQuotaThreshold        *int     `json:"oauth_quota_threshold"`
		BalanceProtocols           *bool    `json:"balance_protocols"`
		LatencyPenaltyMinutes      *int     `json:"latency_penalty_minutes"`
		LatencyPercentile          *int     `json:"latency_percentile"`
		LatencyMeasurement         *string  `json:"latency_measurement"`
		RecoveryExplorationPercent *int     `json:"recovery_exploration_percent"`
	}
	request.PrioritySchedulingConfig = defaults
	if c.ShouldBindJSON(&request) != nil {
		response.BadRequest(c, "Invalid scheduling configuration")
		return
	}
	cfg := request.PrioritySchedulingConfig
	if request.ErrorRateWeight == nil || request.ErrorRateWindowMinutes == nil || request.ErrorRatePriorSamples == nil || request.OAuthQuotaPriority == nil || request.OAuthQuotaThreshold == nil || request.BalanceProtocols == nil || request.LatencyPenaltyMinutes == nil || request.LatencyPercentile == nil || request.LatencyMeasurement == nil || request.RecoveryExplorationPercent == nil {
		current, err := h.settingService.GetPrioritySchedulingConfig(c.Request.Context())
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "Priority scheduling configuration unavailable")
			return
		}
		cfg.ErrorRateWeight = current.ErrorRateWeight
		cfg.ErrorRateWindowMinutes = current.ErrorRateWindowMinutes
		cfg.ErrorRatePriorSamples = current.ErrorRatePriorSamples
		cfg.BalanceProtocols = current.BalanceProtocols
		cfg.OAuthQuotaPriority = current.OAuthQuotaPriority
		cfg.OAuthQuotaThreshold = current.OAuthQuotaThreshold
		cfg.LatencyPenaltyMinutes = current.LatencyPenaltyMinutes
		cfg.LatencyPercentile = current.LatencyPercentile
		cfg.LatencyMeasurement = current.LatencyMeasurement
		cfg.RecoveryExplorationPercent = current.RecoveryExplorationPercent
	}
	if request.ErrorRateWeight != nil {
		cfg.ErrorRateWeight = *request.ErrorRateWeight
	}
	if request.ErrorRateWindowMinutes != nil {
		cfg.ErrorRateWindowMinutes = *request.ErrorRateWindowMinutes
	}
	if request.ErrorRatePriorSamples != nil {
		cfg.ErrorRatePriorSamples = *request.ErrorRatePriorSamples
	}
	if request.OAuthQuotaPriority != nil {
		cfg.OAuthQuotaPriority = *request.OAuthQuotaPriority
	}
	if request.OAuthQuotaThreshold != nil {
		cfg.OAuthQuotaThreshold = *request.OAuthQuotaThreshold
	}
	if request.BalanceProtocols != nil {
		cfg.BalanceProtocols = *request.BalanceProtocols
	}
	if request.LatencyPenaltyMinutes != nil {
		cfg.LatencyPenaltyMinutes = *request.LatencyPenaltyMinutes
	}

	if request.LatencyPercentile != nil {
		cfg.LatencyPercentile = *request.LatencyPercentile
	}
	if request.LatencyMeasurement != nil {
		cfg.LatencyMeasurement = *request.LatencyMeasurement
	}
	if request.RecoveryExplorationPercent != nil {
		cfg.RecoveryExplorationPercent = *request.RecoveryExplorationPercent
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
