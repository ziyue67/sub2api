package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// ReadPrioritySchedulingSignals aggregates only the requested model. Missing
// TTFTs and failed placeholders are not zero-latency successes. Quality rounds
// count once regardless of the number of probes; inconclusive rounds are absent.
func (r *usageLogRepository) ReadPrioritySchedulingSignals(ctx context.Context, query service.PrioritySchedulingQuery) (map[int64]service.PrioritySchedulingSignal, error) {
	rows, err := r.sql.QueryContext(ctx, `
 WITH base_usage AS (
  SELECT account_id, stream, first_token_ms, actual_cost,
   COALESCE(account_stats_cost,total_cost) AS base_cost
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
   COUNT(*) FILTER (WHERE actual_cost>=0 AND base_cost>=0 AND actual_cost+base_cost>0) AS profit_samples,
   SUM(actual_cost) FILTER (WHERE actual_cost>=0 AND base_cost>=0 AND actual_cost+base_cost>0) AS revenue,
   SUM(base_cost) FILTER (WHERE actual_cost>=0 AND base_cost>=0 AND actual_cost+base_cost>0) AS base_cost
  FROM base_usage GROUP BY account_id
 ), rounds AS (
  SELECT p.account_id, r.quality_round_id, MAX(r.created_at) AS observed_at,
   bool_or(r.quality_judgment->>'verdict'='incorrect' AND r.status='failed'
    AND r.error_message IN ('answer_mismatch','state_degraded')) AS failed,
   bool_and(COALESCE(r.quality_judgment->>'verdict'='correct' AND r.status='success',false)) AS passed
  FROM scheduled_test_plans p JOIN scheduled_test_results r ON r.plan_id=p.id
  WHERE p.account_id=ANY($1) AND p.enabled
   AND COALESCE(NULLIF(r.pelican_config->>'model_id',''),p.model_id)=$2
   AND jsonb_typeof(p.pelican_config->'quality')='object' AND r.quality_round_id<>''
   AND r.created_at >= $4 AND r.created_at >= p.updated_at AND r.created_at <= NOW()
   AND r.quality_action<>'' AND r.quality_action<>'stale_run'
  GROUP BY p.account_id,p.id,r.quality_round_id
  HAVING bool_and((r.pelican_config->'parallel_count' IS NULL OR jsonb_typeof(r.pelican_config->'parallel_count')='number')
    AND COALESCE(r.pelican_config->>'parallel_count','1') ~ '^[1-8]$')
   AND COUNT(*) = MAX(CASE WHEN COALESCE(r.pelican_config->>'parallel_count','1') ~ '^[1-8]$'
    THEN COALESCE(r.pelican_config->>'parallel_count','1')::int END)
 ), latest_quality AS (
  SELECT DISTINCT ON (account_id) account_id, (passed AND NOT COALESCE(failed,false)) AS passed
  FROM rounds WHERE passed OR failed
  ORDER BY account_id, observed_at DESC, failed DESC NULLS LAST
 ), quality AS (
  SELECT account_id, COUNT(*) FILTER (WHERE passed AND NOT COALESCE(failed,false)) AS passed,
   COUNT(*) FILTER (WHERE passed OR failed) AS samples
  FROM rounds GROUP BY account_id
 )
 SELECT ids.id, COALESCE(u.samples,0),COALESCE(u.p90,0),COALESCE(q.passed,0),COALESCE(q.samples,0),
 COALESCE(u.profit_samples,0),COALESCE(u.revenue,0),COALESCE(u.base_cost,0),lq.passed
 FROM unnest($1::bigint[]) ids(id)
 LEFT JOIN usage u ON u.account_id=ids.id LEFT JOIN quality q ON q.account_id=ids.id
 LEFT JOIN latest_quality lq ON lq.account_id=ids.id
 `, pq.Array(query.AccountIDs), query.Model, query.UsageSince, query.QualitySince, query.GroupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[int64]service.PrioritySchedulingSignal, len(query.AccountIDs))
	for rows.Next() {
		var id int64
		var signal service.PrioritySchedulingSignal
		if err := rows.Scan(&id, &signal.Samples, &signal.P90TTFTMs, &signal.QualityPassed, &signal.QualitySamples, &signal.ProfitSamples, &signal.Revenue, &signal.BaseCost, &signal.LatestQualityPassed); err != nil {
			return nil, err
		}
		out[id] = signal
	}
	return out, rows.Err()
}
