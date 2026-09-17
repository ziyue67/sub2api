package admin

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ProbeUpstreamUsage probes the account's configured upstream with exactly one
// read-only GET /v1/usage request. It does not enable billing probing or call a
// model endpoint.
func (h *AccountHandler) ProbeUpstreamUsage(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamUsageProbeUnavailable)
		return
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	snapshot, err := h.upstreamBillingProbe.ProbeUpstreamUsage(c.Request.Context(), accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, service.UpstreamUsageProbeResult{AccountID: accountID, Snapshot: snapshot})
}

type upstreamUsageProbeBatchRequest struct {
	AccountIDs []int64 `json:"account_ids" binding:"required"`
}

// ProbeUpstreamUsageBatch bounds the number of read-only upstream requests and
// de-duplicates IDs before dispatching them.
func (h *AccountHandler) ProbeUpstreamUsageBatch(c *gin.Context) {
	if h.upstreamBillingProbe == nil {
		response.ErrorFrom(c, service.ErrUpstreamUsageProbeUnavailable)
		return
	}
	var req upstreamUsageProbeBatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if len(req.AccountIDs) == 0 || len(req.AccountIDs) > service.UpstreamBillingProbeMaxBatchSize {
		response.BadRequest(c, "account_ids must contain between 1 and 20 items")
		return
	}
	for _, accountID := range req.AccountIDs {
		if accountID <= 0 {
			response.BadRequest(c, "account_ids must contain positive IDs")
			return
		}
	}
	accountIDs := uniquePositiveAccountIDs(req.AccountIDs)
	response.Success(c, gin.H{"results": h.upstreamBillingProbe.ProbeUpstreamUsageBatch(c.Request.Context(), accountIDs)})
}

// GetUpstreamUsageSnapshots returns only previously persisted, sanitized
// snapshots for the requested account IDs. It never probes an upstream.
func (h *AccountHandler) GetUpstreamUsageSnapshots(c *gin.Context) {
	if h.adminService == nil {
		response.Error(c, http.StatusServiceUnavailable, "account service unavailable")
		return
	}
	ids, err := parseAccountIDQuery(c.Query("ids"), 1000)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	accounts, err := h.adminService.GetAccountsByIDs(c.Request.Context(), ids)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	byID := make(map[int64]*service.Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	items := make([]service.UpstreamUsageSnapshotItem, 0, len(ids))
	for _, id := range ids {
		account, ok := byID[id]
		if !ok {
			items = append(items, service.UpstreamUsageSnapshotItem{AccountID: id})
			continue
		}
		items = append(items, service.BuildUpstreamUsageSnapshotItems([]service.Account{*account})[0])
	}
	response.Success(c, gin.H{"items": items})
}

func uniquePositiveAccountIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	result := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func parseAccountIDQuery(raw string, max int) ([]int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, responseQueryError("ids is required")
	}
	parts := strings.Split(raw, ",")
	if len(parts) > max {
		return nil, responseQueryError("too many account IDs")
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]struct{}, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, responseQueryError("ids must contain positive integers")
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, responseQueryError("ids is required")
	}
	return ids, nil
}

type responseQueryError string

func (e responseQueryError) Error() string { return string(e) }
