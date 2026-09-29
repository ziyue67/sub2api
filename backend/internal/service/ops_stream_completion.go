package service

import (
	"bytes"
	"encoding/json"

	"github.com/gin-gonic/gin"
)

const opsCompletedStreamAccountKey = "ops_completed_stream_account_id"

// MarkOpsStreamCompleted records an account-bound outcome, not a response body.
// Call only after the gateway has validated a successful terminal and finished
// forwarding without a downstream disconnect. Error observers retain priority.
func MarkOpsStreamCompleted(c *gin.Context, accountID int64) {
	if c != nil && accountID > 0 {
		c.Set(opsCompletedStreamAccountKey, accountID)
	}
}

func OpsStreamCompletedForAccount(c *gin.Context, accountID int64) bool {
	return c != nil && accountID > 0 && c.GetInt64(opsCompletedStreamAccountKey) == accountID
}

// IsSuccessfulStreamTerminal inspects an already available SSE data payload.
// Only small protocol fields are decoded; output text is never retained. The
// caller must independently rule out failed attempts and client cancellation.
func IsSuccessfulStreamTerminal(data []byte) bool {
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
