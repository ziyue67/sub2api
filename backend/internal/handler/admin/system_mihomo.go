package admin

import (
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
	"net/http"
)

func (h *SystemHandler) GetMihomo(c *gin.Context) { response.Success(c, h.kernel.Status()) }
func (h *SystemHandler) ManageMihomo(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
	var req struct {
		Name           string                `json:"name"`
		Action         string                `json:"action"`
		Subscriptions  []string              `json:"subscriptions"`
		DynamicProxies []string              `json:"dynamic_proxies"`
		Append         bool                  `json:"append"`
		CountryFilter  *mihomo.CountryFilter `json:"country_filter"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.Error(c, http.StatusBadRequest, "Invalid kernel request")
		return
	}
	if err := h.kernel.SubmitSourceManagement(req.Action, req.Subscriptions, req.DynamicProxies, req.Append, req.Name, req.CountryFilter); err != nil {
		response.Error(c, http.StatusConflict, err.Error())
		return
	}
	response.Success(c, h.kernel.Status())
}

// SetMihomoDownloadMode configures only subscription retrieval. Both the old
// settings screen and IP management use this stable backend contract.
func (h *SystemHandler) SetMihomoDownloadMode(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	var req struct {
		Mode *mihomo.SubscriptionDownloadMode `json:"mode"`
	}
	if c.ShouldBindJSON(&req) != nil || req.Mode == nil {
		response.Error(c, http.StatusBadRequest, "download mode is required")
		return
	}
	if err := h.kernel.SetSubscriptionDownloadMode(c.Request.Context(), *req.Mode); err != nil {
		status := http.StatusConflict
		if errors.Is(err, mihomo.ErrSubscriptionDownloadMode) {
			status = http.StatusBadRequest
		}
		response.Error(c, status, err.Error())
		return
	}
	response.Success(c, h.kernel.Status())
}
