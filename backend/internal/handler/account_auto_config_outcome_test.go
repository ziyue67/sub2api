package handler

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAutoConfigVerifiedCompletionRespectsFinalOutcome(t *testing.T) {
	for _, tc := range []struct {
		name                                             string
		markedAccount                                    int64
		status                                           int
		failedAttempt, streamError, handlerError, cancel bool
		want                                             int
		success                                          bool
	}{
		{name: "verified completion", markedAccount: 7, status: 200, want: 1, success: true},
		{name: "different final account", markedAccount: 8, status: 200, want: 1},
		{name: "failed attempt wins", markedAccount: 7, status: 200, failedAttempt: true, want: 1},
		{name: "stream failure wins", markedAccount: 7, status: 200, streamError: true, want: 1},
		{name: "handler error wins", markedAccount: 7, status: 200, handlerError: true, want: 1},
		{name: "HTTP error wins", markedAccount: 7, status: 502, want: 1},
		{name: "cancelled request ignored", markedAccount: 7, status: 200, cancel: true},
		{name: "499 ignored", markedAccount: 7, status: 499},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ops := service.NewOpsService(nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
			var got []service.AccountConcurrencyResult
			ops.SetAutoConfigObserver(func(r service.AccountConcurrencyResult) { got = append(got, r) })
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				c.Set(opsAccountIDKey, int64(7))
				c.Set(opsStreamKey, true)
				service.MarkOpsStreamCompleted(c, tc.markedAccount)
				if tc.failedAttempt {
					c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{{AccountID: 7}})
				}
				if tc.streamError {
					service.MarkOpsStreamError(c, "stream_failed", "mock failure", 502)
				}
				if tc.handlerError {
					_ = c.Error(errors.New("mock handler error"))
				}
				if tc.cancel {
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				c.Data(tc.status, "text/event-stream", []byte(": keepalive\n\n"))
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Len(t, got, tc.want)
			if tc.want > 0 {
				require.Equal(t, tc.success, got[0].Success)
			}
		})
	}
}

func TestAutoConfigRealRequestOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body     string
		status         int
		stream, cancel bool
		want           int
		success        bool
	}{
		{"json success", `{"id":"ok"}`, 200, false, false, 1, true},
		{"upstream 429", `{"error":"limited"}`, 429, false, false, 1, false},
		{"complete stream", "data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n", 200, true, false, 1, true},
		{"stream error at 200", "event: error\ndata: {\"type\":\"error\",\"error\":{\"message\":\"failed\"}}\n\n", 200, true, false, 1, false},
		{"truncated stream", "data: {\"type\":\"response.created\"}\n\n", 200, true, false, 1, false},
		{"cancelled", "data: [DONE]\n\n", 200, true, true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ops := service.NewOpsService(nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
			got := []service.AccountConcurrencyResult{}
			ops.SetAutoConfigObserver(func(r service.AccountConcurrencyResult) { got = append(got, r) })
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				c.Set(opsAccountIDKey, int64(7))
				c.Set(opsStreamKey, tc.stream)
				if tc.cancel {
					ctx, cancel := context.WithCancel(c.Request.Context())
					cancel()
					c.Request = c.Request.WithContext(ctx)
				}
				content := "application/json"
				if tc.stream {
					content = "text/event-stream"
				}
				c.Data(tc.status, content, []byte(tc.body))
			})
			router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			require.Len(t, got, tc.want)
			if tc.want > 0 {
				require.Equal(t, tc.success, got[0].Success)
				require.Equal(t, int64(7), got[0].AccountID)
			}
		})
	}
}
func TestAutoConfigRetryDeduplicatesFailedAccount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ops := service.NewOpsService(nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil)
	got := []service.AccountConcurrencyResult{}
	ops.SetAutoConfigObserver(func(r service.AccountConcurrencyResult) { got = append(got, r) })
	router := gin.New()
	router.Use(OpsErrorLoggerMiddleware(ops))
	router.POST("/v1/responses", func(c *gin.Context) {
		c.Set(opsAccountIDKey, int64(8))
		c.Set(service.OpsUpstreamErrorsKey, []*service.OpsUpstreamErrorEvent{{AccountID: 7}, {AccountID: 7}})
		c.JSON(200, gin.H{"ok": true})
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
	require.Len(t, got, 2)
	require.Equal(t, int64(7), got[0].AccountID)
	require.False(t, got[0].Success)
	require.True(t, got[1].Success)
}
func TestAutoConfigSplitSSEAndWriterReuse(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	w := acquireOpsCaptureWriter(c.Writer)
	for _, chunk := range []string{"data: {\"type\":\"message_", "stop\"}\n", "\n"} {
		_, err := w.WriteString(chunk)
		require.NoError(t, err)
	}
	state, _ := w.lockActive()
	require.True(t, state.terminalSuccess)
	state.mu.RUnlock()
	releaseOpsCaptureWriter(w)
	fresh := acquireOpsCaptureWriter(c.Writer)
	defer releaseOpsCaptureWriter(fresh)
	state, _ = fresh.lockActive()
	require.False(t, state.terminalSuccess)
	state.mu.RUnlock()
}
