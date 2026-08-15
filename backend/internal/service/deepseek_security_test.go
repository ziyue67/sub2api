package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/util/responseheaders"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const deepSeekSecurityCanaryKey = "ds-canary-secret-4f29c7"

func deepSeekSecurityTestAccount(apiKey string) *Account {
	return &Account{
		ID:       7301,
		Name:     "deepseek-security-canary",
		Platform: PlatformDeepSeek,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": apiKey,
		},
	}
}

func deepSeekSecurityTestConfig() *config.Config {
	return &config.Config{Gateway: config.GatewayConfig{
		LogUpstreamErrorBody:         true,
		LogUpstreamErrorBodyMaxBytes: 4096,
	}}
}

func deepSeekSecurityTestContext(path string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, path, nil)
	return c, recorder
}

func deepSeekSecurityTestResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func deepSeekSecurityCanaryHeaders(contentType string) http.Header {
	escapedCanary := strings.Replace(deepSeekSecurityCanaryKey, "c", `\u0063`, 1)
	return http.Header{
		"Content-Type":          []string{contentType},
		"X-Request-Id":          []string{"generic-" + deepSeekSecurityCanaryKey},
		"X-Deepseek-Request-Id": []string{"vendor-" + escapedCanary},
		"X-Allowed-Canary":      []string{"allowed-" + escapedCanary},
		"X-Invalid-Value":       []string{"unsafe-" + deepSeekSecurityCanaryKey + "\r\nInjected: true"},
		"Bad Header Name":       []string{"unsafe-" + deepSeekSecurityCanaryKey},
	}
}

func deepSeekSecurityResponseHeaderFilter() *responseheaders.CompiledHeaderFilter {
	return responseheaders.CompileHeaderFilter(config.ResponseHeaderConfig{
		Enabled:           true,
		AdditionalAllowed: []string{"x-allowed-canary", "x-invalid-value"},
	})
}

func requireDeepSeekSecurityHeadersRedacted(t *testing.T, headers http.Header) {
	t.Helper()
	allHeaders := fmt.Sprint(headers)
	require.NotContains(t, allHeaders, deepSeekSecurityCanaryKey)
	require.NotContains(t, allHeaders, `\u0063`)
	require.Equal(t, "generic-"+deepSeekCredentialRedaction, headers.Get("X-Request-Id"))
	require.Equal(t, "vendor-"+deepSeekCredentialRedaction, headers.Get("X-Deepseek-Request-Id"))
	require.Equal(t, "allowed-"+deepSeekCredentialRedaction, headers.Get("X-Allowed-Canary"))
	require.Empty(t, headers.Get("X-Invalid-Value"))
	require.Empty(t, headers.Get("Bad Header Name"))
}

func deepSeekSecuritySurfaces(c *gin.Context, recorder *httptest.ResponseRecorder, err error) string {
	var values []any
	for _, key := range []string{
		OpsUpstreamErrorMessageKey,
		OpsUpstreamErrorDetailKey,
		OpsUpstreamErrorsKey,
	} {
		if value, ok := c.Get(key); ok {
			values = append(values, value)
		}
	}
	encodedOps, _ := json.Marshal(values)
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	return strings.Join([]string{recorder.Body.String(), errText, string(encodedOps)}, "\n")
}

