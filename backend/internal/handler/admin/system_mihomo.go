package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// mihomoNodeChecker runs the static proxy list's connection test and quality
// check through a single Mihomo node.
type mihomoNodeChecker interface {
	TestMihomoNode(ctx context.Context, kernel service.MihomoNodeProber, name string) (*service.ProxyTestResult, error)
	CheckMihomoNodeQuality(ctx context.Context, kernel service.MihomoNodeProber, name string) (*service.ProxyQualityCheckResult, error)
	CreateMihomoNodeProxies(ctx context.Context, kernel service.MihomoStaticNodeBinder, names []string) (*service.MihomoNodeProxyImportResult, error)
}

// SetMihomoNodeChecker enables per-node connection tests and quality checks.
func (h *SystemHandler) SetMihomoNodeChecker(checker mihomoNodeChecker) { h.nodeChecker = checker }

// TestMihomoNode tests one subscription or dynamic node without changing its
// state. POST /api/v1/admin/system/mihomo/nodes/:name/test
func (h *SystemHandler) TestMihomoNode(c *gin.Context) {
	if h.nodeChecker == nil {
		response.Error(c, http.StatusServiceUnavailable, "node checks are unavailable")
		return
	}
	result, err := h.nodeChecker.TestMihomoNode(c.Request.Context(), h.kernel, c.Param("name"))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

// CheckMihomoNodeQuality checks one node against common AI targets without
// changing its state. POST /api/v1/admin/system/mihomo/nodes/:name/quality-check
func (h *SystemHandler) CheckMihomoNodeQuality(c *gin.Context) {
	if h.nodeChecker == nil {
		response.Error(c, http.StatusServiceUnavailable, "node checks are unavailable")
		return
	}
	result, err := h.nodeChecker.CheckMihomoNodeQuality(c.Request.Context(), h.kernel, c.Param("name"))
	if response.ErrorFrom(c, err) {
		return
	}
	response.Success(c, result)
}

// AddMihomoNodesAsStaticProxies exposes selected subscription or dynamic nodes
// as fixed local exits in IP Management's ordinary static proxy list.
func (h *SystemHandler) AddMihomoNodesAsStaticProxies(c *gin.Context) {
	if h.nodeChecker == nil {
		response.Error(c, http.StatusServiceUnavailable, "node proxy creation is unavailable")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
	var req struct {
		Names []string `json:"names" binding:"required,min=1,max=100"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.Error(c, http.StatusBadRequest, "select between 1 and 100 nodes")
		return
	}
	result, err := h.nodeChecker.CreateMihomoNodeProxies(c.Request.Context(), h.kernel, req.Names)
	if response.ErrorFrom(c, err) {
		return
	}
	created := make([]*dto.AdminProxy, 0, len(result.Created))
	for i := range result.Created {
		created = append(created, dto.ProxyFromServiceAdmin(&result.Created[i]))
	}
	existing := make([]*dto.AdminProxy, 0, len(result.Existing))
	for i := range result.Existing {
		existing = append(existing, dto.ProxyFromServiceAdmin(&result.Existing[i]))
	}
	response.Success(c, gin.H{"created": created, "existing": existing, "failed": result.Failed})
}

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
