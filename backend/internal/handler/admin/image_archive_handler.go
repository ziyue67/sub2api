package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ImageArchiveHandler struct{ service *service.ImageArchiveService }

func NewImageArchiveHandler(s *service.ImageArchiveService) *ImageArchiveHandler {
	return &ImageArchiveHandler{service: s}
}

func (h *ImageArchiveHandler) Stats(c *gin.Context) { response.Success(c, h.service.Stats()) }
func (h *ImageArchiveHandler) UpdateConfig(c *gin.Context) {
	var cfg service.ImageArchiveConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "Invalid archive configuration")
		return
	}
	if err := cfg.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.service.UpdateConfig(c.Request.Context(), cfg); err != nil {
		response.InternalError(c, "Unable to save archive configuration")
		return
	}
	response.Success(c, h.service.Stats())
}

func (h *ImageArchiveHandler) List(c *gin.Context) {
	page, size := response.ParsePagination(c)
	filter := service.ImageArchiveFilter{Page: min(page, 100000), PageSize: min(size, 100), Model: c.Query("model")}
	for key, dst := range map[string]*int64{"user_id": &filter.UserID, "group_id": &filter.GroupID, "account_id": &filter.AccountID} {
		if raw := c.Query(key); raw != "" {
			id, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || id <= 0 {
				response.BadRequest(c, "Invalid "+key)
				return
			}
			*dst = id
		}
	}
	for key, dst := range map[string]**time.Time{"from": &filter.From, "to": &filter.To} {
		if raw := c.Query(key); raw != "" {
			v, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				response.BadRequest(c, "Invalid "+key)
				return
			}
			*dst = &v
		}
	}
	if filter.From != nil && filter.To != nil && filter.From.After(*filter.To) {
		response.BadRequest(c, "Invalid time range")
		return
	}
	items, total, err := h.service.List(c.Request.Context(), filter)
	if err != nil {
		response.InternalError(c, "Unable to list image archives")
		return
	}
	response.Success(c, gin.H{"items": items, "total": total, "page": filter.Page, "page_size": filter.PageSize})
}

func (h *ImageArchiveHandler) Get(c *gin.Context) {
	v, err := h.service.Get(c.Request.Context(), c.Param("id"))
	if err != nil {
		archiveReadError(c, err)
		return
	}
	if !v.ExpiresAt.After(time.Now()) {
		response.NotFound(c, "Archive expired")
		return
	}
	response.Success(c, v)
}

func (h *ImageArchiveHandler) File(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("file"))
	if err != nil {
		response.NotFound(c, "Image not found")
		return
	}
	file, mimeType, err := h.service.OpenFile(c.Request.Context(), c.Param("id"), id, c.Query("thumbnail") == "true")
	if err != nil {
		archiveReadError(c, err)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		response.InternalError(c, "Unable to read image")
		return
	}
	c.Header("Content-Type", mimeType)
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Disposition", "inline; filename=\"archive-image\"")
	http.ServeContent(c.Writer, c.Request, "archive-image", info.ModTime(), file)
}

func archiveReadError(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
		response.NotFound(c, "Image archive not found")
		return
	}
	response.InternalError(c, "Unable to read image archive")
}
