package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestExcelBPSToolSchemaDiagnosticHTTPResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			upstream := &httpUpstreamRecorder{}
			svc := openAIClientToolsTestService(upstream)
			rec := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(rec)
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := []byte(fmt.Sprintf(`{"model":"gpt-6.1-sol","stream":%t,"input":"offline","tools":[{"type":"namespace","name":"example_mcp","tools":[{"type":"function","name":"example_tool","parameters":{"properties":{"rule":{"$ref":"https://example.invalid/schema?token=PRIVATE_SECRET"}}}}]}]}`, stream))
			_, err := svc.Forward(context.Background(), ctx, excelAccount(), body)
			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, rec.Code)
			require.True(t, IsResponseCommitted(ctx))
			require.Empty(t, upstream.requests)
			response := rec.Body.String()
			require.Equal(t, "basispoints_request_invalid", gjson.Get(response, "error.code").String())
			require.Equal(t, "invalid_request_error", gjson.Get(response, "error.type").String())
			require.Equal(t, "tools[0].tools[0].parameters#/properties/rule/$ref", gjson.Get(response, "error.param").String())
			require.Contains(t, gjson.Get(response, "error.message").String(), "example_mcp.example_tool")
			require.Contains(t, response, "external_reference_blocked")
			require.NotContains(t, response, "PRIVATE_SECRET")
			require.NotContains(t, response, "example.invalid")
		})
	}
}
