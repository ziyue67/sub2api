package admin

import (
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// ModelTraceTest runs one three-question round using stored account credentials.
// Arbitrary upstream URLs, credentials and answer text are never accepted here.
func (h *AccountHandler) ModelTraceTest(c *gin.Context) {
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	var input struct {
		ModelID         string `json:"model_id"`
		ReasoningEffort string `json:"reasoning_effort"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid request")
		return
	}
	input.ModelID = strings.TrimSpace(input.ModelID)
	if input.ModelID == "" || len(input.ModelID) > 100 {
		response.BadRequest(c, "model_id is required (maximum 100 bytes)")
		return
	}
	switch input.ReasoningEffort {
	case "", "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		response.BadRequest(c, "Invalid reasoning_effort")
		return
	}
	if err := h.accountTestService.TestModelTraceAccountConnection(c, accountID, input.ModelID, input.ReasoningEffort); err != nil && !c.Writer.Written() {
		response.ErrorFrom(c, err)
	}
}
