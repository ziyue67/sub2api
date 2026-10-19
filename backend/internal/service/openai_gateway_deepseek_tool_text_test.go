//go:build unit

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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponsesDeepSeekTextTools(t *testing.T) {
	const valid = `<｜DSML｜tool_calls><｜DSML｜invoke name="functions__exec"><｜DSML｜parameter name="input" string="true">text(42)</｜DSML｜parameter></｜DSML｜invoke></｜DSML｜tool_calls>`
	const broken = `<parameter name="input">{"cell_id":"21631"}</parameter>`
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name, model, text        string
			tools, failed, recovered bool
		}{
			{"mapped_custom", "deepseek-v4.1-flash", valid, true, false, true},
			{"orphan_parameter", "deepseek-v4.1-flash", broken, true, true, false},
			{"malformed_dsml", "deepseek-v4.1-flash", `</｜｜DSML｜｜ calls>`, true, true, false},
			{"other_model", "other-model", broken, true, false, false},
			{"no_tools", "deepseek-v4.1-flash", broken, false, false, false},
		} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				body := map[string]any{"model": "gpt-5.4", "input": "use the provided tool", "stream": stream}
				if tc.tools {
					body["tools"] = []any{map[string]any{"type": "namespace", "name": "functions", "tools": []any{map[string]any{"type": "custom", "name": "exec"}}}}
				}
				raw, _ := json.Marshal(body)
				account := forceChatResponsesFallbackAccount()
				account.Credentials["model_mapping"] = map[string]any{"gpt-5.4": tc.model}
				var upstreamBody strings.Builder
				contentType := "application/json"
				if stream {
					contentType = "text/event-stream"
					// Emit one Unicode character per upstream delta so every SSE
					// frame is independently valid while all delimiters are split.
					for _, part := range strings.Split(tc.text, "") {
						encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}}}})
						_, _ = fmt.Fprintf(&upstreamBody, "data: %s\n\n", encoded)
					}
					_, _ = upstreamBody.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\ndata: [DONE]\n\n")
				} else {
					encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": tc.text}, "finish_reason": "stop"}}, "usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2, "total_tokens": 5}})
					_, _ = upstreamBody.Write(encoded)
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
				upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}}, Body: io.NopCloser(strings.NewReader(upstreamBody.String()))}}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
				result, err := svc.forwardResponsesViaRawChatCompletions(context.Background(), c, account, raw, time.Now())
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, result.Usage.OutputTokens)
				output := rec.Body.String()
				if tc.failed {
					require.Contains(t, output, "upstream_tool_protocol_error")
					require.NotContains(t, output, "response.completed")
					require.NotContains(t, output, "custom_tool_call")
					require.NotContains(t, output, "21631")
				} else if tc.recovered {
					require.Contains(t, output, `"custom_tool_call"`)
					require.Contains(t, output, `"namespace":"functions"`)
					require.Contains(t, output, `"input":"text(42)"`)
					require.NotContains(t, output, "DSML")
				} else {
					require.NotContains(t, output, "upstream_tool_protocol_error")
				}
			})
		}
	}
}