func TestDeepSeekUpstreamErrorCanaryIsRedactedAcrossNativeProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)
	escapedCanary := strings.Replace(deepSeekSecurityCanaryKey, "c", `\u0063`, 1)
	bodies := map[string]string{
		"json":         fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":"upstream echoed %s"}}`, deepSeekSecurityCanaryKey),
		"json_unicode": fmt.Sprintf(`{"error":{"type":"invalid_request_error","message":"upstream echoed %s"}}`, escapedCanary),
		"plain":        "proxy rejected credential " + deepSeekSecurityCanaryKey,
	}

	for protocol, path := range map[string]string{
		"chat":      "/v1/chat/completions",
		"responses": "/v1/responses",
		"messages":  "/v1/messages",
	} {
		protocol, path := protocol, path
		for format, body := range bodies {
			format, body := format, body
			t.Run(protocol+"/"+format, func(t *testing.T) {
				c, recorder := deepSeekSecurityTestContext(path)
				resp := deepSeekSecurityTestResponse(body)
				resp.Header = deepSeekSecurityCanaryHeaders("application/json")
				var err error
				switch protocol {
				case "chat":
					svc := &OpenAIGatewayService{cfg: deepSeekSecurityTestConfig()}
					_, err = svc.handleCompatErrorResponse(
						resp, c, account, writeChatCompletionsError,
					)
				case "responses":
					svc := &OpenAIGatewayService{cfg: deepSeekSecurityTestConfig()}
					_, err = svc.handleErrorResponse(
						context.Background(), resp, c, account, nil,
					)
				case "messages":
					svc := &GatewayService{cfg: deepSeekSecurityTestConfig()}
					_, err = svc.handleErrorResponse(
						context.Background(), resp, c, account,
					)
				}

				require.Error(t, err)
				surfaces := deepSeekSecuritySurfaces(c, recorder, err)
				require.NotContains(t, surfaces, deepSeekSecurityCanaryKey)
				require.Contains(t, surfaces, deepSeekCredentialRedaction)
				requireDeepSeekSecurityHeadersRedacted(t, resp.Header)
			})
		}
	}
	require.Equal(t, deepSeekSecurityCanaryKey, account.GetDeepSeekAPIKey(), "redaction must not mutate account credentials")
}

func TestDeepSeekDerivedUserIDCanaryIsRedactedAcrossNativeProtocols(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)
	derivedUserID := deepSeekUserIDPrefix + strings.Repeat("A", 43)
	escapedDerivedUserID := strings.ReplaceAll(derivedUserID, "_", `\u005f`)
	bodies := map[string]string{
		"json":         fmt.Sprintf(`{"error":{"message":"upstream echoed %s"}}`, derivedUserID),
		"json_unicode": fmt.Sprintf(`{"error":{"message":"upstream echoed %s"}}`, escapedDerivedUserID),
		"plain":        "proxy echoed identity " + derivedUserID,
	}

	for protocol, path := range map[string]string{
		"chat":      "/v1/chat/completions",
		"responses": "/v1/responses",
		"messages":  "/v1/messages",
	} {
		protocol, path := protocol, path
		for format, body := range bodies {
			format, body := format, body
			t.Run(protocol+"/"+format, func(t *testing.T) {
				c, recorder := deepSeekSecurityTestContext(path)
				resp := deepSeekSecurityTestResponse(body)
				var err error
				switch protocol {
				case "chat":
					svc := &OpenAIGatewayService{cfg: deepSeekSecurityTestConfig()}
					_, err = svc.handleCompatErrorResponse(resp, c, account, writeChatCompletionsError)
				case "responses":
					svc := &OpenAIGatewayService{cfg: deepSeekSecurityTestConfig()}
					_, err = svc.handleErrorResponse(context.Background(), resp, c, account, nil)
				case "messages":
					svc := &GatewayService{cfg: deepSeekSecurityTestConfig()}
					_, err = svc.handleErrorResponse(context.Background(), resp, c, account)
				}

				require.Error(t, err)
				surfaces := deepSeekSecuritySurfaces(c, recorder, err)
				require.NotContains(t, surfaces, derivedUserID)
				require.NotContains(t, surfaces, escapedDerivedUserID)
				require.Contains(t, surfaces, deepSeekCredentialRedaction)
			})
		}
	}

	headers := http.Header{
		"X-Request-Id":          []string{"raw-" + derivedUserID},
		"X-Deepseek-Request-Id": []string{"escaped-" + escapedDerivedUserID},
	}
	sanitizeDeepSeekResponseHeadersInPlace(account, headers)
	require.NotContains(t, fmt.Sprint(headers), derivedUserID)
	require.NotContains(t, fmt.Sprint(headers), escapedDerivedUserID)
	require.Equal(t, "raw-"+deepSeekCredentialRedaction, headers.Get("X-Request-Id"))
	require.Equal(t, "escaped-"+deepSeekCredentialRedaction, headers.Get("X-Deepseek-Request-Id"))
}

