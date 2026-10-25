package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const reloginBudgetBatch = 100

func lockReloginBudget(ctx context.Context, tx *sql.Tx, id int64, owner string) (*service.AccountTokenGuardV2Record, error) {
	query := accountTokenGuardV2Select + ` WHERE account_id=$1`
	args := []any{id}
	if owner != "" {
		query += ` AND lease_owner=$2 AND lease_until>NOW()`
		args = append(args, owner)
	}
	record, err := scanAccountTokenGuardV2(tx.QueryRowContext(ctx, query+` FOR UPDATE`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, infraerrors.Conflict("TOKEN_GUARD_V2_BUDGET_BUSY", "Re-login budget or inspection lease changed")
	}
	return record, err
}

func reconcileReloginBudgetTx(ctx context.Context, tx *sql.Tx, record *service.AccountTokenGuardV2Record, rules service.AccountTokenGuardV2Rules, probeAt time.Time, probeState string) (*service.AccountTokenGuardV2Record, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT task.id, task.status, task.finished_at
        FROM openai_oauth_reauth_tasks task
        WHERE task.account_id=$1 AND task.id >= $2 AND task.oauth_profile='codex'
          AND NOT EXISTS (SELECT 1 FROM openai_totp_rotations rotation WHERE rotation.task_id=task.id)
        ORDER BY task.id LIMIT $3 FOR UPDATE`, record.AccountID, record.LastCountedTaskID, reloginBudgetBatch+1)
	if err != nil {
		return nil, false, err
	}
	var outcomes []service.AccountReloginOutcome
	for rows.Next() {
		var outcome service.AccountReloginOutcome
		var finished sql.NullTime
		if err := rows.Scan(&outcome.ID, &outcome.Status, &finished); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		if finished.Valid {
			outcome.FinishedAt = &finished.Time
		}
		outcomes = append(outcomes, outcome)
	}
	readErr := rows.Err()
	_ = rows.Close()
	if readErr != nil {
		return nil, false, readErr
	}
	// One lookahead distinguishes exactly 100 rows from a real next page. The
	// inclusive watermark row is harmless: the transition never recounts it.
	backlog := len(outcomes) > reloginBudgetBatch
	if backlog {
		outcomes = outcomes[:reloginBudgetBatch]
	}
	updated, pending := service.AdvanceAccountReloginBudgetPage(*record, outcomes, rules, probeAt, probeState, !backlog)
	_, err = tx.ExecContext(ctx, `UPDATE account_token_guard_v2_accounts
        SET relogin_fail_streak=$2,last_counted_task_id=$3,auto_relogin_exhausted=$4,cooldown_until=$5,updated_at=NOW()
        WHERE account_id=$1`, record.AccountID, updated.ReloginFailStreak, updated.LastCountedTaskID, updated.AutoReloginExhausted, updated.CooldownUntil)
	return &updated, pending || backlog, err
}

func (r *accountTokenGuardV2Repository) ReconcileReloginBudget(ctx context.Context, id int64, owner string, rules service.AccountTokenGuardV2Rules, probeAt time.Time, probeState string) (*service.AccountTokenGuardV2Record, bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lockReloginBudget(ctx, tx, id, owner)
	if err != nil {
		return nil, false, err
	}
	record, pending, err := reconcileReloginBudgetTx(ctx, tx, record, rules, probeAt, probeState)
	if err != nil {
		return nil, false, err
	}
	return record, pending, tx.Commit()
}

// Accounting and automatic insertion share row locks. A concurrently finishing
// worker is either counted here, or remains active and prevents another task.
func (r *accountTokenGuardV2Repository) CreateAutomaticRelogin(ctx context.Context, id int64, hash, owner string, rules service.AccountTokenGuardV2Rules, now time.Time) (*service.OpenAIOAuthReauthTaskRecord, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lockReloginBudget(ctx, tx, id, owner)
	if err != nil {
		return nil, err
	}
	record, pending, err := reconcileReloginBudgetTx(ctx, tx, record, rules, now, service.AccountTokenGuardV2ProbePending)
	if err != nil {
		return nil, err
	}
	if !record.Enabled || !record.AutoReloginEnabled || record.AutoReloginExhausted || pending ||
		(record.CooldownUntil != nil && record.CooldownUntil.After(now)) {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, infraerrors.Conflict("TOKEN_GUARD_V2_RELOGIN_GATED", "Automatic re-login is gated by its durable budget, cooldown or active task")
	}
	task, err := scanOpenAIOAuthReauthTask(tx.QueryRowContext(ctx, `
        INSERT INTO openai_oauth_reauth_tasks(account_id,status,stage,expected_credentials_hash)
        VALUES($1,'queued','queued',$2)
        RETURNING id,account_id,status,stage,COALESCE(worker_id,''),COALESCE(auth_session_id,''),
          expected_credentials_hash,COALESCE(error_message,''),attempt,created_at,updated_at,finished_at,oauth_profile`, id, hash))
	if err != nil {
		var pg *pq.Error
		if errors.As(err, &pg) && pg.Code == "23505" {
			return nil, infraerrors.Conflict("OPENAI_REAUTH_TASK_ALREADY_ACTIVE", "A re-login task is already active")
		}
		return nil, err
	}
	until := now.Add(time.Duration(rules.ReloginCooldownSeconds) * time.Second)
	_, err = tx.ExecContext(ctx, `UPDATE account_token_guard_v2_accounts
        SET last_reauth_at=$2,cooldown_until=GREATEST(cooldown_until,$3::timestamptz),updated_at=NOW() WHERE account_id=$1`, id, now, until)
	if err != nil {
		return nil, err
	}
	return task, tx.Commit()
}

func (r *accountTokenGuardV2Repository) ResumeReloginBudget(ctx context.Context, id int64, rules service.AccountTokenGuardV2Rules, now time.Time) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	record, err := lockReloginBudget(ctx, tx, id, "")
	if err != nil {
		return err
	}
	var active bool
	err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM openai_oauth_reauth_tasks WHERE account_id=$1 AND status IN ('queued','running','callback_processing'))`, id).Scan(&active)
	if err != nil {
		return err
	}
	if active || (record.LeaseUntil != nil && record.LeaseUntil.After(now)) {
		return infraerrors.Conflict("TOKEN_GUARD_V2_BUDGET_BUSY", "Wait for the active inspection or re-login task before restoring its budget")
	}
	until := now.Add(time.Duration(rules.ReloginCooldownSeconds) * time.Second)
	_, err = tx.ExecContext(ctx, `UPDATE account_token_guard_v2_accounts AS guard SET
        relogin_fail_streak=0,auto_relogin_exhausted=FALSE,
        last_counted_task_id=GREATEST(last_counted_task_id,COALESCE((SELECT MAX(task.id) FROM openai_oauth_reauth_tasks task
          WHERE task.account_id=$1 AND task.oauth_profile='codex'
          AND NOT EXISTS(SELECT 1 FROM openai_totp_rotations rotation WHERE rotation.task_id=task.id)),0)),
        cooldown_until=GREATEST(cooldown_until,$2::timestamptz),updated_at=NOW() WHERE account_id=$1`, id, until)
	if err != nil {
		return err
	}
	return tx.Commit()
}
