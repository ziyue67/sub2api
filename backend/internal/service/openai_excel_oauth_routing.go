package service

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/gin-gonic/gin"
)

// Only the primary Excel grant is BPS-only. A Codex account with an independent
// Excel grant still supports native Codex requests.
func (a *Account) IsExcelOAuth() bool {
	return a != nil && a.IsOpenAIOAuthLike() && strings.TrimSpace(a.GetCredential("client_id")) == openai.ExcelClientID
}

var errExcelOAuthRouteUnavailable = errors.New("an enabled Excel BPS route is required for Excel OAuth credentials and the requested model; native Codex is unavailable")

const excelOAuthRouteErrorCode = "excel_oauth_route_unavailable"

func (s *OpenAIGatewayService) validateExcelOAuthRoute(ctx context.Context, account *Account, model string) error {
	if account.IsExcelOAuth() && (!account.IsExcelBPSEnabledForModel(model) || !s.excelBPSGloballyEnabled(ctx)) {
		return errExcelOAuthRouteUnavailable
	}
	return nil
}

func writeExcelOAuthRouteError(c *gin.Context) error {
	c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
		"type": "invalid_request_error", "code": excelOAuthRouteErrorCode,
		"message": errExcelOAuthRouteUnavailable.Error(),
	}})
	return errExcelOAuthRouteUnavailable
}
