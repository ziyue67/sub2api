package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/iprisk"
	ippkg "github.com/Wei-Shaw/sub2api/internal/pkg/ip"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/testutil"
	"github.com/alicebob/miniredis/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ipRiskSettingsRepo struct {
	service.SettingRepository
	values map[string]string
}

func (r *ipRiskSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}
func (r *ipRiskSettingsRepo) Set(_ context.Context, key, value string) error {
	r.values[key] = value
	return nil
}

func ipRiskHandlerFixture() (*SettingHandler, *ipRiskSettingsRepo) {
	repo := &ipRiskSettingsRepo{values: map[string]string{}}
	svc := service.NewSettingService(repo, &config.Config{Server: config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{"127.0.0.1"}}})
	svc.IPRisk = iprisk.New(svc.IPRiskStore(), nil, nil, false)
	return NewSettingHandler(svc, nil, nil, nil, nil, nil, nil), repo
}

func ipRiskHandlerRequest(t *testing.T, handler gin.HandlerFunc, method string, payload any) *httptest.ResponseRecorder {
	t.Helper()
	var raw []byte
	if payload != nil {
		var err error
		raw, err = json.Marshal(payload)
		require.NoError(t, err)
	}
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Handle(method, "/test", handler)
	req := httptest.NewRequest(method, "/test", bytes.NewReader(raw))
	req.RemoteAddr = "198.51.100.1:1234"
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestIPRiskHandlerDefaultAndPersistence(t *testing.T) {
	h, repo := ipRiskHandlerFixture()
	w := ipRiskHandlerRequest(t, h.GetIPRisk, http.MethodGet, nil)
	require.Equal(t, 200, w.Code)
	require.JSONEq(t, `{"code":0,"message":"success","data":{"config":`+mustIPRiskJSON(t, iprisk.DefaultConfig())+`,"health":{"config_error":"","emergency_disabled":false,"redis_available":false},"current_ip":"198.51.100.1","current_peer_ip":"198.51.100.1","trusted_proxies_configured":true}}`, w.Body.String())
	c := iprisk.DefaultConfig()
	c.Whitelist = []string{"::ffff:192.0.2.1"}
	c.BlockedCountries = []string{"us", "jp"}
	w = ipRiskHandlerRequest(t, h.SaveIPRisk, http.MethodPut, c)
	require.Equal(t, 200, w.Code)
	require.Contains(t, repo.values[iprisk.SettingKey], `"192.0.2.1/32"`)
	require.Contains(t, repo.values[iprisk.SettingKey], `"US","JP"`)
	// A fresh manager reads the saved setting rather than depending on process memory.
	h.settingService.IPRisk = iprisk.New(h.settingService.IPRiskStore(), nil, nil, false)
	w = ipRiskHandlerRequest(t, h.GetIPRisk, http.MethodGet, nil)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"192.0.2.1/32"`)
}

func TestIPRiskHandlerReportsTrustedClientAndTCPPeer(t *testing.T) {
	for _, tt := range []struct {
		name     string
		proxies  []string
		peer     string
		xff      string
		wantIP   string
		wantPeer string
	}{
		{"untrusted Docker gateway", []string{"127.0.0.1/32", "::1/128"}, "172.18.0.1:1234", "198.51.100.25", "172.18.0.1", "172.18.0.1"},
		{"trusted gateway client A", []string{"172.18.0.1/32"}, "172.18.0.1:1234", "198.51.100.25", "198.51.100.25", "172.18.0.1"},
		{"trusted gateway client B", []string{"172.18.0.1/32"}, "172.18.0.1:1234", "203.0.113.26", "203.0.113.26", "172.18.0.1"},
		{"spoofed prefix", []string{"172.18.0.1/32"}, "172.18.0.1:1234", "203.0.113.99, 198.51.100.25", "198.51.100.25", "172.18.0.1"},
		{"explicit empty trust", nil, "172.18.0.1:1234", "198.51.100.25", "172.18.0.1", "172.18.0.1"},
		{"IPv6 peer", []string{"2001:db8::1/128"}, "[2001:db8::1]:1234", "198.51.100.25", "198.51.100.25", "2001:db8::1"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := ipRiskHandlerFixture()
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(tt.proxies))
			r.GET("/test", func(c *gin.Context) {
				// Legacy log resolution must not affect registration diagnostics.
				ippkg.SetLegacyForwardedIPTrust(c, true)
				h.GetIPRisk(c)
			})
			req := httptest.NewRequest(http.MethodGet, "/test", nil)
			req.RemoteAddr = tt.peer
			req.Header.Set("X-Forwarded-For", tt.xff)
			req.Header.Set("X-Real-IP", "192.0.2.99")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			var result struct {
				Data struct {
					CurrentIP     string `json:"current_ip"`
					CurrentPeerIP string `json:"current_peer_ip"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &result))
			require.Equal(t, tt.wantIP, result.Data.CurrentIP)
			require.Equal(t, tt.wantPeer, result.Data.CurrentPeerIP)
		})
	}
}

