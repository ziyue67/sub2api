package admin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) ConfigureIPAudit(history *AuditLogHandler, summary func(context.Context, int64) (service.SecurityPenaltySummary, error), release func(context.Context, int64, int64, string) (service.SecurityPenaltySummary, error)) {
	if history != nil {
		h.ipAuditLogs = history.auditService
	}
	h.ipAuditPenaltySummary = summary
	h.ipAuditPenaltyRelease = release
}
func ipAuditAdministrator(c *gin.Context) bool {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	role, _ := middleware.GetUserRoleFromContext(c)
	if !ok || subject.UserID <= 0 || role != service.RoleAdmin {
		response.Forbidden(c, "Administrator access required")
		return false
	}
	return true
}
func parseIPAuditQuery(c *gin.Context) (iprisk.AuditQuery, int64, error) {
	q := iprisk.AuditQuery{Source: c.DefaultQuery("source", "anonymous_api"), IP: strings.TrimSpace(c.Query("ip")), State: c.Query("state"), Sort: c.Query("sort"), Limit: 20}
	for _, v := range []struct {
		name   string
		target *int
	}{{"cursor", &q.Cursor}, {"limit", &q.Limit}} {
		if raw := c.Query(v.name); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil {
				return q, 0, err
			}
			*v.target = n
		}
	}
	for _, v := range []struct {
		name   string
		target *int64
	}{{"start", &q.Start}, {"end", &q.End}} {
		if raw := c.Query(v.name); raw != "" {
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return q, 0, err
			}
			*v.target = n
		}
	}
	var userID int64
	if raw := c.Query("user_id"); raw != "" {
		var err error
		userID, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || userID <= 0 {
			return q, 0, fmt.Errorf("invalid user ID")
		}
	}
	if q.Source == "security_penalty" && q.IP != "" {
		return q, 0, fmt.Errorf("user penalties cannot be inferred from IP")
	}
	if q.IP != "" && (q.Source == "login_failures" || q.Source == "login_limiter") {
		p, e := iprisk.ParseNetwork(q.IP)
		if e != nil || p.Bits() != p.Addr().BitLen() {
			return q, 0, fmt.Errorf("exact IP required")
		}
	}
	return q, userID, q.Validate()
}
func (h *SettingHandler) GetIPAudit(c *gin.Context) {
	if !ipAuditAdministrator(c) {
		return
	}
	q, userID, err := parseIPAuditQuery(c)
	if q.Source == "admin_history" && (q.IP != "" || q.Sort == "expires_asc" || q.Cursor%max(q.Limit, 1) != 0) {
		err = fmt.Errorf("unsupported history filters")
	}
	if err != nil {
		response.BadRequest(c, "Invalid IP audit filters or pagination")
		return
	}
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	var page iprisk.AuditPage
	switch q.Source {
	case "security_penalty":
		page, err = h.ipAuditPenaltyPage(ctx, q, userID)
	case "admin_history":
		page, err = h.ipAuditHistory(ctx, q)
	default:
		page, err = m.AuditPage(ctx, q)
	}
	if err != nil {
		response.Error(c, 503, "IP audit source unavailable; state is not verified")
		return
	}
	middleware.SetAuditAction(c, "admin.ip_audit.read")
	response.Success(c, page)
}
func (h *SettingHandler) ipAuditPenaltyPage(ctx context.Context, q iprisk.AuditQuery, userID int64) (iprisk.AuditPage, error) {
	rows := []iprisk.AuditEntry{}
	if userID == 0 {
		return iprisk.AuditSlice(rows, q), nil
	}
	if h.ipAuditPenaltySummary == nil {
		return iprisk.AuditPage{}, fmt.Errorf("penalty source unavailable")
	}
	summary, err := h.ipAuditPenaltySummary(ctx, userID)
	if err != nil {
		return iprisk.AuditPage{}, err
	}
	if len(summary.States) > 0 && q.IP == "" {
		rows = append(rows, iprisk.AuditEntry{ID: iprisk.AuditIdentity("security_penalty", strconv.FormatInt(userID, 10)), Source: "security_penalty", UserID: userID, Scope: "all_user_states", Reason: "user_scope_recovery", State: "active", Effective: "user_scope", CanRelease: h.ipAuditPenaltyRelease != nil, Version: "explicit-user-scope-v1"})
	}
	for _, state := range summary.States {
		start := state.WindowStartedAt.Unix()
		row := iprisk.AuditEntry{ID: iprisk.AuditIdentity("security_penalty", fmt.Sprintf("%d:%s:%s:%s", userID, state.Scope, state.SubjectKey, state.Policy)), Source: "security_penalty", UserID: userID, Scope: state.Scope, Reason: "security_penalty", State: "active", Effective: state.Action, Count: &state.Count, Threshold: &summary.Threshold, StartedAt: &start}
		if state.WindowStartedAt.IsZero() {
			row.StartedAt = nil
		}
		expiry := state.BlockedUntil
		if expiry == nil {
			expiry = state.RouteUntil
		}
		if expiry != nil {
			at := expiry.Unix()
			row.ExpiresAt = &at
			row.Duration = "temporary"
		}
		if state.Action == "observe" || state.Action == "record" {
			row.State = "observed"
		}
		rows = append(rows, row)
	}
	page := iprisk.AuditSlice(rows, q)
	// The authoritative existing Summary itself is capped at 100 states.
	if len(summary.States) == 100 {
		page.Partial = true
	}
	return page, nil
}
func (h *SettingHandler) ipAuditHistory(ctx context.Context, q iprisk.AuditQuery) (iprisk.AuditPage, error) {
	empty := iprisk.AuditSlice([]iprisk.AuditEntry{}, q)
	// Stored IP is the operator's address, not necessarily the released target.
	// Do not guess targets from old bodies or pretend an actor-IP filter is one.
	if q.State != "" && q.State != "historical" {
		return empty, nil
	}
	if q.IP != "" || q.Sort == "expires_asc" || q.Cursor%q.Limit != 0 {
		return empty, fmt.Errorf("operator history supports time and descending-page filters only")
	}
	if h.ipAuditLogs == nil {
		return empty, fmt.Errorf("audit history unavailable")
	}
	filter := &service.AuditLogFilter{Action: "admin.ip_audit.release", Page: q.Cursor/q.Limit + 1, PageSize: q.Limit}
	if q.Start > 0 {
		at := time.Unix(q.Start, 0)
		filter.StartTime = &at
	}
	if q.End > 0 {
		at := time.Unix(q.End, 0)
		filter.EndTime = &at
	}
	result, err := h.ipAuditLogs.List(ctx, filter)
	if err != nil {
		return empty, err
	}
	for _, log := range result.Logs {
		at := log.CreatedAt.Unix()
		status := "failed"
		if log.StatusCode < 400 {
			status = "success"
		}
		if value, ok := log.Extra["result"].(string); ok && (value == "released" || value == "still_present" || value == "unavailable" || value == "failed") {
			status = value
		}
		reason := "operator_review"
		if value, ok := log.Extra["reason_code"].(string); ok && iprisk.AuditReasonCode(value) != "" {
			reason = value
		}
		origin, _ := log.Extra["source"].(string)
		known := false
		for _, s := range iprisk.AuditSources() {
			if s.ID == origin {
				known = true
			}
		}
		if !known {
			origin = ""
		}
		empty.Items = append(empty.Items, iprisk.AuditEntry{OriginSource: origin, ID: strconv.FormatInt(log.ID, 10), Source: "admin_history", Scope: "operator", State: "historical", Effective: "historical", Reason: reason, StartedAt: &at, ActorID: log.ActorUserID, Result: status})
	}
	empty.NextCursor = q.Cursor + len(result.Logs)
	empty.HasMore = empty.NextCursor < result.Total
	return empty, nil
}

