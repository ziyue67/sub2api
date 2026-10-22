package admin

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	"github.com/Wei-Shaw/sub2api/internal/pkg/authguard"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func auditAdminRequest(t *testing.T, h gin.HandlerFunc, method, path, role, body string) *httptest.ResponseRecorder {
	t.Helper()
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if role != "" {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 17})
			c.Set(string(middleware.ContextKeyUserRole), role)
		}
		c.Next()
	})
	router.Handle(method, "/test", h)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, request)
	return w
}
func auditFixture(t *testing.T) (*SettingHandler, *miniredis.Miniredis) {
	h, _ := ipRiskHandlerFixture()
	server := miniredis.RunT(t)
	server.SetTime(time.Now())
	m := testutil.NewRedisIPRiskManager(t, h.settingService.IPRiskStore(), server)
	h.settingService.IPRisk = m
	cfg := iprisk.DefaultConfig()
	cfg.Enabled = true
	cfg.ObserveOnly = false
	cfg.AnonymousAPI.Enabled = true
	cfg.AnonymousAPI.Threshold = 2
	cfg.Blacklist = []iprisk.Rule{{Network: "8.8.8.8", Enabled: true}}
	require.NoError(t, m.Save(context.Background(), cfg))
	for i := 0; i < 2; i++ {
		_, err := m.AnonymousFailure(context.Background(), "8.8.8.8", "unknown")
		require.NoError(t, err)
	}
	return h, server
}
func TestIPAuditAdminAuthorityPaginationAndRelease(t *testing.T) {
	h, _ := auditFixture(t)
	for _, role := range []string{"", "user", "observer"} {
		require.Equal(t, 403, auditAdminRequest(t, h.GetIPAudit, "GET", "/test", role, "").Code)
		require.Equal(t, 403, auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", role, `{}`).Code)
	}
	for _, query := range []string{"?limit=1000", "?cursor=-1", "?source=unknown", "?ip=bad", "?start=100&end=1", "?source=login_failures&ip=8.8.8.0/24", "?source=security_penalty&ip=8.8.8.8&user_id=1"} {
		require.Equal(t, 400, auditAdminRequest(t, h.GetIPAudit, "GET", "/test"+query, "admin", "").Code, query)
	}
	page, err := h.settingService.IPRisk.AuditPage(context.Background(), iprisk.AuditQuery{Source: "anonymous_api", IP: "8.8.8.8", Limit: 20})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	entry := page.Items[0]
	payload := map[string]any{"source": entry.Source, "id": entry.ID, "network": entry.Network, "version": entry.Version, "reason": "operator_review"}
	raw, _ := json.Marshal(payload)
	require.Equal(t, 400, auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw)).Code, "explicit confirmation required")
	payload["confirm"] = true
	payload["version"] = "stale"
	raw, _ = json.Marshal(payload)
	require.Equal(t, 409, auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw)).Code)
	payload["version"] = entry.Version
	raw, _ = json.Marshal(payload)
	w := auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw))
	require.Equal(t, 200, w.Code, w.Body.String())
	var result struct {
		Data struct {
			Released     bool                `json:"released"`
			Readback     string              `json:"readback"`
			Remaining    []iprisk.AuditEntry `json:"remaining"`
			Unrestricted bool                `json:"unrestricted"`
			Unknown      []string            `json:"unknown_sources"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
	require.True(t, result.Data.Released)
	require.Equal(t, "released", result.Data.Readback)
	require.False(t, result.Data.Unrestricted)
	require.Contains(t, result.Data.Unknown, "host_firewall")
	require.NotEmpty(t, result.Data.Remaining, "manual deny must remain independently visible")
	require.Equal(t, "manual_deny", result.Data.Remaining[0].Source)
}
func TestIPAuditFailureAndReadOnlySources(t *testing.T) {
	h, server := auditFixture(t)
	h.ipAuditPenaltySummary = func(_ context.Context, id int64) (service.SecurityPenaltySummary, error) {
		require.EqualValues(t, 9, id)
		return service.SecurityPenaltySummary{Threshold: 3, States: []service.SecurityPenaltyState{{UserID: id, Scope: "user", Count: 3, Action: "block"}}}, nil
	}
	w := auditAdminRequest(t, h.GetIPAudit, "GET", "/test?source=security_penalty&user_id=9", "admin", "")
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"can_release":false`)
	require.NotContains(t, w.Body.String(), "full_prompt")
	page, err := h.settingService.IPRisk.AuditPage(context.Background(), iprisk.AuditQuery{Source: "anonymous_api", Limit: 20})
	require.NoError(t, err)
	entry := page.Items[0]
	server.Close()
	raw, _ := json.Marshal(map[string]any{"source": entry.Source, "id": entry.ID, "network": entry.Network, "version": entry.Version, "reason": "false_positive", "confirm": true})
	w = auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw))
	require.Equal(t, 503, w.Code)
	require.Equal(t, http.StatusServiceUnavailable, auditAdminRequest(t, h.GetIPAudit, "GET", "/test", "admin", "").Code)
}

