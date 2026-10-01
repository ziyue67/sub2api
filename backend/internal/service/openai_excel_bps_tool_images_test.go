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

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExcelBPSNativeToolImagesForward(t *testing.T) {
	for _, kind := range []string{"function", "custom"} {
		for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/stream=%t", kind, path, stream), func(t *testing.T) {
					original, _ := nativeGatewayBody(t)
					var source map[string]any
					require.NoError(t, json.Unmarshal(original, &source))
					images := nativeGatewayParts(t, source)
					historyKind := kind
					if kind == "custom" {
						historyKind = "custom_tool"
					}
					call := map[string]any{"type": historyKind + "_call", "name": "view_image", "call_id": "call_image"}
					if kind == "function" {
						call["arguments"] = "{}"
					} else {
						call["input"] = "image.png"
					}
					source["tools"] = []any{map[string]any{"type": kind, "name": "view_image"}}
					output := []any{map[string]any{"type": "input_text", "text": "screenshot result"}}
					for range images {
						output = append(output, map[string]any{"type": "input_image", "file_id": "file-toolimage", "detail": "original"})
					}
					input, ok := source["input"].([]any)
					require.True(t, ok)
					source["input"] = append(input, call, map[string]any{"type": historyKind + "_call_output", "call_id": "call_image", "output": output})
					source["stream"] = stream
					body, err := json.Marshal(source)
					require.NoError(t, err)
					svc := openAIClientToolsTestService(nil)
					enableNativeAttachments(svc)
					uploaded := &nativeAttachmentBody{Reader: strings.NewReader("{\"openai_file_id\":\"file-userimage\"}")}
					calls := 0
					svc.httpUpstream = &nativeAttachmentUpstream{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
						calls++
						if calls == 1 {
							require.Equal(t, basispoints.AttachmentsURL, req.URL.String())
							return &http.Response{StatusCode: 200, Header: http.Header{}, Body: uploaded}, nil
						}
						require.Equal(t, 2, calls)
						require.True(t, uploaded.closed)
						require.Equal(t, basispoints.ResponsesURL, req.URL.String())
						raw, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						require.NotContains(t, string(raw), "file-preflight")
						require.NotContains(t, string(raw), "data:image")
						require.Contains(t, string(raw), "file-userimage")
						items := gjson.GetBytes(raw, "input").Array()
						results, found := 0, 0
						for i, item := range items {
							if item.Get("type").String() != "function_call_output" {
								continue
							}
							results++
							require.Equal(t, "call_image", item.Get("call_id").String())
							require.Equal(t, "screenshot result", item.Get("output.0.text").String())
							for _, part := range item.Get("output").Array() {
								require.NotEqual(t, "input_image", part.Get("type").String())
							}
							require.Less(t, i+1, len(items))
							next := items[i+1]
							require.Equal(t, "user", next.Get("role").String())
							for _, part := range next.Get("content").Array() {
								if part.Get("type").String() == "input_image" {
									found++
									require.Equal(t, "file-toolimage", part.Get("file_id").String())
									require.Len(t, part.Map(), 2, "moved message attachments only accept type and file_id")
								}
							}
						}
						require.Equal(t, 1, results)
						require.Equal(t, 2, found)
						wire := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_toolimage\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}, nil
					}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
					result, err := svc.Forward(context.Background(), c, excelAccount(), body)
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 2, calls, "duplicate images share one upload before the response")
				})
			}
		}
	}
}
