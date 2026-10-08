package service

import (
	"context"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type newAPIHTTP struct {
	HTTPUpstream
	bodies map[string]string
	check  func(*http.Request)
}

func (h *newAPIHTTP) DoWithTLS(r *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	if h.check != nil {
		h.check(r)
	}
	b, ok := h.bodies[r.Method+" "+r.URL.RequestURI()]
	code := 200
	if !ok {
		code = 404
		b = `{}`
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(b)), Header: http.Header{}}, nil
}
func TestNewAPICanonicalSiteKeepsTenantAndPort(t *testing.T) {
	for _, tt := range []struct{ in, out string }{{"https://EXAMPLE.com:443/tenant/v1/", "https://example.com/tenant"}, {"http://example.com:8080/v1", "http://example.com:8080"}, {"https://example.com/v10", "https://example.com/v10"}} {
		got, e := CanonicalNewAPISite(tt.in)
		require.NoError(t, e)
		require.Equal(t, tt.out, got)
	}
	for _, bad := range []string{"https://a:b@example.com", "https://example.com/#secret", "file:///tmp/a", "https://example.com/?x=1"} {
		_, e := CanonicalNewAPISite(bad)
		require.Error(t, e)
	}
}
func TestNewAPIWalletAndUserContextAndMaskMatching(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{"base_url": "https://example.com/tenant/v1", "api_key": "sk-abcd123456789wxyz", "header_override_enabled": true, "header_overrides": map[string]any{"Authorization": "Bearer foreign", "new-api-user": "99", "cookie": "session=foreign"}}}
	h := &newAPIHTTP{bodies: map[string]string{"GET /tenant/api/status": `{"success":true,"data":{"quota_per_unit":500000}}`, "GET /tenant/api/user/self": `{"success":true,"data":{"id":7,"quota":1250000}}`, "GET /tenant/api/pricing": `{"success":true,"group_ratio":{"vip":0.5}}`, "GET /tenant/api/token/?p=0&page_size=100": `{"success":true,"data":{"items":[{"id":3,"key":"abcd**********wxyz","group":"vip","name":"mine","remain_quota":999999999}],"total":1,"p":0,"page_size":100}}`}, check: func(r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/status") {
			return
		}
		require.Equal(t, "Bearer user-secret", r.Header.Get("Authorization"))
		require.Equal(t, "7", r.Header.Get("New-Api-User"))
		for _, name := range []string{"Authorization", "New-Api-User"} {
			count := 0
			for key, values := range r.Header {
				if strings.EqualFold(key, name) {
					count += len(values)
				}
			}
			require.Equal(t, 1, count, name)
		}
		for key := range r.Header {
			require.False(t, strings.EqualFold(key, "Cookie"))
		}
	}}
	s := newUpstreamBillingProbeTestService(nil, h, nil)
	result, e := s.fetchNewAPI(context.Background(), a, "https://example.com/tenant", 7, "user-secret")
	require.NoError(t, e)
	require.Equal(t, 2.5, result.wallet)
	matched := matchNewAPITokens(a, result.tokens)
	require.Len(t, matched, 1)
	require.Equal(t, int64(3), matched[0].ID)
	snapshot := newAPISnapshot(a, matched[0], result, 30, time.Now())
	require.Equal(t, "ok", snapshot.Status)
	require.Equal(t, 2.5, snapshot.Balance.Data["wallet_balance"])
	_, ok := snapshot.CostMultiplierToSync()
	require.False(t, ok)
	result.ratios = map[string]float64{}
	snapshot = newAPISnapshot(a, matched[0], result, 30, time.Now())
	require.Equal(t, "unsupported", snapshot.Status)
	require.Equal(t, "ok", snapshot.Balance.Status)
}
func TestNewAPIRejectsInvalidConversionIdentityAndPagination(t *testing.T) {
	a := &Account{ID: 1, Credentials: map[string]any{}}
	for _, tt := range []struct{ name, status, user, tokens string }{{"no_conversion", `{}`, `{"id":7,"quota":5}`, `[]`}, {"zero_conversion", `{"quota_per_unit":0}`, `{"id":7,"quota":5}`, `[]`}, {"identity", `{"quota_per_unit":1}`, `{"id":8,"quota":5}`, `[]`}, {"truncated_inventory", `{"quota_per_unit":1}`, `{"id":7,"quota":5}`, `{"items":[],"total":1,"p":0,"page_size":100}`}} {
		t.Run(tt.name, func(t *testing.T) {
			h := &newAPIHTTP{bodies: map[string]string{"GET /api/status": `{"success":true,"data":` + tt.status + `}`, "GET /api/user/self": `{"success":true,"data":` + tt.user + `}`, "GET /api/pricing": `{"success":true,"group_ratio":{}}`, "GET /api/token/?p=0&page_size=100": `{"success":true,"data":` + tt.tokens + `}`}}
			s := newUpstreamBillingProbeTestService(nil, h, nil)
			_, e := s.fetchNewAPI(context.Background(), a, "https://example.com", 7, "secret-do-not-leak")
			require.Error(t, e)
			require.NotContains(t, e.Error(), "secret-do-not-leak")
		})
	}
}

