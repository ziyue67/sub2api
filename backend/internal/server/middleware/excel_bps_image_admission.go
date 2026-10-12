package middleware

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ExcelBPSImageAdmission must be shared across the gateway route aliases. It
// only marks client requests: the image relay budget is reserved after account
// selection, when a request is actually forwarded through Excel BPS, so
// requests served by any other account keep the regular gateway limits while
// image support is enabled. The body is not read here.
func ExcelBPSImageAdmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		if bpsImageAdmissionRoute(c) {
			service.MarkExcelBPSImageAdmission(c)
		}
		c.Next()
	}
}

func bpsImageAdmissionRoute(c *gin.Context) bool {
	if c.Request.Method != http.MethodPost {
		return false
	}
	switch c.FullPath() {
	case "/responses", "/responses/*subpath", "/v1/responses", "/v1/responses/*subpath",
		"/backend-api/codex/responses", "/backend-api/codex/responses/*subpath",
		"/chat/completions", "/v1/chat/completions", "/v1/messages":
		return true
	}
	return false
}
