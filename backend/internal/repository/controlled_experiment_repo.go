package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type controlledExperimentRepository struct{ db *sql.DB }

func NewControlledExperimentRepository(db *sql.DB) service.ControlledExperimentRepository {
	return &controlledExperimentRepository{db: db}
}

const controlledRunColumns = `id,name,status,max_calls,reserved_calls,spec,created_at,started_at,finished_at,lease_until,stop_reason`

func scanControlledRun(row interface{ Scan(...any) error }) (*service.ControlledExperiment, error) {
	run := &service.ControlledExperiment{}
	var spec []byte
	if err := row.Scan(&run.ID, &run.Name, &run.Status, &run.MaxCalls, &run.ReservedCalls, &spec, &run.CreatedAt, &run.StartedAt, &run.FinishedAt, &run.LeaseUntil, &run.StopReason); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrExperimentNotFound
		}
		return nil, err
	}
	if err := json.Unmarshal(spec, &run.Spec); err != nil {
		return nil, err
	}
	return run, nil
}

func (r *controlledExperimentRepository) Create(ctx context.Context, run *service.ControlledExperiment) (*service.ControlledExperiment, error) {
	raw, err := json.Marshal(run.Spec)
	if err != nil {
		return nil, err
	}
	return scanControlledRun(r.db.QueryRowContext(ctx, `INSERT INTO controlled_experiments(name,max_calls,spec) VALUES($1,$2,$3) RETURNING `+controlledRunColumns, run.Name, run.MaxCalls, raw))
}

func (r *controlledExperimentRepository) Get(ctx context.Context, id int64) (*service.ControlledExperiment, error) {
	return scanControlledRun(r.db.QueryRowContext(ctx, `SELECT `+controlledRunColumns+` FROM controlled_experiments WHERE id=$1`, id))
}

func (r *controlledExperimentRepository) List(ctx context.Context, before int64, limit int) ([]*service.ControlledExperiment, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,name,status,max_calls,reserved_calls,spec-'tasks',created_at,started_at,finished_at,lease_until,stop_reason FROM controlled_experiments WHERE ($1::bigint=0 OR id<$1) ORDER BY id DESC LIMIT $2`, before, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]*service.ControlledExperiment, 0)
	for rows.Next() {
		run, err := scanControlledRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, run)
	}
	return items, rows.Err()
}

func (r *controlledExperimentRepository) Claim(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE controlled_experiments SET status='running',started_at=NOW(),lease_until=NOW()+INTERVAL '15 minutes' WHERE id=$1 AND status='draft'`, id)
	if err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23505" {
			return false, service.ErrExperimentBusy
		}
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *controlledExperimentRepository) SavePreflight(ctx context.Context, id int64, items []service.ControlledPreflight) error {
	raw, err := json.Marshal(items)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE controlled_experiments SET preflight=$2 WHERE id=$1`, id, raw)
	return err
}

func (r *controlledExperimentRepository) Preflight(ctx context.Context, id int64) ([]service.ControlledPreflight, error) {
	var raw []byte
	if err := r.db.QueryRowContext(ctx, `SELECT preflight FROM controlled_experiments WHERE id=$1`, id).Scan(&raw); err != nil {
		return nil, err
	}
	var items []service.ControlledPreflight
	err := json.Unmarshal(raw, &items)
	return items, err
}

func (r *controlledExperimentRepository) Reserve(ctx context.Context, attempt *service.ControlledAttempt) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	var calls, limit int
	var active bool
	if err = tx.QueryRowContext(ctx, `SELECT status,reserved_calls,max_calls,lease_until>NOW() FROM controlled_experiments WHERE id=$1 FOR UPDATE`, attempt.RunID).Scan(&status, &calls, &limit, &active); err != nil {
		return err
	}
	if status != "running" || !active {
		return service.ErrExperimentStopped
	}
	if calls >= limit {
		return service.ErrExperimentBudget
	}
	attempt.Sequence = calls + 1
	attempt.Status = "reserved"
	if err = tx.QueryRowContext(ctx, `UPDATE controlled_experiments SET reserved_calls=reserved_calls+1,lease_until=NOW()+INTERVAL '15 minutes' WHERE id=$1 RETURNING lease_until-INTERVAL '15 minutes'`, attempt.RunID).Scan(&attempt.StartedAt); err != nil {
		return err
	}
	raw, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO controlled_experiment_attempts(run_id,sequence,status,started_at,data) VALUES($1,$2,'reserved',$3,$4)`, attempt.RunID, attempt.Sequence, attempt.StartedAt, raw); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *controlledExperimentRepository) Resolve(ctx context.Context, attempt *service.ControlledAttempt) error {
	raw, err := json.Marshal(attempt)
	if err != nil {
		return err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE controlled_experiment_attempts SET status=$3,finished_at=NOW(),data=$4 WHERE run_id=$1 AND sequence=$2 AND status='reserved'`, attempt.RunID, attempt.Sequence, attempt.Status, raw)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return service.ErrExperimentStopped
	}
	return err
}

func (r *controlledExperimentRepository) Attempts(ctx context.Context, id int64) ([]*service.ControlledAttempt, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT data,status,started_at,finished_at FROM controlled_experiment_attempts WHERE run_id=$1 ORDER BY sequence`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]*service.ControlledAttempt, 0)
	for rows.Next() {
		var raw []byte
		a := &service.ControlledAttempt{}
		var status string
		if err = rows.Scan(&raw, &status, &a.StartedAt, &a.FinishedAt); err != nil {
			return nil, err
		}
		started, finished := a.StartedAt, a.FinishedAt
		if err = json.Unmarshal(raw, a); err != nil {
			return nil, err
		}
		a.Status, a.StartedAt, a.FinishedAt = status, started, finished
		if status == "unknown" && a.Diagnostic.Code == "" {
			a.Diagnostic.Code = "process_interrupted"
			a.Diagnostic.CostIncomplete = true
		}
		items = append(items, a)
	}
	return items, rows.Err()
}

func (r *controlledExperimentRepository) Finish(ctx context.Context, id int64, status, reason string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE controlled_experiments SET status=$2,stop_reason=$3,finished_at=NOW(),lease_until=NULL WHERE id=$1 AND status IN ('running','stop_requested')`, id, status, reason)
	return err
}

func (r *controlledExperimentRepository) RequestStop(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE controlled_experiments SET status=CASE WHEN status='draft' THEN 'cancelled' ELSE 'stop_requested' END,finished_at=CASE WHEN status='draft' THEN NOW() ELSE NULL END,stop_reason='administrator_stop' WHERE id=$1 AND status IN ('draft','running','stop_requested')`, id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *controlledExperimentRepository) RecoverExpired(ctx context.Context) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `UPDATE controlled_experiments SET status='interrupted',stop_reason='lease_expired_no_replay',finished_at=NOW(),lease_until=NULL WHERE status IN ('running','stop_requested') AND lease_until<NOW()`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE controlled_experiment_attempts SET status='unknown',finished_at=NOW() WHERE status='reserved' AND run_id IN (SELECT id FROM controlled_experiments WHERE status='interrupted')`); err != nil {
		return err
	}
	return tx.Commit()
}
