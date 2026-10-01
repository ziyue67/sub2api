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

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bpsFailureWire(t *testing.T, kind, code, errorType string, status int) string {
	t.Helper()
	detail := map[string]any{"code": code, "type": errorType, "message": "PRIVATE_UPSTREAM_BODY", "param": "PRIVATE_PARAMETER"}
	event := map[string]any{"type": kind}
	if status != 0 {
		event["status"] = status
	}
	if kind == "error" {
		event["error"] = detail
	} else {
		event["response"] = map[string]any{
			"id": "resp_failure", "status": strings.TrimPrefix(kind, "response."), "error": detail,
			"output": []any{}, "usage": map[string]any{"input_tokens": 7, "output_tokens": 2},
		}
	}
	raw, err := json.Marshal(event)
	require.NoError(t, err)
	return "event: " + kind + "\ndata: " + string(raw) + "\n\n"
}

func TestExcelBPSFailureAfterCompactKeepalive(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: bpsCompletionResponse(http.StatusOK,
		bpsFailureWire(t, "response.failed", "rate_limit_exceeded", "", 0))}
	svc := openAIClientToolsTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses/compact", nil)
	MarkOpenAICompactClientStream(c)
	stop := StartOpenAICompactSSEKeepalive(c, time.Hour)
	defer stop()
	value, ok := c.Get(openAICompactSSEKeepaliveKey)
	require.True(t, ok)
	keepalive, ok := value.(*openAICompactSSEKeepalive)
	require.True(t, ok)
	require.True(t, keepalive.beat())
	body, err := json.Marshal(map[string]any{"model": "gpt-6-astra", "input": "continue"})
	require.NoError(t, err)
	_, err = svc.Forward(context.Background(), c, excelAccount(), body)
	require.Error(t, err)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, strings.Count(rec.Body.String(), "event: response.failed\n"))
	require.NotContains(t, rec.Body.String(), "PRIVATE_")
	require.False(t, json.Valid(rec.Body.Bytes()), "already committed keepalive requires an SSE terminal")
	require.Contains(t, rec.Body.String(), "rate_limit_exceeded")
	observed, ok := GetOpsStreamError(c)
	require.True(t, ok)
	require.Equal(t, http.StatusTooManyRequests, observed.IntendedStatus)
	require.Len(t, upstream.requests, 1)
}

func TestExcelBPSFailureQualityObservationDoesNotCoolAccount(t *testing.T) {
	upstream := &httpUpstreamRecorder{resp: bpsCompletionResponse(http.StatusOK,
		bpsFailureWire(t, "response.failed", "rate_limit_exceeded", "", 0))}
	svc := openAIClientToolsTestService(upstream)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	ctx := withPelicanTestOptions(context.Background(), pelicanTestOptions{observeOnly: true})
	body, err := json.Marshal(map[string]any{"model": "gpt-6-astra", "input": "observe"})
	require.NoError(t, err)
	account := excelAccount()
	_, err = svc.Forward(ctx, c, account, body)
	require.Error(t, err)
	require.Equal(t, http.StatusTooManyRequests, rec.Code)
	require.False(t, svc.isExcelBPSCoolingDown(account, "gpt-6-astra"))
	require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
	require.Len(t, upstream.requests, 1)
}

