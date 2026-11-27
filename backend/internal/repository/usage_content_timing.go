package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/pkg/requesttiming"
	"github.com/lib/pq"
)

func (r *usageLogRepository) ReadUsageContentTimings(ctx context.Context, ids []int64) (map[int64]requesttiming.ContentObservation, error) {
	out := make(map[int64]requesttiming.ContentObservation, len(ids))
	if len(ids) == 0 || len(ids) > 1000 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, `SELECT wanted.id, d.detail FROM unnest($1::bigint[]) wanted(id)
 LEFT JOIN LATERAL (SELECT (array_agg(detail))[1] detail FROM (
 SELECT detail FROM request_timing_details WHERE usage_log_id=wanted.id LIMIT 2
 ) traces HAVING COUNT(*)=1) d ON true`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var raw []byte
		if err = rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		v := requesttiming.UnknownContentObservation()
		var s requesttiming.Snapshot
		if len(raw) > 0 && json.Unmarshal(raw, &s) == nil {
			v = requesttiming.DeriveContentObservation(s)
		}
		out[id] = v
	}
	return out, rows.Err()
}
