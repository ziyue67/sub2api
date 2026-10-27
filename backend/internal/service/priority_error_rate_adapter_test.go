//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func priorityMockResponse(status int, body string, stream bool) *http.Response {
	contentType := "application/json"
	if stream {
		contentType = "text/event-stream"
	}
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {contentType}}, Body: io.NopCloser(strings.NewReader(body))}
}

func TestPriorityErrorRateNativeGuardAfterOutput(t *testing.T) {
	const guard = "Tool arguments stopped making progress: consecutive whitespace outside JSON strings (output_index=1, whitespace_bytes=1024, limit_bytes=1024)"
	body := []byte(`{"model":"gpt-6.1-sol","input":"mock","stream":true}`)
	wire := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\ndata: " + fmt.Sprintf(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":%q}}}`, guard) + "\n\n"
	upstream := &httpUpstreamRecorder{resp: priorityMockResponse(200, wire, true)}
	svc := newOpenAIRejectedFieldTestService(upstream)
	account := newOpenAIRejectedFieldTestAccount()
	c := newOpenAIRejectedFieldTestContext(body)
	_, err := svc.Forward(context.Background(), c, account, body)
	require.Error(t, err)
	require.Equal(t, 200, c.Writer.Status(), "the response has already started")
	require.Contains(t, c.Writer.Header().Get("Content-Type"), "text/event-stream")
	got := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: account.ID, model: "gpt-6.1-sol", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	require.Equal(t, int64(1), got.Failures)
	require.Zero(t, got.Successes)
	// Repeated outer health/scheduler feedback cannot write the physical counters.
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-6.1-sol", false, nil, err)
	svc.ReportOpenAIAccountScheduleResult(account, "gpt-6.1-sol", false, nil, err)
	again := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: account.ID, model: "gpt-6.1-sol", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	require.Equal(t, got.Failures, again.Failures)
}

func TestPriorityErrorRateNativeFallbackKeepsOriginalFailure(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":"mock","stream":false}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		priorityMockResponse(502, `{"error":{"type":"server_error","message":"mock unavailable"}}`, false),
		priorityMockResponse(200, `{"id":"resp_mock","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`, false),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	first := newOpenAIRejectedFieldTestAccount()
	second := *first
	second.ID++
	_, err := svc.Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), first, body)
	require.Error(t, err)
	_, err = svc.Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), &second, body)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	failed := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: first.ID, model: "gpt-5.5", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	success := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: second.ID, model: "gpt-5.5", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	require.Equal(t, int64(1), failed.Failures)
	require.Zero(t, failed.Successes)
	require.Equal(t, int64(1), success.Successes)
	require.Zero(t, success.Failures)
}

func TestPriorityErrorRateNativeInternalRetryExcludesInputRejection(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":"mock","truncation":"auto","stream":false}`)
	upstream := &httpUpstreamRecorder{responses: []*http.Response{
		priorityMockResponse(400, `{"error":{"type":"invalid_request_error","code":"unsupported_parameter","param":"truncation","message":"Unsupported parameter: truncation."}}`, false),
		priorityMockResponse(200, `{"id":"resp_mock","object":"response","status":"completed","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`, false),
	}}
	svc := newOpenAIRejectedFieldTestService(upstream)
	account := newOpenAIRejectedFieldTestAccount()
	_, err := svc.Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), account, body)
	require.NoError(t, err)
	require.Len(t, upstream.requests, 2)
	got := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: account.ID, model: "gpt-5.5", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	require.Equal(t, int64(1), got.Successes)
	require.Zero(t, got.Failures)
}

