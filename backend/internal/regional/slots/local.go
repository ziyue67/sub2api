package slots

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type Local struct {
	DB     *sql.DB
	Region string
}

func localGrant(ctx context.Context, tx *sql.Tx, id, region string) (Grant, int, string, error) {
	var raw []byte
	var g Grant
	var used int
	var state string
	err := tx.QueryRowContext(ctx, `SELECT grant_payload,used,state FROM regional_local_slot_grants WHERE lease_id=$1 AND region_id=$2 FOR UPDATE`, id, region).Scan(&raw, &used, &state)
	if err != nil {
		return g, 0, "", err
	}
	if err := json.Unmarshal(raw, &g); err != nil {
		return g, 0, "", err
	}
	return g, used, state, g.Validate()
}

// Install only accepts tokens delivered by the authenticated broker. Renewal
// retains the same capacity/identity and advances revision without duplicating
// the local budget, including when a previous token is already expired.
func (l *Local) Install(ctx context.Context, g Grant) error {
	if err := g.Validate(); err != nil {
		return err
	}
	if g.Region != l.Region {
		return ErrConflict
	}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regional_local_slot_grants(lease_id,grant_payload,region_id,kind,resource_id,slots,state) VALUES($1,$2,$3,$4,$5,$6,'open') ON CONFLICT DO NOTHING`, g.LeaseID, string(raw), g.Region, g.Resource.Kind, g.Resource.ID, g.Slots); err != nil {
		return err
	}
	old, _, state, err := localGrant(ctx, tx, g.LeaseID, l.Region)
	if err != nil {
		return err
	}
	if !sameIdentity(old, g) || g.Revision < old.Revision {
		return ErrConflict
	}
	if g.Revision == old.Revision {
		if !g.ExpiresAt.Equal(old.ExpiresAt) {
			return ErrConflict
		}
		return tx.Commit()
	}
	if state != "open" || !g.ExpiresAt.After(old.ExpiresAt) {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_slot_grants SET grant_payload=$2 WHERE lease_id=$1`, g.LeaseID, string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Local) Acquire(ctx context.Context, id, request string) (bool, error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := l.AcquireTx(ctx, tx, id, request)
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}
func (l *Local) AcquireTx(ctx context.Context, tx *sql.Tx, id, request string) (bool, error) {
	var resource Resource
	if err := tx.QueryRowContext(ctx, "SELECT kind,resource_id FROM regional_local_slot_grants WHERE lease_id=$1 AND region_id=$2", id, l.Region).Scan(&resource.Kind, &resource.ID); err != nil {
		return false, err
	}
	if err := lockBudget(ctx, tx, resource); err != nil {
		return false, err
	}
	var cancelled bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_slot_cancellations WHERE region_id=$1 AND kind=$2 AND resource_id=$3 AND request_id=$4)", l.Region, resource.Kind, resource.ID, request).Scan(&cancelled); err != nil {
		return false, err
	}
	if cancelled {
		return false, nil
	}
	var prior string
	err := tx.QueryRowContext(ctx, "SELECT lease_id FROM regional_local_slots WHERE kind=$1 AND resource_id=$2 AND request_id=$3", resource.Kind, resource.ID, request).Scan(&prior)
	if err == nil && prior != id {
		return false, ErrConflict
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}

	if !validRequest(request) {
		return false, errors.New("invalid slot request id")
	}
	g, used, state, err := localGrant(ctx, tx, id, l.Region)
	if err != nil {
		return false, err
	}
	n, err := now(ctx, tx)
	if err != nil {
		return false, err
	}
	if state != "open" || n.Before(g.IssuedAt) || !n.Before(g.ExpiresAt) {
		return false, ErrUnavailable
	}
	var released bool
	err = tx.QueryRowContext(ctx, `SELECT released FROM regional_local_slots WHERE lease_id=$1 AND request_id=$2`, id, request).Scan(&released)
	if err == nil {
		return !released, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if used >= g.Slots {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_slot_grants SET used=used+1 WHERE lease_id=$1`, id); err != nil {
		return false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO regional_local_slots(lease_id,request_id,kind,resource_id) VALUES($1,$2,$3,$4)`, id, request, g.Resource.Kind, g.Resource.ID); err != nil {
		return false, err
	}
	return true, nil
}
func (l *Local) Release(ctx context.Context, id, request string) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := l.ReleaseTx(ctx, tx, id, request); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Local) ReleaseTx(ctx context.Context, tx *sql.Tx, id, request string) error {
	if !validRequest(request) {
		return errors.New("invalid slot request id")
	}
	var resource Resource
	if err := tx.QueryRowContext(ctx, "SELECT kind,resource_id FROM regional_local_slot_grants WHERE lease_id=$1 AND region_id=$2", id, l.Region).Scan(&resource.Kind, &resource.ID); err != nil {
		return err
	}
	if err := lockBudget(ctx, tx, resource); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO regional_slot_cancellations(region_id,kind,resource_id,request_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", l.Region, resource.Kind, resource.ID, request); err != nil {
		return err
	}

	if _, _, _, err := localGrant(ctx, tx, id, l.Region); err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE regional_local_slots SET released=true WHERE lease_id=$1 AND request_id=$2 AND NOT released`, id, request)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE regional_local_slot_grants SET used=used-1 WHERE lease_id=$1`, id); err != nil {
			return err
		}
	}
	return nil
}
func (l *Local) Drain(ctx context.Context, id string) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, _, state, err := localGrant(ctx, tx, id, l.Region); err != nil {
		return err
	} else if state == "closed" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_slot_grants SET state='draining' WHERE lease_id=$1`, id); err != nil {
		return err
	}
	return tx.Commit()
}

type DrainReceipt struct {
	LeaseID  string `json:"lease_id"`
	Region   string `json:"region_id"`
	Revision int64  `json:"revision,string"`
}

func (l *Local) Close(ctx context.Context, id string) (DrainReceipt, error) {
	receipt := DrainReceipt{LeaseID: id, Region: l.Region}
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return receipt, err
	}
	defer func() { _ = tx.Rollback() }()
	g, used, state, err := localGrant(ctx, tx, id, l.Region)
	if err != nil {
		return receipt, err
	}
	receipt.Revision = g.Revision
	if state == "open" || used != 0 {
		return receipt, ErrUnavailable
	}
	// Keep permanent request identities and closed lease state. Old snapshots
	// and old process tokens must not resurrect a drained grant.
	if _, err := tx.ExecContext(ctx, `UPDATE regional_local_slot_grants SET state='closed' WHERE lease_id=$1`, id); err != nil {
		return receipt, err
	}
	return receipt, tx.Commit()
}

// AcquireResource uses any currently held grant for the dimension. An existing
// request remains bound to its original grant; it cannot migrate to fresh
// capacity on a retry after expiry or release.
// Resource operations serialize the permanent request identity across grants.
func (l *Local) AcquireResource(ctx context.Context, r Resource, request string) (bool, error) {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	ok, err := l.AcquireResourceTx(ctx, tx, r, request)
	if err != nil {
		return false, err
	}
	return ok, tx.Commit()
}
func (l *Local) AcquireResourceTx(ctx context.Context, tx *sql.Tx, r Resource, request string) (bool, error) {
	if !validRequest(request) {
		return false, errors.New("invalid slot request id")
	}
	if err := lockBudget(ctx, tx, r); err != nil {
		return false, err
	}
	var cancelled bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_slot_cancellations WHERE region_id=$1 AND kind=$2 AND resource_id=$3 AND request_id=$4)", l.Region, r.Kind, r.ID, request).Scan(&cancelled); err != nil {
		return false, err
	}
	if cancelled {
		return false, nil
	}
	var existing string
	err := tx.QueryRowContext(ctx, "SELECT s.lease_id FROM regional_local_slots s JOIN regional_local_slot_grants g ON g.lease_id=s.lease_id WHERE s.kind=$1 AND s.resource_id=$2 AND s.request_id=$3 AND g.region_id=$4", r.Kind, r.ID, request, l.Region).Scan(&existing)
	if err == nil {
		return l.AcquireTx(ctx, tx, existing, request)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT lease_id FROM regional_local_slot_grants WHERE kind=$1 AND resource_id=$2 AND region_id=$3 AND state='open' AND (grant_payload->>'expires_at')::timestamptz>clock_timestamp() ORDER BY lease_id", r.Kind, r.ID, l.Region)
	if err != nil {
		return false, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return false, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return false, err
	}
	for _, id := range ids {
		ok, err := l.AcquireTx(ctx, tx, id, request)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

func (l *Local) ReleaseResource(ctx context.Context, r Resource, request string) error {
	tx, err := l.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := l.ReleaseResourceTx(ctx, tx, r, request); err != nil {
		return err
	}
	return tx.Commit()
}
func (l *Local) ReleaseResourceTx(ctx context.Context, tx *sql.Tx, r Resource, request string) error {
	if !validRequest(request) {
		return errors.New("invalid slot request id")
	}
	if err := lockBudget(ctx, tx, r); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO regional_slot_cancellations(region_id,kind,resource_id,request_id) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING", l.Region, r.Kind, r.ID, request); err != nil {
		return err
	}
	var id string
	err := tx.QueryRowContext(ctx, "SELECT s.lease_id FROM regional_local_slots s JOIN regional_local_slot_grants g ON g.lease_id=s.lease_id WHERE s.kind=$1 AND s.resource_id=$2 AND s.request_id=$3 AND g.region_id=$4", r.Kind, r.ID, request, l.Region).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return l.ReleaseTx(ctx, tx, id, request)
}

func (l *Local) Count(ctx context.Context, r Resource) (int, error) {
	var n int
	err := l.DB.QueryRowContext(ctx, `SELECT COALESCE(SUM(used),0) FROM regional_local_slot_grants WHERE kind=$1 AND resource_id=$2 AND region_id=$3`, r.Kind, r.ID, l.Region).Scan(&n)
	return n, err
}
func (p *Primary) Count(ctx context.Context, r Resource) (int, error) {
	var n int
	err := p.DB.QueryRowContext(ctx, `SELECT primary_used+granted FROM regional_slot_budgets WHERE kind=$1 AND resource_id=$2`, r.Kind, r.ID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return n, err
}

// Held verifies an existing ownership record without creating or moving it.
// Expired/draining grants retain admitted request slots until explicit release.
func (l *Local) Held(ctx context.Context, r Resource, request string) (bool, error) {
	var held bool
	err := l.DB.QueryRowContext(ctx, `SELECT NOT s.released FROM regional_local_slots s JOIN regional_local_slot_grants g ON g.lease_id=s.lease_id WHERE s.kind=$1 AND s.resource_id=$2 AND s.request_id=$3 AND g.region_id=$4`, r.Kind, r.ID, request, l.Region).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return held, err
}
func (p *Primary) Held(ctx context.Context, r Resource, request string) (bool, error) {
	var held bool
	err := p.DB.QueryRowContext(ctx, `SELECT NOT released FROM regional_primary_slots WHERE kind=$1 AND resource_id=$2 AND request_id=$3`, r.Kind, r.ID, request).Scan(&held)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return held, err
}
