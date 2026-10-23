package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const deepSeekInputImageDataURI = "data:image/png;base64,AQID"

func openaiMappedDeepSeekResponsesImageAccount() *Account {
	return &Account{
		ID:       18,
		Name:     "openai-deepseek-responses",
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Extra: map[string]any{
			openai_compat.ExtraKeyResponsesSupported: true,
		},
		Credentials: map[string]any{
			"api_key":  "sk-test",
			"base_url": DefaultDeepseekBaseURL,
		},
		Status:      StatusActive,
		Schedulable: true,
	}
}

func deepSeekNativeResponsesImageAccount() *Account {
	return &Account{
		Platform: PlatformDeepseek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"api_protocol": APIProtocolResponses,
		},
	}
}

func deepSeekUserImageBody(partJSON string) []byte {
	return []byte(`{"model":"deepseek-flash","store":true,"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"what is this?"},` + partJSON + `]}]}`)
}

func TestNormalizeDeepSeekResponsesRequestBodyAliasesInputImageURL(t *testing.T) {
	t.Parallel()

	native := deepSeekNativeResponsesImageAccount()
	mapped := openaiMappedDeepSeekResponsesImageAccount()
	kimi := &Account{
		Platform: PlatformKimi, Type: AccountTypeAPIKey,
		Credentials: map[string]any{"api_protocol": APIProtocolResponses},
	}
	openai := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}

	t.Run("openai_image_url_string_gets_url", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `"}`)
		got := normalizeDeepSeekResponsesRequestBody(mapped, body)
		require.Equal(t, "input_image", gjson.GetBytes(got, "input.0.content.1.type").String())
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.0.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
		require.Equal(t, "what is this?", gjson.GetBytes(got, "input.0.content.0.text").String())
		require.True(t, gjson.GetBytes(got, "store").Bool(), "mapped openai 账号不应被无状态适配改掉 store")
	})

	t.Run("nested_image_url_object_flattened", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"input_image","image_url":{"url":"` + deepSeekInputImageDataURI + `"}}`)
		got := normalizeDeepSeekResponsesRequestBody(native, body)
		require.Equal(t, gjson.String, gjson.GetBytes(got, "input.0.content.1.image_url").Type)
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.0.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
	})

	t.Run("chat_completions_image_url_part_converted", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"image_url","image_url":{"url":"` + deepSeekInputImageDataURI + `"}}`)
		got := normalizeDeepSeekResponsesRequestBody(mapped, body)
		require.Equal(t, "input_image", gjson.GetBytes(got, "input.0.content.1.type").String())
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.0.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
	})

	t.Run("anthropic_source_becomes_data_uri", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AQID"}}`)
		got := normalizeDeepSeekResponsesRequestBody(native, body)
		require.Equal(t, "input_image", gjson.GetBytes(got, "input.0.content.1.type").String())
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.0.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
	})

	t.Run("lifted_tool_output_image_keeps_image_url_only", func(t *testing.T) {
		body := []byte(`{"model":"deepseek-flash","input":[{"type":"function_call","call_id":"call_image","name":"view_image","arguments":"{}"},{"type":"function_call_output","call_id":"call_image","output":[{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `"}]}]}`)
		got := normalizeDeepSeekResponsesRequestBody(mapped, body)
		require.Equal(t, gjson.String, gjson.GetBytes(got, "input.1.output").Type)
		require.Equal(t, "input_image", gjson.GetBytes(got, "input.2.content.1.type").String())
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.2.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.2.content.1.url").Exists())
	})

	t.Run("dual_field_input_drops_duplicate_url", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `","url":"` + deepSeekInputImageDataURI + `"}`)
		got := normalizeDeepSeekResponsesRequestBody(native, body)
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.0.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
		require.Equal(t, string(got), string(normalizeDeepSeekResponsesRequestBody(native, got)))
	})

	t.Run("file_id_only_does_not_gain_empty_url", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"input_image","file_id":"file-abc"}`)
		got := normalizeDeepSeekResponsesRequestBody(native, body)
		require.Equal(t, "file-abc", gjson.GetBytes(got, "input.0.content.1.file_id").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.image_url").Exists())
	})

	t.Run("kimi_does_not_alias_url", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `"}`)
		got := normalizeDeepSeekResponsesRequestBody(kimi, body)
		require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(got, "input.0.content.1.image_url").String())
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
	})

	t.Run("openai_host_unchanged", func(t *testing.T) {
		body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `"}`)
		got := normalizeDeepSeekResponsesRequestBody(openai, body)
		require.Equal(t, string(body), string(got))
		require.False(t, gjson.GetBytes(got, "input.0.content.1.url").Exists())
	})
}

