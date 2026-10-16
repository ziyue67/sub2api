// Package slots reserves part of a shared concurrency budget for a region.
// Account ownership does not change. Grants never expire out of the authority
// budget automatically: in-flight requests must drain before capacity returns.
package slots

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"time"
)

var (
	ErrUnavailable     = errors.New("regional slot capacity unavailable")
	ErrExpiredUnissued = errors.New("regional slot issue permanently rejected before allocation")
	ErrConflict        = errors.New("regional slot lease identity conflict")
	identifier         = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)
)

type Resource struct {
	Kind string `json:"kind"`
	ID   int64  `json:"resource_id"`
}

func (r Resource) Validate() error {
	if r.ID <= 0 || (r.Kind != "account" && r.Kind != "user" && r.Kind != "api_key") {
		return errors.New("invalid regional slot resource")
	}
	return nil
}

type Grant struct {
	LeaseID   string    `json:"lease_id"`
	Region    string    `json:"region_id"`
	Resource  Resource  `json:"resource"`
	Slots     int       `json:"slots"`
	Revision  int64     `json:"revision,string"`
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (g Grant) Validate() error {
	if err := g.Resource.Validate(); err != nil {
		return err
	}
	if !identifier.MatchString(g.LeaseID) || !identifier.MatchString(g.Region) || g.Slots <= 0 || g.Slots > math.MaxInt32 || g.Revision <= 0 || g.IssuedAt.IsZero() || !g.ExpiresAt.After(g.IssuedAt) || !g.IssuedAt.Equal(g.IssuedAt.Truncate(time.Microsecond)) || !g.ExpiresAt.Equal(g.ExpiresAt.Truncate(time.Microsecond)) {
		return errors.New("invalid regional slot grant")
	}
	return nil
}
func sameIdentity(a, b Grant) bool {
	return a.LeaseID == b.LeaseID && a.Region == b.Region && a.Resource == b.Resource && a.Slots == b.Slots && a.IssuedAt.Equal(b.IssuedAt)
}
func validRequest(id string) bool { return len(id) > 0 && len(id) <= 128 }
func lockBudget(ctx context.Context, tx *sql.Tx, r Resource) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regional_slot_budgets(kind,resource_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, r.Kind, r.ID); err != nil {
		return err
	}
	var id int64
	return tx.QueryRowContext(ctx, `SELECT resource_id FROM regional_slot_budgets WHERE kind=$1 AND resource_id=$2 FOR UPDATE`, r.Kind, r.ID).Scan(&id)
}
func now(ctx context.Context, tx *sql.Tx) (time.Time, error) {
	var n time.Time
	err := tx.QueryRowContext(ctx, `SELECT clock_timestamp()`).Scan(&n)
	return n, err
}

// LimitReader reads the CURRENT authority row inside the budget transaction.
// Missing/disabled resources must return an error. A limit <=0 denies capacity.
// An unlimited dimension need not obtain regional grants at all.
type LimitReader func(context.Context, *sql.Tx, Resource) (int, error)
type Primary struct {
	DB         *sql.DB
	Limit      LimitReader
	GrantGuard func(context.Context, *sql.Tx) error
}