func mustIPRiskJSON(t *testing.T, c iprisk.Config) string {
	t.Helper()
	raw, err := json.Marshal(c)
	require.NoError(t, err)
	return string(raw)
}

func TestIPRiskHandlerRejectsInvalidAndSelfBlockingPolicy(t *testing.T) {
	h, repo := ipRiskHandlerFixture()
	c := iprisk.DefaultConfig()
	c.Enabled = true
	c.ObserveOnly = false
	c.Blacklist = []iprisk.Rule{{Network: "198.51.100.0/24", Enabled: true}}
	w := ipRiskHandlerRequest(t, h.SaveIPRisk, http.MethodPut, c)
	require.Equal(t, 400, w.Code)
	require.Contains(t, w.Body.String(), "your current IP")
	require.Empty(t, repo.values)
	c.Blacklist = []iprisk.Rule{{Network: "invalid", Enabled: true}}
	w = ipRiskHandlerRequest(t, h.SaveIPRisk, http.MethodPut, c)
	require.Equal(t, 400, w.Code)
	require.Empty(t, repo.values)
	c = iprisk.DefaultConfig()
	c.Enabled = true
	c.ObserveOnly = true
	w = ipRiskHandlerRequest(t, h.SaveIPRisk, http.MethodPut, c)
	require.Equal(t, 503, w.Code, "enabled policy requires Redis even in observation")
}

