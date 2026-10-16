//go:build unit

package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCacheCreationBillingDispatchSnapshotAndRawResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []string{"messages", "responses", "chat/completions"} {
		for _, streaming := range []bool{false, true} {
			for _, enabled := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/stream=%t/enabled=%t", endpoint, streaming, enabled), func(t *testing.T) {
					account := forcedAnthropicCacheTTL1hTestAccount(true)
					account.Extra[CacheCreationBilling1hKey] = enabled
					usage := `{"input_tokens":4,"output_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":0,"cache_creation":{"ephemeral_5m_input_tokens":100,"ephemeral_1h_input_tokens":0}}`
					message := `{"id":"msg_cache","type":"message","role":"assistant","model":"claude-sonnet-4-5","content":[],"usage":` + usage + `}`
					upstreamBody := message
					contentType := "application/json"
					if streaming || endpoint != "messages" {
						contentType = "text/event-stream"
						upstreamBody = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":" + message + "}\n\n" +
							"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
							"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
							"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
							"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n" +
							"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
					}
					upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{StatusCode: 200, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(upstreamBody))}}, onCall: func(*http.Request, *queuedHTTPUpstreamStub) {
						// Change the account after dispatch: this request must keep its snapshot.
						account.Extra[CacheCreationBilling1hKey] = !enabled
					}}
					svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream, rateLimitService: &RateLimitService{}, tlsFPProfileService: &TLSFingerprintProfileService{}}
					recorder := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(recorder)
					body := []byte(fmt.Sprintf(`{"model":"claude-sonnet-4-5","stream":%t,"max_tokens":16,"input":"hello","messages":[{"role":"user","content":"hello"}]}`, streaming))
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+endpoint, strings.NewReader(string(body)))
					var result *ForwardResult
					var err error
					switch endpoint {
					case "messages":
						result, err = svc.Forward(context.Background(), c, account, &ParsedRequest{Body: NewRequestBodyRef(body), Model: "claude-sonnet-4-5", Stream: streaming})
					case "responses":
						result, err = svc.ForwardAsResponses(context.Background(), c, account, body, nil)
					default:
						result, err = svc.ForwardAsChatCompletions(context.Background(), c, account, body, nil)
					}
					require.NoError(t, err)
					require.Equal(t, enabled, result.CacheCreationBilling1h)
					require.Equal(t, 100, result.Usage.CacheCreation5mTokens)
					require.Zero(t, result.Usage.CacheCreation1hTokens)
					if endpoint == "messages" {
						require.Contains(t, recorder.Body.String(), `"ephemeral_5m_input_tokens":100`)
						require.Contains(t, recorder.Body.String(), `"ephemeral_1h_input_tokens":0`)
					}
				})
			}
		}
	}
}
