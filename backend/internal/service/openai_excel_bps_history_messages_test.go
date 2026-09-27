package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExcelBPSHistoryMessageAttributionForward(t *testing.T) {
	for _, kind := range []string{"message", "agent_message"} {
		for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", kind, path, stream), func(t *testing.T) {
					wire := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_attribution\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
					upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}}
					svc := openAIClientToolsTestService(upstream)
					source := map[string]any{"model": "gpt-6-astra", "stream": stream, "input": []any{map[string]any{"type": kind, "role": "user", "author": "/root/worker", "recipient": "/root", "content": []any{map[string]any{"type": "input_text", "text": "task result\nnext line"}}}}}
					body, err := json.Marshal(source)
					require.NoError(t, err)
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					result, err := svc.Forward(context.Background(), c, excelAccount(), body)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, http.StatusOK, rec.Code)
					items := gjson.GetBytes(upstream.lastBody, "input").Array()
					require.NotEmpty(t, items)
					for _, item := range items {
						require.False(t, item.Get("author").Exists())
						require.False(t, item.Get("recipient").Exists())
						require.NotEqual(t, "agent_message", item.Get("type").String())
					}
					messageIndex := len(items) - 1
					if path == "/v1/responses/compact" {
						require.Equal(t, "compaction_trigger", items[messageIndex].Get("type").String())
						messageIndex--
					}
					require.GreaterOrEqual(t, messageIndex, 0)
					last := items[messageIndex]
					require.Equal(t, "message", last.Get("type").String())
					require.Equal(t, "user", last.Get("role").String())
					require.Contains(t, last.Get("content.0.text").String(), "/root/worker")
					require.Equal(t, "task result\nnext line", last.Get("content.1.text").String())
				})
			}
		}
	}
}