func TestDeepSeekDerivedUserIDRedactionPreservesNonIdentityPlaceholders(t *testing.T) {
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)
	for _, placeholder := range []string{
		deepSeekUserIDPrefix + "example",
		deepSeekUserIDPrefix + strings.Repeat("A", deepSeekUserIDEncodedDigestBytes-1),
	} {
		body := []byte(fmt.Sprintf(`{"message":%q}`, placeholder))
		require.Equal(t, body, redactDeepSeekAPIKey(account, body))
		require.Equal(t, "prefix-"+placeholder, redactDeepSeekHeaderValue("prefix-"+placeholder, deepSeekSecurityCanaryKey))
	}
	longValue := deepSeekUserIDPrefix + strings.Repeat("A", deepSeekUserIDEncodedDigestBytes) + "suffix"
	require.Equal(t, deepSeekCredentialRedaction+"suffix",
		string(redactDeepSeekAPIKey(account, []byte(longValue))))
}

func TestDeepSeekFailoverErrorAndOpsRedactCurrentAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)
	c, recorder := deepSeekSecurityTestContext("/v1/responses")
	body := []byte(fmt.Sprintf(`{"error":{"message":"upstream echoed %s"}}`, deepSeekSecurityCanaryKey))
	svc := &OpenAIGatewayService{cfg: deepSeekSecurityTestConfig()}
	resp := &http.Response{
		StatusCode: http.StatusServiceUnavailable,
		Header:     deepSeekSecurityCanaryHeaders("application/json"),
	}

	failoverErr := svc.failoverOpenAIUpstreamHTTPError(
		context.Background(), c, account, resp, body, extractUpstreamErrorMessage(body), "deepseek-v4-flash",
	)
	require.NotNil(t, failoverErr)
	surfaces := deepSeekSecuritySurfaces(c, recorder, failoverErr) + "\n" + string(failoverErr.ResponseBody)
	require.NotContains(t, surfaces, deepSeekSecurityCanaryKey)
	require.Contains(t, surfaces, deepSeekCredentialRedaction)
	requireDeepSeekSecurityHeadersRedacted(t, failoverErr.ResponseHeaders)
}

func TestDeepSeekResponseHeaderSanitizerIsPlatformIsolated(t *testing.T) {
	deepSeekHeaders := deepSeekSecurityCanaryHeaders("application/json")
	sanitizeDeepSeekResponseHeadersInPlace(deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey), deepSeekHeaders)
	requireDeepSeekSecurityHeadersRedacted(t, deepSeekHeaders)

	openAIHeaders := deepSeekSecurityCanaryHeaders("application/json")
	original := openAIHeaders.Clone()
	openAIAccount := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	sanitizeDeepSeekResponseHeadersInPlace(openAIAccount, openAIHeaders)
	require.Equal(t, original, openAIHeaders)
}

func TestDeepSeekTransportErrorOpsRedactsCurrentAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)
	c, recorder := deepSeekSecurityTestContext("/v1/responses")
	svc := &OpenAIGatewayService{}

	err := svc.handleOpenAIUpstreamTransportError(
		context.Background(), c, account,
		errors.New("upstream transport echoed "+deepSeekSecurityCanaryKey), true,
	)
	require.Error(t, err)
	surfaces := deepSeekSecuritySurfaces(c, recorder, err)
	require.NotContains(t, surfaces, deepSeekSecurityCanaryKey)
	require.Contains(t, surfaces, deepSeekCredentialRedaction)
}

func TestDeepSeekMessagesTransportErrorRedactsCurrentAPIKey(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)
	c, recorder := deepSeekSecurityTestContext("/v1/messages")
	upstream := &anthropicHTTPUpstreamRecorder{
		err: errors.New("messages transport echoed " + deepSeekSecurityCanaryKey),
	}
	svc := &GatewayService{cfg: deepSeekSecurityTestConfig(), httpUpstream: upstream}
	body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"messages":[]}`)

	result, err := svc.Forward(context.Background(), c, account, &ParsedRequest{
		Body:  NewRequestBodyRef(body),
		Model: "deepseek-v4-flash",
	})
	require.Nil(t, result)
	require.Error(t, err)
	surfaces := deepSeekSecuritySurfaces(c, recorder, err)
	require.NotContains(t, surfaces, deepSeekSecurityCanaryKey)
	require.Contains(t, surfaces, deepSeekCredentialRedaction)
}

func TestRedactDeepSeekAPIKeyHandlesJSONEscapingAndIsPlatformIsolated(t *testing.T) {
	apiKey := `ds-canary-"quoted\\key`
	account := deepSeekSecurityTestAccount(apiKey)
	payload, err := json.Marshal(map[string]any{"error": map[string]string{"message": "echo " + apiKey}})
	require.NoError(t, err)

	redacted := redactDeepSeekAPIKey(account, payload)
	require.NotContains(t, string(redacted), apiKey)
	require.Contains(t, string(redacted), deepSeekCredentialRedaction)
	require.True(t, json.Valid(redacted))

	openAIAccount := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"api_key": apiKey,
		},
	}
	require.Equal(t, payload, redactDeepSeekAPIKey(openAIAccount, payload))
}

