package handler

import (
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// These counters describe a billing classification, not a physical cache TTL.
type downstreamBilledUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
	CacheCreation5m     int `json:"cache_creation_5m_tokens"`
	CacheCreation1h     int `json:"cache_creation_1h_tokens"`
}

type downstreamUpstreamCacheUsage struct {
	CreationTokens int  `json:"cache_creation_input_tokens"`
	ReadTokens     int  `json:"cache_read_input_tokens"`
	Creation5m     int  `json:"ephemeral_5m_input_tokens"`
	Creation1h     int  `json:"ephemeral_1h_input_tokens"`
	Present5m      bool `json:"ephemeral_5m_present"`
	Present1h      bool `json:"ephemeral_1h_present"`
}

// Deliberately not the admin DTO: no account, upstream prices, credentials,
// network addresses, or relationships are serialized to an API-key holder.
type downstreamBillingRecord struct {
	RequestID          string                        `json:"request_id"`
	RecordStatus       string                        `json:"record_status"`
	BillingBasis       string                        `json:"billing_basis"`
	BilledUsage        downstreamBilledUsage         `json:"billed_usage"`
	UpstreamCacheUsage *downstreamUpstreamCacheUsage `json:"upstream_cache_usage"`
	CacheBillingStatus *string                       `json:"cache_billing_status"`
	ActualCost         float64                       `json:"actual_cost"`
	BillingType        int8                          `json:"billing_type"`
	CreatedAt          time.Time                     `json:"created_at"`
}

// usageBillingRecord is an opt-in query on the existing authenticated /v1/usage
// route. Reading persisted amounts avoids predicting a conversion that pricing
// may later reject. A usage row is not an independent proof of payment.
func (h *GatewayHandler) usageBillingRecord(c *gin.Context, apiKey *service.APIKey, subject middleware.AuthSubject, requestIDs []string) {
	c.Header("Cache-Control", "no-store")
	if apiKey.ID <= 0 || subject.UserID <= 0 || apiKey.UserID != subject.UserID {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	if len(requestIDs) != 1 || strings.TrimSpace(requestIDs[0]) == "" || len(requestIDs[0]) > 256 || strings.IndexFunc(requestIDs[0], unicode.IsControl) >= 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Provide one nonempty request_id of at most 256 bytes")
		return
	}
	requestID := strings.TrimSpace(requestIDs[0])
	if h.usageService == nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Billing records are unavailable")
		return
	}
	logs, _, err := h.usageService.ListWithFilters(c.Request.Context(), pagination.PaginationParams{Page: 1, PageSize: 2}, usagestats.UsageLogFilters{
		UserID: subject.UserID, APIKeyID: apiKey.ID, RequestID: requestID,
	})
	if err != nil {
		h.errorResponse(c, http.StatusServiceUnavailable, "api_error", "Billing records are unavailable")
		return
	}
	if len(logs) == 0 {
		// This also covers an unknown ID or a different key's record. Do not
		// disclose which, or imply that a pending request has zero cost.
		h.errorResponse(c, http.StatusNotFound, "billing_record_not_found", "Billing record not found for this API key; recording may still be pending")
		return
	}
	if len(logs) != 1 {
		h.errorResponse(c, http.StatusConflict, "ambiguous_billing_record", "Multiple billing records match this request")
		return
	}
	log := &logs[0]
	if log.UserID != subject.UserID || log.APIKeyID != apiKey.ID || log.RequestID != requestID {
		h.errorResponse(c, http.StatusNotFound, "billing_record_not_found", "Billing record not found for this API key; recording may still be pending")
		return
	}
	response := downstreamBillingRecord{
		RequestID: log.RequestID, RecordStatus: "recorded", BillingBasis: "site_billing",
		BilledUsage: downstreamBilledUsage{
			InputTokens: log.InputTokens, OutputTokens: log.OutputTokens,
			CacheCreationTokens: log.CacheCreationTokens, CacheReadTokens: log.CacheReadTokens,
			CacheCreation5m: log.CacheCreation5mTokens, CacheCreation1h: log.CacheCreation1hTokens,
		},
		ActualCost: log.ActualCost, BillingType: log.BillingType, CreatedAt: log.CreatedAt,
	}
	if audit := log.CacheCreationBilling; audit != nil {
		response.CacheBillingStatus = &audit.Status
		response.UpstreamCacheUsage = &downstreamUpstreamCacheUsage{
			CreationTokens: audit.OriginalCreationTokens, ReadTokens: audit.OriginalReadTokens,
			Creation5m: audit.Original5mTokens, Creation1h: audit.Original1hTokens,
			Present5m: audit.Original5mPresent, Present1h: audit.Original1hPresent,
		}
	}
	c.JSON(http.StatusOK, response)
}
