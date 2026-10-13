package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGeminiCacheQuotePreservesAccountMultiplier(t *testing.T) {
	s := &GatewayService{billingService: NewBillingService(nil, nil)}
	accountRate := 3.0
	input := &RecordUsageInput{APIKey: &APIKey{User: &User{ID: 1}}, Account: &Account{GroupRateMultiplier: &accountRate}, Result: &ForwardResult{Model: "gemini-3.6-flash", CacheStorageTokenHours: 1000}}
	withAccount := s.QuoteUsageCost(t.Context(), input)
	accountRate = 1
	base := s.QuoteUsageCost(t.Context(), input)
	require.Positive(t, base)
	require.InDelta(t, base*3, withAccount, 1e-12)
}