type ipAuditHistoryRepo struct {
	service.AuditLogRepository
	filter *service.AuditLogFilter
}

func (r *ipAuditHistoryRepo) List(_ context.Context, f *service.AuditLogFilter) (*service.AuditLogList, error) {
	r.filter = f
	actor := int64(17)
	return &service.AuditLogList{Page: f.Page, PageSize: f.PageSize, Total: 1, Logs: []*service.AuditLog{{ID: 2, ActorUserID: &actor, StatusCode: 200, RequestBody: `{"key":"private-canary"}`, CredentialMasked: "private-canary", Extra: map[string]any{"source": "anonymous_api", "result": "released", "reason_code": "false_positive"}}}}, nil
}
func TestIPAuditHistoryUsesOnlyExistingSafeMetadata(t *testing.T) {
	h, _ := auditFixture(t)
	repo := &ipAuditHistoryRepo{}
	h.ipAuditLogs = service.NewAuditLogService(repo, nil)
	w := auditAdminRequest(t, h.GetIPAudit, "GET", "/test?source=admin_history&limit=10&cursor=0", "admin", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), "private-canary")
	require.NotContains(t, w.Body.String(), "request_body")
	require.Contains(t, w.Body.String(), `"actor_id":17`)
	require.Contains(t, w.Body.String(), `"result":"released"`)
	require.Equal(t, "admin.ip_audit.release", repo.filter.Action)
	require.Equal(t, 400, auditAdminRequest(t, h.GetIPAudit, "GET", "/test?source=admin_history&ip=8.8.8.8", "admin", "").Code, "actor IP is not a released target")
}

func TestIPAuditExplicitUserPenaltyRecoveryScopeAndActor(t *testing.T) {
	h, _ := auditFixture(t)
	calls := 0
	h.ipAuditPenaltyRelease = func(_ context.Context, user, actor int64, reason string) (service.SecurityPenaltySummary, error) {
		calls++
		require.EqualValues(t, 9, user)
		require.EqualValues(t, 17, actor)
		require.Equal(t, "operator_review", reason)
		return service.SecurityPenaltySummary{States: []service.SecurityPenaltyState{}}, nil
	}
	payload := map[string]any{"source": "security_penalty", "id": iprisk.AuditIdentity("security_penalty", "9"), "user_id": 9, "version": "explicit-user-scope-v1", "reason": "operator_review", "confirm": true}
	raw, _ := json.Marshal(payload)
	require.Equal(t, 403, auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "user", string(raw)).Code)
	require.Zero(t, calls)
	payload["user_id"] = 10
	raw, _ = json.Marshal(payload)
	require.Equal(t, 400, auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw)).Code)
	require.Zero(t, calls)
	payload["user_id"] = 9
	raw, _ = json.Marshal(payload)
	w := auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw))
	require.Equal(t, 200, w.Code)
	require.Equal(t, 1, calls)
	require.Contains(t, w.Body.String(), `"readback":"released"`)
	require.Contains(t, w.Body.String(), `"unrestricted":false`)
	require.Contains(t, w.Body.String(), "account_status")
}

func TestIPAuditIPv6NetworkReleaseRechecksOriginalExactIP(t *testing.T) {
	h, server := auditFixture(t)
	ip := "2606:4700:4700::1111"
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		_, err := h.settingService.IPRisk.AnonymousFailure(ctx, ip, "missing")
		require.NoError(t, err)
	}
	key := "auth_abuse:{" + authguard.IPKey(ip) + "}:banned"
	require.NoError(t, server.Set(key, "1"))
	server.SetTTL(key, time.Hour)
	page, err := h.settingService.IPRisk.AuditPage(ctx, iprisk.AuditQuery{Source: "login_failures", IP: ip, Limit: 20})
	require.NoError(t, err)
	require.Len(t, page.Items, 1)
	row := page.Items[0]
	require.Equal(t, ip, row.CheckIP)
	payload := map[string]any{"source": row.Source, "id": row.ID, "network": row.Network, "version": row.Version, "check_ip": row.CheckIP, "confirm": true, "reason": "operator_review"}
	raw, _ := json.Marshal(payload)
	w := auditAdminRequest(t, h.ReleaseIPAudit, "POST", "/test", "admin", string(raw))
	require.Equal(t, 200, w.Code, w.Body.String())
	require.Contains(t, w.Body.String(), `"source":"anonymous_api"`)
}
