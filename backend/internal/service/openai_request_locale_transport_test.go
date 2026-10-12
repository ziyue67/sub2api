// Adapted from MACOS-DO/sub4api at 254e7ec4932e65ab8830e7d10e66f184140041bd.
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestOpenAIRequestLanguageHeadersPreserveClientAndAccountValues(t *testing.T) {
	for _, test := range []struct{ name, client, override, want string }{
		{name: "absent"},
		{name: "client preference", client: "zh-CN,zh;q=0.9", want: "zh-CN,zh;q=0.9"},
		{name: "explicit account override", client: "zh-CN", override: "fr-FR", want: "fr-FR"},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			if test.client != "" {
				c.Request.Header.Set("Accept-Language", test.client)
			}
			account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
				credKeyHeaderOverrideEnabled: test.override != "",
				credKeyHeaderOverrides:       map[string]any{"Accept-Language": test.override},
			}}
			svc := &OpenAIGatewayService{}
			body := []byte(`{"model":"gpt-5.4","input":[]}`)
			request, err := svc.buildUpstreamRequest(context.Background(), c, account, body, "test-token", false, "", false)
			require.NoError(t, err)
			require.Equal(t, test.want, getHeaderRaw(request.Header, "Accept-Language"))
			request, err = svc.buildUpstreamRequestOpenAIPassthrough(context.Background(), c, account, body, "test-token")
			require.NoError(t, err)
			require.Equal(t, test.want, getHeaderRaw(request.Header, "Accept-Language"))
			headers, _, err := svc.buildOpenAIWSHeaders(context.Background(), c, account, "test-token", OpenAIWSProtocolDecision{}, false, "", "", "", "gpt-5.4", "")
			require.NoError(t, err)
			require.Equal(t, test.want, getHeaderRaw(headers, "Accept-Language"))
		})
	}
}

type localeStagedWSConn struct{ *stagedPassthroughConn }

func (c *localeStagedWSConn) WriteJSON(ctx context.Context, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.WriteFrame(ctx, coderws.MessageText, payload)
}

func TestOpenAIRequestLocaleWebSocketFirstAndLaterTurns(t *testing.T) {
	for _, mode := range []string{OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(context.Canceled)
			cfg := passthroughLifecycleConfig()
			cfg.Gateway.OpenAIWS.ReadTimeoutSeconds = 5
			cfg.Gateway.OpenAIWS.IngressInterTurnIdleTimeoutSeconds = 5
			cfg.Gateway.OpenAIFirstOutputTimeoutSeconds = 5
			upstream := newStagedPassthroughConn()
			svc := newPassthroughLifecycleService(cfg, upstream)
			svc.settingService = timezoneEnabledSettings()
			pool := newOpenAIWSConnPool(cfg)
			defer pool.Close()
			pool.setClientDialerForTest(&stagedPassthroughDialer{conn: &localeStagedWSConn{upstream}})
			svc.openaiWSPool = pool
			account := passthroughLifecycleAccount()
			account.Extra["openai_apikey_responses_websockets_v2_mode"] = mode
			account.Extra[openAIRequestTimezoneExtraKey] = "America/New_York"
			server, done := startPassthroughLifecycleServer(t, ctx, svc, account)
			defer server.Close()
			texts := []string{
				`<environment_context><current_date status="unavailable" /><network><allowed>a&b.example,<host</allowed></network><timezone>Asia/Shanghai</timezone></environment_context>`,
				`<environment_context><current_date>2001-01-01</current_date><timezone>Asia/Shanghai</timezone></environment_context>`,
			}
			payload := func(text string) []byte {
				body := localeTestBody([]string{text}, nil)
				body, _ = sjson.DeleteBytes(body, "tools")
				body, _ = sjson.DeleteBytes(body, "input.0.internal_chat_message_metadata_passthrough")
				body, _ = sjson.SetBytes(body, "type", "response.create")
				body, _ = sjson.SetBytes(body, "model", "gpt-5.1")
				return body
			}
			client := dialPassthroughLifecycleClientWithPayload(t, server, string(payload(texts[0])))
			defer func() { _ = client.CloseNow() }()
			for turn, text := range texts {
				if turn > 0 {
					body, err := sjson.SetBytes(payload(text), "previous_response_id", "resp_locale_0")
					require.NoError(t, err)
					writeCtx, stop := context.WithTimeout(ctx, 5*time.Second)
					err = client.Write(writeCtx, coderws.MessageText, body)
					stop()
					require.NoError(t, err)
				}
				sent := requirePassthroughUpstreamWrite(t, upstream, 5*time.Second)
				require.Equal(t, strings.Replace(text, "Asia/Shanghai", "America/New_York", 1), gjson.GetBytes(sent, "input.0.content.0.text").String())
				upstream.Send(fmt.Sprintf(`{"type":"response.completed","response":{"id":"resp_locale_%d","model":"gpt-5.1","usage":{"input_tokens":1,"output_tokens":1}}}`, turn))
				response, err := readPassthroughLifecycleFrame(t, client, 5*time.Second)
				require.NoError(t, err)
				require.Equal(t, "response.completed", gjson.GetBytes(response, "type").String())
			}
			require.NoError(t, client.Close(coderws.StatusNormalClosure, "done"))
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("websocket forwarding did not exit")
			}
		})
	}
}
