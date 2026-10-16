package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/regional"
)

// Primary issues grants by debiting spendable balance in the SAME transaction
// as the permanent lease identity. Callers must authenticate region/user and
// scope grants before calling Issue; this library does not expose HTTP routes.
type Primary struct {
	Funding      func(context.Context, *sql.Tx, int64, int64) (bool, error)
	DB           *sql.DB
	ReserveScope func(context.Context, *sql.Tx, regional.Lease) error
	CloseScope   func(context.Context, *sql.Tx, regional.Lease, regional.Amount) error
}

func primaryLease(ctx context.Context, tx *sql.Tx, id string) (regional.Lease, regional.Amount, string, error) {
	l := regional.Lease{SchemaVersion: regional.SchemaVersion}
	var spent regional.Amount
	var state string
	err := tx.QueryRowContext(ctx, `SELECT lease_id,region_id,user_id,granted,spent,issued_at,expires_at,state,api_key_id,account_id,billing_mode FROM regional_quota_leases WHERE lease_id=$1 FOR UPDATE`, id).Scan(&l.LeaseID, &l.RegionID, &l.UserID, &l.Granted, &spent, &l.IssuedAt, &l.ExpiresAt, &state, &l.APIKeyID, &l.AccountID, &l.BillingMode)
	return l, spent, state, err
}
func sameLease(a, b regional.Lease) bool {
	return a.BillingMode == b.BillingMode && a.APIKeyID == b.APIKeyID && a.AccountID == b.AccountID && a.SchemaVersion == b.SchemaVersion && a.LeaseID == b.LeaseID && a.RegionID == b.RegionID && a.UserID == b.UserID && a.Granted == b.Granted && a.IssuedAt.Equal(b.IssuedAt) && a.ExpiresAt.Equal(b.ExpiresAt)
}
func (p *Primary) Issue(ctx context.Context, l regional.Lease) error {
	if err := l.Validate(); err != nil {
		return err
	}
	amount, err := USD(l.Granted)
	if err != nil {
		return err
	}
	// PostgreSQL stores microseconds. Reject loss of identity on a retry rather
	// than silently rounding an immutable grant differently across databases.
	if !l.IssuedAt.Equal(l.IssuedAt.Truncate(time.Microsecond)) || !l.ExpiresAt.Equal(l.ExpiresAt.Truncate(time.Microsecond)) {
		return errors.New("lease timestamps must use microsecond precision")
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Serialize first issuance even before its row exists. Hash collisions only
	// serialize unrelated requests; they cannot grant additional funds.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(72421,hashtext($1))`, l.LeaseID); err != nil {
		return err
	}
	old, _, _, err := primaryLease(ctx, tx, l.LeaseID)
	if err == nil {
		if !sameLease(old, l) {
			return ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var now time.Time
	if err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return err
	}
	if !now.Before(l.ExpiresAt) {
		return ErrExpiredUnissued
	}
	if now.Before(l.IssuedAt) || now.Sub(l.IssuedAt) > 5*time.Second {
		return ErrExhausted
	}
	funded := true
	if p.Funding != nil {
		funded, err = p.Funding(ctx, tx, l.UserID, l.APIKeyID)
		if err != nil {
			return err
		}
	}
	if (l.BillingMode == "subscription") != (!funded) {
		return ErrConflict
	}
	if funded {
		result, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance-$2::numeric,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL AND status='active' AND balance >= $2::numeric`, l.UserID, amount)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return ErrExhausted
		}
	}
	if p.ReserveScope != nil {
		if err = p.ReserveScope(ctx, tx, l); err != nil {
			return err
		}
	} else if l.APIKeyID != 0 || l.AccountID != 0 {
		return errors.New("quota scope required")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO regional_quota_leases(lease_id,region_id,user_id,granted,issued_at,expires_at,api_key_id,account_id,billing_mode) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, l.LeaseID, l.RegionID, l.UserID, int64(l.Granted), l.IssuedAt, l.ExpiresAt, l.APIKeyID, l.AccountID, l.BillingMode)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// VerifyEvent must validate immutable pricing, account assignment and raw usage
// against authority data. It can write the canonical usage log in this SAME
// transaction. Settlement is disabled if no verifier is supplied.
type VerifyEvent func(context.Context, *sql.Tx, Event) error

func (p *Primary) Settle(ctx context.Context, region string, e Event, verify VerifyEvent) (bool, error) {
	if verify == nil {
		return false, errors.New("regional settlement verifier required")
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	lease, _, state, err := primaryLease(ctx, tx, e.Usage.LeaseID)
	if err != nil {
		return false, err
	}
	if region != lease.RegionID {
		return false, ErrConflict
	}
	if err := e.Validate(lease); err != nil {
		return false, err
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return false, err
	}
	var old []byte
	err = tx.QueryRowContext(ctx, `SELECT event FROM regional_settled_events WHERE event_id=$1`, e.Usage.EventID).Scan(&old)
	if err == nil {
		if !sameJSON(old, raw) {
			return false, ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if state != "open" {
		return false, ErrExhausted
	}
	if err := verify(ctx, tx, e); err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_quota_leases SET spent=spent+$2 WHERE lease_id=$1 AND granted-spent >= $2`, lease.LeaseID, int64(e.Usage.Charge))
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
	_, err = tx.ExecContext(ctx, `INSERT INTO regional_settled_events(event_id,lease_id,event) VALUES($1,$2,$3)`, e.Usage.EventID, lease.LeaseID, string(raw))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// DrainReceipt can only be emitted by the local ledger after admission has
// stopped, all requests completed/canceled and all outbox entries acknowledged.
// Transport must authenticate it and verify receipt.Region against the client.
type DrainReceipt struct {
	LeaseID string          `json:"lease_id"`
	Region  string          `json:"region_id"`
	Spent   regional.Amount `json:"spent_nano_usd"`
}

func (p *Primary) Close(ctx context.Context, region string, receipt DrainReceipt) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	lease, spent, state, err := primaryLease(ctx, tx, receipt.LeaseID)
	if err != nil {
		return err
	}
	if lease.RegionID != region || receipt.Region != region || receipt.Spent != spent {
		return ErrConflict
	}
	if state == "closed" {
		return nil
	}
	amount, err := USD(lease.Granted - spent)
	if err != nil {
		return err
	}
	// Do not lose money when a user was soft-deleted while a lease was open.
	if lease.BillingMode != "subscription" {
		result, err := tx.ExecContext(ctx, `UPDATE users SET balance=balance+$2::numeric,updated_at=NOW() WHERE id=$1`, lease.UserID, amount)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("lease user missing")
		}
	}
	if p.CloseScope != nil {
		if err = p.CloseScope(ctx, tx, lease, spent); err != nil {
			return err
		}
	} else if lease.APIKeyID != 0 || lease.AccountID != 0 {
		return errors.New("closure scope required")
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_quota_leases SET state='closed' WHERE lease_id=$1`, lease.LeaseID); err != nil {
		return err
	}
	return tx.Commit()
}
