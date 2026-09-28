package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *channelMonitorV2Repository) ClaimCandyProbe(ctx context.Context, probe service.ChannelMonitorV2CandyProbe, key string, slot time.Time, version int) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `INSERT INTO channel_monitor_v2_candy_results(group_id,config_key,model,reasoning_effort,slot)
 SELECT $1,$2,$3,$4,$5 FROM channel_monitor_v2_config WHERE id=1 AND enabled AND version=$6
 ON CONFLICT DO NOTHING RETURNING id`, probe.GroupID, key, probe.Model, probe.ReasoningEffort, slot, version).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

func (r *channelMonitorV2Repository) FinishCandyProbe(ctx context.Context, result service.ChannelMonitorV2CandyResult) error {
	_, err := r.db.ExecContext(ctx, `UPDATE channel_monitor_v2_candy_results SET verdict=$2,latency_ms=$3,answer_preview=$4,reason=$5,finished_at=NOW() WHERE id=$1 AND verdict='running'`, result.ID, result.Verdict, result.LatencyMs, result.AnswerPreview, result.Reason)
	return err
}

func (r *channelMonitorV2Repository) CandyHistory(ctx context.Context, ids []int64, since time.Time) ([]service.ChannelMonitorV2CandyResult, error) {
	out := []service.ChannelMonitorV2CandyResult{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,group_id,config_key,checked_at,verdict,latency_ms,answer_preview,reason
 FROM (SELECT *,ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY checked_at DESC,id DESC) AS rn
 FROM channel_monitor_v2_candy_results WHERE group_id=ANY($1) AND checked_at >= $2 AND verdict<>'running') recent
 WHERE rn<=60 ORDER BY checked_at,id`, pq.Array(ids), since)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item service.ChannelMonitorV2CandyResult
		if err := rows.Scan(&item.ID, &item.GroupID, &item.ConfigKey, &item.CheckedAt, &item.Verdict, &item.LatencyMs, &item.AnswerPreview, &item.Reason); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *channelMonitorV2Repository) PruneCandyHistory(ctx context.Context, now time.Time) error {
	// A crashed worker's claim must never block a group indefinitely. The lease
	// is longer than the 90-second probe timeout; old workers cannot overwrite it.
	if _, err := r.db.ExecContext(ctx, `UPDATE channel_monitor_v2_candy_results SET verdict='error',reason='interrupted',finished_at=NOW() WHERE verdict='running' AND checked_at<$1`, now.Add(-3*time.Minute)); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM channel_monitor_v2_candy_results WHERE id IN (SELECT id FROM channel_monitor_v2_candy_results WHERE checked_at<$1 LIMIT 10000)`, now.Add(-24*time.Hour))
	return err
}