func TestNewAPIOfficialOneBasedPageEnvelope(t *testing.T) {
	a := &Account{ID: 1, Credentials: map[string]any{}}
	h := &newAPIHTTP{bodies: map[string]string{"GET /api/status": `{"success":true,"data":{"quota_per_unit":1}}`, "GET /api/user/self": `{"success":true,"data":{"id":7,"quota":5}}`, "GET /api/pricing": `{"success":true,"group_ratio":{"vip":0.2}}`, "GET /api/token/?p=0&page_size=100": `{"success":true,"data":{"items":[{"id":3,"key":"abcd**********wxyz","group":"vip"}],"total":1,"page":1,"page_size":100}}`}}
	s := newUpstreamBillingProbeTestService(nil, h, nil)
	d, e := s.fetchNewAPI(context.Background(), a, "https://example.com", 7, "secret")
	require.NoError(t, e)
	require.Len(t, d.tokens, 1)
}
func TestNewAPIMultiRouteGroupIsUnsupported(t *testing.T) {
	var token NewAPIToken
	require.NoError(t, json.Unmarshal([]byte(`{"id":3,"key":"abcd**********wxyz","group":"vip","cross_group_retry":true,"route_groups":["vip","default"]}`), &token))
	snap := newAPISnapshot(&Account{}, token, &newAPIData{wallet: 2.5, ratios: map[string]float64{"vip": 0.5}}, 30, time.Now())
	require.Equal(t, UpstreamBillingProbeStatusUnsupported, snap.Status)
	require.Equal(t, UpstreamBillingProbeStatusOK, snap.Balance.Status)
}
func TestNewAPIOlderArrayInventoryDoesNotStopOnShortPage(t *testing.T) {
	a := &Account{ID: 1, Credentials: map[string]any{}}
	h := &newAPIHTTP{bodies: map[string]string{"GET /api/status": `{"success":true,"data":{"quota_per_unit":1}}`, "GET /api/user/self": `{"success":true,"data":{"id":7,"quota":5}}`, "GET /api/pricing": `{"success":true,"group_ratio":{}}`, "GET /api/token/?p=0&page_size=100": `{"success":true,"data":[{"id":3,"key":"first","group":"vip"}]}`, "GET /api/token/?p=1&page_size=100": `{"success":true,"data":[{"id":4,"key":"second","group":"vip"}]}`, "GET /api/token/?p=2&page_size=100": `{"success":true,"data":[]}`}}
	d, e := newUpstreamBillingProbeTestService(nil, h, nil).fetchNewAPI(context.Background(), a, "https://example.com", 7, "secret")
	require.NoError(t, e)
	require.Len(t, d.tokens, 2)
}
func TestNewAPICostSyncRejectsGroupScopeEvenWithoutProvider(t *testing.T) {
	snap := &UpstreamBillingProbeSnapshot{Status: UpstreamBillingProbeStatusOK, LastAttemptAt: time.Now(), Data: map[string]any{"billing_scope": "group", "resolved_rate_multiplier": 0.5, "effective_rate_multiplier": 0.5, "peak_rate_enabled": false}}
	_, ok := snap.CostMultiplierToSync()
	require.False(t, ok)
}