func TestRedactDeepSeekAPIKeyHandlesArbitraryJSONEscapesWithoutBreakingWire(t *testing.T) {
	tests := []struct {
		name                 string
		apiKey               string
		payload              string
		allowStructuralMatch bool
		assertWire           func(*testing.T, []byte)
	}{
		{
			name:    "lowercase unicode escape",
			apiKey:  "sk-zed",
			payload: `{"error":{"message":"echo sk-\u007aed"}}`,
		},
		{
			name:    "uppercase unicode escape",
			apiKey:  "sk-zed",
			payload: `{"error":{"message":"echo sk-\u007Aed"}}`,
		},
		{
			name:    "surrogate pair and ordinary escape remain valid",
			apiKey:  "sk-secret",
			payload: `{"error":{"message":"emoji \uD83D\uDE00, quote \", key sk-\u0073ecret"}}`,
			assertWire: func(t *testing.T, body []byte) {
				require.Equal(t, "emoji "+string(rune(0x1f600))+", quote \", key "+deepSeekCredentialRedaction, gjson.GetBytes(body, "error.message").String())
			},
		},
		{
			name:    "credential echoed as object key",
			apiKey:  "sk-secret",
			payload: `{"error":{"prefix-sk-\u0073ecret-suffix":true,"message":"bad"}}`,
			assertWire: func(t *testing.T, body []byte) {
				var decoded struct {
					Error map[string]any `json:"error"`
				}
				require.NoError(t, json.Unmarshal(body, &decoded))
				require.Equal(t, true, decoded.Error["prefix-"+deepSeekCredentialRedaction+"-suffix"])
			},
		},
		{
			name:                 "short numeric key does not rewrite JSON numbers",
			apiKey:               "1",
			payload:              `{"usage":{"input_tokens":1},"message":"credential 1"}`,
			allowStructuralMatch: true,
			assertWire: func(t *testing.T, body []byte) {
				require.Equal(t, int64(1), gjson.GetBytes(body, "usage.input_tokens").Int())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			redacted := redactDeepSeekAPIKey(deepSeekSecurityTestAccount(tt.apiKey), []byte(tt.payload))
			require.True(t, json.Valid(redacted))
			if !tt.allowStructuralMatch {
				require.NotContains(t, string(redacted), tt.apiKey)
			}
			require.Contains(t, string(redacted), deepSeekCredentialRedaction)
			if tt.assertWire != nil {
				tt.assertWire(t, redacted)
			}
		})
	}

	quoteKey := `"`
	quotePayload, err := json.Marshal(map[string]any{
		"usage":   map[string]any{"input_tokens": 1},
		"message": `credential "`,
	})
	require.NoError(t, err)
	redacted := redactDeepSeekAPIKey(deepSeekSecurityTestAccount(quoteKey), quotePayload)
	require.True(t, json.Valid(redacted))
	require.Equal(t, int64(1), gjson.GetBytes(redacted, "usage.input_tokens").Int())
	require.Contains(t, gjson.GetBytes(redacted, "message").String(), deepSeekCredentialRedaction)
}

func TestRedactDeepSeekAPIKeyPreservesSSEFramingWithEscapedJSON(t *testing.T) {
	account := deepSeekSecurityTestAccount("sk-secret")
	wire := "event: response.failed\r\n" +
		`data: {"type":"response.failed","error":{"message":"emoji \uD83D\uDE00 key sk-\u0073ecret"}}` + "\r\n\r\n"

	redacted := redactDeepSeekAPIKey(account, []byte(wire))
	require.Contains(t, string(redacted), "\r\n\r\n")
	require.NotContains(t, string(redacted), "sk-secret")
	require.Contains(t, string(redacted), deepSeekCredentialRedaction)
	dataLine := strings.Split(string(redacted), "\r\n")[1]
	require.True(t, json.Valid([]byte(strings.TrimPrefix(dataLine, "data: "))))
}

