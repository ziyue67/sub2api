package handler

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Keep local RPM exhaustion distinct from an upstream failure across retries.
type openAIRPMAdmission struct {
	exhausted bool
	resetAt   time.Time
	state     service.AccountRPMState
}

func (a *openAIRPMAdmission) acquire(ctx context.Context, gateway *service.OpenAIGatewayService, account *service.Account, release func(), excluded map[int64]struct{}) (retry bool, err error) {
	allowed, state, err := gateway.TryAcquireOpenAIOAuthRPM(ctx, account)
	if err == nil && allowed {
		a.state = state
		return false, nil
	}
	if release != nil {
		release()
	}
	if err == nil || errors.Is(err, service.ErrOpenAIRPMExhausted) {
		a.exhausted = true
		a.resetAt = state.ResetAt
		excluded[account.ID] = struct{}{}
		return true, nil
	}
	return false, err
}

func (a *openAIRPMAdmission) forwardContext(ctx context.Context, account *service.Account) context.Context {
	return service.WithOpenAIRPMReservation(ctx, account, a.state)
}

func (h *OpenAIGatewayHandler) handleOpenAIRPMForwardError(c *gin.Context, err error, streamStarted, anthropic bool) bool {
	if !isOpenAIRPMError(err) {
		return false
	}
	admission := openAIRPMAdmission{}
	admission.retryAfter(c, err)
	cls := classifySelectionFailureError(err, noAccountErrorClassification{})
	if anthropic {
		h.anthropicStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
	} else {
		h.handleStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
	}
	return true
}

func (a *openAIRPMAdmission) selectionError(err error) error {
	if a.exhausted && errors.Is(err, service.ErrNoAvailableAccounts) {
		return service.ErrOpenAIRPMExhausted
	}
	return err
}

func isOpenAIRPMError(err error) bool {
	return errors.Is(err, service.ErrOpenAIRPMExhausted) || errors.Is(err, service.ErrOpenAIRPMUnavailable)
}

func (a *openAIRPMAdmission) retryAfter(c *gin.Context, err error) {
	if !errors.Is(err, service.ErrOpenAIRPMExhausted) {
		return
	}
	resetAt := a.resetAt
	if resetAt.IsZero() {
		resetAt = time.Now().Truncate(time.Minute).Add(time.Minute)
	}
	seconds := max(1, int(math.Ceil(time.Until(resetAt).Seconds())))
	c.Header("Retry-After", strconv.Itoa(seconds))
}
