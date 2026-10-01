package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type excelBPSAttachmentError struct {
	status         int
	retryAfter     string
	upstreamStatus int
	stage, kind    string
	cause          error
}

func (e *excelBPSAttachmentError) Error() string {
	return fmt.Sprintf("excel BPS attachment failed: %s/%s (status %d)", e.stage, e.kind, e.status)
}

func (e *excelBPSAttachmentError) Unwrap() error { return e.cause }

// Retain only allowlisted diagnostics and context sentinels, never a raw URL,
// response body, file ID or credential-bearing transport error.
func excelBPSAttachmentFailure(stage, kind string, upstreamStatus int, cause error) *excelBPSAttachmentError {
	failure := &excelBPSAttachmentError{status: http.StatusBadGateway, stage: stage, kind: kind, upstreamStatus: upstreamStatus}
	if errors.Is(cause, context.Canceled) {
		failure.cause = context.Canceled
	} else if errors.Is(cause, context.DeadlineExceeded) {
		failure.cause = context.DeadlineExceeded
	}
	return failure
}

func (s *OpenAIGatewayService) uploadExcelBPSAttachment(ctx context.Context, account *Account, token, accountID, proxyURL string, img basispoints.InlineAttachment) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	ctx = WithHTTPUpstreamRedirectsDisabled(WithHTTPUpstreamProfile(ctx, HTTPUpstreamProfileExcelBPS))
	reader, contentType, length, err := img.Multipart()
	if err != nil {
		return "", excelBPSAttachmentFailure("attachment_prepare", "multipart", 0, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, basispoints.AttachmentsURL, reader)
	if err != nil {
		return "", excelBPSAttachmentFailure("attachment_prepare", "request_construction", 0, err)
	}
	auth, err := newExcelBPSRequest(ctx, nil, token, accountID)
	if err != nil {
		return "", excelBPSAttachmentFailure("attachment_prepare", "authentication", 0, err)
	}
	req.Header = auth.Header
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Encoding", "identity")
	req.ContentLength = length
	resp, err := s.httpUpstream.Do(req, proxyURL, account.ID, account.Concurrency)
	if err != nil {
		return "", excelBPSAttachmentFailure("attachment_transport", transportdiag.Classify(err), 0, err)
	}
	if resp == nil || resp.Body == nil {
		return "", excelBPSAttachmentFailure("attachment_response", "missing_response", 0, nil)
	}
	// Release the account's upstream connection slot before starting Responses.
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		s.handleExcelBPSUnauthorized(ctx, account, resp.StatusCode, resp.Header, raw)
		status := resp.StatusCode
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		failure := excelBPSAttachmentFailure("attachment_http", "upstream_http", resp.StatusCode, nil)
		failure.status, failure.retryAfter = status, resp.Header.Get("Retry-After")
		return "", failure
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (64<<10)+1))
	if err != nil {
		return "", excelBPSAttachmentFailure("attachment_response", transportdiag.Classify(err), resp.StatusCode, err)
	}
	if len(raw) > 64<<10 {
		return "", excelBPSAttachmentFailure("attachment_response", "response_too_large", resp.StatusCode, nil)
	}
	var result struct {
		OpenAIFileID string `json:"openai_file_id"`
	}
	if json.Unmarshal(raw, &result) != nil {
		return "", excelBPSAttachmentFailure("attachment_response", "invalid_json", resp.StatusCode, nil)
	}
	if !basispoints.ValidAttachmentID(result.OpenAIFileID) {
		return "", excelBPSAttachmentFailure("attachment_response", "invalid_file_id", resp.StatusCode, nil)
	}
	return result.OpenAIFileID, nil
}

func recordExcelBPSAttachmentFailure(ctx context.Context, c *gin.Context, account *Account, err error, failover bool) {
	stage, reason, upstreamStatus := "attachment_upload", "unknown", 0
	var failure *excelBPSAttachmentError
	if errors.As(err, &failure) {
		stage, reason, upstreamStatus = failure.stage, failure.kind, failure.upstreamStatus
	} else if errors.Is(err, basispoints.ErrAttachmentBusy) {
		stage, reason = "attachment_capacity", "capacity_exhausted"
	}
	detail, _ := json.Marshal(map[string]any{"stage": stage, "error_kind": reason, "upstream_status_code": upstreamStatus, "generation_started": false})
	message := "Excel BPS " + stage + " failed: " + reason
	kind := "request_error"
	if upstreamStatus >= 300 {
		kind = "http_error"
	}
	if failover {
		kind = "failover"
	} else {
		setOpsUpstreamError(c, upstreamStatus, message, string(detail))
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID,
		ProxyID: opsUpstreamProxyID(account), ProxyName: opsUpstreamProxyName(account),
		UpstreamStatusCode: upstreamStatus, UpstreamURL: basispoints.AttachmentsURL,
		Kind: kind, Stage: stage, Scope: "excel_bps", Reason: reason, Message: message, Detail: string(detail),
	})
	logger.FromContext(ctx).Warn("excel_bps.attachment_failed", zap.Int64("account_id", account.ID),
		zap.String("stage", stage), zap.String("error_kind", reason), zap.Int("upstream_status", upstreamStatus), zap.Bool("generation_started", false))
}

func acquireExcelBPSAttachmentProxy(ctx context.Context, c *gin.Context, account *Account, scope string, acquire excelBPSAcquire) (string, excelBPSLease, error) {
	proxy, lease, err := acquire(ctx, scope)
	if err != nil {
		err = &excelBPSAcquisitionFailure{cause: err}
		recordExcelBPSTransportFailureAt(ctx, c, account, basispoints.AttachmentsURL, scope, "", err, "attachment_proxy_acquisition", 1, false)
	}
	return proxy, lease, err
}
