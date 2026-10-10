//go:build unit

package service

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type accountTestHTTPUpstream struct {
	target *url.URL
}

func (u *accountTestHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	forward := req.Clone(req.Context())
	target := *u.target
	target.Path = req.URL.Path
	forward.URL = &target
	forward.Host = ""
	return http.DefaultClient.Do(forward)
}

func (u *accountTestHTTPUpstream) DoWithTLS(req *http.Request, proxy string, account int64, concurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxy, account, concurrency)
}

// SUB2API_TEST_LONG_WAIT=1 使用生产的 10 秒节拍，分别验证 130 秒首包等待和正文静默。
// 短测仍经过真实 HTTP 和 TestPelicanAccountConnection，只是不等待 125 秒门槛。
func TestPelicanKeepaliveHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	wait := 50 * time.Millisecond
	long := os.Getenv("SUB2API_TEST_LONG_WAIT") == "1"
	if long {
		wait = 130 * time.Second
	}
	for _, tc := range []struct {
		phase string
		abort bool
	}{{"headers", false}, {"body", false}, {"headers", true}, {"body", true}} {
		name := tc.phase
		if tc.abort {
			name += "_client_disconnect"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			payloads := make(chan map[string]any, 1)
			upstreamDone := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer close(upstreamDone)
				var payload map[string]any
				_ = json.NewDecoder(r.Body).Decode(&payload)
				payloads <- payload
				if tc.phase == "body" {
					w.Header().Set("Content-Type", "text/event-stream")
					w.(http.Flusher).Flush()
					_, _ = io.WriteString(w, "data: {\"type\":\"response.created\"}\n\n")
					w.(http.Flusher).Flush()
				}
				timer := time.NewTimer(wait)
				defer timer.Stop()
				select {
				case <-r.Context().Done():
					return
				case <-timer.C:
				}
				_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"<!doctype html><html>verified</html>\"}\n\ndata: {\"type\":\"response.completed\"}\n\n")
			}))
			defer upstream.Close()
			target, err := url.Parse(upstream.URL)
			require.NoError(t, err)
			account := &Account{
				ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
				Credentials: map[string]any{"api_key": "fixture-only", "base_url": "https://fixture.example"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}
			s := &AccountTestService{
				httpUpstream: &accountTestHTTPUpstream{target: target},
				cfg:          &config.Config{},
				accountRepo: &openAIAccountTestRepo{mockAccountRepoForGemini: mockAccountRepoForGemini{
					accountsByID: map[int64]*Account{1: account},
				}},
			}
			result := make(chan error, 1)
			downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, _ := gin.CreateTestContext(w)
				c.Request = r
				result <- s.TestPelicanAccountConnection(c, 1, "gpt-6-astra", "generate independent HTML", "max")
			}))
			defer downstream.Close()
			started := time.Now()
			client := &http.Client{Timeout: wait + 10*time.Second}
			resp, err := client.Get(downstream.URL)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Less(t, time.Since(started), 2*time.Second, "SSE headers must precede upstream headers")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			require.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))
			if tc.abort {
				<-payloads
				_ = resp.Body.Close()
				select {
				case err := <-result:
					require.Error(t, err)
				case <-time.After(2 * time.Second):
					t.Fatal("account test did not stop after client disconnected")
				}
				select {
				case <-upstreamDone:
				case <-time.After(2 * time.Second):
					t.Fatal("account test upstream was not canceled")
				}
				return
			}
			var body strings.Builder
			scanner := bufio.NewScanner(resp.Body)
			last := time.Now()
			maxGap := time.Duration(0)
			beats := 0
			for scanner.Scan() {
				line := scanner.Text()
				body.WriteString(line + "\n")
				if line != "" {
					gap := time.Since(last)
					if gap > maxGap {
						maxGap = gap
					}
					last = time.Now()
				}
				if line == ": keepalive" {
					beats++
				}
			}
			require.NoError(t, scanner.Err())
			require.GreaterOrEqual(t, beats, 1, "shared test entry must immediately flush a keepalive comment")
			require.NoError(t, <-result)
			<-upstreamDone
			text, errMsg, _ := parseTestSSEOutput(body.String())
			require.Equal(t, "<!doctype html><html>verified</html>", text)
			require.Empty(t, errMsg)
			require.Equal(t, 1, strings.Count(body.String(), `"type":"test_complete"`))
			payload := <-payloads
			require.Equal(t, "gpt-6-astra", payload["model"])
			require.Equal(t, "max", payload["reasoning"].(map[string]any)["effort"])
			if long {
				require.GreaterOrEqual(t, time.Since(started), 130*time.Second)
				require.GreaterOrEqual(t, beats, 13)
				require.Less(t, maxGap, 12*time.Second)
			}
			t.Logf("[INFO] phase=%s elapsed=%s keepalives=%d max_gap=%s html_bytes=%d", tc.phase, time.Since(started), beats, maxGap, len(text))
		})
	}
}