func (p *Primary) limit(ctx context.Context, tx *sql.Tx, r Resource) (int, error) {
	if p.Limit == nil {
		return 0, errors.New("slot authority limit reader required")
	}
	return p.Limit(ctx, tx, r)
}
func authorityGrant(ctx context.Context, tx *sql.Tx, id string) (Grant, string, error) {
	var g Grant
	var state string
	err := tx.QueryRowContext(ctx, `SELECT lease_id,kind,resource_id,region_id,slots,revision,issued_at,expires_at,state FROM regional_slot_grants WHERE lease_id=$1`, id).Scan(&g.LeaseID, &g.Resource.Kind, &g.Resource.ID, &g.Region, &g.Slots, &g.Revision, &g.IssuedAt, &g.ExpiresAt, &state)
	return g, state, err
}
func (p *Primary) Issue(ctx context.Context, g Grant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if p.GrantGuard != nil {
		if err := p.GrantGuard(ctx, tx); err != nil {
			return err
		}
	}
	if err := lockBudget(ctx, tx, g.Resource); err != nil {
		return err
	}
	old, state, err := authorityGrant(ctx, tx, g.LeaseID)
	if err == nil {
		if !sameIdentity(old, g) || old.Revision != g.Revision || !old.ExpiresAt.Equal(g.ExpiresAt) {
			return ErrConflict
		}
		if state != "open" {
			return ErrUnavailable
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if g.Revision != 1 {
		return ErrConflict
	}
	n, err := now(ctx, tx)
	if err != nil {
		return err
	}
	var rejected bool
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	var previous []byte
	err = tx.QueryRowContext(ctx, "SELECT payload FROM regional_slot_issue_rejections WHERE lease_id=$1", g.LeaseID).Scan(&previous)
	if err == nil {
		var prior Grant
		if json.Unmarshal(previous, &prior) != nil || prior != g {
			return ErrConflict
		}
		rejected = true
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if rejected {
		return ErrExpiredUnissued
	}
	if n.Before(g.IssuedAt) {
		return ErrUnavailable
	}
	if !n.Before(g.ExpiresAt) || n.Sub(g.IssuedAt) > 5*time.Second {
		if _, err = tx.ExecContext(ctx, "INSERT INTO regional_slot_issue_rejections(lease_id,payload) VALUES($1,$2)", g.LeaseID, string(raw)); err != nil {
			return err
		}
		if err = tx.Commit(); err != nil {
			return err
		}
		return ErrExpiredUnissued
	}

	limit, err := p.limit(ctx, tx, g.Resource)
	if err != nil {
		return err
	}
	if limit <= 0 {
		return ErrUnavailable
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_slot_budgets SET granted=granted+$3 WHERE kind=$1 AND resource_id=$2 AND primary_used+granted <= $4::bigint-$3::bigint`, g.Resource.Kind, g.Resource.ID, g.Slots, limit)
	if err != nil {
		return err
	}
	nrows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if nrows != 1 {
		return ErrUnavailable
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO regional_slot_grants(lease_id,kind,resource_id,region_id,slots,revision,issued_at,expires_at,state) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'open')`, g.LeaseID, g.Resource.Kind, g.Resource.ID, g.Region, g.Slots, g.Revision, g.IssuedAt, g.ExpiresAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Acquire is used by primary requests as well as Issue by the background
// broker; they lock the SAME budget row. Do not combine grants with the legacy
// Redis-only primary acquisition, which is unaware of leased-out capacity.
func (p *Primary) Acquire(ctx context.Context, r Resource, id string) (bool, error) {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := p.AcquireTx(ctx, tx, r, id)
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}
func (p *Primary) AcquireTx(ctx context.Context, tx *sql.Tx, r Resource, id string) (bool, error) {
	if !validRequest(id) {
		return false, errors.New("invalid slot request id")
	}
	if err := lockBudget(ctx, tx, r); err != nil {
		return false, err
	}
	var released bool
	err := tx.QueryRowContext(ctx, `SELECT released FROM regional_primary_slots WHERE kind=$1 AND resource_id=$2 AND request_id=$3`, r.Kind, r.ID, id).Scan(&released)
	if err == nil {
		return !released, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	limit, err := p.limit(ctx, tx, r)
	if err != nil {
		return false, err
	}
	if limit <= 0 {
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_slot_budgets SET primary_used=primary_used+1 WHERE kind=$1 AND resource_id=$2 AND primary_used+granted < $3`, r.Kind, r.ID, limit)
	if err != nil {
		return false, err
	}
	nrows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if nrows != 1 {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regional_primary_slots(kind,resource_id,request_id) VALUES($1,$2,$3)`, r.Kind, r.ID, id); err != nil {
		return false, err
	}
	return true, nil
}
func (p *Primary) Release(ctx context.Context, r Resource, id string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := p.ReleaseTx(ctx, tx, r, id); err != nil {
		return err
	}
	return tx.Commit()
}
func (p *Primary) ReleaseTx(ctx context.Context, tx *sql.Tx, r Resource, id string) error {
	if !validRequest(id) {
		return errors.New("invalid slot request id")
	}
	if err := lockBudget(ctx, tx, r); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_primary_slots SET released=true WHERE kind=$1 AND resource_id=$2 AND request_id=$3 AND NOT released`, r.Kind, r.ID, id)
	if err != nil {
		return err
	}
	nrows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if nrows == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE regional_slot_budgets SET primary_used=primary_used-1 WHERE kind=$1 AND resource_id=$2`, r.Kind, r.ID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO regional_primary_slots(kind,resource_id,request_id,released) VALUES($1,$2,$3,true) ON CONFLICT DO NOTHING", r.Kind, r.ID, id); err != nil {
		return err
	}
	return nil
}
func (p *Primary) Renew(ctx context.Context, g Grant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if p.GrantGuard != nil {
		if err := p.GrantGuard(ctx, tx); err != nil {
			return err
		}
	}
	if err := lockBudget(ctx, tx, g.Resource); err != nil {
		return err
	}
	old, state, err := authorityGrant(ctx, tx, g.LeaseID)
	if err != nil {
		return err
	}
	if !sameIdentity(old, g) || state != "open" {
		return ErrConflict
	}
	if old.Revision == g.Revision && old.ExpiresAt.Equal(g.ExpiresAt) {
		return nil
	}
	if old.Revision == math.MaxInt64 || g.Revision != old.Revision+1 || !g.ExpiresAt.After(old.ExpiresAt) {
		return ErrConflict
	}
	n, err := now(ctx, tx)
	if err != nil {
		return err
	}
	if !n.Before(old.ExpiresAt) {
		return ErrUnavailable
	}
	limit, err := p.limit(ctx, tx, g.Resource)
	if err != nil {
		return err
	}
	var allocated int64
	if err := tx.QueryRowContext(ctx, `SELECT primary_used+granted FROM regional_slot_budgets WHERE kind=$1 AND resource_id=$2`, g.Resource.Kind, g.Resource.ID).Scan(&allocated); err != nil {
		return err
	}
	if limit <= 0 || allocated > int64(limit) {
		return ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_slot_grants SET revision=$2,expires_at=$3 WHERE lease_id=$1`, g.LeaseID, g.Revision, g.ExpiresAt); err != nil {
		return err
	}
	return tx.Commit()
}

// BeginDrain must stop renewal on the authority before the region drains.
func (p *Primary) BeginDrain(ctx context.Context, region, id string) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	g, _, err := authorityGrant(ctx, tx, id)
	if err != nil {
		return err
	}
	if g.Region != region {
		return ErrConflict
	}
	if err := lockBudget(ctx, tx, g.Resource); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_slot_grants SET state='draining' WHERE lease_id=$1 AND state='open'`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// Close is called only with an authenticated local drain receipt. Mere expiry
// cannot reclaim regional slots, because streams may still be in flight.
func (p *Primary) Close(ctx context.Context, region string, receipt DrainReceipt) error {
	tx, err := p.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	g, _, err := authorityGrant(ctx, tx, receipt.LeaseID)
	if err != nil {
		return err
	}
	if err := lockBudget(ctx, tx, g.Resource); err != nil {
		return err
	}
	g, state, err := authorityGrant(ctx, tx, receipt.LeaseID)
	if err != nil {
		return err
	}
	if region != g.Region || receipt.Region != region || receipt.Revision != g.Revision {
		return ErrConflict
	}
	if state == "closed" {
		return nil
	}
	if state != "draining" {
		return ErrUnavailable
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_slot_budgets SET granted=granted-$3 WHERE kind=$1 AND resource_id=$2`, g.Resource.Kind, g.Resource.ID, g.Slots); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_slot_grants SET state='closed' WHERE lease_id=$1`, g.LeaseID); err != nil {
		return err
	}
	return tx.Commit()
}
