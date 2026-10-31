//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// The upstream fixture declares its final cumulative output as 42. No real
// customer traffic, provider credentials, or paid requests are used.
func TestClaudeBillingProbeNativeFinalUsage(t *testing.T) {
	for _, name := range []string{"complete", "downstream_write_failure", "downstream_flush_failure", "context_cancelled"} {
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var writer http.ResponseWriter = httptest.NewRecorder()
			failedWriter := &disconnectOnMarkerResponseWriter{ResponseRecorder: httptest.NewRecorder(), marker: "second"}
			if name == "downstream_write_failure" {
				writer = failedWriter
			}
			if name == "downstream_flush_failure" {
				writer = &claudeFlushFailure{httptest.NewRecorder()}
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			if name == "context_cancelled" {
				ctx, cancel := context.WithCancel(c.Request.Context())
				cancel()
				c.Request = c.Request.WithContext(ctx)
			}
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(anthropicStreamWithMidStreamText() + "\n"))}
			result, err := newStreamingResponseTestGatewayService().handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
			require.NotNil(t, result)
			require.NoError(t, err)
			require.Equal(t, name != "complete", result.clientDisconnect)
			require.Equal(t, 20, result.usage.InputTokens)
			require.Equal(t, 11, result.usage.CacheReadInputTokens)
			require.Equal(t, 4, result.usage.CacheCreationInputTokens)
			require.Zero(t, failedWriter.writesAfterError)
			require.Zero(t, failedWriter.flushesAfterError)
			t.Logf("mode=%s observed_output=%d fixture_final_output=42 error=%v", name, result.usage.OutputTokens, err)
			require.Equal(t, 42, result.usage.OutputTokens, "final upstream Usage should remain collectable after downstream write failure")
		})
	}
}

func TestClaudeNativeDisconnectReturnsOnFinalUsageWithoutEOF(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := &disconnectOnMarkerResponseWriter{ResponseRecorder: httptest.NewRecorder(), marker: "second"}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	resp, pw := stallingAnthropicUpstream(t, anthropicStreamWithMidStreamText()+"\n")
	result, err := newStreamingResponseTestGatewayService().handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	require.NoError(t, err)
	require.Equal(t, 42, result.usage.OutputTokens)
	require.True(t, result.clientDisconnect)
	_, err = io.WriteString(pw, "data: late\n\n")
	require.Error(t, err, "final Usage must release a stalled upstream without waiting for EOF")
}

func TestClaudeNativeDisconnectIncompleteStreamKeepsObservedUsage(t *testing.T) {
	for _, suffix := range []string{"", "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n", "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"test\"}}\n\n"} {
		t.Run(suffix, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			writer := &disconnectOnMarkerResponseWriter{ResponseRecorder: httptest.NewRecorder(), marker: "second"}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			body := anthropicStreamTruncatedBeforeMessageDelta() + suffix
			resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
			result, err := newStreamingResponseTestGatewayService().handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
			require.ErrorContains(t, err, "stream usage incomplete")
			require.True(t, result.clientDisconnect)
			require.Equal(t, 1, result.usage.OutputTokens)
			require.Equal(t, 20, result.usage.InputTokens)
			var failover *UpstreamFailoverError
			var sseError *sseStreamErrorEventError
			require.False(t, errors.As(err, &failover))
			require.False(t, errors.As(err, &sseError), "disconnected requests must not be replayed as SSE-error failovers")
			require.NotNil(t, partialStreamUsageResult(c, resp, result, "model", "model", time.Now(), err))
			require.Zero(t, writer.writesAfterError)
			require.Zero(t, writer.flushesAfterError)
		})
	}
}

func TestClaudeNativeDisconnectTotalLimitStopsBusyUpstream(t *testing.T) {
	setClaudeDisconnectDrainTimeout(t, 100*time.Millisecond)
	gin.SetMode(gin.TestMode)
	writer := &disconnectOnMarkerResponseWriter{ResponseRecorder: httptest.NewRecorder(), marker: "second"}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close(); _ = pr.Close() })
	producerStopped := make(chan struct{})
	go func() {
		defer close(producerStopped)
		if _, err := io.WriteString(pw, anthropicStreamTruncatedBeforeMessageDelta()); err != nil {
			return
		}
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := io.WriteString(pw, "event: ping\ndata: {\"type\":\"ping\"}\n\n"); err != nil {
				return
			}
		}
	}()
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamDataIntervalTimeout = 30
	start := time.Now()
	result, err := svc.handleStreamingResponse(context.Background(), &http.Response{Header: http.Header{}, Body: pr}, c, &Account{ID: 1}, start, "model", "model", false)
	require.ErrorContains(t, err, "disconnect drain timeout")
	require.Less(t, time.Since(start), time.Second)
	require.True(t, result.clientDisconnect)
	require.Equal(t, 1, result.usage.OutputTokens)
	select {
	case <-producerStopped:
	case <-time.After(time.Second):
		t.Fatal("busy upstream was not closed after the total drain limit")
	}
	require.Zero(t, writer.writesAfterError)
	require.Zero(t, writer.flushesAfterError)
}

