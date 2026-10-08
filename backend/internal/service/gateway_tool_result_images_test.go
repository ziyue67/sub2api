package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const toolImageFixture = `{"model":"claude-sonnet-4-5","max_tokens":16,"extension":9007199254740993,"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"a","name":"capture","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","is_error":false,"extra":9007199254740993,"cache_control":{"type":"ephemeral","ttl":"1h"},"content":[{"type":"text","text":"capture"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"},"cache_control":{"type":"ephemeral","ttl":"5m"}}]},{"type":"tool_result","tool_use_id":"b","content":[{"type":"image","source":{"type":"url","url":"https://example.test/image.png"}}]},{"type":"text","text":"tail"}]}]}`

func TestToolImagesStructure(t *testing.T) {
	account := newAnthropicAPIKeyAccountForTest()
	account.Extra["anthropic_tool_result_images"] = true
	body := []byte(toolImageFixture)
	got, err := normalizeAnthropicToolImages(account, body)
	require.NoError(t, err)
	require.Equal(t, toolImageFixture, string(body))
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "extension").Raw)
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "messages.1.content.0.extra").Raw)
	blocks := gjson.GetBytes(got, "messages.1.content").Array()
	require.Len(t, blocks, 8)
	require.Equal(t, "tool_result", blocks[0].Get("type").String())
	require.Equal(t, "tool_result", blocks[1].Get("type").String())
	require.False(t, blocks[0].Get("cache_control").Exists())
	require.Equal(t, "false", blocks[0].Get("is_error").Raw)
	require.Equal(t, "text", blocks[0].Get("content.1.type").String())
	require.Equal(t, "image", blocks[3].Get("type").String())
	require.Equal(t, "5m", blocks[3].Get("cache_control.ttl").String())
	require.Equal(t, "1h", blocks[4].Get("cache_control.ttl").String())
	require.Equal(t, "url", blocks[6].Get("source.type").String())
	require.Equal(t, "tail", blocks[7].Get("text").String())
	again, err := normalizeAnthropicToolImages(account, got)
	require.NoError(t, err)
	require.Equal(t, got, again)
	account.Extra["anthropic_tool_result_images"] = false
	disabled, err := normalizeAnthropicToolImages(account, body)
	require.NoError(t, err)
	require.Equal(t, body, disabled)
}

func TestToolImagesForwardBoundary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, passthrough := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "passthrough"}[passthrough], func(t *testing.T) {
			account := newAnthropicAPIKeyAccountForTest()
			account.Extra["anthropic_passthrough"] = passthrough
			account.Extra["anthropic_tool_result_images"] = true
			account.Extra["force_anthropic_cache_ttl_1h"] = true
			upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"id":"msg_test","type":"message","role":"assistant","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":20,"output_tokens":2}}`))}}
			svc := newForwardPartialUsageServiceForTest(upstream)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(toolImageFixture)), PlatformAnthropic)
			require.NoError(t, err)
			result, err := svc.Forward(context.Background(), c, account, parsed)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, "text", gjson.GetBytes(upstream.lastBody, "messages.1.content.0.content.1.type").String())
			require.Equal(t, "image", gjson.GetBytes(upstream.lastBody, "messages.1.content.3.type").String())
			require.Equal(t, "1h", gjson.GetBytes(upstream.lastBody, "messages.1.content.3.cache_control.ttl").String())
		})
	}
}

func TestToolImagesNoop(t *testing.T) {
	a := newAnthropicAPIKeyAccountForTest()
	a.Extra["anthropic_tool_result_images"] = true
	for _, body := range []string{
		`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"{\"type\":\"image\"}"}]}]}`,
		`{"messages":[{"role":"assistant","content":[{"type":"tool_result","tool_use_id":"a","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]}]}`,
		`{"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}},{"type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]}]}`,
	} {
		got, err := normalizeAnthropicToolImages(a, []byte(body))
		require.NoError(t, err)
		require.Equal(t, body, string(got))
	}
}

func TestToolImagesMultipleMessagesAndRetryIsolation(t *testing.T) {
	a := newAnthropicAPIKeyAccountForTest()
	a.Extra["anthropic_tool_result_images"] = true
	imageMessage := `{"role":"user","content":[{"type":"tool_result","tool_use_id":"x","cache_control":{"type":"ephemeral","ttl":"1h"},"content":[{"type":"image","source":{"type":"base64","data":"AAAA"}}]}]}`
	raw := []byte(`{"model":"claude-sonnet-4-5","messages":[` + imageMessage + `,` + imageMessage + `],"other":9007199254740993}`)
	original, err := ParseGatewayRequest(NewRequestBodyRef(raw), PlatformAnthropic)
	require.NoError(t, err)
	attempt, err := original.CloneForBody(raw)
	require.NoError(t, err)
	transformed, err := normalizeAnthropicToolImages(a, attempt.Body.Bytes())
	require.NoError(t, err)
	require.NoError(t, attempt.ReplaceBody(transformed))
	require.Equal(t, raw, original.Body.Bytes())
	for _, m := range gjson.GetBytes(transformed, "messages").Array() {
		require.Equal(t, "image", m.Get("content.2.type").String())
		require.Equal(t, "1h", m.Get("content.2.cache_control.ttl").String())
		require.False(t, m.Get("content.0.cache_control").Exists())
	}
	a.Extra["anthropic_tool_result_images"] = false
	retry, err := normalizeAnthropicToolImages(a, original.Body.Bytes())
	require.NoError(t, err)
	require.Equal(t, raw, retry)
}

func TestToolImagesCountTokensBoundary(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		a := newAnthropicAPIKeyAccountForTest()
		a.Extra["anthropic_passthrough"] = passthrough
		a.Extra["anthropic_tool_result_images"] = true
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"input_tokens":42}`))}}
		svc := newForwardPartialUsageServiceForTest(upstream)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/messages/count_tokens", nil)
		parsed, err := ParseGatewayRequest(NewRequestBodyRef([]byte(toolImageFixture)), PlatformAnthropic)
		require.NoError(t, err)
		require.NoError(t, svc.ForwardCountTokens(context.Background(), c, a, parsed))
		require.Equal(t, "text", gjson.GetBytes(upstream.lastBody, "messages.1.content.0.content.1.type").String())
		require.Equal(t, "image", gjson.GetBytes(upstream.lastBody, "messages.1.content.3.type").String())
	}
}
