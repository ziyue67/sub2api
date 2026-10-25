package service

import (
	"context"
	"errors"
	"math"
	"strings"
)

const (
	batchImageHoldRequestPrefix    = "batch_image_hold:"
	batchImageCaptureRequestPrefix = "batch_image_capture:"
	batchImageReleaseRequestPrefix = "batch_image_release:"
)

func BatchImageHoldRequestID(batchID string) string {
	return batchImageHoldRequestPrefix + strings.TrimSpace(batchID)
}

func BatchImageCaptureRequestID(batchID string) string {
	return batchImageCaptureRequestPrefix + strings.TrimSpace(batchID)
}

func BatchImageReleaseRequestID(batchID string) string {
	return batchImageReleaseRequestPrefix + strings.TrimSpace(batchID)
}

func buildBatchImageHoldCommand(job *BatchImageJob, requestID string, actualAmount float64, payloadHash string) (*BatchImageBalanceHoldCommand, error) {
	if job == nil {
		return nil, ErrBatchImageBillingHoldFailed
	}
	if job.APIKeyID == nil || *job.APIKeyID <= 0 {
		return nil, ErrBatchImageSettlementMissingAPIKeyID
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	// Preserve the original amounts for receipt verification. A legacy operation
	// may already have committed before the task's status write failed.
	if holdAmount < 0 {
		holdAmount = 0
	}
	if actualAmount < 0 {
		actualAmount = 0
	}
	return &BatchImageBalanceHoldCommand{
		RequestID:          requestID,
		APIKeyID:           *job.APIKeyID,
		UserID:             job.UserID,
		BatchID:            job.BatchID,
		HoldAmount:         holdAmount,
		ActualAmount:       actualAmount,
		MonetaryVersion:    job.PricingSnapshotVersion,
		RequestPayloadHash: strings.TrimSpace(payloadHash),
	}, nil
}

// This is a read-only compatibility check, never permission to execute an
// ambiguous amount. Only the exact committed operation fingerprint can succeed.
func confirmAmbiguousBatchImageRetry(ctx context.Context, repo UsageBillingRepository, cmd *BatchImageBalanceHoldCommand) (bool, error) {
	if math.IsNaN(cmd.HoldAmount) || math.IsInf(cmd.HoldAmount, 0) || math.IsNaN(cmd.ActualAmount) || math.IsInf(cmd.ActualAmount, 0) {
		return true, ErrBatchImageLegacyMoneyPrecision
	}
	if cmd.HoldAmount == QuantizeUsageBillingAmount(cmd.HoldAmount) && cmd.ActualAmount == QuantizeUsageBillingAmount(cmd.ActualAmount) {
		return false, nil
	}
	reader, ok := repo.(interface {
		ConfirmBatchImageBillingReceipt(context.Context, *BatchImageBalanceHoldCommand) (bool, error)
	})
	if !ok {
		return true, ErrBatchImageLegacyMoneyPrecision
	}
	confirmed, err := reader.ConfirmBatchImageBillingReceipt(ctx, cmd)
	if err != nil {
		return true, err
	}
	if !confirmed {
		return true, ErrBatchImageLegacyMoneyPrecision
	}
	return true, nil
}

func reserveBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, payloadHash string) error {
	if repo == nil {
		return ErrBatchImageBillingHoldFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageHoldRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if handled, err := confirmAmbiguousBatchImageRetry(ctx, repo, cmd); handled {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := repo.ReserveBatchImageBalance(ctx, cmd); err != nil {
		if errors.Is(err, ErrBatchImageInsufficientBalance) {
			return ErrBatchImageInsufficientBalance
		}
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	return nil
}

func captureBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, actualAmount float64, payloadHash string) error {
	if repo == nil {
		return ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image billing repository is not configured"))
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageCaptureRequestID(job.BatchID), actualAmount, payloadHash)
	if err != nil {
		return err
	}
	if handled, err := confirmAmbiguousBatchImageRetry(ctx, repo, cmd); handled {
		return err
	}
	if _, err := repo.CaptureBatchImageBalance(ctx, cmd); err != nil {
		return ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	return nil
}

func releaseBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, payloadHash string) error {
	if repo == nil || job == nil {
		return nil
	}
	cmd, err := buildBatchImageHoldCommand(job, BatchImageReleaseRequestID(job.BatchID), 0, payloadHash)
	if err != nil {
		return err
	}
	if handled, err := confirmAmbiguousBatchImageRetry(ctx, repo, cmd); handled {
		return err
	}
	if cmd.HoldAmount <= 0 {
		return nil
	}
	if _, err := repo.ReleaseBatchImageBalance(ctx, cmd); err != nil {
		return ErrBatchImageBillingHoldFailed.WithCause(err)
	}
	return nil
}
