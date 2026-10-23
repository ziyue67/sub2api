package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise the actual ingress mode dispatch. Disabled retains the old mode's
// successful fixture response; enabled rejects before either upstream sends.
func TestWSNodeOwnerNonTargetModeCompatibility(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModePassthrough, OpenAIWSIngressModeHTTPBridge} {
		for _, enabled := range []bool{false, true} {
			for _, store := range []string{"true", "false"} {
				name := mode + "/disabled/store=" + store
				if enabled {
					name = mode + "/enabled/store=" + store
				}
				t.Run(name, func(t *testing.T) {
					gin.SetMode(gin.TestMode)
					cache := &wsNodeOwnerCacheStub{err: io.ErrUnexpectedEOF}
					svc := ownerTestService(cache, "node-a")
					cfg := svc.cfg
					cfg.Runtime.WSNodeOwner.Enabled = enabled
					cfg.Gateway.OpenAIWS.Enabled = true
					cfg.Gateway.OpenAIWS.APIKeyEnabled = true
					cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
					cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
					cfg.Gateway.OpenAIWS.IngressModeDefault = OpenAIWSIngressModeCtxPool
					cfg.Gateway.OpenAIWS.DialTimeoutSeconds = 3
					cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 3
					cfg.Gateway.OpenAIWS.WriteTimeoutSeconds = 3
					terminal := `{"type":"response.completed","response":{"id":"resp_mode_fixture","model":"gpt-6.1-sol","usage":{"input_tokens":1,"output_tokens":1}}}`
					capture := &openAIWSCaptureConn{events: [][]byte{[]byte(terminal)}}
					dialer := &openAIWSCaptureDialer{conn: capture}
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK,
						Header: http.Header{"Content-Type": []string{"text/event-stream"}},
						Body:   io.NopCloser(strings.NewReader("data: " + terminal + "\n\ndata: [DONE]\n\n"))}}
					svc.httpUpstream = upstream
					svc.openaiWSPassthroughDialer = dialer
					svc.openaiWSResolver = NewOpenAIWSProtocolResolver(cfg)
					svc.toolCorrector = NewCodexToolCorrector()
					account := &Account{ID: 201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
						Concurrency: 1, GroupIDs: []int64{17}, Credentials: map[string]any{"api_key": "fixture-not-a-key"},
						Extra: map[string]any{"openai_apikey_responses_websockets_v2_mode": mode}}
					result := make(chan error, 1)
					var afterTurn atomic.Int64
					hooks := &OpenAIWSIngressHooks{AfterTurn: func(_ int, _ *OpenAIForwardResult, _ error) { afterTurn.Add(1) }}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						conn, err := coderws.Accept(w, r, nil)
						if err != nil {
							result <- err
							return
						}
						defer func() { _ = conn.CloseNow() }()
						c, _ := gin.CreateTestContext(httptest.NewRecorder())
						c.Request = r
						groupID := int64(17)
						c.Set("api_key", &APIKey{ID: 101, GroupID: &groupID})
						ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
						defer cancel()
						_, first, err := conn.Read(ctx)
						if err == nil {
							err = svc.ProxyResponsesWebSocketFromClient(ctx, c, conn, account, "fixture-not-a-key", first, hooks)
						}
						result <- err
					}))
					defer server.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
					require.NoError(t, err)
					defer func() { _ = client.CloseNow() }()
					require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(`{"type":"response.create","model":"gpt-6.1-sol","stream":false,"store":`+store+`}`)))
					if !enabled {
						_, event, err := client.Read(ctx)
						require.NoError(t, err)
						require.Equal(t, "resp_mode_fixture", gjson.GetBytes(event, "response.id").String())
						require.Eventually(t, func() bool { return afterTurn.Load() == 1 }, time.Second, time.Millisecond)
						_ = client.Close(coderws.StatusNormalClosure, "fixture done")
					}
					select {
					case err := <-result:
						if enabled {
							require.ErrorIs(t, err, ErrOpenAIWSNodeOwner)
							require.Zero(t, dialer.DialCount())
							require.Empty(t, capture.writes)
							require.Nil(t, upstream.lastReq)
							require.Zero(t, afterTurn.Load())
						} else {
							require.NoError(t, err)
							require.EqualValues(t, 1, afterTurn.Load())
							if mode == OpenAIWSIngressModePassthrough {
								require.Equal(t, 1, dialer.DialCount())
								require.Len(t, capture.writes, 1)
							} else {
								require.NotNil(t, upstream.lastReq)
								require.Zero(t, dialer.DialCount())
							}
						}
					case <-ctx.Done():
						t.Fatal("mode compatibility fixture timed out")
					}
				})
			}
		}
	}
}
