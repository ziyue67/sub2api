package admin

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func serveLatencyReport(c *gin.Context, read func(context.Context, service.LatencyReportFilter) (*service.LatencyReport, error)) {
	end := time.Now().UTC()
	f := service.LatencyReportFilter{Start: end.Add(-24 * time.Hour), End: end, Model: strings.TrimSpace(c.Query("model")), Platform: strings.TrimSpace(c.Query("platform"))}
	if len(f.Model) > 256 || len(f.Platform) > 32 {
		response.BadRequest(c, "Model exceeds length limit")
		return
	}
	for _, field := range []struct {
		name string
		dest *int64
	}{
		{"user_id", &f.UserID}, {"api_key_id", &f.APIKeyID}, {"account_id", &f.AccountID}, {"group_id", &f.GroupID},
	} {
		if raw := c.Query(field.name); raw != "" {
			v, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || v <= 0 {
				response.BadRequest(c, "Invalid "+field.name)
				return
			}
			*field.dest = v
		}
	}
	for _, field := range []struct {
		name string
		dest *time.Time
		end  bool
	}{
		{"start_date", &f.Start, false}, {"end_date", &f.End, true},
	} {
		if raw := c.Query(field.name); raw != "" {
			v, err := parseUsageDateBoundary(raw, c.Query("timezone"), field.end)
			if err != nil {
				response.BadRequest(c, "Invalid "+field.name)
				return
			}
			*field.dest = v
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := read(ctx, f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

func (h *UsageHandler) LatencyReport(c *gin.Context) {
	serveLatencyReport(c, h.usageService.LatencyReport)
}
func (h *OpsHandler) LatencyReport(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, 503, "Ops service not available")
		return
	}
	serveLatencyReport(c, h.opsService.LatencyReport)
}

func (h *OpsHandler) NetworkSeries(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, 503, "Ops service not available")
		return
	}
	start, end, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 8*time.Second)
	defer cancel()
	result, err := h.opsService.NetworkSeries(ctx, start, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
