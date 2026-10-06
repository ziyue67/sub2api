package handler

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ChannelMonitorV3Handler struct {
	service  *service.ChannelMonitorV3Service
	access   channelMonitorV3GroupAccess
	settings channelMonitorModeSetter
}

type channelMonitorV3GroupAccess interface {
	GetAvailableGroups(ctx context.Context, userID int64) ([]service.Group, error)
	GetUserGroupRates(ctx context.Context, userID int64) (map[int64]float64, error)
}

type channelMonitorModeSetter interface {
	SetChannelMonitorMode(ctx context.Context, mode string) (string, error)
}

func NewChannelMonitorV3Handler(svc *service.ChannelMonitorV3Service, apiKeyService *service.APIKeyService, settingService *service.SettingService) *ChannelMonitorV3Handler {
	return &ChannelMonitorV3Handler{service: svc, access: apiKeyService, settings: settingService}
}

// viewer derives what the caller may see from the server side only: admins see
// every enabled component with diagnostics, users see public components and
// those of groups they can use, priced at their own multiplier.
func (h *ChannelMonitorV3Handler) viewer(c *gin.Context) (service.ChannelMonitorV3Viewer, bool) {
	if channelMonitorV2IsAdmin(c) {
		return service.ChannelMonitorV3Viewer{Admin: true}, true
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return service.ChannelMonitorV3Viewer{}, false
	}
	if h.access == nil {
		response.Error(c, http.StatusInternalServerError, "channel monitor group authorization unavailable")
		return service.ChannelMonitorV3Viewer{}, false
	}
	groups, err := h.access.GetAvailableGroups(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return service.ChannelMonitorV3Viewer{}, false
	}
	viewer := service.ChannelMonitorV3Viewer{AllowedGroups: make(map[int64]bool, len(groups))}
	for i := range groups {
		viewer.AllowedGroups[groups[i].ID] = true
	}
	if viewer.GroupRates, err = h.access.GetUserGroupRates(c.Request.Context(), subject.UserID); err != nil {
		response.ErrorFrom(c, err)
		return service.ChannelMonitorV3Viewer{}, false
	}
	return viewer, true
}

// Status serves the status page; end (unix seconds) pages back to an older window.
func (h *ChannelMonitorV3Handler) Status(c *gin.Context) {
	var end *time.Time
	if raw := c.Query("end"); raw != "" {
		seconds, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || seconds <= 0 {
			response.BadRequest(c, "invalid end")
			return
		}
		value := time.Unix(seconds, 0)
		end = &value
	}
	viewer, ok := h.viewer(c)
	if !ok {
		return
	}
	status, err := h.service.Status(c.Request.Context(), viewer, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *ChannelMonitorV3Handler) Incidents(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	viewer, ok := h.viewer(c)
	if !ok {
		return
	}
	result, err := h.service.Incidents(c.Request.Context(), viewer, page, pageSize)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *ChannelMonitorV3Handler) GetSettings(c *gin.Context) {
	settings, err := h.service.Settings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *ChannelMonitorV3Handler) UpdateConfig(c *gin.Context) {
	var input service.ChannelMonitorV3Config
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid channel monitor v3 config")
		return
	}
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "user not found in context")
		return
	}
	updated, err := h.service.UpdateConfig(c.Request.Context(), input, subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, updated)
}

func (h *ChannelMonitorV3Handler) CreateCategory(c *gin.Context) {
	var input service.ChannelMonitorV3CategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid category")
		return
	}
	category, err := h.service.CreateCategory(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, category)
}

func (h *ChannelMonitorV3Handler) UpdateCategory(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	var input service.ChannelMonitorV3CategoryInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid category")
		return
	}
	category, err := h.service.UpdateCategory(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, category)
}

func (h *ChannelMonitorV3Handler) DeleteCategory(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteCategory(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ChannelMonitorV3Handler) CreateComponent(c *gin.Context) {
	var input service.ChannelMonitorV3ComponentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid component")
		return
	}
	component, err := h.service.CreateComponent(c.Request.Context(), input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, component)
}

func (h *ChannelMonitorV3Handler) UpdateComponent(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	var input service.ChannelMonitorV3ComponentInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid component")
		return
	}
	component, err := h.service.UpdateComponent(c.Request.Context(), id, input)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, component)
}

func (h *ChannelMonitorV3Handler) DeleteComponent(c *gin.Context) {
	id, ok := channelMonitorV3ID(c)
	if !ok {
		return
	}
	if err := h.service.DeleteComponent(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

func (h *ChannelMonitorV3Handler) Reorder(c *gin.Context) {
	var input struct {
		CategoryIDs  []int64 `json:"category_ids"`
		ComponentIDs []int64 `json:"component_ids"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid order")
		return
	}
	if err := h.service.Reorder(c.Request.Context(), input.CategoryIDs, input.ComponentIDs); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"updated": true})
}

// SetMode switches the site between V1, V2 and V3 without a full settings save.
func (h *ChannelMonitorV3Handler) SetMode(c *gin.Context) {
	var input struct {
		Mode string `json:"mode"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "invalid mode")
		return
	}
	if h.settings == nil {
		response.Error(c, http.StatusInternalServerError, "settings unavailable")
		return
	}
	mode, err := h.settings.SetChannelMonitorMode(c.Request.Context(), input.Mode)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"mode": mode})
}

func channelMonitorV3ID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}
