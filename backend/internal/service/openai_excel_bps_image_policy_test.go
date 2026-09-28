//go:build unit

package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type imagePolicyMemoryCache struct {
	GatewayCache
	mu       sync.Mutex
	progress map[string]string
	warning  map[string]bool
	err      error
}

func newImagePolicyMemoryCache() *imagePolicyMemoryCache {
	return &imagePolicyMemoryCache{progress: map[string]string{}, warning: map[string]bool{}}
}
func (c *imagePolicyMemoryCache) LoadBPSImageProgress(_ context.Context, k string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.progress[k], c.err
}
func (c *imagePolicyMemoryCache) SaveBPSImageProgress(_ context.Context, k, old, next string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.progress[k] != old {
		return errors.New("image history changed concurrently")
	}
	c.progress[k] = next
	return c.err
}
func (c *imagePolicyMemoryCache) ClaimBPSImageWarning(_ context.Context, k string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	first := !c.warning[k]
	c.warning[k] = true
	return first, c.err
}
func (c *imagePolicyMemoryCache) ResetBPSImageWarning(_ context.Context, k string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.warning, k)
	return c.err
}
func imagePolicyRequest(t *testing.T, h, k int) []byte {
	t.Helper()
	msg := func(n int) map[string]any {
		parts := []any{}
		for i := 0; i < n; i++ {
			parts = append(parts, map[string]any{"type": "input_image", "image_url": "data:image/png;base64,synthetic"})
		}
		return map[string]any{"role": "user", "content": parts}
	}
	b, e := json.Marshal(map[string]any{"model": "test-model", "input": []any{msg(h), map[string]any{"role": "assistant", "content": "seen"}, msg(k)}})
	require.NoError(t, e)
	return b
}
func imagePolicyContext(path string) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	c.Request.Header.Set("User-Agent", "codex_cli_rs/0.116.0")
	return c
}
func imagePolicySettings(policy string) ExcelBPSImageRelaySettings {
	return ExcelBPSImageRelaySettings{Enabled: true, Policy: policy, WarningRemaining: 8, CompactReserve: 3, Limits: basispoints.DefaultImageRelayLimits()}
}
func TestExcelBPSImagePolicyWarningBoundaries(t *testing.T) {
	ctx := context.Background()
	cache := newImagePolicyMemoryCache()
	s := &OpenAIGatewayService{cache: cache}
	settings := imagePolicySettings("warn")
	for _, n := range []int{11, 12, 17, 18, 20, 21} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			scope := fmt.Sprint(n)
			_, e := s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses"), imagePolicyRequest(t, 0, n), settings, scope, true)
			if n < 12 {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
				if n == 12 {
					require.Contains(t, e.Error(), "5 张")
					require.NotContains(t, e.Error(), "预留")
				}
			}
			_, e = s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses"), imagePolicyRequest(t, 0, n), settings, scope, true)
			if n <= 17 {
				require.NoError(t, e)
			} else {
				require.Error(t, e)
			}
			compact, e := s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses/compact"), imagePolicyRequest(t, 0, n), settings, scope, true)
			require.NoError(t, e)
			require.Equal(t, 20, compact.maxImages)
			compact.finish(ctx, true)
			if n == 12 {
				_, e = s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses"), imagePolicyRequest(t, 0, n), settings, scope, true)
				require.Error(t, e)
			}
		})
	}
}
func TestExcelBPSImagePolicyAutomaticPartitions(t *testing.T) {
	s := &OpenAIGatewayService{cache: newImagePolicyMemoryCache()}
	settings := imagePolicySettings("auto_compact")
	for _, tt := range []struct {
		h, k, split int
		code        string
	}{{19, 1, 0, ""}, {19, 2, 2, ""}, {0, 21, 0, "basispoints_image_batch_too_large"}, {21, 1, 0, "basispoints_image_history_limit"}} {
		t.Run(fmt.Sprintf("%d/%d", tt.h, tt.k), func(t *testing.T) {
			p, e := s.prepareExcelImagePolicy(context.Background(), imagePolicyContext("/v1/responses"), imagePolicyRequest(t, tt.h, tt.k), settings, "scope", true)
			if tt.code != "" {
				var pe *excelImagePolicyError
				require.ErrorAs(t, e, &pe)
				require.Equal(t, tt.code, pe.Code)
			} else {
				require.NoError(t, e)
				require.Equal(t, tt.split, p.split)
			}
		})
	}
}
func TestExcelBPSImagePolicyStateFailureAndIsolation(t *testing.T) {
	cache := newImagePolicyMemoryCache()
	s := &OpenAIGatewayService{cache: cache}
	settings := imagePolicySettings("warn")
	ctx := context.Background()
	body := imagePolicyRequest(t, 0, 12)
	for _, scope := range []string{"account1/key1/thread1", "account1/key2/thread1", "account2/key1/thread1", "account1/key1/thread2"} {
		_, e := s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses"), body, settings, scope, true)
		require.Error(t, e)
	}
	cache.err = errors.New("offline")
	_, e := s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses"), body, settings, "scope", true)
	var pe *excelImagePolicyError
	require.ErrorAs(t, e, &pe)
	require.Equal(t, 503, pe.Status)
	settings.Policy = "off"
	_, e = s.prepareExcelImagePolicy(ctx, imagePolicyContext("/v1/responses"), body, settings, "", false)
	require.NoError(t, e)
}
func TestExcelBPSImagePolicySettings(t *testing.T) {
	// GetAllSettings publishes process-wide Grok defaults; restore them so this
	// settings test cannot change unrelated account-routing tests.
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	ctx := context.Background()
	repo := &excelBPSImageSettingsRepo{}
	svc := NewSettingService(repo, &config.Config{})
	saved, e := svc.GetAllSettings(ctx)
	require.NoError(t, e)
	require.Equal(t, "off", saved.ExcelBPSImageLimitPolicy)
	require.Equal(t, 8, saved.ExcelBPSImageWarningRemaining)
	saved.ExcelBPSImageLimitPolicy = "warn"
	require.NoError(t, svc.UpdateSettings(ctx, saved))
	runtime, e := svc.GetExcelBPSImageRelaySettings(ctx)
	require.NoError(t, e)
	require.Equal(t, "warn", runtime.Policy)
	saved.ExcelBPSImageCompactReserve = 8
	require.Error(t, svc.UpdateSettings(ctx, saved))
	require.Equal(t, "3", repo.values[SettingKeyExcelBPSImageCompactReserve])
	saved.ExcelBPSImageCompactReserve = 3
	saved.ExcelBPSImageLimitPolicy = "bad"
	require.Error(t, svc.UpdateSettings(ctx, saved))
}

