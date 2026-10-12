package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBPSTestResponseEvidence(t *testing.T) {
	for _, transportError := range []bool{false, true} {
		upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 403, Header: http.Header{"X-Request-Id": {"req_example"}}, Body: io.NopCloser(strings.NewReader(`{"error":{"code":"basispoints_model_access_changed"}}`))}}
		if transportError {
			upstream.err = errors.New("offline")
		}
		svc := &AccountTestService{openaiGatewayService: openAIClientToolsTestService(upstream)}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest("POST", "/admin/test", nil)
		require.Error(t, svc.testExcelBPSAccountConnection(c, excelAccount(), "gpt-6-astra", "test"))
		wire := recorder.Body.String()
		if transportError {
			require.NotContains(t, wire, `"type":"upstream_response"`)
			continue
		}
		require.Contains(t, wire, `"upstream_status":403`)
		require.Contains(t, wire, `"request_id":"req_example"`)
		require.Contains(t, wire, `"upstream_error_code":"basispoints_model_access_changed"`)
		require.Contains(t, wire, `"upstream_model":"gpt-6-astra"`)
	}
}

func TestBPSTestResponseEvidenceRedactsAndRequiresObserver(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	svc := &AccountTestService{}
	resp := &http.Response{StatusCode: 401, Header: http.Header{"X-Request-Id": {"secret-token"}}}
	observeBPSTestResponse(context.Background(), resp, "gpt-6-astra", "secret-token", "token_revoked")
	require.Empty(t, recorder.Body.String())
	observeBPSTestResponse(svc.withBPSTestEvidence(context.Background(), c), resp, "gpt-6-astra", "secret-token", "token_revoked")
	require.NotContains(t, recorder.Body.String(), "secret-token")
	require.Contains(t, recorder.Body.String(), "token_revoked")
}
