//go:build unit

package service

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const chatBridgeCreated = `data: {"type":"response.created","response":{"id":"resp_bridge","model":"gpt-6-luna"}}` + "\n\n"
const chatBridgeOverload = `data: {"type":"response.failed","response":{"status":"failed","error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}}` + "\n\n"

func TestChatBridgeMetadataPreservesCapacityFailover(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, size := range []int{3493, 64 * 1024} {
		for _, keepalive := range []int{0, 1} {
			for _, failure := range []string{
				chatBridgeOverload,
				`event: error` + "\n" + `data: {"error":{"code":"server_is_overloaded","message":"Our servers are currently overloaded. Please try again later."}}` + "\n\n" + chatBridgeOverload,
			} {
				t.Run(fmt.Sprintf("size=%d/keepalive=%d/bare=%t", size, keepalive, strings.HasPrefix(failure, "event:")), func(t *testing.T) {
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
					cfg := &config.Config{}
					cfg.Gateway.StreamKeepaliveInterval = keepalive
					svc := &OpenAIGatewayService{cfg: cfg}
					resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(chatBridgeCreated + failure))}
					result, err := svc.handleChatStreamingResponse(resp, c,
						&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
						"gpt-6-luna", "gpt-6-luna", "gpt-6-luna", time.Now(), size)

					var failover *UpstreamFailoverError
					require.ErrorAs(t, err, &failover)
					require.Equal(t, http.StatusServiceUnavailable, failover.StatusCode)
					require.True(t, failover.RetryableOnSameAccount)
					require.True(t, failover.RequestScopedTransient)
					require.Nil(t, result, "failed attempts must not submit a zero-usage record")
					require.False(t, c.Writer.Written(), "role and keepalive must not commit an attempt")
					require.Empty(t, rec.Body.String())
					require.Empty(t, GetOpsStreamErrors(c), "an attempt eligible for recovery is not a final request failure")
				})
			}
		}
	}
}

func TestChatBridgeOutputPreventsReplayAndMarksFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for name, output := range map[string]string{
		"text":      `{"type":"response.output_text.delta","delta":"partial"}`,
		"reasoning": `{"type":"response.reasoning_summary_text.delta","delta":"thinking"}`,
		"refusal":   `{"type":"response.refusal.delta","delta":"cannot help"}`,
		"tool":      `{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"lookup"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			failed := strings.Replace(chatBridgeOverload, `"status":"failed"`, `"status":"failed","usage":{"input_tokens":11,"output_tokens":3}`, 1)
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(chatBridgeCreated + "data: " + output + "\n\n" + failed))}
			svc := &OpenAIGatewayService{}
			result, err := svc.handleChatStreamingResponse(resp, c,
				&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				"gpt-6-luna", "gpt-6-luna", "gpt-6-luna", time.Now(), 3493)

			require.Error(t, err)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "replaying text or tool output is unsafe")
			require.NotNil(t, result)
			require.NotNil(t, result.FirstTokenMs)
			require.Equal(t, 11, result.Usage.InputTokens)
			require.Equal(t, 3, result.Usage.OutputTokens)
			require.Equal(t, http.StatusOK, rec.Code, "committed HTTP status stays 200")
			require.Contains(t, rec.Body.String(), `"role":"assistant"`)
			require.Contains(t, rec.Body.String(), "currently overloaded")
			errorsForRequest := GetOpsStreamErrors(c)
			require.Len(t, errorsForRequest, 1)
			require.True(t, errorsForRequest[0].CountTowardsSLA)
			require.Equal(t, http.StatusServiceUnavailable, errorsForRequest[0].IntendedStatus)
			require.Equal(t, "server_is_overloaded", errorsForRequest[0].Code)
		})
	}
}

// Observe the writer at the boundary between preamble and terminal data.
// This avoids timing sleeps while proving metadata has not been released.
type chatBridgeBoundaryReader struct {
	io.Reader
	beforeRead func()
}

func (r *chatBridgeBoundaryReader) Read(p []byte) (int, error) {
	if r.beforeRead != nil {
		r.beforeRead()
		r.beforeRead = nil
	}
	return r.Reader.Read(p)
}

func TestChatBridgeFirstTokenIgnoresMetadataAndEmptyCompletion(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, withText := range []bool{false, true} {
		t.Run(fmt.Sprint(withText), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			terminal := `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":0}}}` + "\n\n"
			if withText {
				terminal = `data: {"type":"response.output_text.delta","delta":"OK"}` + "\n\n" + terminal
			}
			var outputBoundary time.Time
			body := io.MultiReader(strings.NewReader(chatBridgeCreated), &chatBridgeBoundaryReader{
				Reader: strings.NewReader(terminal),
				beforeRead: func() {
					require.False(t, c.Writer.Written())
					require.Empty(t, rec.Body.String())
					outputBoundary = time.Now()
				},
			})
			start := time.Now().Add(-time.Second)
			resp := &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(body)}
			svc := &OpenAIGatewayService{}
			result, err := svc.handleChatStreamingResponse(resp, c,
				&Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				"gpt-6-luna", "gpt-6-luna", "gpt-6-luna", start, 3493)
			require.NoError(t, err)
			require.NotNil(t, result)
			if withText {
				require.NotNil(t, result.FirstTokenMs)
				require.GreaterOrEqual(t, *result.FirstTokenMs, int(outputBoundary.Sub(start).Milliseconds()))
			} else {
				require.Nil(t, result.FirstTokenMs, "role, finish and usage do not report a first token")
			}
			require.Equal(t, 1, strings.Count(rec.Body.String(), `"role":"assistant"`))
			require.Equal(t, 1, strings.Count(rec.Body.String(), "data: [DONE]"))
			require.Contains(t, rec.Body.String(), `"prompt_tokens":1`)
		})
	}
}
