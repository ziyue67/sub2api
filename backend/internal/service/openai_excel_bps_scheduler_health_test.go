package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIAccountSchedulingIgnoresBPSProxyAcquisitionFailures(t *testing.T) {
	for _, existingFailure := range []bool{false, true} {
		t.Run(fmt.Sprintf("existing_failure_%t", existingFailure), func(t *testing.T) {
			cfg := DefaultPrioritySchedulingConfig()
			cfg.Enabled = true
			svc := priorityGateway(cfg, &priorityReaderStub{})
			now := time.Now()
			svc.openaiAccountStats = newOpenAIAccountRuntimeStats()
			svc.openaiAccountStats.now = func() time.Time { return now }
			account := excelAccount()
			ttft := 450
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", true, &ttft)
			if existingFailure {
				svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", false, nil, errors.New("upstream 429"))
			}
			beforeRate, beforeTTFT, beforeKnown := svc.openaiAccountStats.snapshot(account.ID)
			for range 6 {
				err := fmt.Errorf("forward: %w", &excelBPSAcquisitionFailure{cause: errors.New("warm pool empty")})
				localTTFT := 1
				require.False(t, svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", false, &localTTFT, err))
			}
			rate, latency, known := svc.openaiAccountStats.snapshot(account.ID)
			require.Equal(t, beforeRate, rate, "a local proxy outage must neither penalize nor heal account errors")
			require.Equal(t, beforeTTFT, latency)
			require.Equal(t, beforeKnown, known)

			// Upstream failures still count, even if their text resembles the local error.
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", false, nil, errors.New("excel BPS: basispoints_proxy_unavailable"))
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", false, nil, &UpstreamFailoverError{StatusCode: http.StatusTooManyRequests})
			rate, _, _ = svc.openaiAccountStats.snapshot(account.ID)
			require.InDelta(t, 0.2+0.8*(0.2+0.8*beforeRate), rate, 1e-12)
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", true, &ttft)
			recovered, _, _ := svc.openaiAccountStats.snapshot(account.ID)
			require.InDelta(t, rate*0.8, recovered, 1e-12)
		})
	}
}

func TestExcelBPSProxyUnavailableReachesSchedulerWithoutPenalty(t *testing.T) {
	for _, attachments := range []bool{false, true} {
		t.Run(fmt.Sprintf("attachments_%t", attachments), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			svc := openAIClientToolsTestService(upstream)
			now := time.Now()
			svc.openaiAccountStats = newOpenAIAccountRuntimeStats()
			svc.openaiAccountStats.now = func() time.Time { return now }
			enableNativeAttachments(svc)
			cfg := DefaultPrioritySchedulingConfig()
			cfg.Enabled = true
			svc.settingService.prioritySchedulingConfig.config = cfg
			svc.settingService.prioritySchedulingConfig.expires = time.Now().Add(time.Hour)
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = true
			body := []byte(`{"model":"gpt-6-astra","input":"test","stream":true}`)
			if attachments {
				body, _ = nativeGatewayBody(t)
			}
			ttft := 500
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", true, &ttft)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			_, err := svc.Forward(context.Background(), c, account, body)
			require.ErrorIs(t, err, errExcelBPSProxyUnavailable)
			require.EqualError(t, err, "excel BPS: basispoints_proxy_unavailable")
			require.Equal(t, http.StatusServiceUnavailable, rec.Code)
			require.Contains(t, rec.Body.String(), `"code":"basispoints_proxy_unavailable"`)
			require.Nil(t, upstream.lastReq, "local acquisition failure must not send upstream")
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", false, nil, fmt.Errorf("wrapped: %w", err))
			rate, latency, known := svc.openaiAccountStats.snapshot(account.ID)
			require.Zero(t, rate)
			require.Equal(t, float64(ttft), latency)
			require.True(t, known)
		})
	}
}

func TestExcelBPSUpstreamFailuresStillPenalizeScheduling(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"error":{"type":"api_error","message":"test upstream failure"}}`)),
			}}
			svc := openAIClientToolsTestService(upstream)
			now := time.Now()
			svc.openaiAccountStats = newOpenAIAccountRuntimeStats()
			svc.openaiAccountStats.now = func() time.Time { return now }
			cfg := DefaultPrioritySchedulingConfig()
			cfg.Enabled = true
			svc.settingService = priorityGateway(cfg, &priorityReaderStub{}).settingService
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = false
			body := []byte(`{"model":"gpt-6-astra","input":"test"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			_, err := svc.Forward(context.Background(), c, account, body)
			require.Error(t, err)
			require.NotErrorIs(t, err, errExcelBPSProxyUnavailable)
			require.NotNil(t, upstream.lastReq)
			svc.ReportOpenAIAccountScheduleResult(account, "gpt-6-astra", false, nil, err)
			rate, _, _ := svc.openaiAccountStats.snapshot(account.ID)
			require.InDelta(t, 0.2, rate, 1e-12, "actual upstream errors must still affect account health")
		})
	}
}
