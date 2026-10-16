package repository

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type regionalSchedulerAuthority struct{ db *sql.DB }

func NewRegionalSchedulerAuthority(db *sql.DB) service.RegionalSchedulerAuthority {
	return &regionalSchedulerAuthority{db: db}
}
func (r *regionalSchedulerAuthority) Restore(ctx context.Context, region string, bootstrap bool) ([]byte, error) {
	if bootstrap {
		if _, err := r.db.ExecContext(ctx, "INSERT INTO regional_scheduler_streams(region_id, revision, packet) VALUES ($1,0,NULL) ON CONFLICT DO NOTHING", region); err != nil {
			return nil, err
		}
	}
	var wire []byte
	err := r.db.QueryRowContext(ctx, "SELECT packet FROM regional_scheduler_streams WHERE region_id=$1", region).Scan(&wire)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("regional publisher state missing; explicit bootstrap required")
	}
	return wire, err
}
func (r *regionalSchedulerAuthority) Publish(ctx context.Context, region string, build func(context.Context, uint64, service.AccountRepository, service.GroupRepository) ([]byte, error)) ([]byte, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Single active publisher per stream, across all processes/hosts. A contender
	// skips rather than queues an obsolete export. Hash collisions only serialize
	// unrelated streams. Row locking below also prevents a stale transaction from
	// advancing a revision after another publisher committed (SQLSTATE 40001).
	var acquired bool
	if err = tx.QueryRowContext(ctx, "SELECT pg_try_advisory_xact_lock(72419, hashtext($1))", region).Scan(&acquired); err != nil {
		return nil, err
	}
	if !acquired {
		return nil, errors.New("regional publisher busy")
	}
	var revision int64
	if err = tx.QueryRowContext(ctx, "SELECT revision FROM regional_scheduler_streams WHERE region_id=$1 FOR UPDATE", region).Scan(&revision); err != nil {
		return nil, err
	}
	if revision < 0 || revision == math.MaxInt64 {
		return nil, errors.New("regional revision exhausted")
	}
	// Bind BOTH Ent and raw SQL to this transaction. Passing context alone would
	// not work: scheduling repository methods often use their own client directly.
	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: tx})
	client := dbent.NewClient(dbent.Driver(driver))
	accounts := newAccountRepositoryWithSQL(client, tx, nil)
	groups := newGroupRepositoryWithSQL(client, tx)
	wire, err := build(ctx, uint64(revision+1), accounts, groups)
	if err != nil {
		return nil, err
	}
	if len(wire) == 0 {
		return nil, errors.New("empty regional publication")
	}
	if _, err = tx.ExecContext(ctx, "UPDATE regional_scheduler_streams SET revision=$2, packet=$3, updated_at=NOW() WHERE region_id=$1", region, revision+1, wire); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return wire, nil
}
