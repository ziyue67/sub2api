package handler

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpsBridgeFailurePreservesActualHTTPAndLocalOwnership(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			setupOpsErrorLogTestQueue(t, 2)
			repo := &ingressRejectOpsRepo{}
			ops := service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			router := gin.New()
			router.Use(OpsErrorLoggerMiddleware(ops))
			router.POST("/v1/responses", func(c *gin.Context) {
				c.Set(opsAccountIDKey, int64(300))
				service.SetOpsUpstreamError(c, http.StatusOK, "", "")
				failure := &basispoints.BridgeFailure{Message: "basispoints returned a tool outside the client's catalog",
					FailureStage: "bridge_tool_validation", RejectedTool: "collaboration.send_message",
					RepairSkippedReason: "visible_content", UpstreamTerminalEvent: "response.completed"}
				service.MarkOpsStreamErrorValue(c, service.OpsStreamError{
					ErrType: "server_error", Code: "basispoints_protocol_error", Message: failure.Message,
					IntendedStatus: 502, CountTowardsSLA: true, NonStream: !stream, BridgeFailure: failure,
				})
				if stream {
					c.Header("Content-Type", "text/event-stream")
					_, err := c.Writer.WriteString("event: response.failed\ndata: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"basispoints_protocol_error\",\"message\":\"local validation failed\"}}}\n\n")
					require.NoError(t, err)
				} else {
					c.JSON(http.StatusBadGateway, gin.H{"error": failure.Details()})
				}
			})
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/responses", nil))
			for len(opsErrorLogQueue) > 0 {
				job := <-opsErrorLogQueue
				flushOpsErrorLogBatch([]opsErrorLogJob{job})
			}
			require.Len(t, repo.entries, 1)
			entry := repo.entries[0]
			require.Equal(t, http.StatusBadGateway, entry.StatusCode, "logical request failure still counts toward SLA")
			require.NotNil(t, entry.UpstreamStatusCode)
			require.Equal(t, http.StatusOK, *entry.UpstreamStatusCode)
			require.Equal(t, "internal", entry.ErrorPhase)
			require.Equal(t, "platform", entry.ErrorOwner)
			require.Equal(t, "gateway", entry.ErrorSource)
			require.False(t, entry.IsBusinessLimited)
			require.Equal(t, stream, entry.Stream)
			require.Equal(t, "bridge_tool_validation", gjson.Get(entry.ErrorBody, "error.failure_stage").String())
			require.Equal(t, "collaboration.send_message", gjson.Get(entry.ErrorBody, "error.rejected_tool").String())
			require.Equal(t, "response.completed", gjson.Get(entry.ErrorBody, "error.upstream_terminal_event").String())
		})
	}
}
