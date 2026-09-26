package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type nativeAttachmentUpstream struct {
	HTTPUpstream
	do func(*http.Request, string, int64, int) (*http.Response, error)
}

func (u *nativeAttachmentUpstream) Do(r *http.Request, p string, id int64, n int) (*http.Response, error) {
	return u.do(r, p, id, n)
}

type nativeAttachmentBody struct {
	io.Reader
	closed bool
}

func (b *nativeAttachmentBody) Close() error { b.closed = true; return nil }

func nativeGatewayBody(t *testing.T) ([]byte, []byte) {
	t.Helper()
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 3))))
	part := map[string]any{"type": "input_image", "image_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(data.Bytes()), "detail": "original"}
	body, err := json.Marshal(map[string]any{"model": "gpt-6-astra", "input": []any{map[string]any{"role": "user", "content": []any{part, part}}}})
	require.NoError(t, err)
	return body, data.Bytes()
}
func enableNativeAttachments(s *OpenAIGatewayService) {
	s.settingService = NewSettingService(&excelBPSImageSettingsRepo{values: map[string]string{SettingKeyExcelBPSImageRelayEnabled: "true", SettingKeyExcelBPSImageMode: "native"}}, s.cfg)
}

func TestExcelBPSNativeAttachmentForward(t *testing.T) {
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", path, stream), func(t *testing.T) {
				body, pixels := nativeGatewayBody(t)
				body = bytes.Replace(body, []byte("{\"input\""), []byte(fmt.Sprintf("{\"stream\":%t,\"input\"", stream)), 1)
				uploaded := &nativeAttachmentBody{Reader: strings.NewReader("{\"openai_file_id\":\"file-native123\"}")}
				calls := 0
				svc := openAIClientToolsTestService(nil)
				enableNativeAttachments(svc)
				account := excelAccount()
				account.Concurrency = 1
				account.Proxy = &Proxy{Protocol: "http", Host: "proxy.example", Port: 8080}
				svc.httpUpstream = &nativeAttachmentUpstream{do: func(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
					calls++
					require.Equal(t, account.ID, id)
					require.Equal(t, 1, concurrency)
					require.Equal(t, account.Proxy.URL(), proxy)
					require.Equal(t, "Bearer test-token", req.Header.Get("Authorization"))
					require.Equal(t, "test-account", req.Header.Get("Chatgpt-Account-Id"))
					require.True(t, HTTPUpstreamRedirectsDisabled(req.Context()))
					require.Empty(t, req.Header.Get("Cookie"))
					require.Empty(t, req.Header.Get("x-codex-turn-state"))
					if calls == 1 {
						require.Equal(t, basispoints.AttachmentsURL, req.URL.String())
						require.Equal(t, "identity", req.Header.Get("Accept-Encoding"))
						deadline, ok := req.Context().Deadline()
						require.True(t, ok)
						require.LessOrEqual(t, time.Until(deadline), 60*time.Second)
						raw, err := io.ReadAll(req.Body)
						require.NoError(t, err)
						require.Equal(t, int64(len(raw)), req.ContentLength)
						req.Body = io.NopCloser(bytes.NewReader(raw))
						mr, err := req.MultipartReader()
						require.NoError(t, err)
						part, err := mr.NextPart()
						require.NoError(t, err)
						require.Equal(t, "file", part.FormName())
						data, err := io.ReadAll(part)
						require.NoError(t, err)
						require.Equal(t, pixels, data)
						_, err = mr.NextPart()
						require.ErrorIs(t, err, io.EOF)
						return &http.Response{StatusCode: 200, Header: http.Header{}, Body: uploaded}, nil
					}
					require.True(t, uploaded.closed, "upload slot must be released before Responses at concurrency=1")
					require.Equal(t, basispoints.ResponsesURL, req.URL.String())
					raw, err := io.ReadAll(req.Body)
					require.NoError(t, err)
					require.NotContains(t, string(raw), "data:image")
					found := 0
					for _, item := range gjson.GetBytes(raw, "input").Array() {
						for _, part := range item.Get("content").Array() {
							if part.Get("type").String() == "input_image" {
								found++
								require.Equal(t, "file-native123", part.Get("file_id").String())
								require.Equal(t, "original", part.Get("detail").String())
								require.False(t, part.Get("image_url").Exists())
							}
						}
					}
					require.Equal(t, 2, found)
					wire := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_native\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
					return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(wire))}, nil
				}}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				c.Request.Header.Set("Cookie", "private-cookie")
				c.Request.Header.Set("x-codex-turn-state", "private-state")
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, 2, calls)
			})
		}
	}
}

