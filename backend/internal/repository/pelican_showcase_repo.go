package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type pelicanShowcaseRepository struct {
	db *sql.DB
}

func NewPelicanShowcaseRepository(db *sql.DB) service.PelicanShowcaseRepository {
	return &pelicanShowcaseRepository{db: db}
}

// Publish stores the snapshot, then trims its group. Both steps share a transaction so a
// group never keeps more than maxItems snapshots.
func (r *pelicanShowcaseRepository) Publish(ctx context.Context, snapshot service.PelicanShowcaseSnapshot, maxItems int) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	result, err := tx.ExecContext(ctx, `INSERT INTO pelican_showcase_items
 (group_id, source_result_id, model_id, reasoning_effort, response_text, latency_ms, generated_at)
 VALUES ($1, $2, $3, $4, $5, $6, $7)
 ON CONFLICT (group_id, source_result_id) DO NOTHING`,
		snapshot.GroupID, snapshot.SourceResultID, snapshot.ModelID, snapshot.ReasoningEffort, snapshot.ResponseText, snapshot.LatencyMs, snapshot.GeneratedAt)
	if err != nil {
		return err
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 0 {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM pelican_showcase_items WHERE id IN (
 SELECT id FROM (
  SELECT id, ROW_NUMBER() OVER (ORDER BY generated_at DESC, id DESC) AS rn
  FROM pelican_showcase_items WHERE group_id = $1
 ) ranked WHERE rn > $2)`, snapshot.GroupID, maxItems); err != nil {
		return err
	}
	return tx.Commit()
}

// Prune runs every minute; bounded batches keep each transaction short.
func (r *pelicanShowcaseRepository) Prune(ctx context.Context, maxItems int, before time.Time) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM pelican_showcase_items WHERE id IN (
 SELECT id FROM (
  SELECT id, group_id, generated_at,
   ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY generated_at DESC, id DESC) AS rn
  FROM pelican_showcase_items
 ) ranked
 WHERE NOT EXISTS (SELECT 1 FROM pelican_group_test_plans p WHERE p.group_id = ranked.group_id)
  OR rn > $1 OR ($2::timestamptz IS NOT NULL AND generated_at < $2)
 LIMIT 1000)`, maxItems, nullableTime(before))
	return err
}

func (r *pelicanShowcaseRepository) ListGroups(ctx context.Context) ([]*service.PelicanShowcaseGroup, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, platform FROM groups g
 WHERE deleted_at IS NULL AND status = $1
 AND EXISTS (SELECT 1 FROM pelican_group_test_plans p WHERE p.group_id = g.id)
 ORDER BY sort_order ASC, id ASC`, service.StatusActive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	groups := make([]*service.PelicanShowcaseGroup, 0)
	for rows.Next() {
		group := &service.PelicanShowcaseGroup{}
		if err := rows.Scan(&group.ID, &group.Name, &group.Platform); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	return groups, rows.Err()
}

func (r *pelicanShowcaseRepository) ListItems(ctx context.Context, groupIDs []int64, maxItems int, since time.Time) ([]*service.PelicanShowcaseItem, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, group_id, model_id, reasoning_effort, latency_ms, generated_at FROM (
 SELECT id, group_id, model_id, reasoning_effort, latency_ms, generated_at,
  ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY generated_at DESC, id DESC) AS rn
 FROM pelican_showcase_items WHERE group_id = ANY($1)
) ranked
WHERE rn <= $2 AND ($3::timestamptz IS NULL OR generated_at >= $3)
ORDER BY group_id, generated_at DESC, id DESC`, pq.Array(groupIDs), maxItems, nullableTime(since))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]*service.PelicanShowcaseItem, 0)
	for rows.Next() {
		item := &service.PelicanShowcaseItem{}
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ModelID, &item.ReasoningEffort, &item.LatencyMs, &item.GeneratedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// GetItem applies the same visibility as the gallery: an active group with a group test
// plan, inside the retention window and among the group's newest maxItems.
func (r *pelicanShowcaseRepository) GetItem(ctx context.Context, id int64, maxItems int, since time.Time) (*service.PelicanShowcaseItem, error) {
	item := &service.PelicanShowcaseItem{}
	err := r.db.QueryRowContext(ctx, `SELECT i.id, i.group_id, i.model_id, i.reasoning_effort, i.latency_ms, i.generated_at, i.response_text
 FROM pelican_showcase_items i
 JOIN groups g ON g.id = i.group_id AND g.deleted_at IS NULL AND g.status = $4
 WHERE i.id = $1
 AND EXISTS (SELECT 1 FROM pelican_group_test_plans p WHERE p.group_id = i.group_id)
 AND ($3::timestamptz IS NULL OR i.generated_at >= $3)
 AND (SELECT COUNT(*) FROM pelican_showcase_items newer WHERE newer.group_id = i.group_id
  AND (newer.generated_at, newer.id) > (i.generated_at, i.id)) < $2`,
		id, maxItems, nullableTime(since), service.StatusActive).Scan(
		&item.ID, &item.GroupID, &item.ModelID, &item.ReasoningEffort, &item.LatencyMs, &item.GeneratedAt, &item.ResponseText)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return item, nil
}

func (r *pelicanShowcaseRepository) Delete(ctx context.Context, id int64) (bool, error) {
	result, err := r.db.ExecContext(ctx, `DELETE FROM pelican_showcase_items WHERE id = $1`, id)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func nullableTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
