package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSAttachmentFailureDiagnostics(t *testing.T) {
	for _, tc := range []struct {
		name, body, stage, kind string
		status                  int
		transport               error
	}{
		{name: "HTTP rejection", status: 403, body: "PRIVATE_UPSTREAM_BODY", stage: "attachment_http", kind: "upstream_http"},
		{name: "rate limit", status: 429, body: "PRIVATE_UPSTREAM_BODY", stage: "attachment_http", kind: "upstream_http"},
		{name: "redirect", status: 307, body: "PRIVATE_UPSTREAM_BODY", stage: "attachment_http", kind: "upstream_http"},
		{name: "invalid JSON", status: 200, body: "PRIVATE_UPSTREAM_BODY", stage: "attachment_response", kind: "invalid_json"},
		{name: "invalid file ID", status: 200, body: "{\"openai_file_id\":\"PRIVATE_FILE_ID\"}", stage: "attachment_response", kind: "invalid_file_id"},
		{name: "large response", status: 200, body: strings.Repeat("x", (64<<10)+1), stage: "attachment_response", kind: "response_too_large"},
		{name: "DNS", transport: &net.DNSError{Err: "PRIVATE_DNS_ERROR", Name: "PRIVATE_HOST"}, stage: "attachment_transport", kind: "dns_error"},
		{name: "deadline", transport: fmt.Errorf("PRIVATE_URL: %w", context.DeadlineExceeded), stage: "attachment_transport", kind: "deadline_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := openAIClientToolsTestService(nil)
			enableNativeAttachments(svc)
			calls := 0
			svc.httpUpstream = &nativeAttachmentUpstream{do: func(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
				calls++
				require.Equal(t, basispoints.AttachmentsURL, req.URL.String())
				if tc.transport != nil {
					return nil, tc.transport
				}
				return &http.Response{StatusCode: tc.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			}}
			body, _ := nativeGatewayBody(t)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			account := excelAccount()
			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.Equal(t, 1, calls, "attachment failure must not submit generation")
			if tc.status == 429 {
				requireExcelBPSRateLimitFailover(t, err, c)
			} else {
				wantStatus := tc.status
				if wantStatus < 400 || wantStatus > 599 {
					wantStatus = http.StatusBadGateway
				}
				require.Equal(t, wantStatus, rec.Code, "upstream diagnostics must not replace the client failure status")
			}
			events, exists := c.Get(OpsUpstreamErrorsKey)
			require.True(t, exists, "safe stage/reason diagnostic must be persisted")
			attempts, ok := events.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, attempts, 1)
			attempt := attempts[0]
			require.Equal(t, tc.stage, attempt.Stage)
			require.Equal(t, tc.kind, attempt.Reason)
			require.Equal(t, tc.status, attempt.UpstreamStatusCode, "record real upstream HTTP status, not synthesized 502")
			require.Equal(t, basispoints.AttachmentsURL, attempt.UpstreamURL)
			var detail map[string]any
			require.NoError(t, json.Unmarshal([]byte(attempt.Detail), &detail))
			require.Equal(t, tc.kind, detail["error_kind"])
			require.Equal(t, false, detail["generation_started"])
			require.NotContains(t, fmt.Sprint(err, attempt, c.GetString(OpsUpstreamErrorDetailKey), rec.Body.String()), "PRIVATE_")
		})
	}
}

func TestExcelBPSAttachmentClientCancellationIsNotProviderFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc := openAIClientToolsTestService(nil)
	enableNativeAttachments(svc)
	calls := 0
	svc.httpUpstream = &nativeAttachmentUpstream{do: func(*http.Request, string, int64, int) (*http.Response, error) {
		calls++
		cancel()
		return nil, fmt.Errorf("PRIVATE_PROXY: %w", context.Canceled)
	}}
	body, _ := nativeGatewayBody(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body)).WithContext(ctx)
	_, err := svc.Forward(ctx, c, excelAccount(), body)
	require.True(t, errors.Is(err, context.Canceled))
	require.Equal(t, 1, calls)
	require.Empty(t, c.GetString(OpsUpstreamErrorMessageKey))
	_, exists := c.Get(OpsUpstreamErrorsKey)
	require.False(t, exists)
}

func TestExcelBPSAttachmentProxyAcquisitionDiagnostics(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	account := excelAccount()
	account.Extra["openai_excel_bps_mihomo"] = true
	calls := 0
	acquire := func(context.Context, string, ...string) (string, excelBPSLease, error) {
		calls++
		return "", nil, fmt.Errorf("PRIVATE_PROXY: %w", &mihomo.BPSAcquireError{Reason: "warm_pool_empty", Candidates: 0})
	}
	_, lease, err := acquireExcelBPSAttachmentProxy(context.Background(), c, account, "PRIVATE_SESSION", acquire)
	require.ErrorIs(t, err, errExcelBPSProxyUnavailable)
	require.Nil(t, lease)
	require.Equal(t, 1, calls, "diagnostics must not introduce retries")
	events, exists := c.Get(OpsUpstreamErrorsKey)
	require.True(t, exists)
	attempts, ok := events.([]*OpsUpstreamErrorEvent)
	require.True(t, ok)
	require.Len(t, attempts, 1)
	require.Equal(t, "attachment_proxy_acquisition", attempts[0].Stage)
	require.Equal(t, basispoints.AttachmentsURL, attempts[0].UpstreamURL)
	require.Zero(t, attempts[0].UpstreamStatusCode)
	var detail map[string]any
	require.NoError(t, json.Unmarshal([]byte(attempts[0].Detail), &detail))
	require.Equal(t, "warm_pool_empty", detail["acquisition_reason"])
	require.Equal(t, float64(0), detail["candidates_checked"])
	require.NotContains(t, fmt.Sprint(err, attempts[0], detail), "PRIVATE_")
}
