package repository

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type gatewayPinDelegate struct {
	call func(*http.Request, string, int64, int, *tlsfingerprint.Profile) (*http.Response, error)
}

func (d gatewayPinDelegate) Do(r *http.Request, p string, a int64, n int) (*http.Response, error) {
	return d.call(r, p, a, n, nil)
}
func (d gatewayPinDelegate) DoWithTLS(r *http.Request, p string, a int64, n int, f *tlsfingerprint.Profile) (*http.Response, error) {
	return d.call(r, p, a, n, f)
}
func pinResponse(cookie string) *http.Response {
	h := make(http.Header)
	h.Set("Content-Type", "text/event-stream")
	h.Set("X-Codex-Turn-State", "fixture-state")
	if cookie != "" {
		h.Add("Set-Cookie", cookie)
	}
	return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(`data: {"type":"response.completed","response":{"id":"resp_test","model":"gpt-6-astra","status":"completed","output":[{"content":[{"type":"output_text","text":"21"}]}]}}` + "\n\n"))}
}
func pinRequest(t *testing.T) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(service.WithAstraSourceAcquisition(t.Context()), http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-6-astra"}`))
	require.NoError(t, err)
	return r
}
func pinConfig() config.CodexGatewayPinConfig {
	return config.CodexGatewayPinConfig{Enabled: true, SourceAccountIDs: []int64{299, 298}, TargetAccountIDs: []int64{300}}
}

func TestCodexGatewayPinAccountIsolation(t *testing.T) {
	source := pinRequest(t)
	source.Header.Set("Authorization", "Bearer source-test-token")
	source.Header.Set("ChatGPT-Account-ID", "source-test-account")
	target := pinRequest(t)
	target.Header.Set("Authorization", "Bearer target-test-token")
	target.Header.Set("ChatGPT-Account-ID", "target-test-account")
	target.Header.Set("X-Codex-Turn-State", "target-test-state")
	target.Header.Set("Session_id", "target-test-session")
	target.Header.Add("Cookie", "__cflb=target-cf; __oailb=old-route")
	target.Header.Add("Cookie", "custom=keep; __oailb=duplicate-route")
	calls := 0
	profile := &tlsfingerprint.Profile{Name: "test"}
	pin := &codexGatewayPinUpstream{config: pinConfig()}
	pin.delegate = gatewayPinDelegate{call: func(r *http.Request, proxy string, id int64, concurrency int, f *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.True(t, service.HTTPUpstreamRedirectsDisabled(r.Context()))
		if id == 299 {
			require.Equal(t, "Bearer source-test-token", r.Header.Get("Authorization"))
			resp := pinResponse("__oailb=source-route; Path=/; Secure; Max-Age=3600")
			resp.Header.Add("Set-Cookie", "chatgpt_session=never-copy; Path=/; Secure")
			resp.Header.Add("Set-Cookie", "__cf_bm=never-copy; Path=/; Secure")
			return resp, nil
		}
		require.Equal(t, int64(300), id)
		require.Equal(t, "target-proxy", proxy)
		require.Equal(t, 10, concurrency)
		require.Same(t, profile, f)
		require.Equal(t, "Bearer target-test-token", r.Header.Get("Authorization"))
		require.Equal(t, "target-test-account", r.Header.Get("ChatGPT-Account-ID"))
		require.Equal(t, "target-test-state", r.Header.Get("X-Codex-Turn-State"))
		require.Equal(t, "target-test-session", r.Header.Get("Session_id"))
		require.Equal(t, "__cflb=target-cf; custom=keep; __oailb=source-route", r.Header.Get("Cookie"))
		// A target response must never overwrite the source route.
		return pinResponse("__oailb=target-route; Path=/; Secure; Max-Age=3600"), nil
	}}
	_, err := pin.Do(target, "", 300, 10)
	require.ErrorIs(t, err, errCodexGatewayPinUnavailable)
	require.Zero(t, calls)
	resp, err := pin.Do(source, "", 299, 10)
	require.NoError(t, err)
	_, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	require.NoError(t, resp.Body.Close())
	resp, err = pin.DoWithTLS(target, "target-proxy", 300, 10, profile)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, []string{"__cflb=target-cf; __oailb=old-route", "custom=keep; __oailb=duplicate-route"}, target.Header.Values("Cookie"))
	require.Equal(t, "source-route", func() string { c, _ := pin.currentCookie(target.URL.Path, time.Now()); return c.Value }())
}

