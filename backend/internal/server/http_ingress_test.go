//go:build unit

package server

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func ingressTestConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			Host:               "127.0.0.1",
			ReadHeaderTimeout:  1,
			IdleTimeout:        5,
			MaxHeaderBytes:     8 * 1024,
			MaxRequestBodySize: 1024,
		},
		Gateway: config.GatewayConfig{MaxBodySize: 1024},
	}
}

func TestProvideHTTPServerAppliesIngressLimits(t *testing.T) {
	srv := ProvideHTTPServer(ingressTestConfig(), gin.New(), NewLifecycle())
	require.Equal(t, 8*1024, srv.MaxHeaderBytes)
	require.Equal(t, time.Second, srv.ReadHeaderTimeout)
	require.Equal(t, 5*time.Second, srv.IdleTimeout)
}

func TestProvideHTTPServerEnablesBoundedH2C(t *testing.T) {
	cfg := ingressTestConfig()
	cfg.Server.H2C = config.H2CConfig{
		Enabled:                      true,
		MaxConcurrentStreams:         25,
		IdleTimeout:                  30,
		MaxReadFrameSize:             64 * 1024,
		MaxUploadBufferPerConnection: 1024 * 1024,
		MaxUploadBufferPerStream:     256 * 1024,
	}
	srv := ProvideHTTPServer(cfg, gin.New(), NewLifecycle())
	require.NotNil(t, srv.Protocols)
	require.True(t, srv.Protocols.UnencryptedHTTP2())
	require.True(t, srv.Protocols.HTTP1())
}

func TestConfigureTrustedProxies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name string
		cfg  config.ServerConfig
		peer string
		xff  string
		want string
	}{
		{
			name: "configured proxy resolves forwarded client",
			cfg: config.ServerConfig{
				TrustedProxies:           []string{"9.9.9.9/32"},
				TrustedProxiesConfigured: true,
			},
			want: "1.2.3.4",
		},
		{
			name: "explicit empty list ignores forwarded client",
			cfg: config.ServerConfig{
				TrustedProxiesConfigured: true,
			},
			want: "9.9.9.9",
		},
		{
			name: "invalid proxy list fails closed",
			cfg: config.ServerConfig{
				TrustedProxies:           []string{"not-a-cidr"},
				TrustedProxiesConfigured: true,
			},
			want: "9.9.9.9",
		},
		{
			name: "unconfigured IPv4 loopback resolves local nginx client",
			peer: "127.0.0.1:12345", want: "1.2.3.4",
		},
		{
			name: "unconfigured IPv6 loopback resolves local nginx client",
			peer: "[::1]:12345", want: "1.2.3.4",
		},
		{
			name: "unconfigured mapped loopback resolves local nginx client",
			peer: "[::ffff:127.0.0.1]:12345", want: "1.2.3.4",
		},
		{
			name: "nginx appended client wins over spoofed prefix and real IP",
			peer: "127.0.0.1:12345", xff: "203.0.113.99, 198.51.100.10", want: "198.51.100.10",
		},
		{
			name: "local nginx forwards IPv6 visitor",
			peer: "127.0.0.1:12345", xff: "2001:db8::10", want: "2001:db8::10",
		},
		{
			name: "unconfigured public peer cannot spoof forwarding headers",
			peer: "198.51.100.10:12345", want: "198.51.100.10",
		},
		{
			name: "unconfigured docker peer is not implicitly trusted",
			peer: "172.18.0.1:12345", want: "172.18.0.1",
		},
		{
			name: "other loopback addresses require explicit trust",
			peer: "127.0.0.2:12345", want: "127.0.0.2",
		},
		{
			name: "explicit empty list does not trust local nginx",
			cfg:  config.ServerConfig{TrustedProxiesConfigured: true},
			peer: "127.0.0.1:12345", want: "127.0.0.1",
		},
		{
			name: "explicit proxy list replaces loopback defaults",
			cfg:  config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{"9.9.9.9/32"}},
			peer: "127.0.0.1:12345", want: "127.0.0.1",
		},
		{
			name: "invalid mixed list cannot fall back to loopback trust",
			cfg:  config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{"127.0.0.1/32", "invalid"}},
			peer: "127.0.0.1:12345", want: "127.0.0.1",
		},
		{
			name: "whitespace in explicit list fails closed",
			cfg:  config.ServerConfig{TrustedProxiesConfigured: true, TrustedProxies: []string{" 127.0.0.1 "}},
			peer: "127.0.0.1:12345", want: "127.0.0.1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			configureTrustedProxies(r, tc.cfg)
			r.GET("/t", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.RemoteAddr = tc.peer
			if req.RemoteAddr == "" {
				req.RemoteAddr = "9.9.9.9:12345"
			}
			xff := tc.xff
			if xff == "" {
				xff = "1.2.3.4"
			}
			req.Header.Set("X-Forwarded-For", xff)
			req.Header.Set("X-Real-IP", "203.0.113.99")
			req.Header.Set("CF-Connecting-IP", "203.0.113.99")
			r.ServeHTTP(w, req)

			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.want, w.Body.String())
		})
	}
}

func TestLocalProxyForwardedHeaderFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, xff, realIP, cfIP, want string
	}{
		{name: "no headers keeps local peer", want: "127.0.0.1"},
		{name: "real IP fallback", realIP: "198.51.100.10", want: "198.51.100.10"},
		{name: "CF header alone is not trusted", cfIP: "198.51.100.10", want: "127.0.0.1"},
		{name: "malformed XFF falls back to real IP", xff: "invalid", realIP: "198.51.100.10", want: "198.51.100.10"},
		{name: "invalid headers keep peer", xff: "invalid", realIP: "invalid", cfIP: "198.51.100.10", want: "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			configureTrustedProxies(r, config.ServerConfig{})
			r.GET("/", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = "127.0.0.1:12345"
			req.Header.Set("X-Forwarded-For", tc.xff)
			req.Header.Set("X-Real-IP", tc.realIP)
			req.Header.Set("CF-Connecting-IP", tc.cfIP)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			require.Equal(t, tc.want, w.Body.String())
		})
	}
}

func TestHTTPServerRejectsOversizedHTTP1Header(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	srv := ProvideHTTPServer(ingressTestConfig(), r, NewLifecycle())
	addr, stop := serveIngressTestServer(t, srv)
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\nX-Fill: "+strings.Repeat("a", 32*1024)+"\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusRequestHeaderFieldsTooLarge, resp.StatusCode)
}

func TestHTTPServerClosesSlowIncompleteHeader(t *testing.T) {
	r := gin.New()
	r.GET("/", func(c *gin.Context) { c.Status(http.StatusOK) })
	srv := ProvideHTTPServer(ingressTestConfig(), r, NewLifecycle())
	addr, stop := serveIngressTestServer(t, srv)
	defer stop()

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()
	_, err = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: test\r\nX-Slow:")
	require.NoError(t, err)
	time.Sleep(1200 * time.Millisecond)
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, err = bufio.NewReader(conn).ReadByte()
	require.Error(t, err)
}

func TestHTTPServerGlobalBodyLimit(t *testing.T) {
	r := gin.New()
	r.POST("/", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				c.Status(http.StatusRequestEntityTooLarge)
				return
			}
		}
		c.Status(http.StatusOK)
	})
	srv := ProvideHTTPServer(ingressTestConfig(), r, NewLifecycle())
	req, err := http.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("x", 1025)))
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func serveIngressTestServer(t *testing.T, srv *http.Server) (string, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(ln) }()
	return ln.Addr().String(), func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}
