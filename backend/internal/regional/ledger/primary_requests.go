package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// PrimaryRequests coordinates main-site request holds with Primary.Issue by
// immediately debiting users.balance in the same transaction as request identity.
// Authorize must lock and reserve every additional key/subscription/account/
// platform budget. An absent callback fails closed; snapshots are insufficient.
// SettleScope consumes/refunds those budgets in this transaction and must write
// canonical usage. Existing asynchronous balance billing MUST NOT run as well.
type PrimaryRequests struct {
	Funding     func(context.Context, *sql.Tx, int64, int64) (bool, error)
	DB          *sql.DB
	Authorize   func(context.Context, *sql.Tx, Reservation) error
	SettleScope VerifyEvent
	CancelScope func(context.Context, *sql.Tx, Reservation) error
}

func (p *PrimaryRequests) Reserve(ctx context.Context, r Reservation) (bool, error) {
	if p.Authorize == nil || p.SettleScope == nil || p.CancelScope == nil {
		return false, errors.New("atomic budget scope required")
	}
	if r.RequestID == "" || r.UserID <= 0 || r.Maximum <= 0 || !validDigest(r.PayloadSHA256) || r.Model == "" || r.PricingVersion == "" {
		return false, ErrConflict
	}
	amount, err := USD(r.Maximum)
	if err != nil {
		return false, err
	}
	raw, err := json.Marshal(r)
	if err != nil {
		return false, err
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(72422,hashtext($1))", r.RequestID); err != nil {
		return false, err
	}
	var old []byte
	err = tx.QueryRowContext(ctx, "SELECT request FROM regional_primary_requests WHERE request_id=$1", r.RequestID).Scan(&old)
	if err == nil {
		if !sameJSON(raw, old) {
			return false, ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	funded := true
	if p.Funding != nil {
		funded, err = p.Funding(ctx, tx, r.UserID, r.APIKeyID)
		if err != nil {
			return false, err
		}
	}
	if funded {
		result, err := tx.ExecContext(ctx, "UPDATE users SET balance=balance-$2::numeric,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL AND status='active' AND balance >= $2::numeric", r.UserID, amount)
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
	}
	if err = p.Authorize(ctx, tx, r); err != nil {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO regional_primary_requests(request_id,user_id,request,maximum,state,balance_funded) VALUES($1,$2,$3,$4,'reserved',$5)", r.RequestID, r.UserID, string(raw), int64(r.Maximum), funded)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}
func (p *PrimaryRequests) Start(ctx context.Context, id string) error {
	result, err := p.DB.ExecContext(ctx, "UPDATE regional_primary_requests SET state='started' WHERE request_id=$1 AND state='reserved'", id)
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
	return nil
}
func (p *PrimaryRequests) CancelBeforeStart(ctx context.Context, id string) error {
	return p.finish(ctx, id, nil)
}
func (p *PrimaryRequests) Complete(ctx context.Context, e Event) error {
	return p.finish(ctx, e.Usage.EventID, &e)
}
func (p *PrimaryRequests) finish(ctx context.Context, id string, e *Event) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var raw, previous []byte
	var state string
	var funded bool
	if err = tx.QueryRowContext(ctx, "SELECT request,state,event,balance_funded FROM regional_primary_requests WHERE request_id=$1 FOR UPDATE", id).Scan(&raw, &state, &previous, &funded); err != nil {
		return err
	}
	var r Reservation
	if err = json.Unmarshal(raw, &r); err != nil {
		return err
	}
	// Match admission's user -> key/account scope order. The refund below
	// must not request this lock after holding quota rows.
	var lockedUser int64
	if err = tx.QueryRowContext(ctx, "SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE", r.UserID).Scan(&lockedUser); err != nil {
		return err
	}
	refund := r.Maximum
	next := "canceled"
	var event any
	if e == nil {
		if state == "canceled" {
			return nil
		}
		if state != "reserved" {
			return ErrAlreadyDispatched
		}
		if p.CancelScope == nil {
			return errors.New("atomic cancellation scope required")
		}
		if err = p.CancelScope(ctx, tx, r); err != nil {
			return err
		}
	} else {
		encoded, err := json.Marshal(e)
		if err != nil {
			return err
		}
		if state == "completed" {
			if !sameJSON(previous, encoded) {
				return ErrConflict
			}
			return nil
		}
		if state != "started" {
			return ErrAlreadyDispatched
		}
		u := e.Usage
		if u.UserID != r.UserID || u.APIKeyID != r.APIKeyID || u.AccountID != r.AccountID || u.Charge < 0 || u.Charge > r.Maximum || !u.AdmittedAt.Equal(r.AdmittedAt) || u.CompletedAt.Before(u.AdmittedAt) || e.Model != r.Model || e.PricingVersion != r.PricingVersion || e.PayloadSHA256 != r.PayloadSHA256 {
			return ErrConflict
		}
		if p.SettleScope == nil {
			return errors.New("atomic settlement scope required")
		}
		if err = p.SettleScope(ctx, tx, *e); err != nil {
			return err
		}
		refund -= u.Charge
		next = "completed"
		event = string(encoded)
	}
	if !funded {
		refund = 0
	}
	amount, err := USD(refund)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE users SET balance=balance+$2::numeric,updated_at=NOW() WHERE id=$1", r.UserID, amount); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE regional_primary_requests SET state=$2,event=$3 WHERE request_id=$1", id, next, event); err != nil {
		return err
	}
	return tx.Commit()
}