func TestCodexGatewayPinLifetime(t *testing.T) {
	now := time.Now()
	path := "/backend-api/codex/responses"
	pin := &codexGatewayPinUpstream{config: pinConfig()}
	route := codexGatewayRouteFromResponse(pinResponse("__oailb=first; Path=/backend-api; Secure; Max-Age=3600"), path, now)
	require.Equal(t, now.Add(230*time.Second), route.expires)
	pin.recordSource(299, now, route, true)
	c, id := pin.currentCookie(path, now.Add(229*time.Second))
	require.Equal(t, int64(299), id)
	require.Equal(t, "first", c.Value)
	c, _ = pin.currentCookie(path, now.Add(230*time.Second))
	require.Nil(t, c)
	route = codexGatewayRouteFromResponse(pinResponse("__oailb=second; Path=/; Secure; Max-Age=60"), path, now)
	pin.recordSource(298, now, route, true)
	c, id = pin.currentCookie(path, now)
	require.Equal(t, int64(298), id)
	require.Equal(t, "second", c.Value)
	pin.recordSource(298, now.Add(time.Second), nil, false)
	c, _ = pin.currentCookie(path, now)
	require.Nil(t, c)
	// An older in-flight success cannot revive a route rejected by a newer request.
	pin.recordSource(298, now, route, true)
	c, _ = pin.currentCookie(path, now)
	require.Nil(t, c)
}

func TestCodexGatewayPinRejectsUnusableCookies(t *testing.T) {
	now := time.Now()
	for _, cookie := range []string{
		"chatgpt_session=auth; Path=/; Secure; Max-Age=3600",
		"__oailb=insecure; Path=/; Max-Age=3600",
		"__oailb=foreign; Domain=example.com; Path=/; Secure; Max-Age=3600",
		"__oailb=outside; Path=/outside; Secure; Max-Age=3600",
		"__oailb=session; Path=/; Secure",
	} {
		t.Run(cookie, func(t *testing.T) {
			require.Nil(t, codexGatewayRouteFromResponse(pinResponse(cookie), "/backend-api/codex/responses", now))
		})
	}
	deleted := codexGatewayRouteFromResponse(pinResponse("__oailb=; Path=/; Secure; Max-Age=0"), "/backend-api/codex/responses", now)
	require.NotNil(t, deleted)
	require.Empty(t, deleted.cookie.Value)
}

func TestCodexGatewayPinScopeAndDisabled(t *testing.T) {
	for _, tc := range []struct {
		name, url, method string
		id                int64
		disabled          bool
	}{
		{"unrelated account", "https://chatgpt.com/backend-api/codex/responses", "POST", 301, false},
		{"api key endpoint", "https://api.openai.com/v1/responses", "POST", 300, false},
		{"plain HTTP", "http://chatgpt.com/backend-api/codex/responses", "POST", 300, false},
		{"websocket", "wss://chatgpt.com/backend-api/codex/responses", "GET", 300, false},
		{"compact", "https://chatgpt.com/backend-api/codex/responses/compact", "POST", 300, false},
		{"disabled", "https://chatgpt.com/backend-api/codex/responses", "POST", 300, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := pinConfig()
			cfg.Enabled = !tc.disabled
			pin := &codexGatewayPinUpstream{config: cfg}
			r, err := http.NewRequestWithContext(t.Context(), tc.method, tc.url, nil)
			require.NoError(t, err)
			r.Header.Set("Cookie", "__oailb=original")
			pin.delegate = gatewayPinDelegate{call: func(got *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
				require.Same(t, r, got)
				return pinResponse(""), nil
			}}
			resp, err := pin.Do(r, "", tc.id, 1)
			require.NoError(t, err)
			require.NoError(t, resp.Body.Close())
		})
	}
	require.IsType(t, &httpUpstreamService{}, NewHTTPUpstream(nil))
	require.IsType(t, &httpUpstreamService{}, NewHTTPUpstream(&config.Config{}))
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	require.IsType(t, &astraRoutingUpstream{}, NewHTTPUpstream(cfg))
}

func TestCodexGatewayPinConcurrentCaptureAndRead(t *testing.T) {
	pin := &codexGatewayPinUpstream{config: pinConfig()}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			for range 20 {
				now := time.Now()
				route := codexGatewayRouteFromResponse(pinResponse("__oailb=route; Path=/; Secure; Max-Age=3600"), "/backend-api/codex/responses", now)
				pin.recordSource(299, now, route, true)
				_, _ = pin.currentCookie("/backend-api/codex/responses", now)
			}
		})
	}
	wg.Wait()
	c, _ := pin.currentCookie("/backend-api/codex/responses", time.Now())
	require.Equal(t, "route", c.Value)
}