func TestExcelBPSFailureClassification(t *testing.T) {
	for _, tc := range []struct {
		name, event, code, errorType string
		explicit, status             int
		wantCode, wantType           string
	}{
		{"invalid input", "response.failed", "invalid_value", "", 0, 400, "invalid_value", "invalid_request_error"},
		{"authentication", "response.failed", "invalid_api_key", "", 0, 401, "invalid_api_key", "authentication_error"},
		{"permission", "response.failed", "permission_denied", "", 0, 403, "permission_denied", "permission_error"},
		{"rate limit", "error", "rate_limit_exceeded", "", 0, 429, "rate_limit_exceeded", "rate_limit_error"},
		{"type fallback", "response.failed", "", "overloaded_error", 0, 503, "basispoints_upstream_error", "server_error"},
		{"explicit status", "response.failed", "invalid_value", "invalid_request_error", 429, 429, "invalid_value", "rate_limit_error"},
		{"cancelled", "response.cancelled", "", "", 0, 502, "basispoints_upstream_cancelled", "server_error"},
		{"cancelled rate limit", "response.cancelled", "rate_limit_exceeded", "", 0, 429, "rate_limit_exceeded", "rate_limit_error"},
		{"unknown", "error", "PRIVATE_ERROR_CODE", "PRIVATE_ERROR_TYPE", 0, 502, "basispoints_upstream_error", "server_error"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", tc.name, stream), func(t *testing.T) {
				wire := bpsFailureWire(t, tc.event, tc.code, tc.errorType, tc.explicit)
				if stream {
					prefix, err := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": "already delivered"})
					require.NoError(t, err)
					wire = "data: " + string(prefix) + "\n\n" + wire
				}
				upstream := &httpUpstreamRecorder{resp: bpsCompletionResponse(http.StatusOK, wire)}
				svc := openAIClientToolsTestService(upstream)
				account := excelAccount()
				account.Extra["openai_excel_bps_auto_disable_on_403"] = true
				body, err := json.Marshal(map[string]any{"model": "gpt-6-astra", "stream": stream, "input": "hello"})
				require.NoError(t, err)
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
				result, err := svc.Forward(context.Background(), c, account, body)
				require.Error(t, err)
				require.NotNil(t, result)
				require.Equal(t, tc.event, result.UpstreamTerminalEvent)
				require.Len(t, upstream.requests, 1, "never replay a request after an upstream terminal")
				require.True(t, IsResponseCommitted(c))
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
				require.NotContains(t, rec.Body.String(), "PRIVATE_")
				require.NotContains(t, err.Error(), "PRIVATE_")
				require.NotContains(t, rec.Body.String(), "basispoints_stream_incomplete")
				require.NotContains(t, rec.Body.String(), "response.completed")
				if tc.event != "error" {
					require.EqualValues(t, 7, result.Usage.InputTokens)
					require.EqualValues(t, 2, result.Usage.OutputTokens)
				}
				if stream {
					require.Equal(t, http.StatusOK, rec.Code)
					require.Contains(t, rec.Body.String(), "already delivered")
					found := 0
					for _, line := range strings.Split(rec.Body.String(), "\n") {
						if !strings.HasPrefix(line, "data: ") {
							continue
						}
						raw := strings.TrimPrefix(line, "data: ")
						if gjson.Get(raw, "type").String() != tc.event {
							continue
						}
						found++
						path := "error"
						if tc.event != "error" {
							path = "response.error"
						}
						require.Equal(t, tc.wantCode, gjson.Get(raw, path+".code").String())
						require.Equal(t, tc.wantType, gjson.Get(raw, path+".type").String())
						require.EqualValues(t, tc.status, gjson.Get(raw, path+".status").Int())
					}
					require.Equal(t, 1, found)
				} else {
					require.Equal(t, tc.status, rec.Code)
					require.Equal(t, tc.wantCode, gjson.GetBytes(rec.Body.Bytes(), "error.code").String())
					require.Equal(t, tc.wantType, gjson.GetBytes(rec.Body.Bytes(), "error.type").String())
				}
				observed, ok := GetOpsStreamError(c)
				require.True(t, ok)
				require.Equal(t, tc.status, observed.IntendedStatus)
				require.Equal(t, tc.wantCode, observed.Code)
				require.Equal(t, http.StatusOK, c.GetInt(OpsUpstreamStatusCodeKey), "mapped status is not an upstream HTTP status")
				require.Equal(t, tc.status == 429, svc.isExcelBPSCoolingDown(account, "gpt-6-astra"))
				require.False(t, svc.isOpenAIAccountRuntimeBlocked(account))
				require.True(t, account.IsSchedulable())
				require.True(t, account.IsExcelBPSEnabled(), "an in-band 403 must not disable the account")
			})
		}
	}
}
