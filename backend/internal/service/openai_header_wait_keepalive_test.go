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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type headerWaitFlushSignalWriter struct {
	gin.ResponseWriter
	flushed chan struct{}
	err     error
}

func (w *headerWaitFlushSignalWriter) FlushError() error {
	defer func() {
		select {
		case w.flushed <- struct{}{}:
		default:
		}
	}()
	if w.err != nil {
		return w.err
	}
	return FlushGatewayResponse(w.ResponseWriter)
}
func newHeaderWaitTest(t *testing.T, flushErr error) (*gin.Context, *httptest.ResponseRecorder, *openAIHeaderWaitWriter, <-chan struct{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	signal := &headerWaitFlushSignalWriter{ResponseWriter: c.Writer, flushed: make(chan struct{}, 8), err: flushErr}
	c.Writer = signal
	cleanup := WrapOpenAIResponsesHeaderWait(c, true)
	t.Cleanup(cleanup)
	w, ok := c.Writer.(*openAIHeaderWaitWriter)
	require.True(t, ok)
	return c, rec, w, signal.flushed
}
func waitHeaderWaitBeat(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(3 * time.Second):
		t.Fatal("header wait heartbeat did not flush")
	}
}
func TestOpenAIHeaderWaitHeartbeatAcrossAttempts(t *testing.T) {
	c, rec, w, signal := newHeaderWaitTest(t, nil)
	for i := 0; i < 2; i++ {
		for len(signal) > 0 {
			<-signal
		}
		stop := startOpenAIHeaderWaitKeepaliveInterval(c, w, 5*time.Millisecond)
		t.Cleanup(stop)
		waitHeaderWaitBeat(t, signal)
		stop()
		require.True(t, w.committed)
		require.False(t, openAIStreamClientOutputStarted(c, false), "heartbeat must not block existing safe failover")
		require.Equal(t, -1, OpenAICompactKeepaliveAdjustedWrittenSize(c))
		require.NotContains(t, rec.Body.String(), "response.completed")
	}
	require.GreaterOrEqual(t, strings.Count(rec.Body.String(), ": keepalive\n\n"), 2)
	require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
	// Once the response processor owns the writer, only its event bytes follow.
	prefix := rec.Body.String()
	_, err := c.Writer.WriteString("data: semantic\n\n")
	require.NoError(t, err)
	require.True(t, openAIStreamClientOutputStarted(c, false))
	require.Equal(t, prefix+"data: semantic\n\n", rec.Body.String())
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamKeepaliveInterval: 1}}}
	stop := svc.startOpenAIHeaderWaitKeepalive(c, true)
	stop()
	value, _ := c.Get(openAICompactSSEKeepaliveKey)
	require.Nil(t, value, "must not arm another header heartbeat after semantic output")
}
func TestOpenAIHeaderWaitFastErrorKeepsHTTPBoundary(t *testing.T) {
	c, rec, w, _ := newHeaderWaitTest(t, nil)
	stop := startOpenAIHeaderWaitKeepaliveInterval(c, w, time.Hour)
	t.Cleanup(stop)
	stop()
	c.JSON(400, gin.H{"error": gin.H{"type": "invalid_request_error", "message": "bad request"}})
	require.Equal(t, 400, rec.Code)
	require.NotContains(t, rec.Body.String(), "keepalive")
	require.Equal(t, "bad request", gjson.Get(rec.Body.String(), "error.message").String())
}
func TestOpenAIHeaderWaitCommittedErrorsBecomeTerminalSSE(t *testing.T) {
	for _, status := range []int{400, 403, 429, 502, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c, rec, w, signal := newHeaderWaitTest(t, nil)
			stop := startOpenAIHeaderWaitKeepaliveInterval(c, w, 5*time.Millisecond)
			t.Cleanup(stop)
			waitHeaderWaitBeat(t, signal)
			stop()
			c.JSON(status, gin.H{"error": gin.H{"type": "invalid_request_error", "code": "invalid_encrypted_content", "param": "input[2]", "message": "request rejected"}})
			require.Equal(t, 200, rec.Code)
			require.Equal(t, "text/event-stream", rec.Header().Get("Content-Type"))
			require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed"))
			var terminal []byte
			forEachOpenAISSEFrame(rec.Body.String(), func(event string, data []byte) {
				if event == "response.failed" {
					terminal = data
				}
			})
			require.NotEmpty(t, terminal)
			require.Equal(t, "request rejected", gjson.GetBytes(terminal, "response.error.message").String())
			require.Equal(t, "invalid_encrypted_content", gjson.GetBytes(terminal, "response.error.code").String())
			require.Equal(t, "input[2]", gjson.GetBytes(terminal, "response.error.param").String())
			require.Equal(t, "failed", gjson.GetBytes(terminal, "response.status").String())
			require.NotEmpty(t, gjson.GetBytes(terminal, "response.id").String())
			require.True(t, gjson.GetBytes(terminal, "response.created_at").Exists())
			require.True(t, gjson.GetBytes(terminal, "sequence_number").Exists())
			streamErr, ok := GetOpsStreamError(c)
			require.True(t, ok)
			require.Equal(t, status, streamErr.IntendedStatus)
			require.True(t, IsResponseCommitted(c))
			before := rec.Body.String()
			c.JSON(502, gin.H{"error": gin.H{"message": "duplicate fallback"}})
			require.Equal(t, before, rec.Body.String(), "error fallback must not append a second representation")
		})
	}
}
func TestOpenAIHeaderWaitFlushFailureDrainsUsageWithoutCompletion(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			want := errors.New("heartbeat flush disconnected")
			c, _, w, signal := newHeaderWaitTest(t, want)
			stop := startOpenAIHeaderWaitKeepaliveInterval(c, w, 5*time.Millisecond)
			t.Cleanup(stop)
			waitHeaderWaitBeat(t, signal)
			stop()
			require.True(t, OpenAIHeaderWaitDisconnected(c))
			svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
			resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"))}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			var usage *OpenAIUsage
			if passthrough {
				result, err := svc.handleStreamingResponsePassthrough(context.Background(), resp, c, account, time.Now(), "", "")
				require.NoError(t, err)
				require.True(t, result.clientDisconnect)
				usage = result.usage
			} else {
				result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "", "")
				require.NoError(t, err)
				require.True(t, result.clientDisconnect)
				usage = result.usage
			}
			require.Equal(t, 3, usage.InputTokens)
			require.Equal(t, 2, usage.OutputTokens)
			require.False(t, OpsStreamCompletedForAccount(c, 42))
		})
	}
}
func TestOpenAIHeaderWaitDisabledAndCompactAreUntouched(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		c, rec := newCompactBridgeTestContext(t, true)
		original := c.Writer
		cleanup := WrapOpenAIResponsesHeaderWait(c, enabled)
		cleanup()
		require.Same(t, original, c.Writer)
		require.Zero(t, rec.Body.Len())
	}
	c, rec, w, _ := newHeaderWaitTest(t, nil)
	svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{StreamKeepaliveInterval: 0}}}
	stop := svc.startOpenAIHeaderWaitKeepalive(c, true)
	stop()
	require.False(t, w.committed)
	require.Zero(t, rec.Body.Len())
}

func TestOpenAIHeaderWaitAdoptsEarlierQueueHeartbeat(t *testing.T) {
	c, rec, _, _ := newHeaderWaitTest(t, nil)
	c.Writer.Header().Set("Content-Type", "text/event-stream")
	_, err := c.Writer.WriteString(":\n\n")
	require.NoError(t, err)
	recordOpenAIStreamKeepaliveBytes(c, 3)
	c.JSON(400, gin.H{"error": gin.H{"code": "bad_input", "message": "bad request"}})
	require.Equal(t, 200, rec.Code)
	require.Contains(t, rec.Body.String(), "event: response.failed")
	require.NotContains(t, rec.Body.String(), "response.completed")
}