func (h *SettingHandler) ReleaseIPAudit(c *gin.Context) {
	if !ipAuditAdministrator(c) {
		return
	}
	middleware.SetAuditAction(c, "admin.ip_audit.release")
	middleware.SetAuditExtra(c, map[string]any{"result": "failed"})
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	var req struct {
		UserID  int64  `json:"user_id"`
		Source  string `json:"source"`
		ID      string `json:"id"`
		Network string `json:"network"`
		CheckIP string `json:"check_ip"`
		Version string `json:"version"`
		Confirm bool   `json:"confirm"`
		Reason  string `json:"reason"`
	}
	if c.ShouldBindJSON(&req) != nil || !req.Confirm || iprisk.AuditReasonCode(req.Reason) == "" {
		response.BadRequest(c, "Explicit source confirmation and a supported reason are required")
		return
	}
	if req.Source == "security_penalty" {
		if req.UserID <= 0 || req.ID != iprisk.AuditIdentity("security_penalty", strconv.FormatInt(req.UserID, 10)) || req.Version != "explicit-user-scope-v1" || h.ipAuditPenaltyRelease == nil {
			response.BadRequest(c, "Explicit user-wide recovery target required")
			return
		}
		subject, _ := middleware.GetAuthSubjectFromContext(c)
		middleware.SetAuditExtra(c, map[string]any{"source": "security_penalty", "reason_code": req.Reason, "confirm": true, "target_user_id": req.UserID})
		ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
		defer cancel()
		summary, releaseErr := h.ipAuditPenaltyRelease(ctx, req.UserID, subject.UserID, req.Reason)
		if releaseErr != nil {
			response.Error(c, 503, "User penalty release or readback is unverified; refresh the source")
			return
		}
		readback := "released"
		if len(summary.States) > 0 || summary.Restricted {
			readback = "still_present"
		}
		middleware.SetAuditExtra(c, map[string]any{"result": readback, "readback": readback, "remaining_count": len(summary.States)})
		response.Success(c, gin.H{"released": readback == "released", "readback": readback, "remaining": []iprisk.AuditEntry{}, "unknown_sources": []string{"account_status", "key_acl", "manual_deny", "login_failures", "anonymous_api", "host_firewall"}, "unrestricted": false})
		return
	}
	network, err := iprisk.ParseNetwork(req.Network)
	if err != nil {
		response.BadRequest(c, "Invalid source network")
		return
	}
	switch req.Source {
	case "anonymous_api", "ip_risk", "ip_risk_observe", "login_failures", "login_limiter":
	default:
		response.Error(c, 409, "This source is read-only; use its own management controls")
		return
	}
	req.Reason = iprisk.AuditReasonCode(req.Reason)
	checkIP := network.Addr().String()
	if req.CheckIP != "" {
		address, e := netip.ParseAddr(req.CheckIP)
		if e != nil || address.Zone() != "" || !network.Contains(address.Unmap()) {
			response.BadRequest(c, "Readback IP must belong to the selected source network")
			return
		}
		checkIP = address.Unmap().String()
	}
	digest := sha256.Sum256([]byte(network.String()))
	middleware.SetAuditExtra(c, map[string]any{"source": req.Source, "reason_code": req.Reason, "filter_hash": hex.EncodeToString(digest[:8]), "confirm": true})
	m := h.ipRiskManager(c)
	if m == nil {
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()
	err = m.ReleaseAuditEntry(ctx, req.Source, req.ID, network.String(), req.Version)
	if err != nil {
		if errors.Is(err, iprisk.ErrAuditConflict) || errors.Is(err, iprisk.ErrAuditReadOnly) {
			response.Error(c, 409, "Source changed or is read-only; refresh and use its own management controls")
		} else {
			response.Error(c, 503, "Source release failed or is unverified; refresh before retrying")
		}
		return
	}
	// Re-read the actual released key before collecting other remaining sources.
	present, readErr := m.AuditEntryPresent(ctx, req.Source, req.ID, network.String())
	readback := "released"
	if readErr != nil {
		readback = "unavailable"
	} else if present {
		readback = "still_present"
	}
	remaining, unknown := m.AuditRemaining(ctx, checkIP)
	if req.CheckIP == "" && network.Bits() != network.Addr().BitLen() {
		unknown = append(unknown, "exact_subject")
	}
	middleware.SetAuditExtra(c, map[string]any{"result": readback, "readback": readback, "remaining_count": len(remaining)})
	response.Success(c, gin.H{"released": readback == "released", "readback": readback, "remaining": remaining, "unknown_sources": unknown, "unrestricted": false})
}