func TestClaudeNativeDisconnectRespectsConfiguredIdleLimit(t *testing.T) {
	setClaudeDisconnectDrainTimeout(t, 3*time.Second)
	gin.SetMode(gin.TestMode)
	writer := &disconnectOnMarkerResponseWriter{ResponseRecorder: httptest.NewRecorder(), marker: "second"}
	c, _ := gin.CreateTestContext(writer)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	resp, _ := stallingAnthropicUpstream(t, anthropicStreamTruncatedBeforeMessageDelta())
	svc := newStreamingResponseTestGatewayService()
	svc.cfg.Gateway.StreamDataIntervalTimeout = 1
	result, err := svc.handleStreamingResponse(context.Background(), resp, c, &Account{ID: 1}, time.Now(), "model", "model", false)
	require.ErrorContains(t, err, "stream data interval timeout")
	require.True(t, result.clientDisconnect)
	require.Equal(t, 1, result.usage.OutputTokens)
}

func TestClaudeStreamUpstreamContextCancellationStages(t *testing.T) {
	type traceKey struct{}
	t.Run("non_streaming", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		upstream, accepted, release := newClaudeStreamUpstreamContext(ctx, false)
		defer release()
		accepted()
		cancel()
		require.ErrorIs(t, upstream.Err(), context.Canceled)
	})
	t.Run("before_headers", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		upstream, _, release := newClaudeStreamUpstreamContext(ctx, true)
		defer release()
		cancel()
		select {
		case <-upstream.Done():
		case <-time.After(time.Second):
			t.Fatal("header wait lost cancellation")
		}
	})
	t.Run("already_cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		upstream, _, release := newClaudeStreamUpstreamContext(ctx, true)
		defer release()
		require.ErrorIs(t, upstream.Err(), context.Canceled)
	})
	t.Run("accepted_stream", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), traceKey{}, "trace"))
		defer cancel()
		upstream, accepted, release := newClaudeStreamUpstreamContext(ctx, true)
		defer release()
		accepted()
		cancel()
		require.NoError(t, upstream.Err())
		require.Equal(t, "trace", upstream.Value(traceKey{}))
		release()
		require.ErrorIs(t, upstream.Err(), context.Canceled)
	})
}

// Exercise Forward -> RecordUsage -> billing command with the same synthetic
// provider Usage, not just the stream parser. The repository is a local stub;
// actual PostgreSQL deduplication is covered by repository integration tests.
func TestClaudeNativeDisconnectBillsFinalUsageLikeCompleteStream(t *testing.T) {
	var completeCost float64
	for _, disconnect := range []bool{false, true} {
		name := "complete"
		if disconnect {
			name = "disconnected"
		}
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			var writer http.ResponseWriter = httptest.NewRecorder()
			if disconnect {
				writer = &disconnectOnMarkerResponseWriter{ResponseRecorder: httptest.NewRecorder(), marker: "second"}
			}
			c, _ := gin.CreateTestContext(writer)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(`{"model":"claude-sonnet-4","stream":true,"max_tokens":64,"messages":[{"role":"user","content":"synthetic billing test"}]}`)), PlatformAnthropic)
			require.NoError(t, err)
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: io.NopCloser(strings.NewReader(anthropicStreamWithMidStreamText() + "\n")),
			}}
			account := newAnthropicOAuthAccountForPartialUsageTest()
			result, err := newForwardPartialUsageServiceForTest(upstream).Forward(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.Equal(t, 42, result.Usage.OutputTokens)
			require.Equal(t, disconnect, result.ClientDisconnect)
			result.RequestID = "synthetic-final-usage"
			logs := &openAIRecordUsageLogRepoStub{}
			billing := &simpleModeUsageBillingRepoStub{}
			svc := newGatewayRecordUsageServiceWithBillingRepoForTest(logs, billing, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
			input := &RecordUsageInput{Result: result, APIKey: &APIKey{ID: 501, Quota: 100}, User: &User{ID: 601}, Account: account}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			require.NoError(t, svc.RecordUsage(ctx, input), "settlement must not inherit client cancellation")
			require.NoError(t, svc.RecordUsage(ctx, input), "repeated settlement must retain the same dedup key")
			require.Len(t, billing.cmds, 2)
			require.Len(t, billing.seen, 1)
			require.Equal(t, billing.cmds[0].RequestID, billing.cmds[1].RequestID)
			require.Equal(t, billing.cmds[0].BalanceCost, billing.cmds[1].BalanceCost)
			require.Equal(t, 42, billing.cmds[0].OutputTokens)
			require.Equal(t, 42, logs.lastLog.OutputTokens)
			require.Positive(t, billing.cmds[0].BalanceCost)
			require.InDelta(t, logs.lastLog.ActualCost, billing.cmds[0].BalanceCost, 1e-12)
			if !disconnect {
				completeCost = billing.cmds[0].BalanceCost
			} else {
				require.Equal(t, completeCost, billing.cmds[0].BalanceCost, "same real Usage must produce the same charge despite a downstream disconnect")
			}
		})
	}
}