func TestExcelBPSImagePolicyForwardCompactAndContinue(t *testing.T) {
	for _, mode := range []string{"native", "relay"} {
		for _, stream := range []bool{false, true} {
			for _, failure := range []string{"", "compact_reject", "compact_failed", "compact_eof", "generation_reject", "invalid_compaction"} {
				t.Run(fmt.Sprintf("%s/stream=%v/%s", mode, stream, failure), func(t *testing.T) {
					svc := openAIClientToolsTestService(nil)
					svc.cache = newImagePolicyMemoryCache()
					repo := &excelBPSImageSettingsRepo{values: map[string]string{SettingKeyExcelBPSImageRelayEnabled: "true", SettingKeyExcelBPSImageMode: mode, SettingKeyExcelBPSImageBaseURL: "https://images.example", SettingKeyExcelBPSImageLimitPolicy: "auto_compact"}}
					svc.settingService = NewSettingService(repo, svc.cfg)
					_, pixels := nativeGatewayBody(t)
					raw := bytes.ReplaceAll(imagePolicyRequest(t, 19, 2), []byte("data:image/png;base64,synthetic"), []byte("data:image/png;base64,"+base64.StdEncoding.EncodeToString(pixels)))
					raw, e := sjson.SetBytes(raw, "stream", stream)
					require.NoError(t, e)
					raw, e = sjson.SetBytes(raw, "model", "gpt-6-astra")
					require.NoError(t, e)
					raw, e = sjson.SetBytes(raw, "tools", []any{map[string]any{"type": "function", "name": "inspect_image", "parameters": map[string]any{"type": "object", "properties": map[string]any{}}}})
					require.NoError(t, e)
					var phase int
					var compactBody *nativeAttachmentBody
					svc.httpUpstream = &nativeAttachmentUpstream{do: func(req *http.Request, proxy string, id int64, concurrency int) (*http.Response, error) {
						b, e := io.ReadAll(req.Body)
						require.NoError(t, e)
						if req.URL.String() == basispoints.AttachmentsURL {
							return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{\"openai_file_id\":\"file-policy\"}"))}, nil
						}
						phase++
						response := map[string]any{"id": "resp-policy", "status": "completed", "model": "gpt-6-astra", "output": []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "continued"}}}}, "usage": map[string]any{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}}
						if phase == 1 {
							require.Contains(t, string(b), "compaction_trigger")
							require.Contains(t, gjson.GetBytes(b, "input.0.content.0.text").String(), "Do not call Excel, Office, workbook or connector tools.")
							count := 0
							for _, v := range gjson.GetBytes(b, "input").Array() {
								for _, part := range v.Get("content").Array() {
									if part.Get("type").String() == "input_image" {
										count++
									}
								}
							}
							require.Equal(t, 19, count, "new images must not enter compaction")
							if failure == "compact_reject" {
								return &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
							}
							response["output"] = []any{map[string]any{"type": "compaction", "id": "cmp-policy", "encrypted_content": "opaque-policy-state"}}
							if failure == "invalid_compaction" {
								response["output"] = []any{map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "not a compact"}}}}
							}
							response["usage"] = map[string]any{"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}
						} else {
							require.True(t, compactBody.closed, "release upstream body before continuing")
							require.NotContains(t, string(b), "compaction_trigger")
							require.Contains(t, string(b), "opaque-policy-state")
							count := 0
							for _, v := range gjson.GetBytes(b, "input").Array() {
								for _, part := range v.Get("content").Array() {
									if part.Get("type").String() == "input_image" {
										count++
									}
								}
							}
							require.Equal(t, 2, count, "preserve all new images")
							if failure == "generation_reject" {
								return &http.Response{StatusCode: 429, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}"))}, nil
							}
						}
						kind := "response.completed"
						if phase == 1 && failure == "compact_failed" {
							kind = "response.failed"
							response["status"] = "failed"
						}
						event, _ := json.Marshal(map[string]any{"type": kind, "response": response})
						wire := "data: " + string(event) + "\n\n"
						if phase == 1 && failure == "compact_eof" {
							wire = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-policy\"}}\n\n"
						}
						body := &nativeAttachmentBody{Reader: strings.NewReader(wire)}
						if phase == 1 {
							compactBody = body
						}
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: body}, nil
					}}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(raw))
					c.Request.Header.Set("User-Agent", "codex_cli_rs/0.116.0")
					c.Request.Header.Set("session_id", "policy-session")
					result, err := svc.forwardExcelBPS(context.Background(), c, excelAccount(), raw, time.Now())
					if failure != "" {
						require.Error(t, err)
						require.NotContains(t, rec.Body.String(), "continued")
						if failure == "generation_reject" || failure == "compact_failed" || failure == "invalid_compaction" {
							require.NotNil(t, result)
							require.Equal(t, 7, result.Usage.InputTokens)
							require.Equal(t, 3, result.Usage.OutputTokens)
						}
						return
					}
					require.NoError(t, err)
					require.Equal(t, 2, phase)
					require.Equal(t, 17, result.Usage.InputTokens)
					require.Equal(t, 5, result.Usage.OutputTokens)
					require.Contains(t, rec.Body.String(), "continued")
					require.Contains(t, rec.Body.String(), "opaque-policy-state")
					// Echo actual downstream output with the entire stale client history,
					// then verify a second turn reaches BPS without another compaction.
					var output []any
					if stream {
						for _, line := range strings.Split(rec.Body.String(), "\n") {
							if strings.HasPrefix(line, "data: ") {
								event := []byte(strings.TrimPrefix(line, "data: "))
								if gjson.GetBytes(event, "type").String() == "response.completed" {
									require.NoError(t, json.Unmarshal([]byte(gjson.GetBytes(event, "response.output").Raw), &output))
								}
							}
						}
					} else {
						require.NoError(t, json.Unmarshal([]byte(gjson.Get(rec.Body.String(), "output").Raw), &output))
					}
					require.NotEmpty(t, output)
					var original map[string]any
					require.NoError(t, json.Unmarshal(raw, &original))
					originalInput, ok := original["input"].([]any)
					require.True(t, ok)
					input := append(originalInput, output...)
					input = append(input, map[string]any{"role": "user", "content": "Continue using the two new images."})
					original["input"] = input
					nextRaw, e := json.Marshal(original)
					require.NoError(t, e)
					nextRec := httptest.NewRecorder()
					nextCtx, _ := gin.CreateTestContext(nextRec)
					nextCtx.Request = httptest.NewRequest("POST", "/v1/responses", bytes.NewReader(nextRaw))
					nextCtx.Request.Header.Set("User-Agent", "codex_app/1.0")
					nextCtx.Request.Header.Set("session_id", "policy-session")
					nextResult, e := svc.forwardExcelBPS(context.Background(), nextCtx, excelAccount(), nextRaw, time.Now())
					require.NoError(t, e)
					require.Equal(t, 3, phase)
					require.Equal(t, 10, nextResult.Usage.InputTokens)
					require.NotContains(t, nextRec.Body.String(), "opaque-policy-state")

				})
			}
		}
	}

}

