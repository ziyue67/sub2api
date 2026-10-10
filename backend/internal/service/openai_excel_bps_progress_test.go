package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestExcelBPSProgressTimeoutPreservesUsageAndDoesNotReplay(t *testing.T) {
	r, w := io.Pipe()
	defer func() { _ = w.Close() }()
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: r}}
	svc := openAIClientToolsTestService(upstream)
	svc.cfg.Gateway.ExcelBPSStreamDataIntervalTimeout = 1
	go func() {
		_, _ = io.WriteString(w, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_synthetic\",\"usage\":{\"input_tokens\":7,\"output_tokens\":2}}}\n\n")
	}()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := svc.Forward(ctx, c, excelAccount(), []byte(`{"model":"gpt-6-astra","stream":true,"input":"test"}`))
	require.Error(t, err)
	require.Len(t, upstream.requests, 1)
	require.NotNil(t, result)
	require.Equal(t, 7, result.Usage.InputTokens)
	require.Equal(t, 2, result.Usage.OutputTokens)
	require.False(t, result.ClientDisconnect)
	require.Empty(t, result.UpstreamTerminalEvent)
	require.Contains(t, rec.Body.String(), "response.failed")
}

func TestExcelBPSProgressGuardDoesNotTimeoutBufferedBridgeText(t *testing.T) {
	// The complete upstream response may be buffered for local structured-output
	// validation; the timeout watches raw reads rather than withheld client text.
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_synthetic\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"{}\"}]}]}}\n\n"))}}
	svc := openAIClientToolsTestService(upstream)
	svc.cfg.Gateway.ExcelBPSStreamDataIntervalTimeout = 1
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	_, err := svc.Forward(context.Background(), c, excelAccount(), []byte(`{"model":"gpt-6-astra","stream":true,"input":"test","text":{"format":{"type":"json_object"}}}`))
	require.NoError(t, err)
	require.Contains(t, rec.Body.String(), "response.completed")
	require.NotContains(t, rec.Body.String(), "response.failed")
}
