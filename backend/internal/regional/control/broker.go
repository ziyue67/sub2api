package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
)

type BrokerRemote interface {
	IssueSlots(context.Context, slots.Grant) error
	RenewSlots(context.Context, slots.Grant) error
	IssueQuota(context.Context, regional.Lease) error
}

// Broker persists the exact operation before sending. An unconfirmed operation
// is never assigned another ID or a refreshed timestamp, even after restart.
// Multiple workers serialize per-operation, including install and completion.
type Broker struct {
	DB     *sql.DB
	Remote BrokerRemote
	Slots  *slots.Local
	Quota  *ledger.Local
}

func (b *Broker) Queue(ctx context.Context, id, op string, payload any) error {
	if b.DB == nil || id == "" || len(id) > 128 {
		return errors.New("invalid broker identity")
	}
	var raw []byte
	var err error
	switch op {
	case "slots/issue", "slots/renew":
		g, ok := payload.(slots.Grant)
		if !ok {
			return errors.New("broker requires slot grant")
		}
		if err = g.Validate(); err != nil {
			return err
		}
		raw, err = json.Marshal(g)
	case "quota/close":
		r, ok := payload.(ledger.DrainReceipt)
		if !ok || r.LeaseID == "" || r.Region == "" {
			return errors.New("broker requires drain receipt")
		}
		raw, err = json.Marshal(r)
	case "quota/issue":
		q, ok := payload.(regional.Lease)
		if !ok {
			return errors.New("broker requires quota lease")
		}
		if err = q.Validate(); err != nil {
			return err
		}
		raw, err = json.Marshal(q)
	default:
		return errors.New("unsupported broker operation")
	}
	if err != nil {
		return err
	}
	result, err := b.DB.ExecContext(ctx, "INSERT INTO regional_broker_operations(operation_id,operation,payload) VALUES($1,$2,$3) ON CONFLICT(operation_id) DO UPDATE SET operation_id=EXCLUDED.operation_id WHERE regional_broker_operations.operation=EXCLUDED.operation AND regional_broker_operations.payload=EXCLUDED.payload", id, op, string(raw))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return slots.ErrConflict
	}
	return nil
}
func (b *Broker) Replay(ctx context.Context, id string) error {
	if b.Remote == nil || b.DB == nil {
		return errors.New("broker unavailable")
	}
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var op, state string
	var raw []byte
	err = tx.QueryRowContext(ctx, "SELECT operation,payload,state FROM regional_broker_operations WHERE operation_id=$1 FOR UPDATE", id).Scan(&op, &raw, &state)
	if err != nil {
		return err
	}
	if state == "installed" || state == "rejected" {
		return nil
	}
	switch op {
	case "slots/issue", "slots/renew":
		if b.Slots == nil {
			return errors.New("local slots unavailable")
		}
		var g slots.Grant
		if err = json.Unmarshal(raw, &g); err != nil {
			return err
		}
		if op == "slots/issue" {
			err = b.Remote.IssueSlots(ctx, g)
		} else {
			err = b.Remote.RenewSlots(ctx, g)
		}
		if errors.Is(err, slots.ErrExpiredUnissued) && op == "slots/issue" {
			if _, err = tx.ExecContext(ctx, "UPDATE regional_broker_operations SET state='rejected' WHERE operation_id=$1", id); err != nil {
				return err
			}
			return tx.Commit()
		}
		if err != nil {
			return err
		}
		if err = b.Slots.Install(ctx, g); err != nil {
			return err
		}
	case "quota/issue":
		if b.Quota == nil {
			return errors.New("local quota unavailable")
		}
		var q regional.Lease
		if err = json.Unmarshal(raw, &q); err != nil {
			return err
		}
		if err = b.Remote.IssueQuota(ctx, q); errors.Is(err, slots.ErrExpiredUnissued) {
			if _, err = tx.ExecContext(ctx, "UPDATE regional_broker_operations SET state='rejected' WHERE operation_id=$1", id); err != nil {
				return err
			}
			return tx.Commit()
		} else if err != nil {
			return err
		}
		if err = b.Quota.Install(ctx, q); err != nil {
			return err
		}
	case "quota/close":
		remote, ok := b.Remote.(interface {
			CloseQuota(context.Context, ledger.DrainReceipt) error
		})
		if !ok {
			return errors.New("quota closure unavailable")
		}
		var receipt ledger.DrainReceipt
		if err = json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		if err = remote.CloseQuota(ctx, receipt); err != nil {
			return err
		}
	default:
		return errors.New("unknown persisted broker operation")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE regional_broker_operations SET state='installed' WHERE operation_id=$1", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (b *Broker) RunOnce(ctx context.Context) error {
	rows, err := b.DB.QueryContext(ctx, "SELECT operation_id FROM regional_broker_operations WHERE state='pending' AND (($1 AND operation IN ('quota/issue','quota/close')) OR ($2 AND operation IN ('slots/issue','slots/renew'))) ORDER BY created_at,operation_id LIMIT 100", b.Quota != nil, b.Slots != nil)
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
	var failures []error
	for _, id := range ids {
		if err = b.Replay(ctx, id); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// EnsureSlots creates at most one outstanding operation per resource. An old
// unconfirmed issue remains pending for reconciliation; it cannot be replaced
// with a fresh ID and silently leak an authority grant after a lost response.
func (b *Broker) EnsureSlots(ctx context.Context, resource slots.Resource, capacity int, lifetime time.Duration) error {
	if b.Slots == nil || lifetime < 30*time.Second || lifetime > time.Hour || capacity <= 0 {
		return errors.New("invalid slot broker target")
	}
	if err := resource.Validate(); err != nil {
		return err
	}
	tx, err := b.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	identity := fmt.Sprintf("regional-broker:%s:%s:%d", b.Slots.Region, resource.Kind, resource.ID)
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(hashtextextended($1,0))", identity); err != nil {
		return err
	}
	var pending bool
	if err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_broker_operations WHERE state='pending' AND operation IN ('slots/issue','slots/renew') AND payload->>'region_id'=$1 AND payload->'resource'->>'kind'=$2 AND (payload->'resource'->>'resource_id')::bigint=$3)", b.Slots.Region, resource.Kind, resource.ID).Scan(&pending); err != nil {
		return err
	}
	if pending {
		return nil
	}
	var raw []byte
	var g slots.Grant
	var now time.Time
	if err = tx.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&now); err != nil {
		return err
	}
	err = tx.QueryRowContext(ctx, "SELECT grant_payload FROM regional_local_slot_grants WHERE region_id=$1 AND kind=$2 AND resource_id=$3 AND state='open' ORDER BY lease_id LIMIT 1", b.Slots.Region, resource.Kind, resource.ID).Scan(&raw)
	op := "slots/renew"
	if errors.Is(err, sql.ErrNoRows) {
		op = "slots/issue"
		g = slots.Grant{LeaseID: uuid.NewString(), Region: b.Slots.Region, Resource: resource, Slots: capacity, Revision: 1, IssuedAt: now.UTC().Truncate(time.Microsecond), ExpiresAt: now.Add(lifetime).UTC().Truncate(time.Microsecond)}
	} else if err != nil {
		return err
	} else {
		if err = json.Unmarshal(raw, &g); err != nil {
			return err
		}
		if g.Slots != capacity {
			return slots.ErrConflict
		}
		if g.ExpiresAt.Sub(now) > lifetime/2 {
			return nil
		}
		if !now.Before(g.ExpiresAt) {
			return slots.ErrUnavailable
		}
		g.Revision++
		g.ExpiresAt = now.Add(lifetime).UTC().Truncate(time.Microsecond)
	}
	if err = g.Validate(); err != nil {
		return err
	}
	raw, err = json.Marshal(g)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO regional_broker_operations(operation_id,operation,payload) VALUES($1,$2,$3)", fmt.Sprintf("%s-%d", g.LeaseID, g.Revision), op, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
