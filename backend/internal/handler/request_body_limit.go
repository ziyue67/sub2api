package handler

import (
	"errors"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/config"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// extractMaxBytesError distinguishes the per-request size cap from other read
// failures so callers can answer 413 instead of a generic 400.
func extractMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return maxErr, true
	}
	return nil, false
}

// handleRequestBodyReadError handles cancellation and temporary capacity errors
// before callers classify malformed or oversized request bodies. HTTP/1 read
// deadlines can also cancel the context; preserve their timeout response (408).
func handleRequestBodyReadError(c *gin.Context, reqLog *zap.Logger, err error) bool {
	if err != nil && !pkghttputil.IsRequestBodyReadTimeout(err) && failoverClientGone(c) {
		logRequestBodyReadFailure(reqLog, pkghttputil.DescribeRequestBodyReadFailure(c.Request, err))
		c.Abort()
		return true
	}
	return middleware2.RejectRequestBodyMemoryOverload(c, err)
}

func readLenientJSONRequestBodyWithPrealloc(req *http.Request, cfg *config.Config) ([]byte, error) {
	return pkghttputil.ReadLenientJSONRequestBodyWithPrealloc(req, gatewayMaxBodySize(cfg))
}

func gatewayMaxBodySize(cfg *config.Config) int64 {
	if cfg == nil {
		return 0
	}
	return cfg.Gateway.MaxBodySize
}

func buildBodyTooLargeMessage(limit int64) string { return pkghttputil.BodyTooLargeMessage(limit) }
