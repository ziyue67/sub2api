//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageBillingCanonicalLogKeepsOriginalFingerprintAndAccountCost(t *testing.T) {
	raw := 0.000078125
	cost := &CostBreakdown{ActualCost: raw, TotalCost: 0.0000625}
	p := &postUsageBillingParams{Cost: cost, User: &User{ID: 1}, APIKey: &APIKey{ID: 2},
		Account: &Account{ID: 3, Type: AccountTypeAPIKey}, AccountRateMultiplier: 2}
	usage := &UsageLog{RequestID: "precision", UserID: 1, APIKeyID: 2, AccountID: 3, ActualCost: raw}
	wantFingerprint := buildUsageBillingCommand("precision", usage, p).RequestFingerprint
	repo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: false}}
	_, err := applyUsageBilling(context.Background(), "precision", usage, p, &billingDeps{deferredService: &DeferredService{}}, repo)
	require.NoError(t, err)
	require.Equal(t, 0.00007813, usage.ActualCost)
	require.Equal(t, usage.ActualCost, repo.lastCmd.BalanceCost)
	require.Equal(t, raw, *repo.lastCmd.QuotedActualCost)
	require.Equal(t, wantFingerprint, repo.lastCmd.RequestFingerprint)
	require.Equal(t, raw, cost.ActualCost, "the original pricing breakdown is immutable")
	require.Equal(t, 0.0000625, cost.TotalCost, "account cost keeps its separate basis")
}
