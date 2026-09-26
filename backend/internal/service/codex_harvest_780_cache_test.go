package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestCodex780RouteSurvivesModelMismatchAndSharesAcrossModels(t *testing.T) {
	account := ticketTestAccount(1)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{OpenAICodexTicket: config.OpenAICodexTicketConfig{TargetLength: 780}}}}
	pair := mint780Pair(time.Now().Add(time.Hour), "unified-88")
	calls := 0
	svc.httpUpstream = &harvestProxyUpstream{do: func(req *http.Request, _ string) (*http.Response, error) {
		calls++
		header := http.Header{}
		declared := "gpt-6-astra"
		switch calls {
		case 1:
			require.Empty(t, req.Header.Get("Cookie"))
			header["Set-Cookie"] = append(append([]string{}, pair...), "session=DO_NOT_CACHE")
			declared = "gpt-6-luna"
		case 2, 3:
			require.Equal(t, strings.Join(pair, "; "), req.Header.Get("Cookie"))
			if calls == 3 {
				declared = "gpt-6-sol"
			}
		default:
			require.Empty(t, req.Header.Get("Cookie"), "credentials and accounts must be isolated")
			header["Set-Cookie"] = pair
		}
		header.Set(openAICodexTurnStateHeader, mint780State(time.Now()))
		body := fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"r\",\"model\":%q}}\n\n", declared)
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
	}}
	probe := func(a *Account, token, model string) codexHarvestProbeResult {
		return svc.executeCodexHarvestProbe(context.Background(), a, token, model, "", time.Second, func() bool { return true }, "session")
	}
	require.Equal(t, "model_mismatch", probe(account, "token", "gpt-6-astra").Kind)
	require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"), "a route must not count as an accepted ticket")
	require.Equal(t, "success", probe(account, "token", "gpt-6-astra").Kind)
	require.Equal(t, "success", probe(account, "token", "gpt-6-sol").Kind)
	require.Equal(t, "success", probe(account, "new-token", "gpt-6-astra").Kind)
	require.Equal(t, "success", probe(ticketTestAccount(2), "token", "gpt-6-astra").Kind)
}

func TestCodex780RouteCacheExpiryInvalidationAndBounds(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	var cache codex780RouteCache
	a := ticketTestAccount(1)
	key := codex780RouteKey(a, "token", "account", "unified-88", "sse")
	pair := mint780Pair(now.Add(time.Hour), "unified-88")
	good := codexHarvestProbeResult{Sent: true, Status: 200, Cookies: pair}
	cache.update(key, "unified-88", nil, good, now)
	got, ok := cache.get(key, now.Add(241*time.Second))
	require.True(t, ok, "cookie remains valid after ticket lifetime")
	require.Equal(t, pair, got)
	got[0] = "changed"
	again, _ := cache.get(key, now)
	require.Equal(t, pair, again, "callers cannot mutate cached cookies")
	for _, other := range [][32]byte{
		codex780RouteKey(a, "other", "account", "unified-88", "sse"),
		codex780RouteKey(a, "token", "other", "unified-88", "sse"),
		codex780RouteKey(a, "token", "account", "unified-95", "sse"),
		codex780RouteKey(a, "token", "account", "unified-88", "websocket"),
	} {
		_, found := cache.get(other, now)
		require.False(t, found)
	}
	bad := codexHarvestProbeResult{Sent: true, Status: 200, Err: mintRouteError("partial_rotation")}
	cache.update(key, "unified-88", pair, bad, now)
	got, ok = cache.get(key, now)
	require.True(t, ok, "tombstone prevents fallback to persisted rejected pair")
	require.Empty(t, got)
	cache.update(key, "unified-88", pair, good, now)
	got, _ = cache.get(key, now)
	require.Empty(t, got, "late unchanged responses must not undo invalidation")
	newPair := append([]string{}, pair...)
	newPair[0] = "__cflb=new-route"
	good.Cookies = newPair
	cache.update(key, "unified-88", nil, good, now)
	cache.update(key, "unified-88", pair, bad, now)
	got, ok = cache.get(key, now)
	require.True(t, ok)
	require.Equal(t, newPair, got, "late failure must preserve the newer pair")
	_, ok = cache.get(key, now.Add(time.Hour))
	require.False(t, ok)
	var wg sync.WaitGroup
	for i := range codex780RouteCacheLimit + 20 {
		wg.Go(func() {
			k := codex780RouteKey(a, fmt.Sprint(i), "account", "unified-88", "sse")
			cache.update(k, "unified-88", nil, good, now)
			cache.get(k, now)
		})
	}
	wg.Wait()
	require.Len(t, cache.entries, codex780RouteCacheLimit)
}

func TestCodex780RouteCacheRejectsOffTargetAndUnsentResults(t *testing.T) {
	var cache codex780RouteCache
	now := time.Now()
	key := codex780RouteKey(ticketTestAccount(1), "token", "account", "unified-88", "sse")
	for _, result := range []codexHarvestProbeResult{
		{Sent: true, Status: 200, Cookies: mint780Pair(now.Add(time.Hour), "unified-95")},
		{Sent: true, Status: 200, Cookies: mint780Pair(now.Add(-time.Second), "unified-88")},
		{Sent: false, Status: 200, Cookies: mint780Pair(now.Add(time.Hour), "unified-88")},
		{Sent: true, Status: 403, Cookies: mint780Pair(now.Add(time.Hour), "unified-88")},
	} {
		cache.update(key, "unified-88", nil, result, now)
		_, found := cache.get(key, now)
		require.False(t, found)
	}
}

func TestCodex780RouteCookieDeletionAndAliases(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pair := mint780Pair(now.Add(time.Hour), "unified-88")
	for _, suffix := range []string{"; Max-Age=0", "; Max-Age=-1", "; Expires=" + now.Add(-time.Hour).Format(http.TimeFormat)} {
		resp := &http.Response{Header: http.Header{"Set-Cookie": {pair[0] + suffix, pair[1]}}}
		_, err := codex780ResponseRoute(resp, pair, "unified-88", now)
		require.EqualError(t, err, "route_cookie_deleted")
	}
	for _, input := range []string{"88", "unified88", "unified_88", "unified.88", " UNIFIED-88 ", "chat.gateway.unified-88.api.openai.com"} {
		v := CodexHarvestControls{Version: 1, TargetGateway: input, Speed: CodexHarvestSpeedPresets()["standard"]}
		normalizeCodexHarvestControls(&v)
		require.Equal(t, "unified-88", v.TargetGateway)
		require.NoError(t, ValidateCodexHarvestControls(v))
	}
}
