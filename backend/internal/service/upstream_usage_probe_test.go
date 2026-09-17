package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

type liveUsageProbeHTTPUpstream struct {
	client  *http.Client
	request *http.Request
	profile HTTPUpstreamProfile
}

func (u *liveUsageProbeHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return u.captureAndDo(req)
}

func (u *liveUsageProbeHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.captureAndDo(req)
}

func (u *liveUsageProbeHTTPUpstream) captureAndDo(req *http.Request) (*http.Response, error) {
	u.request = req.Clone(req.Context())
	u.profile = HTTPUpstreamProfileFromContext(req.Context())
	return u.client.Do(req)
}

func newLiveUsageProbeService(t *testing.T, status int, responseBody string) (*UpstreamBillingProbeService, *upstreamBillingProbeAccountRepo, *liveUsageProbeHTTPUpstream) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(responseBody))
	}))
	t.Cleanup(server.Close)

	const accountID int64 = 417
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{
		accountID: {
			ID:          accountID,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Concurrency: 3,
			Credentials: map[string]any{"api_key": "sk-usage-only", "base_url": server.URL},
			Extra:       map[string]any{},
		},
	}}
	upstream := &liveUsageProbeHTTPUpstream{client: &http.Client{
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}
	cfg := &config.Config{}
	cfg.Security.URLAllowlist.AllowInsecureHTTP = true
	accountTestService := NewAccountTestService(repo, nil, nil, nil, nil, upstream, cfg, nil)
	return NewUpstreamBillingProbeService(repo, accountTestService, nil), repo, upstream
}

func TestParseUpstreamUsageProbeResponseSanitizesWalletAndQuota(t *testing.T) {
	data, err := parseUpstreamUsageProbeResponse([]byte(`{
		"mode":"unrestricted","planName":"钱包余额","unit":"USD","isValid":true,
		"balance":10000724.41559168,"remaining":"10000724.41559168",
		"secret":"must-not-persist","usage":[{"tokens":123}],
		"quota":{"limit":100,"used":12.5,"remaining":87.5,"unit":"USD","secret":"x"}
	}`))
	require.NoError(t, err)
	require.Equal(t, "remaining", data["source_field"])
	require.Equal(t, "wallet", data["amount_kind"])
	require.Equal(t, 10000724.41559168, data["remaining"])
	require.NotContains(t, data, "secret")
	require.NotContains(t, data, "usage")
	require.Equal(t, map[string]any{"limit": float64(100), "used": 12.5, "remaining": 87.5, "unit": "USD"}, data["quota"])
}

func TestParseUpstreamUsageProbeResponseRejectsMissingOrNonFiniteAmount(t *testing.T) {
	for _, body := range []string{
		`{"mode":"x"}`,
		`{"remaining":"NaN"}`,
		`{"balance":"+Inf"}`,
		`{"quota":{"limit":10,"used":10}}`,
	} {
		_, err := parseUpstreamUsageProbeResponse([]byte(body))
		require.Error(t, err, body)
	}
}

func TestParseUpstreamUsageProbeResponseBoundsPersistedText(t *testing.T) {
	data, err := parseUpstreamUsageProbeResponse([]byte(`{"remaining":1,"unit":"` + strings.Repeat("U", 80) + `","planName":"` + strings.Repeat("计", 200) + `"}`))

	require.NoError(t, err)
	require.Len(t, []rune(data["unit"].(string)), 32)
	require.Len(t, []rune(data["planName"].(string)), 128)
}

func TestProbeUpstreamUsageSendsOnlyReadOnlyUsageRequestAndPersistsSanitizedData(t *testing.T) {
	svc, repo, upstream := newLiveUsageProbeService(t, http.StatusOK, `{
		"mode":"wallet","planName":"test","unit":"USD","isValid":true,
		"balance":52.75,"remaining":41.25,
		"secret":"must-not-persist","usage":[{"model":"gpt","tokens":999}],
		"quota":{"limit":100,"used":58.75,"remaining":41.25,"unit":"USD","secret":"drop"}
	}`)

	snapshot, err := svc.ProbeUpstreamUsage(context.Background(), 417)

	require.NoError(t, err)
	require.Equal(t, UpstreamUsageProbeStatusOK, snapshot.Status)
	require.Equal(t, 41.25, snapshot.Data["remaining"])
	require.NotContains(t, snapshot.Data, "secret")
	require.NotContains(t, snapshot.Data, "usage")
	require.NotNil(t, upstream.request)
	require.Equal(t, http.MethodGet, upstream.request.Method)
	require.Equal(t, "/v1/usage", upstream.request.URL.Path)
	require.Equal(t, "Bearer sk-usage-only", upstream.request.Header.Get("Authorization"))
	require.Equal(t, int64(0), upstream.request.ContentLength)
	require.True(t, HTTPUpstreamRedirectsDisabled(upstream.request.Context()))
	require.Equal(t, HTTPUpstreamProfileOpenAI, upstream.profile)

	storedAccount, err := repo.GetByID(context.Background(), 417)
	require.NoError(t, err)
	stored := decodeUpstreamUsageProbeSnapshot(storedAccount.Extra)
	require.NotNil(t, stored)
	encoded, err := json.Marshal(stored)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "must-not-persist")
	require.NotContains(t, string(encoded), `"usage"`)
	require.NotContains(t, string(encoded), `"tokens"`)
}

func TestProbeUpstreamUsageMarksMissingEndpointUnsupported(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusMethodNotAllowed} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			svc, _, upstream := newLiveUsageProbeService(t, status, `{"error":"not available"}`)

			snapshot, err := svc.ProbeUpstreamUsage(context.Background(), 417)

			require.NoError(t, err)
			require.Equal(t, UpstreamUsageProbeStatusUnsupported, snapshot.Status)
			require.Equal(t, status, snapshot.HTTPStatus)
			require.Equal(t, "unsupported", snapshot.LastError)
			require.Equal(t, "/v1/usage", upstream.request.URL.Path)
		})
	}
}

func TestProbeUpstreamUsageRejectsOversizedResponseWithoutPersistingIt(t *testing.T) {
	oversized := strings.Repeat("x", upstreamUsageProbeMaxBodyBytes+1)
	svc, repo, _ := newLiveUsageProbeService(t, http.StatusOK, oversized)

	snapshot, err := svc.ProbeUpstreamUsage(context.Background(), 417)

	require.NoError(t, err)
	require.Equal(t, UpstreamUsageProbeStatusFailed, snapshot.Status)
	require.Equal(t, "response_too_large", snapshot.LastError)
	storedAccount, err := repo.GetByID(context.Background(), 417)
	require.NoError(t, err)
	encoded, err := json.Marshal(storedAccount.Extra[UpstreamUsageProbeExtraKey])
	require.NoError(t, err)
	require.Less(t, len(encoded), 1024)
	require.NotContains(t, string(encoded), strings.Repeat("x", 32))
}

func TestProbeUpstreamUsageRecordsUnavailableProxyWithoutSendingRequest(t *testing.T) {
	svc, repo, upstream := newLiveUsageProbeService(t, http.StatusOK, `{"remaining":12.5}`)
	proxyID := int64(9)
	repo.accounts[417].ProxyID = &proxyID

	snapshot, err := svc.ProbeUpstreamUsage(context.Background(), 417)

	require.NoError(t, err)
	require.Equal(t, UpstreamUsageProbeStatusFailed, snapshot.Status)
	require.Equal(t, "proxy_unavailable", snapshot.LastError)
	require.Nil(t, upstream.request)
}
