package repository

import (
	"context"
	"database/sql"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// One read snapshot spans hot/archive movement. This method neither claims an
// ID nor changes money, owned holds or task state. User/batch/amount/payload are
// bound by the original fingerprint, recomputed rather than trusting a caller.
func (r *usageBillingRepository) ConfirmBatchImageBillingReceipt(ctx context.Context, cmd *service.BatchImageBalanceHoldCommand) (bool, error) {
	if cmd == nil || cmd.UserID <= 0 || cmd.APIKeyID <= 0 || cmd.BatchID == "" ||
		math.IsNaN(cmd.HoldAmount) || math.IsInf(cmd.HoldAmount, 0) || cmd.HoldAmount < 0 ||
		math.IsNaN(cmd.ActualAmount) || math.IsInf(cmd.ActualAmount, 0) || cmd.ActualAmount < 0 {
		return false, service.ErrBatchImageLegacyMoneyPrecision
	}
	copy := *cmd
	copy.RequestFingerprint = ""
	copy.Normalize()
	if copy.RequestID != service.BatchImageHoldRequestID(copy.BatchID) &&
		copy.RequestID != service.BatchImageCaptureRequestID(copy.BatchID) && copy.RequestID != service.BatchImageReleaseRequestID(copy.BatchID) {
		return false, service.ErrUsageBillingRequestConflict
	}
	var matches sql.NullBool
	err := r.db.QueryRowContext(ctx, `SELECT bool_and(request_fingerprint=$3) FROM (
		SELECT request_fingerprint FROM usage_billing_dedup WHERE request_id=$1 AND api_key_id=$2
		UNION ALL SELECT request_fingerprint FROM usage_billing_dedup_archive WHERE request_id=$1 AND api_key_id=$2
	) receipts`, copy.RequestID, copy.APIKeyID, copy.RequestFingerprint).Scan(&matches)
	if err != nil || !matches.Valid {
		return false, err
	}
	if !matches.Bool {
		return false, service.ErrUsageBillingRequestConflict
	}
	return true, nil
}
