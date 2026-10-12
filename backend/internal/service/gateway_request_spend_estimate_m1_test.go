//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 审计 M1：客户端可控、结算会计入的因子必须进入预检上界。
func TestRequestSpendClientControlledFactors(t *testing.T) {
	maxOut, ok := ExtractRequestMaxOutputTokens([]byte(`{"generationConfig":{"maxOutputTokens":65536}}`))
	require.True(t, ok)
	require.Equal(t, 65536, maxOut, "gemini maxOutputTokens must be honored")

	require.Equal(t, 8, requestOutputChoices([]byte(`{"n":8}`)))
	require.Equal(t, 4, requestOutputChoices([]byte(`{"generationConfig":{"candidateCount":4}}`)))
	require.Equal(t, 1, requestOutputChoices([]byte(`{}`)))
	require.Equal(t, 128, requestOutputChoices([]byte(`{"n":100000}`)), "clamped against overflow")

	require.Equal(t, "priority", requestDeclaredServiceTier([]byte(`{"service_tier":"priority"}`)))
	require.Equal(t, "high", requestDeclaredReasoningEffort([]byte(`{"reasoning":{"effort":"high"}}`)))
	require.Equal(t, "max", requestDeclaredReasoningEffort([]byte(`{"output_config":{"effort":"max"}}`)))
}

// 审计 M3：瞬时结算错误重试，业务性拒绝不重试；失败时预留保留到 TTL。
func TestUsageBillingApplyRetryableAndSlotSettle(t *testing.T) {
	require.False(t, usageBillingApplyRetryable(nil))
	require.False(t, usageBillingApplyRetryable(ErrInsufficientBalance))
	require.False(t, usageBillingApplyRetryable(ErrUsageBillingRequestConflict))
	require.True(t, usageBillingApplyRetryable(errTransientForTest))

	cache := &reservationCacheStub{}
	cache.balance = 10
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var held BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(t.Context(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&held, 1)...))
	held.Settle(t.Context(), errTransientForTest)
	require.InDelta(t, 1.0, cache.reservedAmount(), 1e-9, "uncharged settlement keeps the reservation until TTL")
	held.Release(t.Context())
	require.InDelta(t, 1.0, cache.reservedAmount(), 1e-9, "the deferred backstop release is a no-op after Settle")

	var ok BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(t.Context(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&ok, 1)...))
	ok.Settle(t.Context(), ErrInsufficientBalance)
	require.InDelta(t, 1.0, cache.reservedAmount(), 1e-9, "business rejection releases immediately")
}

var errTransientForTest = errTransient("db timeout")

type errTransient string

func (e errTransient) Error() string { return string(e) }

// 视频异步结算：创建请求 Detach 预留交给待结算记录，轮询请求 Adopt 后结算归还。
func TestBillingReservationSlot_DetachAndAdopt(t *testing.T) {
	cache := &reservationCacheStub{}
	cache.balance = 10
	svc := newReservationTestService(cache)
	t.Cleanup(svc.Stop)

	var create BillingReservationSlot
	require.NoError(t, svc.CheckBillingEligibility(t.Context(), &User{ID: 1}, nil, nil, nil, "", reservationEligibilityOpts(&create, 2)...))
	receipts := create.Detach(t.Context())
	require.Len(t, receipts, 1)
	create.ReleaseOnExit(t.Context())
	require.InDelta(t, 2.0, cache.reservedAmount(), 1e-9, "create handler exit must not release a detached reservation")

	var poll BillingReservationSlot
	svc.AdoptBillingReservation(&poll, receipts)
	poll.HandOff()
	poll.ReleaseOnExit(t.Context())
	require.InDelta(t, 2.0, cache.reservedAmount(), 1e-9)
	poll.Settle(t.Context(), nil)
	require.Zero(t, cache.reservedAmount(), "settlement releases the adopted reservation")
}

func TestRequestSpendServerSideFactors(t *testing.T) {
	require.Equal(t, 5, requestHostedWebSearchMaxCalls([]byte(`{"tools":[{"type":"web_search_20250305","max_uses":5}]}`)))
	require.Equal(t, 1, requestHostedWebSearchMaxCalls([]byte(`{"tools":[{"type":"web_search_preview"}]}`)))
	require.Equal(t, 0, requestHostedWebSearchMaxCalls([]byte(`{"tools":[{"type":"function","name":"web_search"}]}`)))
	require.Equal(t, 64, requestHostedWebSearchMaxCalls([]byte(`{"tools":[{"type":"web_search_20250305","max_uses":100000}]}`)))
	require.True(t, requestDeclaresCacheControl([]byte(`{"system":[{"type":"text","text":"x","cache_control":{"type":"ephemeral","ttl":"1h"}}]}`)))
	require.False(t, requestDeclaresCacheControl([]byte(`{"messages":[]}`)))
}
