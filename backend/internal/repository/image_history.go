package repository

import (
	"context"
	"encoding/json"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *imageArchiveRepository) ImageHistoryBilling(ctx context.Context, userID, keyID int64, requestID string) (service.ImageHistoryBilling, error) {
	return r.imageHistoryBillingReceipt(ctx, userID, keyID, 0, requestID)
}

func (r *imageArchiveRepository) imageHistoryBillingReceipt(ctx context.Context, userID, keyID, accountID int64, requestID string) (service.ImageHistoryBilling, error) {
	unknown := service.ImageHistoryBilling{Status: "unknown"}
	var state string
	var payload []byte
	err := r.db.QueryRowContext(ctx, `SELECT state,payload FROM usage_billing_events
		WHERE request_id=$1 AND api_key_id=$2 AND (payload->'command'->>'UserID')::bigint=$3`, requestID, keyID, userID).Scan(&state, &payload)
	if err != nil {
		return unknown, err
	}
	var event service.UsageBillingEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return unknown, err
	}
	if event.Version != 1 || event.Command.UserID != userID || event.Command.APIKeyID != keyID || event.Command.RequestID != requestID || (accountID > 0 && event.Command.AccountID != accountID) {
		return unknown, nil
	}
	switch state {
	case "pending":
		return service.ImageHistoryBilling{Status: "pending"}, nil
	case "applied", "complete":
		amount := event.Command.BalanceCost + event.Command.SubscriptionCost
		if event.Command.BalanceCost < 0 || event.Command.SubscriptionCost < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
			return unknown, nil
		}
		status := "no_charge"
		if amount > 0 {
			status = "charged"
		}
		return service.ImageHistoryBilling{Status: status, Amount: &amount}, nil
	default:
		return unknown, nil
	}
}

// ImageHistoryLegacyBilling resolves old archives only through a unique usage
// identity and a durable receipt. A missing log is never evidence of no charge.
func (r *imageArchiveRepository) ImageHistoryLegacyBilling(ctx context.Context, userID, keyID, accountID int64, upstreamID string) (service.ImageHistoryBilling, error) {
	unknown := service.ImageHistoryBilling{Status: "unknown"}
	if userID <= 0 || keyID <= 0 || accountID <= 0 || upstreamID == "" {
		return unknown, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT DISTINCT request_id FROM usage_logs
		WHERE user_id=$1 AND api_key_id=$2 AND account_id=$3 AND image_count>0
		AND request_id<>'' AND (upstream_request_id=$4 OR request_id=$4) LIMIT 2`, userID, keyID, accountID, upstreamID)
	if err != nil {
		return unknown, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return unknown, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return unknown, err
	}
	if len(ids) != 1 {
		return unknown, nil
	}
	return r.imageHistoryBillingReceipt(ctx, userID, keyID, accountID, ids[0])
}
