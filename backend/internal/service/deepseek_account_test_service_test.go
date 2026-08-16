//go:build unit

package service

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func deepSeekAccountTestFixture(baseURL string) *Account {
	credentials := map[string]any{"api_key": "deepseek-secret"}
	if strings.TrimSpace(baseURL) != "" {
		credentials["base_url"] = baseURL
	}
	return &Account{
		ID:          901,
		Name:        "deepseek-apikey",
		Platform:    PlatformDeepSeek,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: credentials,
	}
}

func TestDeepSeekDefaultModelsMatchOfficialHarness(t *testing.T) {
	t.Parallel()

	models := DeepSeekDefaultModelIDs()
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, models)
	models[0] = "mutated"
	require.Equal(t, "deepseek-v4-flash", DeepSeekDefaultModelIDs()[0], "callers must receive a detached catalog")
}

func TestBuildDeepSeekModelsURLUsesProviderRoot(t *testing.T) {
	t.Parallel()

	require.Equal(t, "https://api.deepseek.com/models", buildDeepSeekModelsURL("https://api.deepseek.com"))
	require.Equal(t, "https://relay.example/deepseek/models", buildDeepSeekModelsURL("https://relay.example/deepseek"))
	require.Equal(t, "https://relay.example/models", buildDeepSeekModelsURL("https://relay.example/models"))
}

func TestBuildDeepSeekUpstreamModelsRequest(t *testing.T) {
	t.Parallel()

	svc := &AccountTestService{cfg: upstreamModelSyncTestConfig()}
	req, err := svc.buildUpstreamModelsRequest(context.Background(), deepSeekAccountTestFixture(""))
	require.NoError(t, err)
	require.Equal(t, http.MethodGet, req.Method)
	require.Equal(t, "https://api.deepseek.com/models", req.URL.String())
	require.Equal(t, "application/json", req.Header.Get("Accept"))
	require.Equal(t, "Bearer deepseek-secret", req.Header.Get("Authorization"))
	require.Equal(t, HTTPUpstreamProfileOpenAI, HTTPUpstreamProfileFromContext(req.Context()))
	require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))

	customReq, err := svc.buildUpstreamModelsRequest(context.Background(), deepSeekAccountTestFixture("https://relay.example/deepseek"))
	require.NoError(t, err)
	require.Equal(t, "https://relay.example/deepseek/models", customReq.URL.String())

	_, err = svc.buildUpstreamModelsRequest(context.Background(), deepSeekAccountTestFixture("https://relay.example/v1"))
	require.Error(t, err, "custom DeepSeek roots must not include a version suffix")
}

func TestFetchDeepSeekUpstreamSupportedModels(t *testing.T) {
	t.Parallel()

	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"object":"list","data":[{"id":"deepseek-v4-pro"},{"id":"deepseek-v4-flash"},{"id":"deepseek-v4-pro"}]}`,
		)),
	}}
	svc := &AccountTestService{httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}

	models, err := svc.FetchUpstreamSupportedModels(context.Background(), deepSeekAccountTestFixture(""))
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, models)
	require.Equal(t, http.MethodGet, upstream.lastReq.Method)
	require.Equal(t, "https://api.deepseek.com/models", upstream.lastReq.URL.String())
}

func TestAccountTestServiceDispatchesDeepSeekToModelsProbe(t *testing.T) {
	account := deepSeekAccountTestFixture("")
	repo := &mockAccountRepoForGemini{accountsByID: map[int64]*Account{account.ID: account}}
	upstream := &queuedHTTPUpstream{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"data":[{"id":"deepseek-v4-flash"},{"id":"deepseek-v4-pro"}]}`)),
	}}}
	svc := &AccountTestService{accountRepo: repo, httpUpstream: upstream, cfg: upstreamModelSyncTestConfig()}
	c, recorder := newTestContext()

	err := svc.TestAccountConnection(c, account.ID, "", "", AccountTestModeDefault)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 1)
	require.Equal(t, http.MethodGet, upstream.requests[0].Method)
	require.Equal(t, "https://api.deepseek.com/models", upstream.requests[0].URL.String())
	require.Equal(t, "Bearer deepseek-secret", upstream.requests[0].Header.Get("Authorization"))
	require.Contains(t, recorder.Body.String(), `"type":"test_start"`)
	require.Contains(t, recorder.Body.String(), `"model":"deepseek-v4-flash"`)
	require.Contains(t, recorder.Body.String(), "deepseek-v4-flash, deepseek-v4-pro")
	require.Contains(t, recorder.Body.String(), `"type":"test_complete"`)
}