func TestExcelBPSImagePolicyCodexClients(t *testing.T) {
	for _, ua := range []string{"codex_cli_rs/1.0", "codex_app/1.0", "codex_chatgpt_desktop/1.0", "Codex Desktop/1.0"} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
		c.Request.Header.Set("User-Agent", ua)
		require.True(t, isSupportedImagePolicyCodex(c), ua)
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	c.Request.Header.Set("User-Agent", "other-client/1.0")
	require.False(t, isSupportedImagePolicyCodex(c))
}

func TestExcelBPSImagePolicyCompactSurvivesStateFailure(t *testing.T) {
	cache := newImagePolicyMemoryCache()
	cache.err = errors.New("offline")
	svc := &OpenAIGatewayService{cache: cache}
	settings := imagePolicySettings("warn")
	body := imagePolicyRequest(t, 0, 20)
	p, e := svc.prepareExcelImagePolicy(context.Background(), imagePolicyContext("/v1/responses/compact"), body, settings, "scope", true)
	require.NoError(t, e)
	require.True(t, p.compact)
	require.Equal(t, 20, p.maxImages)
	body, e = sjson.SetBytes(body, "input.-1", map[string]any{"type": "compaction_trigger"})
	require.NoError(t, e)
	p, e = svc.prepareExcelImagePolicy(context.Background(), imagePolicyContext("/v1/responses"), body, settings, "scope", true)
	require.NoError(t, e)
	require.True(t, p.compact)
}

