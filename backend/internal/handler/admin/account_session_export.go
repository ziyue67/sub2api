package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ExportOpenAISession creates a one-shot 2FA-backed ChatGPT Session task.
func (h *AccountHandler) ExportOpenAISession(c *gin.Context) {
	if h.openaiOAuthReauth == nil {
		response.Error(c, http.StatusServiceUnavailable, "ChatGPT Session export is unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var input map[string]any
	if c.Request.Body != nil {
		if err := json.NewDecoder(c.Request.Body).Decode(&input); err != nil && err != io.EOF {
			response.BadRequest(c, "Invalid Session export request")
			return
		}
	}
	if len(input) != 0 {
		response.BadRequest(c, "Session export does not accept credentials or cookies")
		return
	}
	task, err := h.openaiOAuthReauth.CreateSessionExportTask(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Accepted(c, task)
}

func (h *AccountHandler) GetOpenAISessionExportTask(c *gin.Context) {
	if h.openaiOAuthReauth == nil {
		response.Error(c, http.StatusServiceUnavailable, "ChatGPT Session export is unavailable")
		return
	}
	accountID, taskID, ok := parseSessionExportIDs(c)
	if !ok {
		return
	}
	task, err := h.openaiOAuthReauth.GetSessionExportTask(c.Request.Context(), accountID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, task)
}

func (h *AccountHandler) DownloadOpenAISessionExport(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Pragma", "no-cache")
	if h.openaiOAuthReauth == nil {
		response.Error(c, http.StatusServiceUnavailable, "ChatGPT Session export is unavailable")
		return
	}
	accountID, taskID, ok := parseSessionExportIDs(c)
	if !ok {
		return
	}
	body, err := h.openaiOAuthReauth.DownloadSessionExport(c.Request.Context(), accountID, taskID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, body)
}

func parseSessionExportIDs(c *gin.Context) (int64, int64, bool) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	taskID, taskErr := strconv.ParseInt(c.Param("task_id"), 10, 64)
	if err != nil || taskErr != nil || accountID <= 0 || taskID <= 0 {
		response.BadRequest(c, "Invalid Session export task ID")
		return 0, 0, false
	}
	return accountID, taskID, true
}
