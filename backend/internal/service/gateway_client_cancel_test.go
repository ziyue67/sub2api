package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setClaudeDisconnectDrainTimeout(t *testing.T, timeout time.Duration) {
	t.Helper()
	original := anthropicNativeDisconnectDrainTimeout
	anthropicNativeDisconnectDrainTimeout = timeout
	t.Cleanup(func() { anthropicNativeDisconnectDrainTimeout = original })
}

// Even an idle accepted stream must release its upstream within the total
// disconnect limit, independently of tokens, keepalives or the idle setting.
func TestClaudeClientCancelSilentStream(t *testing.T) {
	setClaudeDisconnectDrainTimeout(t, 100*time.Millisecond)
	gin.SetMode(gin.TestMode)
	svc := newStreamingResponseTestGatewayService()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	watchdog := time.AfterFunc(3*time.Second, func() { _ = pw.CloseWithError(io.ErrUnexpectedEOF) })
	defer watchdog.Stop()
	finished := make(chan struct{})
	var result *streamingResult
	var err error
	go func() {
		result, err = svc.handleStreamingResponse(context.Background(), &http.Response{Header: http.Header{}, Body: pr}, c, &Account{ID: 1}, time.Now(), "model", "model", false)
		close(finished)
	}()
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("idle upstream outlived the disconnect drain limit")
	}
	require.ErrorIs(t, err, context.Canceled)
	require.True(t, result.clientDisconnect)
	require.False(t, result.usage.hasObservedTokens())
	_, err = pw.Write([]byte("future generation"))
	require.Error(t, err, "upstream body must be closed")
}

func TestClaudeUsage_WebSearchOnlyCountsAsObservedUsage(t *testing.T) {
	require.True(t, (&ClaudeUsage{WebSearchRequests: 1}).hasObservedTokens())
}

type claudeFlushFailure struct{ *httptest.ResponseRecorder }

func (w *claudeFlushFailure) FlushError() error { return io.ErrClosedPipe }

type claudeTerminalFlushFailure struct{ *httptest.ResponseRecorder }

func (w *claudeTerminalFlushFailure) FlushError() error {
	if strings.Contains(w.Body.String(), "message_stop") {
		return io.ErrClosedPipe
	}
	w.Flush()
	return nil
}

func TestClaudeClientCancelAfterTerminalPreservesCompletedUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(&claudeTerminalFlushFailure{httptest.NewRecorder()})
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	body := strings.Join([]string{
		`data: {"type":"message_start","message":{"usage":{"input_tokens":11,"output_tokens":2}}}`,
		`data: {"type":"message_delta","usage":{"output_tokens":7}}`,
		`data: {"type":"message_stop"}`,
		"",
	}, "\n\n")
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	result, err := newStreamingResponseTestGatewayService().handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	require.NoError(t, err, "a terminal event already established complete upstream usage")
	require.True(t, result.clientDisconnect)
	require.Equal(t, 11, result.usage.InputTokens)
	require.Equal(t, 7, result.usage.OutputTokens)
}

func TestClaudeClientCancelWriteOrFlushPreservesCurrentUsage(t *testing.T) {
	setClaudeDisconnectDrainTimeout(t, 100*time.Millisecond)
	for _, mode := range []string{"write", "flush"} {
		t.Run(mode, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var writer http.ResponseWriter = httptest.NewRecorder()
			if mode == "flush" {
				writer = &claudeFlushFailure{httptest.NewRecorder()}
			}
			c, _ := gin.CreateTestContext(writer)
			if mode == "write" {
				c.Writer = &failWriteResponseWriter{c.Writer}
			}
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			pr, pw := io.Pipe()
			defer func() { _ = pw.Close() }()
			watchdog := time.AfterFunc(3*time.Second, func() { _ = pw.CloseWithError(io.ErrUnexpectedEOF) })
			defer watchdog.Stop()
			producer := make(chan error, 1)
			go func() {
				_, err := io.WriteString(pw, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":7,\"output_tokens\":2}}}\n\n")
				if err == nil {
					_, err = pw.Write([]byte("data: "))
				}
				// Never finish the stream unless the reader is closed.
				if err == nil {
					_, err = pw.Write([]byte("future output"))
				}
				producer <- err
			}()
			result, err := newStreamingResponseTestGatewayService().handleStreamingResponse(context.Background(), &http.Response{Header: http.Header{}, Body: pr}, c, &Account{ID: 1}, time.Now(), "model", "model", false)
			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.True(t, result.clientDisconnect)
			require.Equal(t, 11, result.usage.InputTokens)
			require.Equal(t, 7, result.usage.CacheReadInputTokens)
			require.Equal(t, 2, result.usage.OutputTokens)
			select {
			case <-producer:
			case <-time.After(time.Second):
				t.Fatal("upstream reader remained open")
			}
		})
	}
}

