package tlsfingerprint

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFingerprintProxyHandshakeCancellation(t *testing.T) {
	for _, scheme := range []string{"http", "socks5h"} {
		t.Run(scheme, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			defer func() { _ = listener.Close() }()
			entered := make(chan struct{})
			closed := make(chan struct{})
			go func() {
				c, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = c.Close() }()
				defer close(closed)
				if scheme == "http" {
					_, err = http.ReadRequest(bufio.NewReader(c))
				} else {
					b := make([]byte, 3)
					_, err = io.ReadFull(c, b)
				}
				if err != nil {
					return
				}
				close(entered)
				_, _ = io.Copy(io.Discard, c)
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			u, err := url.Parse(scheme + "://" + listener.Addr().String())
			require.NoError(t, err)
			done := make(chan error, 1)
			go func() {
				var err error
				var c net.Conn
				if scheme == "http" {
					c, err = NewHTTPProxyDialer(&Profile{Name: "test"}, u).DialTLSContext(ctx, "tcp", "api.example.test:443")
				} else {
					c, err = NewSOCKS5ProxyDialer(&Profile{Name: "test"}, u).DialTLSContext(ctx, "tcp", "api.example.test:443")
				}
				if c != nil {
					_ = c.Close()
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("proxy handshake not started")
			}
			cancel()
			select {
			case err := <-done:
				require.Error(t, err)
			case <-time.After(time.Second):
				t.Fatal("proxy handshake ignored cancellation")
			}
			select {
			case <-closed:
			case <-time.After(time.Second):
				t.Fatal("proxy socket leaked")
			}
		})
	}
}
