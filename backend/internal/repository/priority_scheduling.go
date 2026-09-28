package repository

import (
	"context"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// ReadPrioritySchedulingSignals aggregates only the requested model. Missing
// TTFTs and failed placeholders are not zero-latency successes. Quality rounds
// count once regardless of the number of probes; inconclusive rounds are absent.
// First-use Teams anchors are recorded once in account metadata, so retention
// cannot silently roll a paid window forward. This never changes account.updated_at.
func (r *usageLogRepository) ReadPrioritySchedulingSignals(ctx context.Context, query service.PrioritySchedulingQuery) (map[int64]service.PrioritySchedulingSignal, error) {
	windows := query.TeamsWindows
	if windows == nil {
		windows = []service.PriorityTeamsWindow{}
	}
	rawWindows, err := json.Marshal(windows)
	if err != nil {
		return nil, err
	}
	rows, err := r.sql.QueryContext(ctx, `
 WITH base_usage AS (
  SELECT account_id, stream, first_token_ms, actual_cost,
   COALESCE(account_stats_cost,total_cost) * COALESCE(account_rate_multiplier,1) AS theoretical_cost
  FROM usage_logs
  WHERE account_id=ANY($1) AND created_at >= $3 AND created_at <= NOW()
   AND COALESCE(NULLIF(requested_model,''),model)=$2
   AND group_id IS NOT DISTINCT FROM $5::bigint
   AND (actual_cost>0 OR total_cost>0 OR account_stats_cost>0
    OR input_tokens+output_tokens+cache_read_tokens+cache_creation_tokens>0)
 ), usage AS (
  SELECT account_id,
   COUNT(*) FILTER (WHERE stream AND first_token_ms>0) AS samples,
   percentile_cont(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE stream AND first_token_ms>0) AS p90,
   COUNT(*) FILTER (WHERE actual_cost>=0 AND theoretical_cost>=0 AND actual_cost+theoretical_cost>0) AS profit_samples,
   SUM(actual_cost) FILTER (WHERE actual_cost>=0 AND theoretical_cost>=0 AND actual_cost+theoretical_cost>0) AS revenue,
   SUM(theoretical_cost) FILTER (WHERE actual_cost>=0 AND theoretical_cost>=0 AND actual_cost+theoretical_cost>0) AS theoretical_cost
  FROM base_usage GROUP BY account_id
 ), rounds AS (
  SELECT p.account_id, r.quality_round_id,
   bool_or(r.quality_judgment->>'verdict'='incorrect' AND r.status='failed'
    AND r.error_message IN ('answer_mismatch','state_degraded')) AS failed,
   bool_and(COALESCE(r.quality_judgment->>'verdict'='correct' AND r.status='success',false)) AS passed
  FROM scheduled_test_plans p JOIN scheduled_test_results r ON r.plan_id=p.id
  WHERE p.account_id=ANY($1) AND p.model_id=$2 AND p.enabled
   AND p.pelican_config->'quality' IS NOT NULL AND r.quality_round_id<>''
   AND r.created_at >= $4 AND r.created_at >= p.updated_at AND r.created_at <= NOW()
   AND r.quality_action<>'' AND r.quality_action<>'stale_run'
  GROUP BY p.account_id,r.quality_round_id
  HAVING COUNT(*) = MAX(COALESCE((r.pelican_config->>'parallel_count')::int,1))
 ), quality AS (
  SELECT account_id, COUNT(*) FILTER (WHERE passed AND NOT COALESCE(failed,false)) AS passed,
   COUNT(*) FILTER (WHERE passed OR failed) AS samples
  FROM rounds GROUP BY account_id
 ), teams_anchors AS (
  SELECT w.account_id,COALESCE(w.start_at,NULLIF(a.extra->>'priority_teams_first_used_at','')::timestamptz,first_usage.created_at) AS start_at,w.end_at,
   w.start_at IS NULL AS from_first_usage
  FROM jsonb_to_recordset($6::jsonb) AS w(account_id bigint,start_at timestamptz,end_at timestamptz)
  JOIN accounts a ON a.id=w.account_id
  LEFT JOIN LATERAL (
   SELECT created_at FROM usage_logs WHERE account_id IN (SELECT id FROM accounts WHERE id=w.account_id OR parent_account_id=w.account_id) AND w.start_at IS NULL
   ORDER BY created_at ASC LIMIT 1
  ) first_usage ON true
 ), persist_teams_start AS (
  UPDATE accounts a SET extra=COALESCE(a.extra,'{}'::jsonb)||jsonb_build_object('priority_teams_first_used_at',w.start_at)
  FROM teams_anchors w WHERE a.id=w.account_id AND w.from_first_usage AND w.start_at IS NOT NULL
   AND COALESCE(a.extra->>'priority_teams_first_used_at','')=''
  RETURNING a.id
 ), teams_windows AS (
  SELECT account_id,start_at,COALESCE(end_at,start_at+$7::int*INTERVAL '1 hour') AS end_at FROM teams_anchors
 ), teams_usage AS (
  SELECT w.account_id,w.start_at,w.end_at,COALESCE(SUM(u.actual_cost),0) AS revenue
  FROM teams_windows w LEFT JOIN usage_logs u ON u.account_id IN (SELECT id FROM accounts WHERE id=w.account_id OR parent_account_id=w.account_id)
   AND u.created_at>=w.start_at AND u.created_at<w.end_at AND u.created_at<=NOW()
  GROUP BY w.account_id,w.start_at,w.end_at
 )
 SELECT ids.id, COALESCE(u.samples,0),COALESCE(u.p90,0),COALESCE(q.passed,0),COALESCE(q.samples,0),
 COALESCE(u.profit_samples,0),COALESCE(u.revenue,0),COALESCE(u.theoretical_cost,0),COALESCE(t.revenue,0),t.start_at,t.end_at
 FROM unnest($1::bigint[]) ids(id)
 LEFT JOIN usage u ON u.account_id=ids.id LEFT JOIN quality q ON q.account_id=ids.id
 LEFT JOIN teams_usage t ON t.account_id=ids.id
 `, pq.Array(query.AccountIDs), query.Model, query.UsageSince, query.QualitySince, query.GroupID, string(rawWindows), query.TeamsWindowHours)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]service.PrioritySchedulingSignal, len(query.AccountIDs))
	for rows.Next() {
		var id int64
		var signal service.PrioritySchedulingSignal
		if err := rows.Scan(&id, &signal.Samples, &signal.P90TTFTMs, &signal.QualityPassed, &signal.QualitySamples, &signal.ProfitSamples, &signal.Revenue, &signal.TheoreticalCost, &signal.TeamsRevenue, &signal.TeamsWindowStart, &signal.TeamsWindowEnd); err != nil {
			return nil, err
		}
		out[id] = signal
	}
	return out, rows.Err()
}
