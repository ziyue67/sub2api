package service

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func newImageOwnerService(t *testing.T, origin string) *OpenAIGatewayService {
	t.Helper()
	s := &OpenAIGatewayService{cfg: &config.Config{Runtime: config.RuntimeConfig{ImageRelayOwnerOrigin: origin}}}
	t.Cleanup(func() { require.NoError(t, s.CloseExcelBPSImages()) })
	return s
}

func ownerImageSettings() ExcelBPSImageRelaySettings {
	return ExcelBPSImageRelaySettings{Enabled: true, Mode: ExcelBPSImageModeRelay, BaseURL: "https://shared.example.invalid", Limits: basispoints.DefaultImageRelayLimits()}
}

func TestImageRelayOwnerOriginKeepsCapabilitiesOnGeneratingNode(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	settings := ownerImageSettings()
	nodeA := newImageOwnerService(t, "https://node-a.example.invalid/")
	nodeB := newImageOwnerService(t, "https://node-b.example.invalid")
	relayA, err := nodeA.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	relayB, err := nodeB.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	body, pixels := nativeGatewayBody(t)
	rewrite := func(relay *basispoints.ImageRelay, scope string) string {
		t.Helper()
		out, err := relay.Rewrite(body, scope)
		require.NoError(t, err)
		return gjson.GetBytes(out, "input.0.content.0.image_url").String()
	}
	urlA := rewrite(relayA, "account:201/key:101/thread:a")
	urlB := rewrite(relayB, "account:201/key:101/thread:a")
	require.True(t, strings.HasPrefix(urlA, "https://node-a.example.invalid"+basispoints.ImageRelayPath))
	require.True(t, strings.HasPrefix(urlB, "https://node-b.example.invalid"+basispoints.ImageRelayPath))
	require.NotEqual(t, urlA, urlB)
	require.Equal(t, "https://shared.example.invalid", settings.BaseURL, "node override must not mutate shared settings")
	probe := func(relay *basispoints.ImageRelay, method, target string, want int) {
		t.Helper()
		// Exercise net/http's actual HEAD semantics: NotFound writes an error
		// body to a raw ResponseRecorder, while the server suppresses it on HEAD.
		server := httptest.NewServer(relay)
		defer server.Close()
		capability, err := url.Parse(target)
		require.NoError(t, err)
		request, err := http.NewRequest(method, server.URL+capability.RequestURI(), nil)
		require.NoError(t, err)
		request.Host = capability.Host
		client := server.Client()
		client.Timeout = 2 * time.Second
		response, err := client.Do(request)
		require.NoError(t, err)
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		require.NoError(t, err)
		require.Equal(t, want, response.StatusCode)
		require.Equal(t, "private, no-store", response.Header.Get("Cache-Control"))
		if want == http.StatusOK && method == http.MethodGet {
			require.Equal(t, pixels, body)
		}
		if method == http.MethodHead {
			require.Empty(t, body)
		}
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		probe(relayA, method, urlA, http.StatusOK)
		probe(relayB, method, urlB, http.StatusOK)
		probe(relayB, method, urlA, http.StatusNotFound)
		probe(relayA, method, urlB, http.StatusNotFound)
	}
	// Possession of a capability is intentionally sufficient for the upstream
	// GET; authenticated creation scopes still produce distinct opaque tokens.
	otherScope := rewrite(relayA, "account:201/key:102/thread:a")
	require.NotEqual(t, urlA, otherScope)
	tampered := urlA[:len(urlA)-1] + "A"
	if tampered == urlA {
		tampered = urlA[:len(urlA)-1] + "B"
	}
	probe(relayA, http.MethodGet, tampered, http.StatusNotFound)
	// A subsequent shared setting refresh cannot reset the node's own origin.
	settings.BaseURL = "https://changed-shared.example.invalid"
	again, err := nodeA.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	require.Same(t, relayA, again)
	require.True(t, strings.HasPrefix(rewrite(again, "account:201/key:101/thread:b"), "https://node-a.example.invalid/"))
	probe(relayA, http.MethodGet, urlA, http.StatusOK)
	// A new process/relay has a different private key and empty index. Same
	// origin and data directory do not imply restart or cross-node recovery.
	require.NoError(t, nodeA.CloseExcelBPSImages())
	restarted := newImageOwnerService(t, "https://node-a.example.invalid")
	afterRestart, err := restarted.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	probe(afterRestart, http.MethodGet, urlA, http.StatusNotFound)
}

func TestImageRelayOwnerOriginLegacyAndModeCompatibility(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	settings := ownerImageSettings()
	legacy := newImageOwnerService(t, "")
	relay, err := legacy.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	body, _ := nativeGatewayBody(t)
	out, err := relay.Rewrite(body, "fixture-scope")
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(gjson.GetBytes(out, "input.0.content.0.image_url").String(), settings.BaseURL+"/"))
	invalid := newImageOwnerService(t, "https://node-a.example.invalid?token=fixture")
	_, err = invalid.excelBPSImageRelayForSettings(settings)
	require.Error(t, err)
	require.NotContains(t, err.Error(), "token=fixture")
	require.Nil(t, invalid.excelBPSImages, "bad owner config must fail before creating storage")
	owner := newImageOwnerService(t, "https://node-a.example.invalid")
	settings.Enabled = false
	got, err := owner.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	require.Nil(t, got, "owner override cannot turn on a disabled relay")
	settings.Enabled, settings.Mode = true, ExcelBPSImageModeNative
	got, err = owner.excelBPSImageRelayForSettings(settings)
	require.NoError(t, err)
	require.Nil(t, got, "native upstream attachment mode must not create a local relay")
}
