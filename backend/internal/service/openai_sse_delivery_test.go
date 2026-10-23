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
	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAITerminalFlushWriter struct {
	gin.ResponseWriter
	recorder        *httptest.ResponseRecorder
	failTerminal    bool
	flushErr        error
	terminalFlushes int
}

func (w *openAITerminalFlushWriter) FlushError() error {
	if strings.Contains(w.recorder.Body.String(), `"type":"response.completed"`) {
		w.terminalFlushes++
		if w.failTerminal {
			return w.flushErr
		}
	}
	return FlushGatewayResponse(w.ResponseWriter)
}

// The upstream completes and every Write succeeds; only the transport flush of
// the final event fails. Usage must survive without a false delivery success.
func TestOpenAISSETerminalFlushDelivery(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			name := "native"
			if passthrough {
				name = "passthrough"
			}
			if fail {
				name += "/flush_failure"
			} else {
				name += "/success"
			}
			t.Run(name, func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				recorder := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(recorder)
				collector := requesttiming.New(time.Now(), 0)
				ctx := requesttiming.With(context.Background(), collector)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
				writer := &openAITerminalFlushWriter{ResponseWriter: c.Writer, recorder: recorder, failTerminal: fail, flushErr: errors.New("terminal flush failed")}
				c.Writer = writer
				account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
				body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
					"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"status\":\"completed\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}}\n\n"
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(body))}
				var disconnected bool
				var usage *OpenAIUsage
				if passthrough {
					result, err := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "", "")
					require.NoError(t, err) // Preserve upstream usage; do not replay committed output.
					require.NotNil(t, result)
					disconnected, usage = result.clientDisconnect, result.usage
				} else {
					result, err := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "", "")
					require.NoError(t, err)
					require.NotNil(t, result)
					disconnected, usage = result.clientDisconnect, result.usage
				}
				require.Equal(t, fail, disconnected)
				require.Equal(t, 3, usage.InputTokens)
				require.Equal(t, 2, usage.OutputTokens)
				require.Equal(t, 1, writer.terminalFlushes)
				require.Equal(t, !fail, OpsStreamCompletedForAccount(c, account.ID))
				var snapshot requesttiming.Snapshot
				collector.WhenFinished(func(s requesttiming.Snapshot) { snapshot = s })
				collector.Finish(200, false)
				require.Equal(t, "completed", snapshot.Terminal) // Upstream observation only.
				if fail {
					require.Empty(t, snapshot.TerminalFlushed)
					require.NotContains(t, snapshot.Events, "terminal_output_flush")
				} else {
					require.Equal(t, "response.completed", snapshot.TerminalFlushed)
					require.Contains(t, snapshot.Events, "terminal_output_flush")
				}
			})
		}
	}
}

func TestOpenAIAPIKeyBareErrorRetainsUpstreamProvenance(t *testing.T) {
	for _, errType := range []string{"api_error", "service_unavailable_error"} {
		t.Run(errType, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
			body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"error\",\"error\":{\"type\":\"" + errType + "\",\"code\":\"server_error\",\"message\":\"Our servers are currently overloaded. Please try again later.\"}}\n\n"
			resp := &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": []string{"upstream_test"}}, Body: io.NopCloser(strings.NewReader(body))}
			account := &Account{ID: 42, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			result, err := svc.handleStreamingResponse(context.Background(), resp, c, account, time.Now(), "", "")
			require.ErrorContains(t, err, "upstream response failed")
			require.NotNil(t, result)
			var failover *UpstreamFailoverError
			require.NotErrorAs(t, err, &failover)
			events, ok := c.Get(OpsUpstreamErrorsKey)
			require.True(t, ok)
			upstream, typed := events.([]*OpsUpstreamErrorEvent)
			require.True(t, typed)
			require.Len(t, upstream, 1)
			require.Equal(t, "stream_failed", upstream[0].Kind)
			require.Equal(t, account.ID, upstream[0].AccountID)
			require.Equal(t, "upstream_test", upstream[0].UpstreamRequestID)
			require.GreaterOrEqual(t, upstream[0].UpstreamStatusCode, 500)
			require.Contains(t, recorder.Body.String(), `"type":"error"`)
			require.NotContains(t, recorder.Body.String(), "response.completed")
		})
	}
}
