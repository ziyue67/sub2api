package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Lock order is dedup -> user -> owned hold. All changes commit with the claim.
// A completed hold cannot spend another job's share of pooled frozen_balance.
func applyOwnedBatchImageMoney(ctx context.Context, tx *sql.Tx, cmd *service.BatchImageBalanceHoldCommand, operation string,
	apply func(context.Context, *sql.Tx, *service.BatchImageBalanceHoldCommand) (*service.BatchImageBalanceHoldResult, error),
) (*service.BatchImageBalanceHoldResult, error) {
	wantID := map[string]string{"hold": service.BatchImageHoldRequestID(cmd.BatchID),
		"capture": service.BatchImageCaptureRequestID(cmd.BatchID), "release": service.BatchImageReleaseRequestID(cmd.BatchID)}[operation]
	if cmd.RequestID != wantID || cmd.BatchID == "" {
		return nil, service.ErrUsageBillingRequestConflict
	}
	var user int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR NO KEY UPDATE", cmd.UserID).Scan(&user); err != nil {
		return nil, err
	}
	var owner int64
	var hold float64
	var state string
	err := tx.QueryRowContext(ctx, "SELECT user_id,hold_amount,state FROM batch_image_money_holds WHERE batch_id=$1 AND api_key_id=$2 FOR UPDATE", cmd.BatchID, cmd.APIKeyID).Scan(&owner, &hold, &state)
	if errors.Is(err, sql.ErrNoRows) {
		if operation == "capture" && cmd.HoldAmount == 0 && cmd.ActualAmount == 0 {
			// Free jobs deliberately skip ReserveBatchImageBalance. Record a
			// zero-money terminal receipt without touching pooled frozen funds.
			_, err = tx.ExecContext(ctx, `INSERT INTO batch_image_money_holds(batch_id,api_key_id,user_id,hold_amount,actual_amount,state)
				VALUES($1,$2,$3,0,0,'captured')`, cmd.BatchID, cmd.APIKeyID, cmd.UserID)
			return &service.BatchImageBalanceHoldResult{}, err
		}
		if operation == "release" {
			claimed, err := batchImageHoldClaimExists(ctx, tx, service.BatchImageHoldRequestID(cmd.BatchID), cmd.APIKeyID)
			if err != nil {
				return nil, err
			}
			if !claimed {
				return &service.BatchImageBalanceHoldResult{}, nil
			}
		}
		if operation != "hold" {
			return nil, service.ErrBatchImageLegacyMoneyPrecision
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO batch_image_money_holds(batch_id,api_key_id,user_id,hold_amount,state)
			VALUES($1,$2,$3,$4,'held')`, cmd.BatchID, cmd.APIKeyID, cmd.UserID, cmd.HoldAmount); err != nil {
			return nil, err
		}
		return apply(ctx, tx, cmd)
	}
	if err != nil {
		return nil, err
	}
	if owner != cmd.UserID || hold != cmd.HoldAmount || state != "held" || operation == "hold" {
		return nil, service.ErrUsageBillingRequestConflict
	}
	result, err := apply(ctx, tx, cmd)
	if err != nil {
		return nil, err
	}
	next := "captured"
	if operation == "release" {
		next = "released"
	}
	_, err = tx.ExecContext(ctx, `UPDATE batch_image_money_holds SET state=$3,actual_amount=$4 WHERE batch_id=$1 AND api_key_id=$2`, cmd.BatchID, cmd.APIKeyID, next, cmd.ActualAmount)
	return result, err
}