func TestDeepSeekStreamingResponsesRedactCredentialCanary(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := deepSeekSecurityTestAccount(deepSeekSecurityCanaryKey)

	t.Run("chat", func(t *testing.T) {
		c, recorder := deepSeekSecurityTestContext("/v1/chat/completions")
		body := []byte(`{"model":"deepseek-v4-flash","messages":[],"stream":true}`)
		upstreamBody := "data: {\"choices\":[{\"delta\":{\"content\":\"" + deepSeekSecurityCanaryKey + "\"}}]}\n\n" +
			"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1}}\n\n" +
			"data: [DONE]\n\n"
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     deepSeekSecurityCanaryHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		}}
		svc := &OpenAIGatewayService{
			cfg:                  deepSeekSecurityTestConfig(),
			httpUpstream:         upstream,
			responseHeaderFilter: deepSeekSecurityResponseHeaderFilter(),
		}

		result, err := svc.ForwardAsChatCompletions(context.Background(), c, account, body, "", "")
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotContains(t, recorder.Body.String(), deepSeekSecurityCanaryKey)
		require.Contains(t, recorder.Body.String(), deepSeekCredentialRedaction)
		requireDeepSeekSecurityHeadersRedacted(t, recorder.Header())
	})

	t.Run("responses failed terminal", func(t *testing.T) {
		c, recorder := deepSeekSecurityTestContext("/v1/responses")
		body := []byte(`{"model":"deepseek-v4-pro","input":"hello","stream":true}`)
		upstreamBody := "event: response.failed\n" +
			"data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_secret\",\"status\":\"failed\",\"error\":{\"message\":\"" + deepSeekSecurityCanaryKey + "\"},\"usage\":{\"input_tokens\":2,\"output_tokens\":1}}}\n\n"
		upstream := &httpUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     deepSeekSecurityCanaryHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		}}
		svc := &OpenAIGatewayService{
			cfg:                  deepSeekSecurityTestConfig(),
			httpUpstream:         upstream,
			responseHeaderFilter: deepSeekSecurityResponseHeaderFilter(),
		}

		result, err := svc.Forward(context.Background(), c, account, body)
		require.Error(t, err)
		require.NotNil(t, result)
		require.Equal(t, "response.failed", result.UpstreamTerminalEvent)
		require.NotContains(t, recorder.Body.String(), deepSeekSecurityCanaryKey)
		require.Contains(t, recorder.Body.String(), deepSeekCredentialRedaction)
		requireDeepSeekSecurityHeadersRedacted(t, recorder.Header())
	})

	t.Run("messages", func(t *testing.T) {
		c, recorder := deepSeekSecurityTestContext("/v1/messages")
		body := []byte(`{"model":"deepseek-v4-flash","max_tokens":16,"stream":true,"messages":[]}`)
		upstreamBody := "event: message_start\n" +
			"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_secret\",\"usage\":{\"input_tokens\":2,\"output_tokens\":0}}}\n\n" +
			"event: content_block_delta\n" +
			"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"" + deepSeekSecurityCanaryKey + "\"}}\n\n" +
			"event: message_delta\n" +
			"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":1}}\n\n" +
			"event: message_stop\n" +
			"data: {\"type\":\"message_stop\"}\n\n"
		upstream := &anthropicHTTPUpstreamRecorder{resp: &http.Response{
			StatusCode: http.StatusOK,
			Header:     deepSeekSecurityCanaryHeaders("text/event-stream"),
			Body:       io.NopCloser(strings.NewReader(upstreamBody)),
		}}
		svc := &GatewayService{
			cfg:                  deepSeekSecurityTestConfig(),
			httpUpstream:         upstream,
			responseHeaderFilter: deepSeekSecurityResponseHeaderFilter(),
		}

		result, err := svc.Forward(context.Background(), c, account, &ParsedRequest{
			Body:   NewRequestBodyRef(body),
			Model:  "deepseek-v4-flash",
			Stream: true,
		})
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotContains(t, recorder.Body.String(), deepSeekSecurityCanaryKey)
		require.Contains(t, recorder.Body.String(), deepSeekCredentialRedaction)
		requireDeepSeekSecurityHeadersRedacted(t, recorder.Header())
	})
}
