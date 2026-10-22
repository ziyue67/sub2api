package service

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestHTTPWSRouteDiagnosticsFollowActualDispatch(t *testing.T) {
	for _, scenario := range []string{"handshake_fallback", "after_send_failure", "success"} {
		t.Run(scenario, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			SetOpenAIClientTransport(c, OpenAIClientTransportHTTP)
			collector := requesttiming.New(time.Now(), 0)
			ctx := requesttiming.With(c.Request.Context(), collector)
			c.Request = c.Request.WithContext(ctx)
			d := &wsSSETestDialer{conn: &openAIWSCaptureConn{}}
			switch scenario {
			case "handshake_fallback":
				d.conn, d.status, d.err = nil, 426, errors.New("SECRET_DIAL_DETAIL")
			case "success":
				d.conn = &openAIWSCaptureConn{events: [][]byte{
					[]byte(`{"type":"response.created","response":{"id":"resp_test"}}`),
					[]byte(`{"type":"response.output_text.delta","delta":"hello"}`),
					[]byte(`{"type":"response.completed","response":{"id":"resp_test","usage":{"input_tokens":3,"output_tokens":1}}}`),
				}}
			}
			httpUpstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n"))}}
			s := wsSSETestService(t, d, httpUpstream)
			_, err := s.Forward(ctx, c, wsSSETestAccount(), []byte(wsSSETestRequest))
			if scenario == "after_send_failure" {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			collector.Finish(200, false)
			collector.WhenFinished(func(snapshot requesttiming.Snapshot) {
				require.NotEmpty(t, snapshot.Routes)
				first := snapshot.Routes[0]
				require.Equal(t, "websocket_handshake", first.Phase)
				require.Equal(t, "http", first.Route.Downstream)
				require.Equal(t, "websocket", first.Route.Upstream)
				require.Equal(t, "ctx_pool", first.Route.Mode)
				require.Equal(t, "oauth_http_sse_acceleration", first.Route.Reason)
				require.NotNil(t, first.EndMS)
				if scenario == "handshake_fallback" {
					require.Len(t, snapshot.Routes, 1)
					require.Equal(t, 426, first.Status)
					require.Equal(t, "transport_error", first.Error)
					require.NotNil(t, httpUpstream.lastReq)
					route := transportdiag.RouteFromContext(httpUpstream.lastReq.Context())
					require.Equal(t, "native", route.Mode)
					require.Equal(t, "oauth_ws_sse_handshake_fallback", route.FallbackReason)
				} else {
					require.Nil(t, httpUpstream.lastReq, "a sent WS request must not fall back to HTTP")
					require.Len(t, snapshot.Routes, 2)
					require.Equal(t, "websocket_write", snapshot.Routes[1].Phase)
					require.Empty(t, snapshot.Routes[1].Route.FallbackReason)
				}
			})
			require.EqualValues(t, 1, d.calls.Load())
		})
	}
}