func (c *imagePolicyMemoryCache) SetSessionAccountID(context.Context, int64, string, int64, time.Duration) error {
	return nil
}

func TestExcelBPSImagePolicyGatewayReconcile(t *testing.T) {
	svc := &OpenAIGatewayService{cache: newImagePolicyMemoryCache()}
	settings := imagePolicySettings("auto_compact")
	firstBody := imagePolicyRequest(t, 19, 2)
	state, e := svc.prepareExcelImagePolicy(context.Background(), imagePolicyContext("/v1/responses"), firstBody, settings, "reconcile", true)
	require.NoError(t, e)
	window := []any{map[string]any{"type": "compaction", "encrypted_content": "opaque-state"}}
	require.NoError(t, state.checkpoint(context.Background(), window))
	state.finish(context.Background(), true)
	var body map[string]any
	require.NoError(t, json.Unmarshal(firstBody, &body))
	items, ok := body["input"].([]any)
	require.True(t, ok)
	body["input"] = append(items, window[0], map[string]any{"role": "assistant", "content": "continued"}, map[string]any{"role": "user", "content": "continue"})
	raw, e := json.Marshal(body)
	require.NoError(t, e)
	next, e := svc.prepareExcelImagePolicy(context.Background(), imagePolicyContext("/v1/responses"), raw, settings, "reconcile", true)
	require.NoError(t, e)
	require.NotEmpty(t, next.reconciledBody)
	require.Equal(t, 2, next.history.Count)
	require.Equal(t, 0, next.split)
	require.Equal(t, items[2], next.history.Input[1])
	// A different thread has no authenticated checkpoint and cannot remove history.
	other, e := svc.prepareExcelImagePolicy(context.Background(), imagePolicyContext("/v1/responses"), raw, settings, "other-thread", true)
	require.Error(t, e)
	require.Nil(t, other)
}
