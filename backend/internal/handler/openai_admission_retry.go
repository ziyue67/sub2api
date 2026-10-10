package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

// The forward call has already released its concurrency slot. Reselect only
// after an explicitly marked initial admission rejection, never based on the
// error text of a later send. Written heartbeats are conservatively terminal too.
func retryOpenAIInitialAdmission(c *gin.Context, err error, result *service.OpenAIForwardResult, body []byte, accountID int64, excluded map[int64]struct{}, switches *int, maxSwitches int) bool {
	if !service.IsOpenAIInitialAdmissionRejection(err) || result != nil ||
		c.Request.Context().Err() != nil || c.Writer.Written() || *switches >= maxSwitches {
		return false
	}
	// These requests may depend on upstream state held by the selected account.
	if gjson.GetBytes(body, "previous_response_id").String() != "" ||
		gjson.GetBytes(body, "conversation").Exists() {
		return false
	}
	if _, alreadyExcluded := excluded[accountID]; alreadyExcluded {
		return false
	}
	excluded[accountID] = struct{}{}
	*switches++
	return true
}

// handleTurnAdmissionError retains model-capability attribution when a final
// pre-send check rejects an account after model mapping.
func (h *OpenAIGatewayHandler) handleTurnAdmissionError(c *gin.Context, err error, streamStarted bool) {
	if service.IsOpenAIModelCapabilityRejection(err) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalModelConfiguration)
		h.handleStreamingAwareError(c, http.StatusNotFound, "model_not_found", "The requested model is not supported by the selected account", streamStarted)
		return
	}
	h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "admission_unavailable", "Account eligibility changed; please retry with complete context", streamStarted)
}
