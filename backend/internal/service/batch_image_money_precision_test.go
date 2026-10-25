//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBatchImageNewSnapshotUsesOneMoneyQuantum(t *testing.T) {
	for _, tc := range []struct {
		unit, rate, want float64
	}{
		{0.05, 1.2345, 0.07619951}, // down
		{0.05, 1.2346, 0.07621186}, // up
	} {
		svc, _, _, _, _ := newTestBatchImagePublicService(true)
		groupID := int64(7)
		svc.GroupRepo = &publicBatchImageGroupRepo{groups: map[int64]*Group{
			groupID: {ID: groupID, Platform: PlatformGemini, RateMultiplier: tc.rate,
				AllowBatchImageGeneration: true, ImagePrice1K: &tc.unit,
				BatchImageDiscountMultiplier: 1, BatchImageHoldMultiplier: 1},
		}}
		account := testBatchImageAccount(101, AccountTypeAPIKey)
		account.RateMultiplier = &tc.rate
		req := validBatchImageSubmitRequest()
		req.ImageSize = "1K"
		req.Items = req.Items[:1]
		price, err := svc.resolvePricingSnapshot(context.Background(), BatchImageOwner{GroupID: &groupID}, req, BatchImageProviderGeminiAPI, &account)
		require.NoError(t, err)
		require.Equal(t, tc.want, price.HoldAmount)
		require.Equal(t, price.HoldAmount, price.EstimatedCost)
	}
}

func TestBatchImageLegacyAmbiguousHoldRequiresReview(t *testing.T) {
	for _, hold := range []float64{0.0761995125, 0.076211858} {
		job := testSettlingBatchImageJob("legacy-precision")
		job.HoldAmount = &hold
		for _, id := range []string{BatchImageHoldRequestID(job.BatchID), BatchImageCaptureRequestID(job.BatchID), BatchImageReleaseRequestID(job.BatchID)} {
			cmd, err := buildBatchImageHoldCommand(job, id, 0, "payload")
			require.NoError(t, err)
			require.Equal(t, hold, cmd.HoldAmount, "retain the exact input for read-only receipt verification")
			handled, err := confirmAmbiguousBatchImageRetry(context.Background(), &fakeBatchImageBillingRepo{}, cmd)
			require.True(t, handled)
			require.ErrorIs(t, err, ErrBatchImageLegacyMoneyPrecision)
		}
	}
	job := testSettlingBatchImageJob("legacy-actual-precision")
	cmd, err := buildBatchImageHoldCommand(job, BatchImageCaptureRequestID(job.BatchID), 0.000078125, "payload")
	require.NoError(t, err)
	handled, err := confirmAmbiguousBatchImageRetry(context.Background(), &fakeBatchImageBillingRepo{}, cmd)
	require.True(t, handled)
	require.ErrorIs(t, err, ErrBatchImageLegacyMoneyPrecision)
}

func TestBatchImageVersionTwoSettlementRoundsCompleteAmountOnce(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("money-v2")
	job.PricingSnapshotVersion = 2
	job.BillableUnitPrice = 0.0761995125
	job.SuccessCount, job.FailCount, job.ItemCount = 1, 0, 1
	hold := 0.07619951
	job.HoldAmount = &hold
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := &BatchImageSettlementService{Repo: repo, BillingRepo: billing, Pricing: &fakeBatchImagePricingResolver{}}
	result, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.Equal(t, hold, result.ActualCost)
	require.Equal(t, hold, billing.captures[0].ActualAmount)
}
