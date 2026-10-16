package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
)

var (
	ErrExpiredUnissued   = errors.New("quota lease expired and never issued")
	ErrConflict          = errors.New("regional ledger immutable identity conflict")
	ErrExhausted         = errors.New("regional lease unavailable or exhausted")
	ErrAlreadyDispatched = errors.New("regional request already dispatched")
)

// Local is shared by every Pod in the same region. SQL transactions, not a
// per-process mutex, fence reservations and dispatch across replicas/restarts.
type Local struct {
	DB     *sql.DB
	Region string
}

func sameJSON(a, b []byte) bool {
	var left, right any
	ld := json.NewDecoder(bytes.NewReader(a))
	ld.UseNumber()
	rd := json.NewDecoder(bytes.NewReader(b))
	rd.UseNumber()
	if ld.Decode(&left) != nil || rd.Decode(&right) != nil {
		return false
	}
	l, _ := json.Marshal(left)
	r, _ := json.Marshal(right)
	return bytes.Equal(l, r)
}
func (l *Local) Install(ctx context.Context, lease regional.Lease) error {
	if err := lease.Validate(); err != nil {
		return err
	}
	if lease.RegionID != l.Region {
		return ErrConflict
	}
	if _, err := USD(lease.Granted); err != nil {
		return err
	}
	raw, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	// Install only accepts leases fetched through the authenticated authority
	// client; this API is not exposed on a public ingress.
	_, err = l.DB.ExecContext(ctx, `INSERT INTO regional_local_leases(lease_id,lease,region_id,user_id,granted) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, lease.LeaseID, string(raw), lease.RegionID, lease.UserID, int64(lease.Granted))
	if err != nil {
		return err
	}
	var old []byte
	if err := l.DB.QueryRowContext(ctx, `SELECT lease FROM regional_local_leases WHERE lease_id=$1`, lease.LeaseID).Scan(&old); err != nil {
		return err
	}
	if !sameJSON(old, raw) {
		return ErrConflict
	}
	return nil
}
func localLease(ctx context.Context, tx *sql.Tx, id, region string) (regional.Lease, string, error) {
	var raw []byte
	var state string
	var lease regional.Lease
	err := tx.QueryRowContext(ctx, `SELECT lease,state FROM regional_local_leases WHERE lease_id=$1 AND region_id=$2 FOR UPDATE`, id, region).Scan(&raw, &state)
	if errors.Is(err, sql.ErrNoRows) {
		return lease, "", ErrExhausted
	}
	if err != nil {
		return lease, "", err
	}
	if err := json.Unmarshal(raw, &lease); err != nil {
		return lease, "", err
	}
	return lease, state, lease.Validate()
}

// Reserve returns fresh=false on an identical retry; the caller must never
// repeat upstream work for that retry. Failed admission writes nothing.
func (l *Local) Reserve(ctx context.Context, r Reservation) (fresh bool, err error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	lease, state, err := localLease(ctx, tx, r.LeaseID, l.Region)
	if err != nil {
		return false, err
	}
	if err := r.Validate(lease); err != nil {
		return false, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	var old []byte
	err = tx.QueryRowContext(ctx, `SELECT request FROM regional_local_requests WHERE request_id=$1`, r.RequestID).Scan(&old)
	if err == nil {
		if !sameJSON(old, raw) {
			return false, ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// PostgreSQL is the regional clock authority. Never let a stale caller time
	// make an expired lease spendable or backdate admission after a restart.
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return false, err
	}
	if state != "open" || r.RegionID != l.Region || now.Before(lease.IssuedAt) || !now.Before(lease.ExpiresAt) || r.AdmittedAt.After(now) || now.Sub(r.AdmittedAt) > 5*time.Second {
		return false, ErrExhausted
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_local_leases SET reserved=reserved+$2 WHERE lease_id=$1 AND granted-spent-reserved >= $2`, r.LeaseID, int64(r.Maximum))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if n != 1 {
		return false, ErrExhausted
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO regional_local_requests(request_id,lease_id,request,maximum,state) VALUES($1,$2,$3,$4,'reserved')`, r.RequestID, r.LeaseID, string(raw), int64(r.Maximum))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// Start must commit before the first upstream byte. A crash after this commit
// leaves the full maximum occupied; recovery cannot assume no upstream charge.
func (l *Local) Start(ctx context.Context, id string) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var leaseID string
	if err := tx.QueryRowContext(ctx, `SELECT lease_id FROM regional_local_requests WHERE request_id=$1`, id).Scan(&leaseID); err != nil {
		return err
	}
	lease, state, err := localLease(ctx, tx, leaseID, l.Region)
	if err != nil {
		return err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if state != "open" || now.Before(lease.IssuedAt) || !now.Before(lease.ExpiresAt) {
		return ErrExhausted
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_local_requests SET state='started' WHERE request_id=$1 AND state='reserved'`, id)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrAlreadyDispatched
	}
	return tx.Commit()
}