func TestCodexGatewayPinRequiresCompletedAstra(t *testing.T) {
	good := `data: {"type":"response.completed","response":{"model":"gpt-6-astra","status":"completed","output":[{"content":[{"type":"output_text","text":"21"}]}]}}` + "\n\n"
	for _, tc := range []struct {
		name, body string
		want       bool
	}{
		{"completed", good, true},
		{"luna", strings.ReplaceAll(good, "gpt-6-astra", "gpt-5.6-luna"), false},
		{"failed", `data: {"type":"response.failed","response":{"model":"gpt-6-astra"}}` + "\n\n", false},
		{"truncated", `data: {"type":"response.created","response":{"model":"gpt-6-astra"}}` + "\n\n", false},
		{"empty output", strings.ReplaceAll(good, "21", ""), false},
		{"malformed", "data: broken\n\n", false},
		{"oversized", "data: " + strings.Repeat("x", (1<<20)+1) + "\n\n", false},
		{"conflicting model", `data: {"type":"response.created","response":{"model":"gpt-5.6-luna"}}` + "\n\n" + good, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			got := false
			b := &codexGatewayProbeBody{ReadCloser: io.NopCloser(strings.NewReader(tc.body)), onResult: func(ok bool) { calls++; got = ok }}
			// One-byte reads exercise every possible event boundary without changing bytes.
			var out strings.Builder
			size := 1
			if len(tc.body) > 1<<20 {
				size = 4096
			}
			buf := make([]byte, size)
			for {
				n, err := b.Read(buf)
				_, writeErr := out.Write(buf[:n])
				require.NoError(t, writeErr)
				if err != nil {
					require.ErrorIs(t, err, io.EOF)
					break
				}
			}
			require.Equal(t, tc.body, out.String())
			require.Equal(t, 1, calls)
			require.Equal(t, tc.want, got)
			require.NoError(t, b.Close())
		})
	}
}

func TestAstraGatewayRuntimeRevisionClearsPool(t *testing.T) {
	cfg := &config.Config{}
	settings := config.AstraRoutingSettings{CookiePool: pinConfig(), Revision: "one"}
	cfg.SetAstraRoutingLoader(func(context.Context) config.AstraRoutingSettings { return settings })
	wrapper := &astraRoutingUpstream{cfg: cfg}
	first := wrapper.current(t.Context())
	now := time.Now()
	first.recordSource(299, now, &codexGatewayRoute{cookie: http.Cookie{Name: "__oailb", Value: "route", Path: "/"}, expires: now.Add(time.Minute)}, true)
	cookie, err := wrapper.CodexGatewayPinWSHeader(300)
	require.NoError(t, err)
	require.Equal(t, "route", cookie)
	settings.Revision = "two"
	second := wrapper.current(t.Context())
	require.NotSame(t, first, second)
	_, err = wrapper.CodexGatewayPinWSHeader(300)
	require.Error(t, err)
	settings.CookiePool.Enabled = false
	require.Nil(t, wrapper.current(t.Context()))
	cookie, err = wrapper.CodexGatewayPinWSHeader(300)
	require.NoError(t, err)
	require.Empty(t, cookie)
}

// Account tests stop after reading the terminal data line, before the empty SSE
// delimiter. The transport observer must not reject that complete response.
func TestAstraGatewayAccountTestStopsAtTerminalLine(t *testing.T) {
	stream := `data: {"type":"response.completed","response":{"model":"gpt-6-astra","status":"completed","output":[{"content":[{"type":"output_text","text":"21"}]}]}}` + "\n\n"
	qualified := false
	b := &codexGatewayProbeBody{ReadCloser: &oneByteReadCloser{Reader: strings.NewReader(stream)}, onResult: func(ok bool) { qualified = ok }}
	reader := bufio.NewReader(b)
	_, err := reader.ReadString('\n')
	require.NoError(t, err)
	require.NoError(t, b.Close())
	require.True(t, qualified)
}

type oneByteReadCloser struct{ io.Reader }

func (r *oneByteReadCloser) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return r.Reader.Read(p)
}
func (r *oneByteReadCloser) Close() error { return nil }

