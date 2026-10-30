package repository

import (
	"context"
	"encoding/json"
	"math"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *imageArchiveRepository) ImageHistoryBilling(ctx context.Context, userID, keyID int64, requestID string) (service.ImageHistoryBilling, error) {
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
	if event.Version != 1 || event.Command.UserID != userID || event.Command.APIKeyID != keyID || event.Command.RequestID != requestID {
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
