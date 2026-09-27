package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestExcelBPSAcquisitionDiagnosticsPreserveReasonWithoutSending(t *testing.T) {
	for _, reason := range []string{"candidate_checks_exhausted", "acquisition_timeout", "no_eligible_nodes", "session_draining", "session_capacity", "manager_unavailable"} {
		t.Run(reason, func(t *testing.T) {
			core, logs := observer.New(zap.WarnLevel)
			ctx := logger.IntoContext(context.Background(), zap.New(core))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			account := excelAccount()
			account.Extra["openai_excel_bps_mihomo"] = true
			acquire := func(context.Context, string, ...string) (string, excelBPSLease, error) {
				return "", nil, fmt.Errorf("secret-user:proxy-password: %w", &mihomo.BPSAcquireError{Reason: reason, Candidates: 2})
			}
			svc := &OpenAIGatewayService{httpUpstream: &bpsTestUpstream{send: func(*http.Request, string) (*http.Response, error) {
				t.Fatal("acquisition failure must not send a model request")
				return nil, nil
			}}}
			_, lease, _, err := svc.doExcelBPSRequest(ctx, c, account, "private-session", []byte("body"), "secret-token", "secret-account", acquire)
			require.ErrorIs(t, err, errExcelBPSProxyUnavailable)
			require.Nil(t, lease)
			var acquisition *mihomo.BPSAcquireError
			require.ErrorAs(t, err, &acquisition)
			events, exists := c.Get(OpsUpstreamErrorsKey)
			require.True(t, exists)
			attempts, ok := events.([]*OpsUpstreamErrorEvent)
			require.True(t, ok)
			require.Len(t, attempts, 1)
			var detail map[string]any
			require.NoError(t, json.Unmarshal([]byte(attempts[0].Detail), &detail))
			require.Equal(t, reason, detail["acquisition_reason"])
			require.Equal(t, float64(2), detail["candidates_checked"])
			require.Zero(t, attempts[0].UpstreamStatusCode)
			require.Equal(t, reason, logs.All()[0].ContextMap()["acquisition_reason"])
			all := fmt.Sprint(err, attempts[0], logs.All()[0].ContextMap())
			for _, secret := range []string{"secret-user", "proxy-password", "private-session", "secret-token", "secret-account"} {
				require.NotContains(t, all, secret)
			}
		})
	}
}