func TestAstraGatewayMissingContentTypeAndDeltaOnlyOutput(t *testing.T) {
	cfg := pinConfig()
	pin := &codexGatewayPinUpstream{config: cfg}
	stream := `data: {"type":"response.output_text.delta","delta":"21"}` + "\n\n" + `data: {"type":"response.completed","response":{"model":"gpt-6-astra","status":"completed","output":[]}}` + "\n\n"
	pin.delegate = gatewayPinDelegate{call: func(_ *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		resp := pinResponse("__oailb=route; Path=/; Secure; Max-Age=3600")
		resp.Header.Del("Content-Type")
		resp.Body = &oneByteReadCloser{Reader: strings.NewReader(stream)}
		return resp, nil
	}}
	resp, err := pin.Do(pinRequest(t), "", 299, 1)
	require.NoError(t, err)
	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		require.NoError(t, err)
		if strings.Contains(line, "response.completed") {
			break
		}
	}
	require.NoError(t, resp.Body.Close())
	cookie, id := pin.currentCookie("/backend-api/codex/responses", time.Now())
	require.NotNil(t, cookie)
	require.Equal(t, int64(299), id)
}

func TestAstraGatewayAutomaticPrepareAndCooldown(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	wrapper := &astraRoutingUpstream{cfg: cfg}
	calls := 0
	wrapper.SetAstraGatewayPreparer(func(_ context.Context, id int64) error {
		calls++
		p := wrapper.current(t.Context())
		now := time.Now()
		if id == 299 {
			return errors.New("unavailable")
		}
		p.recordSource(id, now, &codexGatewayRoute{cookie: http.Cookie{Name: "__oailb", Value: "qualified", Path: "/"}, expires: now.Add(time.Minute)}, true)
		return nil
	})
	require.NoError(t, wrapper.PrepareAstraGateway(t.Context()))
	require.Equal(t, 2, calls)
	status := wrapper.AstraGatewaySnapshot(t.Context())
	require.Zero(t, status.ReadyRoutes, "source success alone is not a target-validated route")
	require.Equal(t, "source_test_failed", status.Sources[0].Reason)
	require.NoError(t, wrapper.PrepareAstraGateway(t.Context()))
	require.Equal(t, 3, calls)
	wrapper.pool = nil
	wrapper.SetAstraGatewayPreparer(func(context.Context, int64) error { calls++; return errors.New("failed") })
	require.Error(t, wrapper.PrepareAstraGateway(t.Context()))
	n := calls
	require.ErrorContains(t, wrapper.PrepareAstraGateway(t.Context()), "cooldown")
	require.Equal(t, n, calls)
}
func TestAstraGatewayLifetimeConfiguration(t *testing.T) {
	now := time.Now()
	resp := pinResponse("__oailb=route; Path=/; Secure; Max-Age=3600")
	require.Equal(t, now.Add(60*time.Second), codexGatewayRouteFromResponse(resp, "/backend-api/codex/responses", now, 60).expires)
	require.Equal(t, now.Add(240*time.Second), codexGatewayRouteFromResponse(resp, "/backend-api/codex/responses", now, 240).expires)
	require.Empty(t, astraRoutingHost("not-a-jwt"))
}

func TestAstraGatewayTargetPreparesSourceBeforeForwarding(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.CodexGatewayPin = pinConfig()
	wrapper := &astraRoutingUpstream{cfg: cfg}
	calls := 0
	wrapper.delegate = gatewayPinDelegate{call: func(r *http.Request, _ string, id int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
		calls++
		require.Equal(t, int64(300), id)
		require.Equal(t, "__oailb=ready", r.Header.Get("Cookie"))
		return pinResponse(""), nil
	}}
	wrapper.SetAstraGatewayPreparer(func(_ context.Context, id int64) error {
		now := time.Now()
		wrapper.current(t.Context()).recordSource(id, now, &codexGatewayRoute{cookie: http.Cookie{Name: "__oailb", Value: "ready", Path: "/"}, expires: now.Add(time.Minute)}, true)
		return nil
	})
	resp, err := wrapper.Do(pinRequest(t), "", 300, 1)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, 3, calls)
}

func TestTrustedSourceAcquisitionDoesNotRequireCandyAnswer(t *testing.T) {
	for _, controlled := range []bool{false, true} {
		pool := &codexGatewayPinUpstream{config: pinConfig()}
		pool.delegate = gatewayPinDelegate{call: func(r *http.Request, p string, a int64, n int, f *tlsfingerprint.Profile) (*http.Response, error) {
			resp := pinResponse("__oailb=trusted; Path=/; Secure; Max-Age=230")
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(string(body), `"21"`, `"OK"`)))
			return resp, nil
		}}
		ctx := context.Background()
		if controlled {
			ctx = service.WithAstraSourceAcquisition(ctx)
		}
		resp, err := pool.Do(pinRequest(t).WithContext(ctx), "", 299, 1)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
		cookie, _ := pool.currentCookie("/backend-api/codex/responses", time.Now())
		if controlled {
			require.NotNil(t, cookie)
		} else {
			require.Nil(t, cookie)
		}
	}
}
