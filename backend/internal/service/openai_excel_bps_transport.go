package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/mihomo"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/Wei-Shaw/sub2api/internal/util/transportdiag"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

var errExcelBPSProxyUnavailable = errors.New("BPS proxy unavailable")

type excelBPSLease interface {
	Release()
	ReportFailure()
	ReportStreamFailure()
	ReportSuccess()
	ReportUpstreamFailure()
}

type excelBPSAcquire func(context.Context, string, ...string) (string, excelBPSLease, error)

func acquireExcelBPSProxy(ctx context.Context, scope string, excluded ...string) (string, excelBPSLease, error) {
	acquire := mihomo.AcquireBPSLease
	if strings.HasPrefix(scope, "transient:") {
		acquire = mihomo.AcquireBPSTransientLease
	}
	lease, err := acquire(ctx, scope, excluded...)
	if err != nil {
		return "", nil, err
	}
	return lease.ProxyURL, lease, nil
}

// Missing trace is not evidence of safety. Standard net/http emits GetConn
// before dialing and GotConn before handing a connection to request writing.
// Keep evidence for the whole attempt, including any internal reconnects.
type excelBPSWriteEvidence struct {
	started      atomic.Bool
	handedToHTTP atomic.Bool
}

func (e *excelBPSWriteEvidence) request(req *http.Request) *http.Request {
	mark := func() { e.handedToHTTP.Store(true) }
	trace := &httptrace.ClientTrace{
		GetConn:              func(string) { e.started.Store(true) },
		GotConn:              func(httptrace.GotConnInfo) { mark() },
		WroteHeaderField:     func(string, []string) { mark() },
		WroteHeaders:         mark,
		WroteRequest:         func(httptrace.WroteRequestInfo) { mark() },
		GotFirstResponseByte: mark,
	}
	req = req.Clone(httptrace.WithClientTrace(req.Context(), trace))
	if req.Body != nil {
		req.Body = &excelBPSTrackedBody{ReadCloser: req.Body, mark: mark}
	}
	if getBody := req.GetBody; getBody != nil {
		req.GetBody = func() (io.ReadCloser, error) {
			body, err := getBody()
			if err != nil {
				return nil, err
			}
			return &excelBPSTrackedBody{ReadCloser: body, mark: mark}, nil
		}
	}
	return req
}

func (e *excelBPSWriteEvidence) unsent() bool { return e.started.Load() && !e.handedToHTTP.Load() }

type excelBPSTrackedBody struct {
	io.ReadCloser
	mark func()
}

func (b *excelBPSTrackedBody) Read(p []byte) (int, error) {
	if len(p) > 0 {
		b.mark()
	}
	return b.ReadCloser.Read(p)
}

// At most one extra model attempt, on another healthy managed exit, and only
// before HTTP could have written anything. The caller owns the returned lease
// through response closure. Static account proxies retain their old behavior.
func (s *OpenAIGatewayService) doExcelBPSRequest(ctx context.Context, c *gin.Context, account *Account, scope string, body []byte, token, accountID string, acquire excelBPSAcquire) (*http.Response, excelBPSLease, string, error) {
	managed := account.IsExcelBPSMihomoEnabled()
	var excluded []string
	proxy := ""
	if account.Proxy != nil {
		proxy = account.Proxy.URL()
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, nil, proxy, err
		}
		var lease excelBPSLease
		if managed {
			var err error
			proxy, lease, err = acquire(ctx, scope, excluded...)
			if err != nil {
				if ctx.Err() != nil {
					return nil, nil, proxy, ctx.Err()
				}
				recordExcelBPSTransportFailure(ctx, c, account, scope, proxy, errExcelBPSProxyUnavailable, "proxy_acquisition", attempt, false)
				return nil, nil, proxy, errExcelBPSProxyUnavailable
			}
		}
		req, err := newExcelBPSRequest(ctx, body, token, accountID)
		if err != nil {
			if lease != nil {
				lease.Release()
			}
			return nil, nil, proxy, err
		}
		c.Set("excel_bps_upstream_attempt", attempt)
		evidence := &excelBPSWriteEvidence{}
		resp, err := s.httpUpstream.Do(evidence.request(req), proxy, account.ID, account.Concurrency)
		if err == nil {
			return resp, lease, proxy, nil
		}
		// Even an unusual response+error result makes replay unsafe.
		retry := managed && attempt == 1 && resp == nil && evidence.unsent() && ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		if lease != nil {
			if ctx.Err() == nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				lease.ReportFailure()
			}
			lease.Release()
		}
		recordExcelBPSTransportFailure(ctx, c, account, scope, proxy, err, "transport", attempt, retry)
		if !retry {
			return nil, nil, proxy, err
		}
		excluded = append(excluded, proxy)
	}
	return nil, nil, proxy, errExcelBPSProxyUnavailable
}

func excelBPSLocalProxyPort(proxy string) int {
	u, err := url.Parse(proxy)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	if port < 19000 || port >= 23096 {
		return 0
	}
	return port
}

func recordExcelBPSTransportFailure(ctx context.Context, c *gin.Context, account *Account, scope, proxy string, err error, stage string, attempt int, retry bool) {
	kind := transportdiag.Classify(err)
	if errors.Is(err, errExcelBPSProxyUnavailable) {
		kind = "proxy_unavailable"
	}
	digest := sha256.Sum256([]byte(scope))
	sessionHash := hex.EncodeToString(digest[:8])
	port := 0
	if account.IsExcelBPSMihomoEnabled() {
		port = excelBPSLocalProxyPort(proxy)
	}
	detail, _ := json.Marshal(map[string]any{
		"error_kind": kind, "error_type": fmt.Sprintf("%T", err),
		"proxy_port": port, "session_hash": sessionHash, "attempt": attempt, "retry_before_send": retry,
	})
	message := "Excel BPS " + stage + " failed: " + kind
	// Keep UI client errors generic; persist only explicitly safe diagnostics.
	if !retry {
		setOpsUpstreamError(c, 0, message, string(detail))
	}
	appendOpsUpstreamError(c, OpsUpstreamErrorEvent{
		Platform: account.Platform, AccountID: account.ID,
		UpstreamURL: basispoints.ResponsesURL, Kind: "request_error", Stage: stage,
		Scope: "excel_bps", Reason: kind, Message: message, Detail: string(detail),
	})
	logger.FromContext(ctx).Warn("excel_bps.transport_failed",
		zap.Int64("account_id", account.ID), zap.String("stage", stage),
		zap.String("error_kind", kind), zap.String("error_type", fmt.Sprintf("%T", err)),
		zap.Int("proxy_port", port), zap.String("session_hash", sessionHash),
		zap.Int("attempt", attempt), zap.Bool("retry_before_send", retry))
}

// Attachment requests own their lease through upload, generation and correction.
// A borrowed lease reports health but cannot release the caller's ownership.
type excelBPSBorrowedLease struct{ excelBPSLease }

func (excelBPSBorrowedLease) Release() {}
func pinnedExcelBPSAcquire(proxy string, lease excelBPSLease) excelBPSAcquire {
	return func(ctx context.Context, _ string, excluded ...string) (string, excelBPSLease, error) {
		if err := ctx.Err(); err != nil {
			return "", nil, err
		}
		// An attachment has already been sent on this exit. Never move this request
		// to another node, even when the later Responses request was not sent.
		if len(excluded) != 0 {
			return "", nil, errExcelBPSProxyUnavailable
		}
		return proxy, excelBPSBorrowedLease{lease}, nil
	}
}
