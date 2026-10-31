package admin

import (
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

type priorityStickyBindingRow struct {
	AccountID int64 `json:"account_id"`
	Count     int   `json:"count"`
}

func priorityStickyRows(counts map[int64]int) []priorityStickyBindingRow {
	rows := make([]priorityStickyBindingRow, 0, len(counts))
	for accountID, count := range counts {
		rows = append(rows, priorityStickyBindingRow{AccountID: accountID, Count: count})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count != rows[j].Count {
			return rows[i].Count > rows[j].Count
		}
		return rows[i].AccountID < rows[j].AccountID
	})
	return rows
}

func priorityStickyTotal(counts map[int64]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func parsePriorityStickyGroupID(c *gin.Context) (int64, bool) {
	raw := strings.TrimSpace(c.Query("group_id"))
	if raw == "" {
		response.BadRequest(c, "group_id is required")
		return 0, false
	}
	groupID, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "group_id must be a positive integer")
		return 0, false
	}
	return groupID, true
}

func (h *AccountHandler) PrioritySchedulingStickyBindings(c *gin.Context) {
	groupID, ok := parsePriorityStickyGroupID(c)
	if !ok {
		return
	}
	counts, err := h.openAIGatewayService.StickySessionBindingCounts(c.Request.Context(), groupID)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Sticky session bindings unavailable")
		return
	}
	response.Success(c, gin.H{"group_id": groupID, "total": priorityStickyTotal(counts), "by_account": priorityStickyRows(counts)})
}

func (h *AccountHandler) ClearPrioritySchedulingStickyBindings(c *gin.Context) {
	var request struct {
		GroupID    int64   `json:"group_id"`
		AccountIDs []int64 `json:"account_ids"`
		DryRun     bool    `json:"dry_run"`
	}
	if c.ShouldBindJSON(&request) != nil {
		response.BadRequest(c, "Invalid sticky session request")
		return
	}
	if request.GroupID <= 0 {
		response.BadRequest(c, "group_id must be a positive integer")
		return
	}
	for _, accountID := range request.AccountIDs {
		if accountID <= 0 {
			response.BadRequest(c, "account_ids must be positive integers")
			return
		}
	}
	if request.DryRun {
		counts, err := h.openAIGatewayService.StickySessionBindingCounts(c.Request.Context(), request.GroupID)
		if err != nil {
			response.Error(c, http.StatusServiceUnavailable, "Sticky session bindings unavailable")
			return
		}
		response.Success(c, gin.H{"group_id": request.GroupID, "dry_run": true, "deleted": 0, "remaining": priorityStickyTotal(counts), "by_account": priorityStickyRows(counts)})
		return
	}
	deleted, err := h.openAIGatewayService.ClearStickySessionBindings(c.Request.Context(), request.GroupID, request.AccountIDs)
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "Sticky session cleanup unavailable")
		return
	}
	slog.Info("priority_sticky_bindings_cleared", "group_id", request.GroupID, "account_ids", request.AccountIDs, "deleted", deleted)
	counts, countErr := h.openAIGatewayService.StickySessionBindingCounts(c.Request.Context(), request.GroupID)
	if countErr != nil {
		counts = map[int64]int{}
	}
	response.Success(c, gin.H{"group_id": request.GroupID, "deleted": deleted, "remaining": priorityStickyTotal(counts), "by_account": priorityStickyRows(counts)})
}