func TestForwardResponses_OpenAIMappedDeepSeekAliasesInputImageURL(t *testing.T) {
	body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `"}`)
	c := newDeepSeekChatFallbackContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader([]byte(
			`{"id":"resp_ds_img","object":"response","model":"deepseek-flash","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
		))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          deepSeekChatFallbackTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, openaiMappedDeepSeekResponsesImageAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, upstream.lastReq.URL.String(), "/responses")
	require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(upstream.lastBody, "input.0.content.1.image_url").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.content.1.url").Exists())
}

func TestForwardResponses_NativeDeepSeekAliasesInputImageURL(t *testing.T) {
	body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `"}`)
	c := newDeepSeekChatFallbackContext(t, body)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(bytes.NewReader([]byte(
			`{"id":"resp_ds_img","object":"response","model":"deepseek-flash","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`,
		))),
	}}
	svc := &OpenAIGatewayService{
		cfg:          deepSeekChatFallbackTestConfig(),
		httpUpstream: upstream,
	}

	result, err := svc.Forward(context.Background(), c, deepSeekNativeResponsesImageAccount(), body)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://api.deepseek.com/responses", upstream.lastReq.URL.String())
	require.Equal(t, deepSeekInputImageDataURI, gjson.GetBytes(upstream.lastBody, "input.0.content.1.image_url").String())
	require.False(t, gjson.GetBytes(upstream.lastBody, "input.0.content.1.url").Exists())
}

// Exercise the real forwarding adapter, including native and OpenAI-mapped
// accounts. A legacy schema is selected before sending, never by replaying.
func TestForwardResponsesDeepSeekExplicitImageSchema(t *testing.T) {
	for _, field := range []string{"image_url", "url"} {
		for name, makeAccount := range map[string]func() *Account{
			"native": deepSeekNativeResponsesImageAccount,
			"mapped": openaiMappedDeepSeekResponsesImageAccount,
		} {
			t.Run(name+"/"+field, func(t *testing.T) {
				account := makeAccount()
				account.Credentials[DeepSeekResponsesImageFieldCredential] = field
				body := deepSeekUserImageBody(`{"type":"input_image","image_url":"` + deepSeekInputImageDataURI + `","url":"` + deepSeekInputImageDataURI + `"}`)
				c := newDeepSeekChatFallbackContext(t, body)
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
					Body: io.NopCloser(strings.NewReader(`{"id":"resp_schema","object":"response","model":"deepseek-flash","status":"completed","output":[],"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}`)),
				}}
				svc := &OpenAIGatewayService{cfg: deepSeekChatFallbackTestConfig(), httpUpstream: upstream}
				result, err := svc.Forward(context.Background(), c, account, body)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Len(t, upstream.requests, 1, "schema selection must not probe or replay")
				image := gjson.GetBytes(upstream.lastBody, "input.0.content.1")
				require.Equal(t, deepSeekInputImageDataURI, image.Get(field).String())
				other := "url"
				if field == "url" {
					other = "image_url"
				}
				require.False(t, image.Get(other).Exists(), "only the configured image payload may be sent")
				normalized := normalizeDeepSeekResponsesRequestBody(account, body)
				require.Equal(t, string(normalized), string(normalizeDeepSeekResponsesRequestBody(account, normalized)))
			})
		}
	}
}
