package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/upstreamroute"
	"github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

func regionalWSConfig(t *testing.T, proxy, domain string) *config.Config {
	t.Helper()
	t.Setenv("TEST_WS_REGION_PROXY", proxy)
	return &config.Config{Gateway: config.GatewayConfig{UpstreamRouting: upstreamroute.Config{Enabled: true, Regions: []upstreamroute.Region{{ID: "us", ProxyURLEnv: "TEST_WS_REGION_PROXY"}}, Rules: []upstreamroute.Rule{{Domain: domain, Region: "us"}}}}}
}
func TestRegionalWebSocketPayloadAndExplicitProxy(t *testing.T) {
	var upstreamHits, usHits, accountHits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		require.Equal(t, "Bearer synthetic", r.Header.Get("Authorization"))
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		require.NoError(t, err)
		defer func() { _ = c.CloseNow() }()
		kind, b, err := c.Read(r.Context())
		if err == nil {
			_ = c.Write(r.Context(), kind, b)
		}
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	forward := httputil.NewSingleHostReverseProxy(u)
	us := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		usHits.Add(1)
		require.Equal(t, "api.ws.test", r.URL.Host)
		forward.ServeHTTP(w, r)
	}))
	defer us.Close()
	account := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { accountHits.Add(1); forward.ServeHTTP(w, r) }))
	defer account.Close()
	cfg := regionalWSConfig(t, us.URL, "api.ws.test")
	dialer := newConfiguredOpenAIWSClientDialer(cfg)
	for _, p := range []string{"", account.URL} {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		c, _, _, err := dialer.Dial(ctx, "ws://api.ws.test/v1/responses", http.Header{"Authorization": []string{"Bearer synthetic"}}, p)
		require.NoError(t, err)
		require.NoError(t, c.WriteJSON(ctx, map[string]string{"type": "response.create"}))
		payload, err := c.ReadMessage(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"type":"response.create"}`, string(payload))
		require.NoError(t, c.Close())
		cancel()
	}
	require.EqualValues(t, 2, upstreamHits.Load())
	require.EqualValues(t, 1, usHits.Load())
	require.EqualValues(t, 1, accountHits.Load())
	pool := newOpenAIWSConnPool(cfg)
	t.Cleanup(pool.Close)
	impl, ok := pool.clientDialer.(*coderOpenAIWSClientDialer)
	require.True(t, ok)
	require.NotNil(t, impl.upstreamRoutes)
	service := &OpenAIGatewayService{cfg: cfg}
	pd, ok := service.getOpenAIWSPassthroughDialer().(*coderOpenAIWSClientDialer)
	require.True(t, ok)
	require.NotNil(t, pd.upstreamRoutes)
}

func TestRegionalWebSocketAccountScope(t *testing.T) {
	var direct, regional atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		direct.Add(1)
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		require.NoError(t, err)
		_ = conn.CloseNow()
	}))
	defer upstream.Close()
	u, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	forward := httputil.NewSingleHostReverseProxy(u)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		regional.Add(1)
		forward.ServeHTTP(w, r)
	}))
	defer proxy.Close()
	cfg := regionalWSConfig(t, proxy.URL, "127.0.0.1")
	cfg.Gateway.UpstreamRouting.Rules[0].AccountIDs = []int64{101}
	dialer := newConfiguredOpenAIWSClientDialer(cfg)
	for _, id := range []int64{101, 102} {
		ctx := upstreamroute.WithAccountID(t.Context(), id)
		conn, _, _, err := dialer.Dial(ctx, "ws"+strings.TrimPrefix(upstream.URL, "http"), nil, "")
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	}
	require.EqualValues(t, 2, direct.Load())
	require.EqualValues(t, 1, regional.Load())
}
func TestRegionalWebSocketRejectsRedirectAndNeverFallsBack(t *testing.T) {
	var targetHits, proxyHits atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHits.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		w.Header().Set("Location", target.URL)
		w.WriteHeader(302)
	}))
	defer proxy.Close()
	u, err := url.Parse(target.URL)
	require.NoError(t, err)
	cfg := regionalWSConfig(t, proxy.URL, u.Hostname())
	d := newConfiguredOpenAIWSClientDialer(cfg)
	_, status, _, err := d.Dial(t.Context(), "ws"+strings.TrimPrefix(target.URL, "http"), nil, "")
	require.Error(t, err)
	require.Equal(t, 302, status)
	require.Zero(t, targetHits.Load())
	require.EqualValues(t, 1, proxyHits.Load())
	t.Setenv("TEST_WS_REGION_PROXY", "http://user:private-secret@127.0.0.1:1")
	d = newConfiguredOpenAIWSClientDialer(cfg)
	_, _, _, err = d.Dial(t.Context(), "ws"+strings.TrimPrefix(target.URL, "http"), nil, "")
	require.Error(t, err)
	require.NotContains(t, err.Error(), "private-secret")
	require.Zero(t, targetHits.Load())
}
func TestRegionalWebSocketHandshakeCancellation(t *testing.T) {
	entered := make(chan struct{})
	cancelled := make(chan struct{})
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(entered)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer proxy.Close()
	cfg := regionalWSConfig(t, proxy.URL, "api.ws.test")
	d := newConfiguredOpenAIWSClientDialer(cfg)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, _, _, err := d.Dial(ctx, "ws://api.ws.test/", nil, ""); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("egress not reached")
	}
	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("dial not cancelled")
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("proxy not cancelled")
	}
}

func TestRegionalWebSocketDirectRedirectCannotBypassPolicy(t *testing.T) {
	var targetCalls, proxyCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1); w.WriteHeader(204) }))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { proxyCalls.Add(1); w.WriteHeader(204) }))
	defer proxy.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	// localhost is deliberately not the numeric 127.0.0.1 rule; the Location is.
	cfg := regionalWSConfig(t, proxy.URL, "127.0.0.1")
	dialer := newConfiguredOpenAIWSClientDialer(cfg)
	source := "ws" + strings.TrimPrefix(strings.Replace(redirect.URL, "127.0.0.1", "localhost", 1), "http")
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, status, _, err := dialer.Dial(ctx, source, nil, "")
	require.Error(t, err)
	require.Equal(t, http.StatusFound, status)
	require.Zero(t, targetCalls.Load(), "must not send a regional target through the first hop's direct transport")
	require.Zero(t, proxyCalls.Load(), "redirects requiring a different exit are rejected")
}
