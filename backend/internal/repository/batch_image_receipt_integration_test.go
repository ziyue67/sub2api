//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type batchSnapshotOnlyPrice struct{}

func (*batchSnapshotOnlyPrice) BatchImageUnitPrice(context.Context, *service.BatchImageJob) (float64, error) {
	panic("submitted snapshot must be used instead of repricing")
}

func TestLegacyBatchReceiptsReadOnlyMatchHotArchiveAndOwnership(t *testing.T) {
	ctx := context.Background()
	db, _ := newBillingAuditDatabase(t)
	repo := &usageBillingRepository{db: db}
	for _, archive := range []bool{false, true} {
		for _, prefix := range []func(string) string{service.BatchImageHoldRequestID, service.BatchImageCaptureRequestID, service.BatchImageReleaseRequestID} {
			batch := uuid.NewString()
			cmd := &service.BatchImageBalanceHoldCommand{BatchID: batch, RequestID: prefix(batch), UserID: 31, APIKeyID: 41,
				HoldAmount: 0.0761995125, RequestPayloadHash: "original"}
			cmd.Normalize()
			confirmed, err := repo.ConfirmBatchImageBillingReceipt(ctx, cmd)
			require.NoError(t, err)
			require.False(t, confirmed)
			table := "usage_billing_dedup"
			if archive {
				table = "usage_billing_dedup_archive"
			}
			_, err = db.ExecContext(ctx, "INSERT INTO "+table+"(request_id,api_key_id,request_fingerprint,created_at) VALUES($1,$2,$3,NOW())", cmd.RequestID, cmd.APIKeyID, cmd.RequestFingerprint)
			require.NoError(t, err)
			confirmed, err = repo.ConfirmBatchImageBillingReceipt(ctx, cmd)
			require.NoError(t, err)
			require.True(t, confirmed)
			wrong := *cmd
			wrong.UserID++
			confirmed, err = repo.ConfirmBatchImageBillingReceipt(ctx, &wrong)
			require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
			require.False(t, confirmed)
			wrong = *cmd
			wrong.RequestPayloadHash = "changed"
			_, err = repo.ConfirmBatchImageBillingReceipt(ctx, &wrong)
			require.ErrorIs(t, err, service.ErrUsageBillingRequestConflict)
			var ownedRows int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM batch_image_money_holds WHERE batch_id=$1", batch).Scan(&ownedRows))
			require.Zero(t, ownedRows, "receipt confirmation must not install historical owned holds")
		}
	}
}

func TestLegacyCaptureCommittedBeforeTaskStatusUpgradeRetryDoesNotMoveMoney(t *testing.T) {
	ctx := context.Background()
	db, client := newBillingAuditDatabase(t)
	user := mustCreateUser(t, client, &service.User{Email: uuid.NewString() + "@example.com", PasswordHash: "hash", Balance: 99.93876543})
	key, account := int64(17), int64(23)
	hold, actual := 0.0761995125, 0.0612345678
	jobs := NewBatchImageRepository(db)
	job, err := jobs.CreateBatchImageJob(ctx, service.CreateBatchImageJobParams{
		BatchID: uuid.NewString(), UserID: user.ID, APIKeyID: &key, AccountID: &account,
		Provider: service.BatchImageProviderGeminiAPI, Model: "legacy-image", Status: service.BatchImageJobStatusSettling,
		ItemCount: 1, SuccessCount: 1, HoldAmount: &hold, EstimatedCost: hold, BillableUnitPrice: actual, PricingSnapshotVersion: 1,
	})
	require.NoError(t, err)
	receipt := &service.BatchImageBalanceHoldCommand{RequestID: service.BatchImageCaptureRequestID(job.BatchID), BatchID: job.BatchID,
		UserID: user.ID, APIKeyID: key, HoldAmount: hold, ActualAmount: actual, RequestPayloadHash: service.BuildBatchImageSettlementManifestHash(job)}
	receipt.Normalize()
	_, err = db.ExecContext(ctx, "INSERT INTO usage_billing_dedup(request_id,api_key_id,request_fingerprint) VALUES($1,$2,$3)", receipt.RequestID, key, receipt.RequestFingerprint)
	require.NoError(t, err)
	s := &service.BatchImageSettlementService{Repo: jobs, BillingRepo: &usageBillingRepository{db: db}, Pricing: &batchSnapshotOnlyPrice{}}
	result, err := s.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.False(t, result.AlreadySettled)
	result, err = s.Settle(ctx, job.BatchID)
	require.NoError(t, err)
	require.True(t, result.AlreadySettled)
	saved, err := jobs.GetBatchImageJobByBatchID(ctx, job.BatchID)
	require.NoError(t, err)
	require.Equal(t, service.BatchImageJobStatusCompleted, saved.Status)
	var balance, frozen string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT balance::text,frozen_balance::text FROM users WHERE id=$1", user.ID).Scan(&balance, &frozen))
	require.Equal(t, "99.93876543", balance)
	require.Equal(t, "0.00000000", frozen)
}
