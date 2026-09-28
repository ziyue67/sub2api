package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync/atomic"
	"time"
)

type openAIRPMReservationKey struct{}

type openAIRPMReservation struct {
	accountID int64
	resetAt   time.Time
	claimed   atomic.Bool
}

// WithOpenAIRPMReservation carries the handler's admission to the first actual
// send. Retries and subsequent WebSocket turns must acquire their own slot.
func WithOpenAIRPMReservation(ctx context.Context, account *Account, state AccountRPMState) context.Context {
	if account == nil || !state.Enabled {
		return ctx
	}
	return context.WithValue(ctx, openAIRPMReservationKey{}, &openAIRPMReservation{
		accountID: account.RPMAccountID(), resetAt: state.ResetAt,
	})
}

func (s *OpenAIGatewayService) acquireOpenAIRPMForSend(ctx context.Context, account *Account) error {
	if account == nil || !account.IsOpenAIOAuth() || account.GetBaseRPM() <= 0 {
		return nil
	}
	if reservation, ok := ctx.Value(openAIRPMReservationKey{}).(*openAIRPMReservation); ok &&
		reservation.accountID == account.RPMAccountID() && time.Now().Before(reservation.resetAt) &&
		reservation.claimed.CompareAndSwap(false, true) {
		return nil
	}
	_, _, err := s.TryAcquireOpenAIOAuthRPM(ctx, account)
	return err
}

func IsOpenAIRPMError(err error) bool {
	return errors.Is(err, ErrOpenAIRPMExhausted) || errors.Is(err, ErrOpenAIRPMUnavailable)
}

const (
	openAIRPMNeutralFactor  = 0.5
	openAIRPMHeadroomWeight = 3.0
)

func openAIRPMEffectiveLoad(ctx context.Context, account *Account, concurrencyLoad int) float64 {
	if rpm, ok := accountRPMStateFromContext(ctx, account); ok && account.IsOpenAIOAuth() {
		return math.Max(float64(concurrencyLoad), 100*rpm.Utilization())
	}
	return float64(concurrencyLoad)
}

var (
	// ErrOpenAIRPMExhausted tells handlers that every eligible OpenAI OAuth
	// account is at its strict per-minute ceiling.
	ErrOpenAIRPMExhausted = errors.New("openai oauth rpm exhausted")
	// ErrOpenAIRPMUnavailable is fail-closed when a configured account cannot
	// reach the shared counter store.
	ErrOpenAIRPMUnavailable = errors.New("openai oauth rpm unavailable")
)

func (s *OpenAIGatewayService) withOpenAIRPMPrefetch(ctx context.Context, accounts []Account) (context.Context, error) {
	prefetched, err := withAccountRPMPrefetch(ctx, s.rpmCache, accounts, PlatformOpenAI)
	if err != nil {
		return ctx, fmt.Errorf("%w: %v", ErrOpenAIRPMUnavailable, err)
	}
	return prefetched, nil
}

// OpenAIRPMSchedulable applies the 80% headroom rule to movable requests. A
// sticky continuation may remain on an account until the hard ceiling.
func (s *OpenAIGatewayService) OpenAIRPMSchedulable(ctx context.Context, account *Account, movable bool) (bool, AccountRPMState, error) {
	if account == nil || !account.IsOpenAIOAuth() {
		return true, AccountRPMState{}, nil
	}
	state, err := readAccountRPMState(ctx, s.rpmCache, account)
	if err != nil {
		return false, state, fmt.Errorf("%w: %v", ErrOpenAIRPMUnavailable, err)
	}
	if !state.Enabled {
		return true, state, nil
	}
	if account.CheckRPMSchedulability(state.Current) == WindowCostNotSchedulable {
		return false, state, nil
	}
	if movable && state.Utilization() >= accountRPMWarningRatio {
		return false, state, nil
	}
	return true, state, nil
}

// TryAcquireOpenAIOAuthRPM reserves one outbound request immediately before
// forwarding. It is safe to call for every failover attempt; failed upstream
// requests remain counted as required by the strict RPM definition.
func (s *OpenAIGatewayService) TryAcquireOpenAIOAuthRPM(ctx context.Context, account *Account) (bool, AccountRPMState, error) {
	if account == nil || !account.IsOpenAIOAuth() || account.GetBaseRPM() <= 0 {
		return true, AccountRPMState{}, nil
	}
	if s == nil || s.rpmCache == nil {
		return false, AccountRPMState{Limit: account.GetBaseRPM(), Enabled: true}, fmt.Errorf("%w: counter cache is not configured", ErrOpenAIRPMUnavailable)
	}
	strict, ok := s.rpmCache.(StrictRPMCache)
	if !ok {
		return false, AccountRPMState{Limit: account.GetBaseRPM(), Enabled: true}, fmt.Errorf("%w: counter cache does not support atomic acquire", ErrOpenAIRPMUnavailable)
	}
	allowed, count, resetAt, err := strict.TryAcquireRPM(ctx, account.RPMAccountID(), account.GetBaseRPM())
	state := AccountRPMState{Current: count, Limit: account.GetBaseRPM(), ResetAt: resetAt, Enabled: true}
	if err != nil {
		return false, state, fmt.Errorf("%w: %v", ErrOpenAIRPMUnavailable, err)
	}
	if !allowed {
		return false, state, ErrOpenAIRPMExhausted
	}
	return true, state, nil
}
