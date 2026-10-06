package admin

import (
	"context"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) AstraGatewayStatus(c *gin.Context) {
	response.Success(c, h.accountTestService.AstraGatewayStatus(c.Request.Context()))
}
func (h *AccountHandler) AstraGatewayHistory(c *gin.Context) {
	page, err := strconv.Atoi(c.DefaultQuery("page", "1"))
	if err != nil || page < 1 || page > 1000000 {
		response.BadRequest(c, "invalid_page")
		return
	}
	result, err := h.accountTestService.AstraGatewayHistory(c.Request.Context(), c.Query("host"), c.Query("passed") == "true", (page-1)*20, 20)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
func (h *AccountHandler) AstraGatewayTest(c *gin.Context) {
	var req struct {
		TestKind  string `json:"test_kind"`
		Action    string `json:"action"`
		AccountID int64  `json:"account_id"`
	}
	if c.ShouldBindJSON(&req) != nil {
		response.BadRequest(c, "invalid_request")
		return
	}
	subject, _ := middleware.GetAuthSubjectFromContext(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 300*time.Second)
	defer cancel()
	result, err := h.accountTestService.TestAstraGateway(ctx, req.Action, req.AccountID, subject.UserID, req.TestKind)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, result)
}
