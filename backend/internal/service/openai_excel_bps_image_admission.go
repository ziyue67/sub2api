package service

import (
	"net/http"
	"sync"

	"github.com/Wei-Shaw/sub2api/internal/service/basispoints"
	"github.com/gin-gonic/gin"
)

// Excel BPS image support converts request bodies, so a request forwarded
// through BPS holds a share of the image relay budget until its response ends.
// The share is taken only after an account is selected and the request takes
// the BPS route: requests served by any other account keep the regular gateway
// limits, even while image support is enabled.
const (
	// ExcelBPSImageRequestBusyReason marks a BPS forward rejected because the
	// image relay budget is full. Nothing reached the upstream, so another
	// account may still serve the request; the code is shown to the client once
	// failover is exhausted.
	ExcelBPSImageRequestBusyReason = GatewayFailureReason("basispoints_image_request_busy")
	// ExcelBPSImageBodyTooLargeReason marks a body above the image relay
	// ingress limit. Only the BPS route enforces that limit.
	ExcelBPSImageBodyTooLargeReason = GatewayFailureReason("basispoints_image_body_too_large")

	excelBPSImageRequestBusyMessage  = "Image relay request capacity is busy; retry later"
	excelBPSImageBodyTooLargeMessage = "Request body exceeds the image relay ingress limit"

	// Each request reserves at least 1 MiB, multiplied for its processing copies.
	excelBPSImageAdmissionMinBodyBytes   = 1 << 20
	excelBPSImageAdmissionBodyMultiplier = 8

	excelBPSImageAdmissionContextKey = "excel_bps_image_admission"
)

// MarkExcelBPSImageAdmission is called by the gateway routes for client
// requests. Account tests, probes and scheduled checks never pass those routes
// and keep forwarding without a reservation.
func MarkExcelBPSImageAdmission(c *gin.Context) {
	if c != nil {
		c.Set(excelBPSImageAdmissionContextKey, true)
	}
}

// IsExcelBPSImageAdmissionMarked reports whether the gateway routes marked c.
func IsExcelBPSImageAdmissionMarked(c *gin.Context) bool {
	return c != nil && c.GetBool(excelBPSImageAdmissionContextKey)
}

// excelBPSImageAdmission accounts request bodies and their processing copies,
// not RSS. The zero value uses the default limits.
type excelBPSImageAdmission struct {
	mu          sync.Mutex
	bytes       int64
	requests    int
	limitBytes  int64
	maxRequests int
}

// acquire applies the current settings and reserves weight bytes and one
// request slot. Lowering a limit never evicts existing reservations; new
// requests wait until they drain.
func (b *excelBPSImageAdmission) acquire(budgetMiB, maxRequests int, weight int64) (func(), bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.limitBytes, b.maxRequests = int64(budgetMiB)<<20, maxRequests
	limitBytes, maxRequests := b.limits()
	if b.requests >= maxRequests || weight > limitBytes-b.bytes {
		return nil, false
	}
	b.bytes += weight
	b.requests++
	var once sync.Once
	return func() {
		once.Do(func() {
			b.mu.Lock()
			b.bytes -= weight
			b.requests--
			b.mu.Unlock()
		})
	}, true
}

func (b *excelBPSImageAdmission) limits() (int64, int) {
	limitBytes, maxRequests := b.limitBytes, b.maxRequests
	if limitBytes == 0 {
		limitBytes = int64(DefaultExcelBPSImageBudgetMiB) << 20
	}
	if maxRequests == 0 {
		maxRequests = DefaultExcelBPSImageMaxRequests
	}
	// Each small request reserves at least 8 MiB. Scale the effective budget
	// with the request cap so the configured slots remain usable, while
	// preserving a larger explicitly configured budget.
	if minimum := int64(maxRequests) * excelBPSImageAdmissionMinBodyBytes * excelBPSImageAdmissionBodyMultiplier; minimum > limitBytes {
		limitBytes = minimum
	}
	return limitBytes, maxRequests
}

// admitExcelBPSImageRequest reserves image relay capacity for a client request
// that is about to be forwarded through BPS. Rejections happen before any
// upstream work and are returned as failover errors, so the handler can switch
// to another account. The returned release must be called when the forward,
// including any streamed response, has finished.
func (s *OpenAIGatewayService) admitExcelBPSImageRequest(c *gin.Context, settings ExcelBPSImageRelaySettings, bodyBytes int) (func(), error) {
	if !IsExcelBPSImageAdmissionMarked(c) || !settings.Enabled {
		return func() {}, nil
	}
	size := int64(bodyBytes)
	if size > s.excelBPSImageMaxBodyBytes(settings) {
		return nil, newExcelBPSImageAdmissionFailoverError(ExcelBPSImageBodyTooLargeReason)
	}
	if size < excelBPSImageAdmissionMinBodyBytes {
		size = excelBPSImageAdmissionMinBodyBytes
	}
	release, ok := s.excelBPSImageAdmission.acquire(settings.BudgetMiB, settings.MaxRequests, size*excelBPSImageAdmissionBodyMultiplier)
	if !ok {
		return nil, newExcelBPSImageAdmissionFailoverError(ExcelBPSImageRequestBusyReason)
	}
	return release, nil
}

func (s *OpenAIGatewayService) excelBPSImageMaxBodyBytes(settings ExcelBPSImageRelaySettings) int64 {
	bodyLimitMiB := settings.BodyLimitMiB
	if bodyLimitMiB == 0 {
		bodyLimitMiB = DefaultExcelBPSImageBodyLimitMiB
	}
	maxBody := int64(bodyLimitMiB) << 20
	if ceiling := int64(basispoints.MaxImageBodyMiB) << 20; maxBody > ceiling {
		maxBody = ceiling
	}
	if s != nil && s.cfg != nil && s.cfg.Gateway.MaxBodySize > 0 && s.cfg.Gateway.MaxBodySize < maxBody {
		maxBody = s.cfg.Gateway.MaxBodySize
	}
	return maxBody
}

// newExcelBPSImageAdmissionFailoverError describes a local, pre-send rejection
// of the BPS route. It neither counts against the account's scheduling health
// nor cools the account.
func newExcelBPSImageAdmissionFailoverError(reason GatewayFailureReason) *UpstreamFailoverError {
	if reason == ExcelBPSImageBodyTooLargeReason {
		return &UpstreamFailoverError{
			StatusCode:        http.StatusRequestEntityTooLarge,
			Stage:             GatewayFailureStageInference,
			Scope:             GatewayFailureScopeRequest,
			Reason:            reason,
			NextAccountAction: NextAccountRetry,
			ClientStatusCode:  http.StatusRequestEntityTooLarge,
			ClientMessage:     excelBPSImageBodyTooLargeMessage,
		}
	}
	return &UpstreamFailoverError{
		StatusCode:        http.StatusServiceUnavailable,
		Stage:             GatewayFailureStageInference,
		Scope:             GatewayFailureScopeRequest,
		Reason:            ExcelBPSImageRequestBusyReason,
		NextAccountAction: NextAccountRetry,
		ClientStatusCode:  http.StatusServiceUnavailable,
		ClientMessage:     excelBPSImageRequestBusyMessage,
		ResponseHeaders:   http.Header{"Retry-After": {"1"}},
	}
}

// IsExcelBPSImageAdmissionRejection reports a local BPS image admission
// rejection, which must reach the client with its fixed code and message.
func (e *UpstreamFailoverError) IsExcelBPSImageAdmissionRejection() bool {
	return e != nil && (e.Reason == ExcelBPSImageRequestBusyReason || e.Reason == ExcelBPSImageBodyTooLargeReason)
}
