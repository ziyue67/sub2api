package service

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Exercise both HTTP boundaries with the real Forward/protocol/SSE pipeline.
// Only the upstream endpoint and persisted account/settings are supplied locally.
// Image payloads are inert strings; this test never needs to render an image.
type excelBPSIgnoreImagesHTTPUpstream struct {
	HTTPUpstream
	target *url.URL
	client *http.Client
}

func (u *excelBPSIgnoreImagesHTTPUpstream) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	local := req.Clone(req.Context())
	local.URL.Scheme, local.URL.Host = u.target.Scheme, u.target.Host
	return u.client.Do(local)
}

func TestExcelBPSIgnoreImagesHTTPFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DATA_DIR", t.TempDir())
	const history = `[
		{"role":"user","content":[{"type":"input_text","text":"before screenshot"},{"type":"input_image","image_url":"data:image/png;base64,PRIVATE_IMAGE"},{"type":"input_text","text":"after screenshot"}]},
		{"type":"message","role":"user","content":[{"type":"input_image","image_url":"https://images.example/PRIVATE_IMAGE"}]},
		{"type":"function_call","call_id":"call_function","name":"inspect","arguments":"{\"ticket\":9007199254740993,\"payload\":{\"type\":\"input_image\",\"image_url\":\"opaque-argument\"}}"},
		{"type":"function_call_output","call_id":"call_function","output":[{"type":"input_text","text":"function result"},{"type":"input_image","image_url":"data:image/png;base64,PRIVATE_IMAGE"}]},
		{"type":"function_call","call_id":"call_function_only","name":"inspect","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_function_only","output":[{"type":"input_image","file_id":"file-PRIVATE_IMAGE"}]},
		{"type":"custom_tool_call","call_id":"call_custom","name":"capture","input":"capture text"},
		{"type":"custom_tool_call_output","call_id":"call_custom","output":[{"type":"input_image","file_id":"file-PRIVATE_IMAGE"},{"type":"input_text","text":"custom result"}]},
		{"type":"custom_tool_call","call_id":"call_custom_only","name":"capture","input":"capture only"},
		{"type":"custom_tool_call_output","call_id":"call_custom_only","output":[{"type":"input_image","image_url":"data:image/png;base64,PRIVATE_IMAGE"}]},
		{"role":"user","content":"continue using text; data:image is literal text"}
	]`
	const tools = `[{"type":"function","name":"inspect","parameters":{"type":"object"}},{"type":"custom","name":"capture"}]`

	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", path, stream), func(t *testing.T) {
				forwarded := make(chan []byte, 16)
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						http.Error(w, "read request", http.StatusBadRequest)
						return
					}
					forwarded <- body
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ignore_images\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"conversation continued\"}]}]}}\n\n")
				}))
				defer upstream.Close()
				target, err := url.Parse(upstream.URL)
				require.NoError(t, err)
				router := gin.New()
				router.POST(path, func(c *gin.Context) {
					svc := openAIClientToolsTestService(nil)
					svc.httpUpstream = &excelBPSIgnoreImagesHTTPUpstream{target: target, client: upstream.Client()}
					defer func() { _ = svc.CloseExcelBPSImages() }()
					mode := c.GetHeader("X-Test-Image-Mode")
					svc.settingService = NewSettingService(&excelBPSImageSettingsRepo{values: map[string]string{
						SettingKeyExcelBPSImageRelayEnabled: fmt.Sprint(mode != ""),
						SettingKeyExcelBPSImageMode:         mode, SettingKeyExcelBPSImageBaseURL: "https://images.example",
					}}, svc.cfg)
					account := excelAccount()
					if value := c.GetHeader("X-Test-Ignore-Images"); value != "" {
						account.Extra[ExcelBPSIgnoreImagesKey] = value == "true"
					}
					body, err := c.GetRawData()
					if err != nil {
						c.AbortWithStatus(http.StatusBadRequest)
						return
					}
					_, _ = svc.Forward(c.Request.Context(), c, account, body)
				})
				gateway := httptest.NewServer(router)
				defer gateway.Close()
				send := func(input, ignore, mode string, wantStatus int) string {
					t.Helper()
					body := fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"tools":%s,"input":%s}`, stream, tools, input)
					req, err := http.NewRequest(http.MethodPost, gateway.URL+path, bytes.NewBufferString(body))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("session_id", t.Name())
					req.Header.Set("X-Test-Ignore-Images", ignore)
					req.Header.Set("X-Test-Image-Mode", mode)
					resp, err := gateway.Client().Do(req)
					require.NoError(t, err)
					defer func() {
						require.NoError(t, resp.Body.Close())
					}()
					result, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					require.Equal(t, wantStatus, resp.StatusCode, string(result))
					return string(result)
				}

				// The same expanded history fails repeatedly until the account opts in.
				for _, ignore := range []string{"", "false"} {
					result := send(history, ignore, "", http.StatusBadRequest)
					require.Contains(t, result, "image support is disabled")
					require.Contains(t, result, ExcelBPSIgnoreImagesKey, "operators must be told how to enable text-only forwarding")
					require.NotContains(t, result, "PRIVATE_IMAGE")
					require.Empty(t, forwarded)
				}
				for range 2 {
					require.Contains(t, send(history, "true", "", http.StatusOK), "conversation continued")
					require.Len(t, forwarded, 1)
					wire := <-forwarded
					require.NotContains(t, string(wire), "PRIVATE_IMAGE")
					for _, text := range []string{"before screenshot", "after screenshot", "function result", "custom result", "continue using text; data:image is literal text", "opaque-argument", "9007199254740993"} {
						require.Contains(t, string(wire), text)
					}
					calls, results, omitted := 0, 0, 0
					for _, item := range gjson.GetBytes(wire, "input").Array() {
						switch item.Get("type").String() {
						case "function_call":
							calls++
						case "function_call_output":
							results++
							require.Contains(t, []string{"call_function", "call_function_only", "call_custom", "call_custom_only"}, item.Get("call_id").String())
						}
						for _, field := range []string{"content", "output"} {
							for _, part := range item.Get(field).Array() {
								require.NotEqual(t, "input_image", part.Get("type").String())
								if strings.Contains(part.Get("text").String(), "Image omitted") {
									omitted++
								}
							}
						}
					}
					require.Equal(t, 4, calls)
					require.Equal(t, calls, results)
					require.Equal(t, 3, omitted)
				}

				// Re-enabling either image mode restores validation even with opt-in.
				for _, mode := range []string{ExcelBPSImageModeNative, ExcelBPSImageModeRelay} {
					send(history, "true", mode, http.StatusBadRequest)
					require.Empty(t, forwarded)
					https := `[{"role":"user","content":[{"type":"input_image","image_url":"https://images.example/photo.png"}]}]`
					send(https, "true", mode, http.StatusOK)
					require.Len(t, forwarded, 1)
					require.Contains(t, string(<-forwarded), `"type":"input_image"`)
				}

				// Other unsupported content still fails after images are removed.
				file := strings.Replace(history, `"type":"input_text","text":"after screenshot"`, `"type":"input_file","file_id":"file-private"`, 1)
				result := send(file, "true", "", http.StatusBadRequest)
				require.Contains(t, result, "type=input_file")
				require.Empty(t, forwarded)
			})
		}
	}
}
