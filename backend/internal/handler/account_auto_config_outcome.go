package handler

import (
	"bytes"
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
	"time"
)

func autoConfigSuccessfulFrame(frame []byte) bool {
	_, data := parseOpsSSEFrameEnvelope(frame)
	if bytes.Equal(bytes.TrimSpace(data), []byte("[DONE]")) {
		return true
	}
	if !bytes.Contains(data, []byte("response.completed")) && !bytes.Contains(data, []byte("message_stop")) && !bytes.Contains(data, []byte("finishReason")) {
		return false
	}
	var v struct {
		Type     string `json:"type"`
		Response struct {
			Status string `json:"status"`
		} `json:"response"`
		Candidates []struct {
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
	}
	if json.Unmarshal(data, &v) != nil {
		return false
	}
	if v.Type == "message_stop" || v.Type == "response.completed" && (v.Response.Status == "" || v.Response.Status == "completed") {
		return true
	}
	for _, c := range v.Candidates {
		if c.FinishReason == "STOP" || c.FinishReason == "MAX_TOKENS" {
			return true
		}
	}
	return false
}
func observeAutoConfigRequest(c *gin.Context, ops *service.OpsService, w *opsCaptureWriter, started time.Time) {
	if ops == nil || c.Request.Method != http.MethodPost || isCountTokensRequest(c) {
		return
	}
	path := c.Request.URL.Path
	if !strings.HasSuffix(path, "/responses") && !strings.HasSuffix(path, "/messages") && !strings.HasSuffix(path, "/chat/completions") && !strings.Contains(path, ":generateContent") && !strings.Contains(path, ":streamGenerateContent") {
		return
	}
	failed := map[int64]bool{}
	if v, ok := c.Get(service.OpsUpstreamErrorsKey); ok {
		if events, ok := v.([]*service.OpsUpstreamErrorEvent); ok {
			for _, e := range events {
				if e != nil && e.AccountID > 0 {
					failed[e.AccountID] = true
				}
			}
		}
	}
	for id := range failed {
		ops.ObserveConcurrencyResult(service.AccountConcurrencyResult{AccountID: id, StartedAt: started})
	}
	id := c.GetInt64(opsAccountIDKey)
	if id <= 0 || failed[id] {
		return
	}
	if c.Request.Context().Err() != nil || c.Writer.Status() == 499 {
		return
	}
	state, _ := w.lockActive()
	if state == nil {
		return
	}
	terminalError, completed := state.terminalFound, state.terminalSuccess
	state.mu.RUnlock()
	success := c.Writer.Status() >= 200 && c.Writer.Status() < 300 && len(c.Errors) == 0 && !terminalError && len(service.GetOpsStreamErrors(c)) == 0
	streaming := c.GetBool(opsStreamKey) || strings.Contains(c.Writer.Header().Get("Content-Type"), "text/event-stream")
	if streaming && !completed {
		success = false
	}
	ops.ObserveConcurrencyResult(service.AccountConcurrencyResult{AccountID: id, StartedAt: started, Success: success})
}
