package handler

import (
	"context"
	"mime"
	"net/http"
	"strings"
	"time"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// MiniMaxH3Videos exposes the custom relay's JSON OpenAI Videos API.
func (h *OpenAIGatewayHandler) MiniMaxH3Videos(c *gin.Context) {
	endpoint, taskID := service.MiniMaxH3EndpointCreate, ""
	if c.Request.Method != http.MethodPost {
		endpoint = service.MiniMaxH3EndpointStatus
		taskID = service.MiniMaxH3TaskKey(c.Param("request_id"))
		if strings.HasSuffix(c.Request.URL.Path, "/content") {
			endpoint = service.MiniMaxH3EndpointContent
		}
	} else {
		mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/json" {
			h.errorResponse(c, http.StatusUnsupportedMediaType, "invalid_request_error", "MiniMax-H3 requires application/json")
			return
		}
	}
	setActualUpstreamEndpoint(c, "/v1/videos")
	h.handleGrokMedia(c, endpoint, taskID)
}

// Successful status and content requests share one claim and durable billing ID.
// Failed/queued/unknown jobs never charge. Always bill requested seconds, even
// when the completed MP4 duration differs or the upstream omits billing fields.
func prepareMiniMaxH3CompletionBilling(ctx context.Context, h *OpenAIGatewayHandler, key *service.APIKey, subject middleware.AuthSubject, taskID string, result *service.OpenAIForwardResult) *service.OpenAIForwardResult {
	if result == nil || result.VideoCount != 1 {
		return nil
	}
	pending, err := h.gatewayService.LoadGrokVideoPendingBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil || pending == nil || pending.MiniMaxH3BaseCost == nil {
		return nil
	}
	claimed, err := h.gatewayService.ClaimGrokVideoBilling(ctx, taskID, subject.UserID, key.ID)
	if err != nil || !claimed {
		return nil
	}
	merged := *result
	merged.Model, merged.BillingModel, merged.UpstreamModel = pending.Model, pending.BillingModel, pending.UpstreamModel
	merged.RequestID, merged.ResponseID = service.StableGrokVideoBillingRequestID(taskID), taskID
	merged.VideoResolution, merged.VideoDurationSeconds = pending.VideoResolution, pending.VideoDurationSeconds
	merged.MiniMaxH3BaseCost = pending.MiniMaxH3BaseCost
	merged.Duration = service.GrokVideoE2EDuration(pending.CreatedAt, time.Now())
	return &merged
}
