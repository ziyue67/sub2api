package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
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

func bpsCompletionResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestExcelBPSNativeAttachmentsUseSelectedTransport(t *testing.T) {
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 2, 3))))
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{responses: []*http.Response{
				bpsCompletionResponse(200, `{"openai_file_id":"file-native"}`),
				bpsCompletionResponse(200, "data: "+`{"type":"response.completed","response":{"id":"resp_image","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":1}}}`+"\n\n"),
			}}
			svc := openAIClientToolsTestService(upstream)
			enableNativeAttachments(svc)
			account := excelAccount()
			body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":%v,"input":[{"role":"user","content":[{"type":"input_image","image_url":%q,"detail":"high"}]}]}`, stream, imageURL))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(body))
			result, err := svc.Forward(context.Background(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Len(t, upstream.requests, 2)
			require.Equal(t, basispoints.AttachmentsURL, upstream.requests[0].URL.String())
			require.True(t, HTTPUpstreamRedirectsDisabled(upstream.requests[0].Context()))
			require.Equal(t, basispoints.ResponsesURL, upstream.requests[1].URL.String())
			require.Equal(t, upstream.requests[0].Header.Get("Authorization"), upstream.requests[1].Header.Get("Authorization"))
			require.Equal(t, upstream.requests[0].Header.Get("Chatgpt-Account-Id"), upstream.requests[1].Header.Get("Chatgpt-Account-Id"))
			multipart, err := upstream.requests[0].MultipartReader()
			require.NoError(t, err)
			part, err := multipart.NextPart()
			require.NoError(t, err)
			data, err := io.ReadAll(part)
			require.NoError(t, err)
			require.Equal(t, pngBytes.Bytes(), data)
			require.Contains(t, string(upstream.bodies[1]), `"file_id":"file-native"`)
			require.NotContains(t, string(upstream.bodies[1]), "data:image")
			require.Nil(t, svc.excelBPSImages, "native mode must not create disk relay")
		})
	}
}
func TestExcelBPSNativeAttachmentFailureDoesNotGenerate(t *testing.T) {
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))))
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes.Bytes())
	for _, status := range []int{302, 400, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: bpsCompletionResponse(status, "PRIVATE_RESPONSE")}
			svc := openAIClientToolsTestService(upstream)
			enableNativeAttachments(svc)
			body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","input":[{"role":"user","content":[{"type":"input_image","image_url":%q}]}]}`, url))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			_, err := svc.Forward(context.Background(), c, excelAccount(), body)
			require.Error(t, err)
			require.Len(t, upstream.requests, 1)
			expected := status
			if status == 302 {
				expected = 502
			}
			require.Equal(t, expected, rec.Code)
			require.NotContains(t, rec.Body.String(), "PRIVATE_RESPONSE")
		})
	}
}
func TestExcelBPSCorrectionUsesSameModelAndCountsBothAttempts(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			response := func(name, id string, tokens int) *http.Response {
				envelope, _ := json.Marshal(map[string]any{"name": name, "arguments": map[string]any{"cmd": "pwd"}})
				event, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "usage": map[string]any{"input_tokens": tokens, "output_tokens": 1}, "output": []any{map[string]any{"id": "fc_" + id, "call_id": "call_" + id, "type": "function_call", "name": "run_officejs", "arguments": map[string]any{"code": string(envelope)}}}}})
				return bpsCompletionResponse(200, "data: "+string(event)+"\n\n")
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{response("missing", "original", 5), response("shell", "fixed", 7)}}
			svc := openAIClientToolsTestService(upstream)
			enableNativeAttachments(svc)
			body := []byte(fmt.Sprintf(`{"model":"gpt-6-astra","stream":%v,"input":"run pwd","tools":[{"type":"function","name":"shell","parameters":{"type":"object","required":["cmd"],"properties":{"cmd":{"type":"string"}}}}]}`, stream))
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			result, err := svc.Forward(context.Background(), c, excelAccount(), body)
			require.NoError(t, err)
			require.Len(t, upstream.requests, 2)
			require.EqualValues(t, 12, result.Usage.InputTokens)
			require.EqualValues(t, 2, result.Usage.OutputTokens)
			require.Equal(t, upstream.requests[0].Header.Get("Authorization"), upstream.requests[1].Header.Get("Authorization"))
			require.Equal(t, gjson.GetBytes(upstream.bodies[0], "model").String(), gjson.GetBytes(upstream.bodies[1], "model").String())
			require.Contains(t, string(upstream.bodies[1]), "No client tool from that response was executed")
			require.Contains(t, rec.Body.String(), `"name":"shell"`)
			require.NotContains(t, rec.Body.String(), `"name":"missing"`)
		})
	}
}
