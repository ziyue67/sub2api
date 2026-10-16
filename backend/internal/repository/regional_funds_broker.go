package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/google/uuid"
	"time"
)

func ensureRegionalFunds(ctx context.Context, db *sql.DB, broker *control.Broker, region string, target RegionalFundsTarget, lifetime time.Duration) error {
	if lifetime < 30*time.Second || lifetime > time.Hour {
		return errors.New("invalid funds lease lifetime")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	identity := fmt.Sprintf("funds:%s:%d:%d:%d", region, target.UserID, target.APIKeyID, target.AccountID)
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", identity); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_broker_operations WHERE operation='quota/issue' AND state='pending' AND payload->>'region_id'=$1 AND (payload->>'user_id')::bigint=$2 AND (payload->>'api_key_id')::bigint=$3 AND (payload->>'account_id')::bigint=$4)", region, target.UserID, target.APIKeyID, target.AccountID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return nil
	}
	var exists bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_local_leases l WHERE region_id=$1 AND user_id=$2 AND (state<>'closed' OR NOT EXISTS(SELECT 1 FROM regional_broker_operations b WHERE b.operation_id='close:'||l.lease_id AND b.operation='quota/close' AND b.state='installed')) AND (lease->>'api_key_id')::bigint=$3 AND (lease->>'account_id')::bigint=$4)", region, target.UserID, target.APIKeyID, target.AccountID).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return nil
	}
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	lease := regional.Lease{SchemaVersion: regional.SchemaVersion, LeaseID: uuid.NewString(), RegionID: region, UserID: target.UserID, APIKeyID: target.APIKeyID, AccountID: target.AccountID, BillingMode: target.BillingMode, Granted: target.Amount, IssuedAt: now.UTC().Truncate(time.Microsecond), ExpiresAt: now.Add(lifetime).UTC().Truncate(time.Microsecond)}
	raw, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO regional_broker_operations(operation_id,operation,payload) VALUES($1,'quota/issue',$2)", lease.LeaseID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

// Reconstruct missing close operations after a crash between local closure and
// durable enqueue. A target cannot be reissued until the primary acknowledges
// its exact receipt. Unknown started consumption always keeps its reservation.
func drainRegionalFunds(ctx context.Context, db *sql.DB, broker *control.Broker, local *ledger.Local) error {
	rows, err := db.QueryContext(ctx, "SELECT lease_id FROM regional_local_leases l WHERE region_id=$1 AND ((state='open' AND ((lease->>'expires_at')::timestamptz<=clock_timestamp() OR (granted-spent=0 AND reserved=0))) OR state='draining' OR (state='closed' AND NOT EXISTS(SELECT 1 FROM regional_broker_operations b WHERE b.operation_id='close:'||l.lease_id AND b.state='installed'))) ORDER BY lease_id LIMIT 100", local.Region)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err = local.Drain(ctx, id); err != nil {
			return err
		}
		var unresolved bool
		if err = db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_local_requests r LEFT JOIN regional_usage_outbox o ON o.event_id=r.request_id WHERE r.lease_id=$1 AND (r.state IN ('reserved','started') OR (r.state='completed' AND (o.event_id IS NULL OR NOT o.acknowledged))))", id).Scan(&unresolved); err != nil {
			return err
		}
		if unresolved {
			continue
		}
		receipt, err := local.Close(ctx, id)
		if err != nil {
			return err
		}
		if err = broker.Queue(ctx, "close:"+id, "quota/close", receipt); err != nil {
			return err
		}
	}
	return nil
}