func TestPriorityErrorRateScopeRetryDisconnectAndTransportClassification(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		err                   error
		disconnected, timeout bool
		failures              int64
	}{
		{"network timeout", &net.DNSError{IsTimeout: true}, false, false, 1},
		{"upstream watchdog", context.Canceled, false, true, 1},
		{"local storage", errors.New("storage write failed"), false, false, 0},
		{"client cancel", context.Canceled, false, false, 0},
		{"downstream disconnected", &net.DNSError{IsTimeout: true}, true, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newOpenAIRejectedFieldTestService(&httpUpstreamRecorder{})
			ctx, scope := withPriorityAttemptScope(context.Background(), "mock-model", "/v1/responses")
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example/v1/responses", nil)
			account := newOpenAIRejectedFieldTestAccount()
			observe := svc.observePriorityAttempt(req, account)
			observe(nil, tc.err)
			if tc.timeout {
				markPriorityAttemptFailure(ctx)
			}
			scope.finish(&OpenAIForwardResult{ClientDisconnect: tc.disconnected})
			got := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: account.ID, model: "mock-model", endpoint: "/v1/responses"}, time.Now(), 10, 5)
			require.Equal(t, tc.failures, got.Failures)
			require.Zero(t, got.Successes)
		})
	}
	// A genuine same-account retry has a failure followed by an independent success.
	svc := newOpenAIRejectedFieldTestService(&httpUpstreamRecorder{})
	ctx, scope := withPriorityAttemptScope(context.Background(), "mock-model", "/v1/responses")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example/v1/responses", nil)
	a := newOpenAIRejectedFieldTestAccount()
	first := svc.observePriorityAttempt(req, a)
	resp := priorityMockResponse(503, `{"error":{"type":"server_error"}}`, false)
	first(resp, nil)
	_, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	second := svc.observePriorityAttempt(req, a)
	resp = priorityMockResponse(200, "data: {\"type\":\"response.completed\"}\n\n", true)
	second(resp, nil)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	scope.finish(nil)
	scope.finish(nil)
	got := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: a.ID, model: "mock-model", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	require.Equal(t, int64(1), got.Successes)
	require.Equal(t, int64(1), got.Failures)
}

func TestPriorityErrorRateLateCloseCannotOverwriteRetry(t *testing.T) {
	svc := newOpenAIRejectedFieldTestService(&httpUpstreamRecorder{})
	ctx, scope := withPriorityAttemptScope(context.Background(), "mock-model", "/v1/responses")
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://upstream.example/v1/responses", nil)
	a := newOpenAIRejectedFieldTestAccount()
	first := svc.observePriorityAttempt(req, a)
	one := priorityMockResponse(503, `{"error":{"type":"server_error"}}`, false)
	first(one, nil)
	_, err := io.ReadAll(one.Body)
	require.NoError(t, err)
	second := svc.observePriorityAttempt(req, a)
	two := priorityMockResponse(200, "data: {\"type\":\"response.completed\"}\n\n", true)
	second(two, nil)
	_, err = io.ReadAll(two.Body)
	require.NoError(t, err)
	require.NoError(t, two.Body.Close())
	scope.finish(nil)
	require.NoError(t, one.Body.Close())
	require.NoError(t, one.Body.Close())
	scope.finish(nil)
	got := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: a.ID, model: "mock-model", endpoint: "/v1/responses"}, time.Now(), 10, 5)
	require.Equal(t, int64(1), got.Successes)
	require.Equal(t, int64(1), got.Failures)
}

func TestPriorityErrorRateBPSActualAdapter(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			wire := "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_mock\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"
			if failed {
				wire = "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"mock upstream failure\"}}}\n\n"
			}
			upstream := &httpUpstreamRecorder{resp: priorityMockResponse(200, wire, true)}
			svc := openAIClientToolsTestService(upstream)
			a := excelAccount()
			a.Extra["openai_excel_bps_mihomo"] = false
			body := []byte(`{"model":"gpt-5.6-sol","input":"mock","stream":true}`)
			_, err := svc.Forward(context.Background(), newOpenAIRejectedFieldTestContext(body), a, body)
			if failed {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			require.NotNil(t, upstream.lastReq)
			require.Equal(t, "bps.openai.com", upstream.lastReq.URL.Host)
			got := svc.openaiAccountStats.attempts.snapshot(priorityAttemptKey{accountID: a.ID, model: "gpt-5.6-sol", endpoint: "/v1/responses"}, time.Now(), 10, 5)
			if failed {
				require.Equal(t, int64(1), got.Failures)
				require.Zero(t, got.Successes)
			} else {
				require.Equal(t, int64(1), got.Successes)
				require.Zero(t, got.Failures)
			}
		})
	}
}
