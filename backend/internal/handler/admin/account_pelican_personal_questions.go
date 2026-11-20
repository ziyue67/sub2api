package admin

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) SetPelicanPersonalSettings(settings *service.SettingService) {
	h.pelicanPersonalSettings = settings
}

func (h *AccountHandler) pelicanPersonalOwner(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	role, _ := middleware.GetUserRoleFromContext(c)
	if !ok || subject.UserID <= 0 || role != service.RoleAdmin || c.GetString("auth_method") != service.AuditAuthMethodJWT {
		response.Error(c, http.StatusForbidden, "An administrator session is required")
		return 0, false
	}
	if h.pelicanPersonalSettings == nil {
		response.Error(c, http.StatusServiceUnavailable, "Personal SVG question bank is unavailable")
		return 0, false
	}
	c.Header("Cache-Control", "private, no-store")
	return subject.UserID, true
}

func (h *AccountHandler) GetPelicanPersonalQuestions(c *gin.Context) {
	owner, ok := h.pelicanPersonalOwner(c)
	if !ok {
		return
	}
	items, err := h.pelicanPersonalSettings.GetPelicanPersonalQuestions(c.Request.Context(), owner)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *AccountHandler) SavePelicanPersonalQuestions(c *gin.Context) {
	owner, ok := h.pelicanPersonalOwner(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 2<<20)
	var req struct {
		Items []service.PelicanPersonalQuestion `json:"items"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.Error(c, http.StatusRequestEntityTooLarge, "Personal SVG question bank is too large")
			return
		}
		response.BadRequest(c, "Invalid personal SVG question bank")
		return
	}
	items, err := h.pelicanPersonalSettings.SavePelicanPersonalQuestions(c.Request.Context(), owner, req.Items)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, items)
}

func (h *AccountHandler) PreparePelicanPersonalQuestion(c *gin.Context) {
	owner, ok := h.pelicanPersonalOwner(c)
	if !ok {
		return
	}
	cfg, err := h.pelicanPersonalSettings.NewPelicanPersonalRound(c.Request.Context(), owner, c.Query("previous_id"))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, cfg)
}
