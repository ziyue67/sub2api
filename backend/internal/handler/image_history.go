package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Explicit customer DTOs: never embed the admin archive, its parameters, source
// URLs, prompts, reference images, account names or upstream cost information.
type customerImageHistoryFile struct {
	ID          int    `json:"id"`
	URL         string `json:"url"`
	MIME        string `json:"mime_type"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	PreviewOnly bool   `json:"preview_only"`
	Thumbnail   bool   `json:"thumbnail"`
}

type customerImageHistoryRecord struct {
	ID             string                      `json:"id"`
	RequestID      string                      `json:"request_id"`
	Model          string                      `json:"model"`
	Status         string                      `json:"status"`
	DeliveryStatus string                      `json:"delivery_status"`
	Message        string                      `json:"message,omitempty"`
	ImageCount     int                         `json:"image_count"`
	Files          []customerImageHistoryFile  `json:"files"`
	Billing        service.ImageHistoryBilling `json:"billing"`
	CreatedAt      time.Time                   `json:"created_at"`
	ExpiresAt      time.Time                   `json:"expires_at"`
}

func projectCustomerImageHistory(record service.ImageArchiveRecord, prefix string) customerImageHistoryRecord {
	v := customerImageHistoryRecord{
		ID: record.ID, Model: record.Model, RequestID: record.ID,
		Status: "unknown", DeliveryStatus: "unknown",
		Files: []customerImageHistoryFile{}, Billing: service.ImageHistoryBilling{Status: "unknown"},
		CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt,
	}
	if id, _ := record.Parameters["_client_request_id"].(string); id != "" {
		v.RequestID = id
	}
	if status, _ := record.Parameters["_generation_status"].(string); status == "succeeded" || status == "failed" {
		v.Status = status
	}
	if status, _ := record.Parameters["_delivery_status"].(string); status == "returned" || status == "interrupted" {
		v.DeliveryStatus = status
	}
	for _, file := range record.Files {
		if file.Role != "result" {
			continue
		}
		v.Files = append(v.Files, customerImageHistoryFile{
			ID: file.ID, URL: prefix + "/" + record.ID + "/files/" + strconv.Itoa(file.ID),
			MIME: file.MIME, Width: file.Width, Height: file.Height, PreviewOnly: file.PreviewOnly, Thumbnail: file.Thumbnail,
		})
	}
	v.ImageCount = len(v.Files)
	if v.Status == "unknown" && len(v.Files) > 0 {
		v.Status = "succeeded"
	}
	// Archive/storage failure does not prove generation failed. Only new
	// capture metadata explicitly describes the generation outcome.
	if v.Status == "failed" {
		v.Message = "生图未完成"
	} else if v.DeliveryStatus == "interrupted" {
		v.Message = "结果传输中断"
	}
	return v
}

func imageHistoryOwner(c *gin.Context, api bool) (int64, int64, bool) {
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		return 0, 0, false
	}
	if !api {
		return subject.UserID, 0, true
	}
	key, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok || key == nil || key.ID <= 0 || key.UserID != subject.UserID {
		return 0, 0, false
	}
	return subject.UserID, key.ID, true
}

func (h *OpenAIGatewayHandler) ImageHistory(c *gin.Context)           { h.listImageHistory(c, true) }
func (h *OpenAIGatewayHandler) UserImageHistory(c *gin.Context)       { h.listImageHistory(c, false) }
func (h *OpenAIGatewayHandler) ImageHistoryDetail(c *gin.Context)     { h.getImageHistory(c, true) }
func (h *OpenAIGatewayHandler) UserImageHistoryDetail(c *gin.Context) { h.getImageHistory(c, false) }
func (h *OpenAIGatewayHandler) ImageHistoryFile(c *gin.Context)       { h.imageHistoryFile(c, true) }
func (h *OpenAIGatewayHandler) UserImageHistoryFile(c *gin.Context)   { h.imageHistoryFile(c, false) }

func writeImageHistory(c *gin.Context, api bool, data any) {
	if api {
		c.JSON(http.StatusOK, data)
	} else {
		response.Success(c, data)
	}
}

func imageHistoryPrefix(api bool) string {
	if api {
		return "/v1/images/history"
	}
	return "/api/v1/user/image-history"
}

func (h *OpenAIGatewayHandler) listImageHistory(c *gin.Context, api bool) {
	c.Header("Cache-Control", "private, no-store")
	userID, keyID, ok := imageHistoryOwner(c, api)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	if h.imageArchive == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image history unavailable")
		return
	}
	page := parseBoundedQueryInt(c, "page", 1, 1, 100000)
	filter := service.ImageArchiveFilter{Page: page, PageSize: 20}
	if !api && c.Query("api_key_id") != "" {
		id, err := strconv.ParseInt(c.Query("api_key_id"), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "Invalid API key filter")
			return
		}
		keyID = id
	}
	items, total, err := h.imageArchive.ListHistory(c.Request.Context(), userID, keyID, filter)
	if err != nil {
		response.InternalError(c, "Image history unavailable")
		return
	}
	data := make([]customerImageHistoryRecord, 0, len(items))
	for _, item := range items {
		// Defence in depth in addition to the SQL ownership predicates.
		if item.UserID != userID || (keyID > 0 && item.APIKeyID != keyID) {
			continue
		}
		v := projectCustomerImageHistory(item, imageHistoryPrefix(api))
		v.Billing = h.imageArchive.HistoryBilling(c.Request.Context(), &item)
		data = append(data, v)
	}
	writeImageHistory(c, api, gin.H{"items": data, "total": total, "page": page, "page_size": 20})
}

func (h *OpenAIGatewayHandler) getImageHistory(c *gin.Context, api bool) {
	c.Header("Cache-Control", "private, no-store")
	userID, keyID, ok := imageHistoryOwner(c, api)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	if h.imageArchive == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image history unavailable")
		return
	}
	v, err := h.imageArchive.GetHistory(c.Request.Context(), userID, keyID, c.Param("id"))
	if err != nil {
		imageHistoryReadError(c, err)
		return
	}
	data := projectCustomerImageHistory(*v, imageHistoryPrefix(api))
	data.Billing = h.imageArchive.HistoryBilling(c.Request.Context(), v)
	writeImageHistory(c, api, data)
}

func imageHistoryReadError(c *gin.Context, err error) {
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, os.ErrNotExist) {
		response.NotFound(c, "Image history not found")
		return
	}
	response.InternalError(c, "Image history unavailable")
}

func (h *OpenAIGatewayHandler) imageHistoryFile(c *gin.Context, api bool) {
	c.Header("Cache-Control", "private, no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	userID, keyID, ok := imageHistoryOwner(c, api)
	if !ok {
		response.Unauthorized(c, "Authentication required")
		return
	}
	if h.imageArchive == nil {
		response.Error(c, http.StatusServiceUnavailable, "Image history unavailable")
		return
	}
	fileID, err := strconv.Atoi(c.Param("file"))
	if err != nil || fileID <= 0 {
		response.NotFound(c, "Image not found")
		return
	}
	file, mimeType, err := h.imageArchive.OpenHistoryFile(c.Request.Context(), userID, keyID, c.Param("id"), fileID, c.Query("thumbnail") == "true")
	if err != nil {
		imageHistoryReadError(c, err)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		response.InternalError(c, "Image unavailable")
		return
	}
	if !strings.HasPrefix(mimeType, "image/") {
		response.NotFound(c, "Image not found")
		return
	}
	c.Header("Content-Type", mimeType)
	http.ServeContent(c.Writer, c.Request, "image", info.ModTime(), file)
}