func (l *Local) finish(ctx context.Context, id string, event *Event) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var leaseID string
	if err := tx.QueryRowContext(ctx, `SELECT lease_id FROM regional_local_requests WHERE request_id=$1`, id).Scan(&leaseID); err != nil {
		return err
	}
	lease, _, err := localLease(ctx, tx, leaseID, l.Region)
	if err != nil {
		return err
	}
	var raw []byte
	var state string
	var maximum int64
	if err := tx.QueryRowContext(ctx, `SELECT request,state,maximum FROM regional_local_requests WHERE request_id=$1 FOR UPDATE`, id).Scan(&raw, &state, &maximum); err != nil {
		return err
	}
	var r Reservation
	if err := json.Unmarshal(raw, &r); err != nil {
		return err
	}
	if event == nil {
		if state == "canceled" {
			return nil
		}
		if state != "reserved" {
			return ErrAlreadyDispatched
		}
		if _, err := tx.ExecContext(ctx, `UPDATE regional_local_requests SET state='canceled' WHERE request_id=$1`, id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE regional_local_leases SET reserved=reserved-$2 WHERE lease_id=$1`, leaseID, maximum); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err := event.Validate(lease); err != nil {
		return err
	}
	e := event.Usage
	if e.EventID != r.RequestID || e.APIKeyID != r.APIKeyID || e.AccountID != r.AccountID || !e.AdmittedAt.Equal(r.AdmittedAt) || e.Charge > r.Maximum || event.Model != r.Model || event.PricingVersion != r.PricingVersion || event.PayloadSHA256 != r.PayloadSHA256 {
		return ErrConflict
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if state == "completed" {
		var old []byte
		if err := tx.QueryRowContext(ctx, `SELECT event FROM regional_usage_outbox WHERE event_id=$1`, id).Scan(&old); err != nil {
			return err
		}
		if !sameJSON(old, encoded) {
			return ErrConflict
		}
		return nil
	}
	if state != "started" {
		return ErrAlreadyDispatched
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_leases SET reserved=reserved-$2,spent=spent+$3 WHERE lease_id=$1`, leaseID, maximum, int64(e.Charge)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_requests SET state='completed' WHERE request_id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regional_usage_outbox(event_id,event) VALUES($1,$2)`, id, string(encoded)); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Local) CancelBeforeStart(ctx context.Context, id string) error {
	return l.finish(ctx, id, nil)
}
func (l *Local) Complete(ctx context.Context, event Event) error {
	return l.finish(ctx, event.Usage.EventID, &event)
}
func (l *Local) Pending(ctx context.Context, limit int) ([]Event, error) {
	if limit < 1 || limit > 1000 {
		return nil, errors.New("invalid outbox batch size")
	}
	rows, err := l.DB.QueryContext(ctx, `SELECT o.event FROM regional_usage_outbox o JOIN regional_local_requests r ON r.request_id=o.event_id JOIN regional_local_leases l ON l.lease_id=r.lease_id WHERE NOT o.acknowledged AND l.region_id=$1 ORDER BY o.event_id LIMIT $2`, l.Region, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Event{}
	for rows.Next() {
		var raw []byte
		var e Event
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Acknowledge is only called after authenticated primary settlement confirms
// this EXACT event. Rows remain for permanent local collision detection.
func (l *Local) Acknowledge(ctx context.Context, event Event) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	result, err := l.DB.ExecContext(ctx, `UPDATE regional_usage_outbox o SET acknowledged=true FROM regional_local_requests r,regional_local_leases l WHERE o.event_id=$1 AND o.event=$2::jsonb AND r.request_id=o.event_id AND l.lease_id=r.lease_id AND l.region_id=$3`, event.Usage.EventID, string(raw), l.Region)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

// Drain serializes against Reserve and Start. Started requests retain their
// maximum until completed; expiry or process death never cancels them.
func (l *Local) Drain(ctx context.Context, id string) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, state, err := localLease(ctx, tx, id, l.Region)
	if err != nil {
		return err
	}
	if state == "closed" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_leases SET state='draining' WHERE lease_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Local) Close(ctx context.Context, id string) (DrainReceipt, error) {
	receipt := DrainReceipt{LeaseID: id, Region: l.Region}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = tx.Rollback() }()
	_, state, err := localLease(ctx, tx, id, l.Region)
	if err != nil {
		return receipt, err
	}
	if state == "open" {
		return receipt, errors.New("lease must drain before closure")
	}
	var reserved int64
	if err := tx.QueryRowContext(ctx, `SELECT spent,reserved FROM regional_local_leases WHERE lease_id=$1`, id).Scan(&receipt.Spent, &reserved); err != nil {
		return receipt, err
	}
	var pending bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM regional_local_requests r LEFT JOIN regional_usage_outbox o ON o.event_id=r.request_id WHERE r.lease_id=$1 AND (r.state IN ('reserved','started') OR (r.state='completed' AND (o.event_id IS NULL OR NOT o.acknowledged))))`, id).Scan(&pending); err != nil {
		return receipt, err
	}
	if reserved != 0 || pending {
		return receipt, errors.New("lease has unresolved reservations or outbox")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_leases SET state='closed' WHERE lease_id=$1`, id); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit()
}
