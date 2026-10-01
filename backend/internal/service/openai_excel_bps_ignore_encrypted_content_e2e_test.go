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

// Replays an old Codex multi-agent history through the real Forward, protocol
// and SSE pipeline. Only the upstream endpoint and the account are local.
func TestExcelBPSIgnoreEncryptedContentHTTPFlow(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("DATA_DIR", t.TempDir())
	const history = `[
		{"type":"message","role":"user","content":[{"type":"input_text","text":"review the appendix"}]},
		{"type":"agent_message","author":"/root/audit","recipient":"/root","content":[{"type":"input_text","text":"Message Type: MESSAGE\nPayload:\n"},{"type":"encrypted_content","encrypted_content":"PRIVATE_CIPHERTEXT"}]},
		{"type":"agent_message","author":"/root/audit","recipient":"/root","content":[{"type":"input_text","text":"Message Type: FINAL_ANSWER\nPayload:\naudit passed"}]},
		{"type":"function_call","call_id":"call_function","name":"inspect","arguments":"{\"ticket\":9007199254740993}"},
		{"type":"function_call_output","call_id":"call_function","output":[{"type":"input_text","text":"function result"},{"type":"encrypted_content","encrypted_content":"PRIVATE_CIPHERTEXT"}]},
		{"role":"user","content":"continue"}
	]`
	const tools = `[{"type":"function","name":"inspect","parameters":{"type":"object"}}]`

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
					_, _ = io.WriteString(w, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ignore_encrypted\",\"status\":\"completed\",\"model\":\"gpt-6-astra\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"conversation continued\"}]}]}}\n\n")
				}))
				defer upstream.Close()
				target, err := url.Parse(upstream.URL)
				require.NoError(t, err)
				router := gin.New()
				router.POST(path, func(c *gin.Context) {
					svc := openAIClientToolsTestService(nil)
					svc.httpUpstream = &excelBPSImageHTTPUpstream{target: target, client: upstream.Client()}
					defer func() { _ = svc.CloseExcelBPSImages() }()
					svc.settingService = NewSettingService(&excelBPSImageSettingsRepo{values: map[string]string{}}, svc.cfg)
					account := excelAccount()
					if value := c.GetHeader("X-Test-Ignore-Encrypted"); value != "" {
						account.Extra[ExcelBPSIgnoreEncryptedContentKey] = value == "true"
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
				send := func(ignore string, wantStatus int) string {
					t.Helper()
					body := fmt.Sprintf(`{"model":"gpt-6-astra","stream":%t,"tools":%s,"input":%s}`, stream, tools, history)
					req, err := http.NewRequest(http.MethodPost, gateway.URL+path, bytes.NewBufferString(body))
					require.NoError(t, err)
					req.Header.Set("Content-Type", "application/json")
					req.Header.Set("session_id", t.Name())
					req.Header.Set("X-Test-Ignore-Encrypted", ignore)
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

				// The resent history keeps failing until the account opts in.
				for _, ignore := range []string{"", "false"} {
					result := send(ignore, http.StatusBadRequest)
					require.Contains(t, result, "type=encrypted_content")
					require.NotContains(t, result, "PRIVATE_CIPHERTEXT")
					require.Empty(t, forwarded)
				}
				for range 2 {
					require.Contains(t, send("true", http.StatusOK), "conversation continued")
					require.Len(t, forwarded, 1)
					wire := string(<-forwarded)
					require.NotContains(t, wire, "PRIVATE_CIPHERTEXT")
					for _, text := range []string{"review the appendix", "Message Type: MESSAGE", "audit passed", "function result", "continue", "9007199254740993"} {
						require.Contains(t, wire, text)
					}
					require.Equal(t, 2, strings.Count(wire, "Encrypted content omitted"))
					calls, results := 0, 0
					for _, item := range gjson.Get(wire, "input").Array() {
						switch item.Get("type").String() {
						case "function_call":
							calls++
						case "function_call_output":
							results++
							require.Equal(t, "call_function", item.Get("call_id").String())
						}
					}
					require.Equal(t, 1, calls)
					require.Equal(t, calls, results)
				}
			})
		}
	}
}