func TestClaudeClientCancelKeepaliveFlush(t *testing.T) {
	setClaudeDisconnectDrainTimeout(t, 100*time.Millisecond)
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(&claudeFlushFailure{httptest.NewRecorder()})
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()
	watchdog := time.AfterFunc(3*time.Second, func() { _ = pw.CloseWithError(io.ErrUnexpectedEOF) })
	defer watchdog.Stop()
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamKeepaliveInterval = 1
	result, err := svc.handleStreamingResponse(context.Background(), &http.Response{Header: http.Header{}, Body: pr}, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	require.ErrorIs(t, err, io.ErrClosedPipe)
	require.True(t, result.clientDisconnect)
	_, err = pw.Write([]byte("future output"))
	require.Error(t, err)
}

type claudeCancelHTTPUpstream struct {
	HTTPUpstream
	endpoint *url.URL
	calls    atomic.Int32
}

func (u *claudeCancelHTTPUpstream) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	u.calls.Add(1)
	req.URL.Scheme, req.URL.Host = u.endpoint.Scheme, u.endpoint.Host
	req.Host = u.endpoint.Host
	return http.DefaultClient.Do(req)
}

func TestClaudeClientCancelForwardActualHTTPAndDoesNotRetry(t *testing.T) {
	setClaudeDisconnectDrainTimeout(t, 200*time.Millisecond)
	for _, name := range []string{"before_headers", "after_usage", "after_usage_api_key", "after_usage_timeout"} {
		sendUsage := name != "before_headers"
		wantFinalUsage := name == "after_usage" || name == "after_usage_api_key"
		t.Run(name, func(t *testing.T) {
			started, stopped := make(chan struct{}), make(chan struct{})
			sendFinal := make(chan struct{})
			cleanup := make(chan struct{})
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if sendUsage {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11,\"cache_read_input_tokens\":7,\"output_tokens\":2}}}\n\n")
					if err := http.NewResponseController(w).Flush(); err != nil {
						t.Errorf("flush synthetic upstream usage: %v", err)
					}
				}
				close(started)
				if sendUsage {
					select {
					case <-sendFinal:
						_, _ = io.WriteString(w, "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":7}}\n\ndata: {\"type\":\"message_stop\"}\n\n")
						_ = http.NewResponseController(w).Flush()
					case <-r.Context().Done():
						close(stopped)
						return
					case <-cleanup:
						return
					}
				}
				select {
				case <-r.Context().Done():
					close(stopped)
				case <-cleanup:
				}
			}))
			defer upstream.Close()
			// Release a detached upstream even if the cancellation assertion fails.
			defer close(cleanup)
			endpoint, _ := url.Parse(upstream.URL)
			transport := &claudeCancelHTTPUpstream{endpoint: endpoint}
			svc := newForwardPartialUsageServiceForTest(nil)
			svc.httpUpstream = transport
			repo := &gatewayForwardErrorPolicyRepoStub{}
			svc.accountRepo = repo
			account := newAnthropicOAuthAccountForPartialUsageTest()
			if name == "after_usage_api_key" {
				account.Type = AccountTypeAPIKey
				// Keep normal HTTPS validation; claudeCancelHTTPUpstream redirects
				// the actual transport exclusively to the local test server.
				account.Credentials = map[string]any{"api_key": "synthetic-test-key", "base_url": "https://api.anthropic.com"}
			}
			gin.SetMode(gin.TestMode)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			observed := make(chan struct{}, 1)
			rec := &claudeNotifyWriter{ResponseRecorder: httptest.NewRecorder(), observed: observed}
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"max_tokens":32,"messages":[{"role":"user","content":"test"}]}`)), PlatformAnthropic)
			require.NoError(t, err)
			finished := make(chan struct{})
			var result *ForwardResult
			go func() { result, err = svc.Forward(ctx, c, account, parsed); close(finished) }()
			select {
			case <-started:
			case <-finished:
				t.Fatalf("forward returned before reaching the local upstream: %v", err)
			case <-time.After(2 * time.Second):
				t.Fatal("upstream not reached")
			}
			if sendUsage {
				select {
				case <-observed:
				case <-time.After(time.Second):
					t.Fatal("usage not forwarded")
				}
			}
			cancel()
			if wantFinalUsage {
				select {
				case <-stopped:
					t.Fatal("accepted upstream was canceled before it could report final Usage")
				case <-time.After(30 * time.Millisecond):
				}
				close(sendFinal)
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("actual upstream HTTP was not released after cancellation or final Usage")
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("forward did not return")
			}
			if wantFinalUsage {
				require.NoError(t, err, "accepted stream must collect the final Usage after downstream cancellation")
			} else if sendUsage {
				require.ErrorContains(t, err, "disconnect drain timeout")
			} else {
				require.ErrorIs(t, err, context.Canceled, "cancellation before headers must still stop the upstream")
			}
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover))
			require.EqualValues(t, 1, transport.calls.Load())
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.overloadCalls)
			require.Empty(t, repo.modelRateLimitCalls)
			if sendUsage {
				require.NotNil(t, result)
				require.True(t, result.ClientDisconnect)
				require.Equal(t, 11, result.Usage.InputTokens)
				require.Equal(t, 7, result.Usage.CacheReadInputTokens)
				if wantFinalUsage {
					require.Equal(t, 7, result.Usage.OutputTokens)
				} else {
					require.Equal(t, 2, result.Usage.OutputTokens, "timeout must preserve actual observed Usage, not guess a final value")
				}
			} else {
				require.Nil(t, result)
			}
		})
	}
}

type claudeNotifyWriter struct {
	*httptest.ResponseRecorder
	observed chan struct{}
}

func (w *claudeNotifyWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseRecorder.Write(p)
	if strings.Contains(string(p), "message_start") {
		select {
		case w.observed <- struct{}{}:
		default:
		}
	}
	return n, err
}
