package repository

import "strings"

// Additive observations only. Existing p90/sort/profit/ledger attribution and
// scheduling keep their legacy values. Historical NULL never enters this cohort.
// JSON milliseconds are promoted to bigint before any numeric aggregation.
func queueInclusiveOperationsAggregate(durationColumn string) string {
	return strings.ReplaceAll(`,
 count(*) FILTER(WHERE queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0) AS queue_measured_requests,
 count(*) FILTER(WHERE NOT COALESCE(queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0,false)) AS queue_unknown_requests,
 count(*) FILTER(WHERE ttft IS NOT NULL AND queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0 AND (queue_timing->>'server_first_token_ms')::bigint>=0) AS queue_inclusive_samples,
 percentile_cont(.9) WITHIN GROUP(ORDER BY CASE WHEN ttft IS NOT NULL AND queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0 THEN (queue_timing->>'server_first_token_ms')::bigint END) AS queue_inclusive_p90_ms,
 count(*) FILTER(WHERE __duration__ IS NOT NULL AND queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0) AS queue_inclusive_duration_samples,
 percentile_cont(.9) WITHIN GROUP(ORDER BY CASE WHEN __duration__ IS NOT NULL AND queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0 THEN (queue_timing->>'server_duration_ms')::bigint END) AS queue_inclusive_duration_p90_ms,
 COALESCE(jsonb_agg(DISTINCT queue_timing->>'source') FILTER(WHERE queue_timing->>'status'='measured' AND queue_timing->>'source'<>'' AND queue_wait_ms>=0),'[]'::jsonb) AS queue_timing_sources,
 'server_observed_measured_only_no_complete_failure_denominator'::text AS queue_timing_scope`, "__duration__", durationColumn)
}