func TestExcelBPSNativeUploadFailureStopsWithoutQuotaWrite(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		transport bool
		want      int
	}{
		{"429", 429, "PRIVATE_BODY", false, 429}, {"403", 403, "PRIVATE_BODY", false, 403}, {"500", 500, "PRIVATE_BODY", false, 500},
		{"redirect", 307, "PRIVATE_BODY", false, 502}, {"wrong ID", 200, "{\"openai_file_id\":\"PRIVATE_BODY\"}", false, 502},
		{"invalid JSON", 200, "PRIVATE_BODY", false, 502}, {"oversized JSON", 200, strings.Repeat("X", (64<<10)+1), false, 502},
		{"transport", 0, "", true, 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := openAIClientToolsTestService(nil)
			enableNativeAttachments(svc)
			repo := &excelBPSQuotaRepo{writes: make(chan excelBPSQuotaWrite, 4)}
			svc.accountRepo = repo
			svc.rateLimitService = NewRateLimitService(repo, nil, svc.cfg, nil, nil)
			svc.rateLimitService.SetAccountRuntimeBlocker(svc)
			calls := 0
			closed := &nativeAttachmentBody{Reader: strings.NewReader(tc.body)}
			svc.httpUpstream = &nativeAttachmentUpstream{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				require.Equal(t, basispoints.AttachmentsURL, req.URL.String())
				if tc.transport {
					return nil, errors.New("PRIVATE_TOKEN")
				}
				return &http.Response{StatusCode: tc.status, Header: excelBPSQuotaHeaders("100", "100"), Body: closed}, nil
			}}
			body, _ := nativeGatewayBody(t)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			account := excelAccount()
			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Equal(t, tc.want, rec.Code)
			require.Equal(t, 1, calls)
			require.True(t, IsResponseCommitted(c))
			require.NotContains(t, rec.Body.String(), "PRIVATE")
			require.NotContains(t, err.Error(), "PRIVATE")
			requireNoExcelBPSQuotaWrite(t, repo)
			require.True(t, account.IsSchedulable())
			require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
			if !tc.transport {
				require.True(t, closed.closed)
			}
			if tc.status == 429 {
				require.Contains(t, rec.Body.String(), "basispoints_rate_limited")
			}
		})
	}
}

func TestExcelBPSNativePreflightRejectsBeforeNetwork(t *testing.T) {
	body, _ := nativeGatewayBody(t)
	for name, change := range map[string]func(map[string]any){
		"invalid second image": func(v map[string]any) {
			parts := nativeGatewayParts(t, v)
			part, ok := parts[1].(map[string]any)
			require.True(t, ok)
			part["image_url"] = "data:image/png;base64,PRIVATE"
		},
		"invalid tool choice": func(v map[string]any) { v["tool_choice"] = "required" },
		"previous response":   func(v map[string]any) { v["previous_response_id"] = "resp_unsupported" },
		"ambiguous reference": func(v map[string]any) {
			parts := nativeGatewayParts(t, v)
			part, ok := parts[0].(map[string]any)
			require.True(t, ok)
			part["file_id"] = "file-mixed"
		},
	} {
		t.Run(name, func(t *testing.T) {
			var source map[string]any
			require.NoError(t, json.Unmarshal(body, &source))
			change(source)
			raw, err := json.Marshal(source)
			require.NoError(t, err)
			upstream := &httpUpstreamRecorder{}
			svc := openAIClientToolsTestService(upstream)
			enableNativeAttachments(svc)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(raw))
			_, err = svc.Forward(context.Background(), c, excelAccount(), raw)
			require.Error(t, err)
			require.Equal(t, 400, rec.Code)
			require.Empty(t, upstream.requests)
			require.NotContains(t, rec.Body.String(), "PRIVATE")
		})
	}
}

func TestExcelBPSNativeAttachmentIDRedaction(t *testing.T) {
	raw := "{\"error\":{\"message\":\"cannot read file-private123 from this account\"}}"
	out := excelBPSSanitizeErrorBody(raw, "test-token", excelAccount())
	require.NotContains(t, out, "private123")
	require.Contains(t, out, "file-[redacted]")
}

func nativeGatewayParts(t *testing.T, v map[string]any) []any {
	t.Helper()
	input, ok := v["input"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, input)
	message, ok := input[0].(map[string]any)
	require.True(t, ok)
	parts, ok := message["content"].([]any)
	require.True(t, ok)
	return parts
}

func TestExcelBPSAttachmentUsesResolvedSessionProxy(t *testing.T) {
	body, _ := nativeGatewayBody(t)
	images, err := basispoints.PrepareNativeImages(body)
	require.NoError(t, err)
	svc := openAIClientToolsTestService(nil)
	account := excelAccount()
	account.Proxy = &Proxy{Protocol: "http", Host: "account-proxy.example", Port: 8080}
	const selected = "http://127.0.0.1:19007"
	calls := 0
	svc.httpUpstream = &nativeAttachmentUpstream{do: func(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
		calls++
		require.Equal(t, selected, proxy)
		require.Equal(t, account.ID, id)
		require.Equal(t, basispoints.AttachmentsURL, req.URL.String())
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("{\"openai_file_id\":\"file-selected-proxy\"}"))}, nil
	}}
	_, err = images.Upload(context.Background(), new(basispoints.AttachmentCache), "", func(ctx context.Context, img basispoints.InlineAttachment) (string, error) {
		return svc.uploadExcelBPSAttachment(ctx, account, "test-token", "test-account", selected, img)
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}