func TestIPRiskHandlerEmergencyRepairAndUnavailableManager(t *testing.T) {
	h, _ := ipRiskHandlerFixture()
	h.settingService.IPRisk = iprisk.New(h.settingService.IPRiskStore(), nil, nil, true)
	c := iprisk.DefaultConfig()
	c.Enabled = true
	c.ObserveOnly = false
	c.Blacklist = []iprisk.Rule{{Network: "198.51.100.0/24", Enabled: true}}
	w := ipRiskHandlerRequest(t, h.SaveIPRisk, http.MethodPut, c)
	require.Equal(t, 200, w.Code)
	w = ipRiskHandlerRequest(t, h.GetIPRisk, http.MethodGet, nil)
	require.Contains(t, w.Body.String(), `"emergency_disabled":true`)
	w = ipRiskHandlerRequest(t, h.CheckIPRisk, http.MethodPost, map[string]any{"ip": "198.51.100.1", "auth": true})
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"decision":"disabled"`)
	w = ipRiskHandlerRequest(t, h.UnbanIPRisk, http.MethodPost, map[string]string{"network": "invalid"})
	require.Equal(t, 400, w.Code)
	h.settingService.IPRisk = nil
	w = ipRiskHandlerRequest(t, h.GetIPRisk, http.MethodGet, nil)
	require.Equal(t, 503, w.Code)
}

func TestAnonymousAPIHandlerConfigurationAndScopedRelease(t *testing.T) {
	h, _ := ipRiskHandlerFixture()
	server := miniredis.RunT(t)
	m := testutil.NewRedisIPRiskManager(t, h.settingService.IPRiskStore(), server)
	h.settingService.IPRisk = m
	cfg := iprisk.DefaultConfig()
	cfg.AnonymousAPI.Enabled = true
	cfg.AnonymousAPI.Threshold = 2
	w := ipRiskHandlerRequest(t, h.SaveIPRisk, http.MethodPut, cfg)
	require.Equal(t, 200, w.Code)
	for i := 0; i < 2; i++ {
		_, err := m.AnonymousFailure(context.Background(), "8.8.8.8", "unknown")
		require.NoError(t, err)
	}
	w = ipRiskHandlerRequest(t, h.GetAnonymousAPIBans, http.MethodGet, nil)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), "anonymous_api_unknown")
	w = ipRiskHandlerRequest(t, h.UnbanAnonymousAPI, http.MethodPost, map[string]string{"network": "8.8.8.0/24"})
	require.Equal(t, 400, w.Code)
	var action string
	w = ipRiskHandlerRequest(t, func(c *gin.Context) {
		h.UnbanAnonymousAPI(c)
		value, _ := c.Get("audit_action")
		action, _ = value.(string)
	}, http.MethodPost, map[string]string{"network": "8.8.8.8"})
	require.Equal(t, 200, w.Code)
	require.Equal(t, "admin.ip_risk.anonymous.unban", action)
	w = ipRiskHandlerRequest(t, h.GetAnonymousAPIBans, http.MethodGet, nil)
	require.Equal(t, 200, w.Code)
	require.Contains(t, w.Body.String(), `"items":[]`)
}

func TestIPRiskHandlerEventPagination(t *testing.T) {
	h, _ := ipRiskHandlerFixture()
	server := miniredis.RunT(t)
	m := testutil.NewRedisIPRiskManager(t, h.settingService.IPRiskStore(), server)
	h.settingService.IPRisk = m
	cfg := iprisk.DefaultConfig()
	cfg.Enabled = true
	cfg.ObserveOnly = true
	cfg.LoginAttempts.Limit = 0
	require.NoError(t, m.Save(context.Background(), cfg))
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(m.Middleware())
	r.POST("/api/v1/auth/login", func(c *gin.Context) { c.Status(http.StatusOK) })
	for i := 0; i < 25; i++ {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		req.RemoteAddr = "198.51.100.1:1234"
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
	}
	for _, test := range []struct {
		query                           string
		page, size, total, pages, count int
	}{
		{"", 1, 20, 25, 2, 20},
		{"page=2&page_size=10&ip=198.51.100.1&category=login", 2, 10, 25, 3, 10},
		{"page=3&page_size=10", 3, 10, 25, 3, 5},
		{"page=99&page_size=10", 3, 10, 25, 3, 5},
		{"page=-1&page_size=invalid", 1, 20, 25, 2, 20},
		{"page=2&category=register", 1, 20, 0, 1, 0},
		{"reason=ip_blacklist", 1, 20, 0, 1, 0},
	} {
		t.Run(test.query, func(t *testing.T) {
			w := ipRiskHandlerRequest(t, func(c *gin.Context) {
				c.Request.URL.RawQuery = test.query
				h.GetIPRiskEvents(c)
			}, http.MethodGet, nil)
			require.Equal(t, http.StatusOK, w.Code)
			var response struct {
				Data iprisk.EventPage `json:"data"`
			}
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
			require.Equal(t, test.page, response.Data.Page)
			require.Equal(t, test.size, response.Data.PageSize)
			require.Equal(t, test.total, response.Data.Total)
			require.Equal(t, test.pages, response.Data.Pages)
			require.Len(t, response.Data.Items, test.count)
			require.NotNil(t, response.Data.Items)
		})
	}
	for _, query := range []string{"ip=" + url.QueryEscape("invalid"), "category=invalid"} {
		w := ipRiskHandlerRequest(t, func(c *gin.Context) {
			c.Request.URL.RawQuery = query
			h.GetIPRiskEvents(c)
		}, http.MethodGet, nil)
		require.Equal(t, http.StatusBadRequest, w.Code)
	}
}
