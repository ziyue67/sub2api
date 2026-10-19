package repository

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type channelMonitorV2SamplePair struct {
	platform string
	groupID  int64
}

// channelMonitorV2RecentSampleWindow is the history searched for card samples.
// It deliberately ignores filter.Start, which is only the selected UI range.
// End may sit on the next range bucket, so the lookback is anchored at now and
// the upper bound is the later of now and filter.End.
func channelMonitorV2RecentSampleWindow(filter service.ChannelMonitorV2Filter, now time.Time) (start, end time.Time) {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	now = now.UTC()
	end = now
	if filter.End.After(end) {
		end = filter.End.UTC()
	}
	start = now.Add(-channelMonitorV2RetentionRollup5m)
	if !start.Before(end) {
		start = end.Add(-channelMonitorV2RetentionRollup5m)
	}
	return start, end
}

func channelMonitorV2RecentSampleMinimum(cfg service.ChannelMonitorV2Config) int64 {
	return service.NormalizeChannelMonitorV2HealthThresholds(cfg.HealthThresholds).MinimumSample
}

func channelMonitorV2RecentSampleFactsSQL(modelClause string) string {
	modelSQL := ""
	if modelClause != "" {
		modelSQL = " AND " + modelClause
	}
	// $5 is the normalized minimum sample. HAVING runs before LIMIT, so the 18
	// rows are the newest buckets that can be scored, not the newest noisy ones.
	return fmt.Sprintf(`SELECT s.bucket_start, s.platform, s.group_id, s.success_requests, s.error_requests, s.upstream_affected, s.upstream_attempts, s.input_tokens, s.output_tokens, s.cache_creation_tokens, s.cache_read_tokens, s.ttft_sum_ms, s.ttft_count, s.duration_sum_ms, s.duration_count
FROM unnest($1::text[], $2::bigint[]) AS keys(platform, group_id)
CROSS JOIN LATERAL (
  SELECT m.bucket_start, m.platform, m.group_id,
         SUM(m.success_requests)::bigint AS success_requests,
         SUM(m.error_requests)::bigint AS error_requests,
         SUM(m.upstream_affected_requests)::bigint AS upstream_affected,
         SUM(m.upstream_attempt_count)::bigint AS upstream_attempts,
         SUM(m.input_tokens)::bigint AS input_tokens,
         SUM(m.output_tokens)::bigint AS output_tokens,
         SUM(m.cache_creation_tokens)::bigint AS cache_creation_tokens,
         SUM(m.cache_read_tokens)::bigint AS cache_read_tokens,
         SUM(m.ttft_sum_ms)::bigint AS ttft_sum_ms,
         SUM(m.ttft_count)::bigint AS ttft_count,
         SUM(m.duration_sum_ms)::bigint AS duration_sum_ms,
         SUM(m.duration_count)::bigint AS duration_count
  FROM channel_monitor_v2_metrics_rollup m
  WHERE m.bucket_seconds = %d
    AND m.platform = keys.platform
    AND m.group_id = keys.group_id
    AND m.bucket_start >= $3
    AND m.bucket_start < $4
    AND (m.group_id <= 0 OR m.group_id IN (SELECT id FROM groups WHERE deleted_at IS NULL))
    AND (m.success_requests + m.error_requests) > 0%s
  GROUP BY m.bucket_start, m.platform, m.group_id
  HAVING SUM(m.success_requests) + SUM(m.error_requests) >= $5
  ORDER BY m.bucket_start DESC
  LIMIT %d
) s`, int(service.ChannelMonitorV2RecentSampleBucket.Seconds()), modelSQL, service.ChannelMonitorV2RecentSampleLimit)
}

func channelMonitorV2RecentSampleQuery(platforms []string, groupIDs []int64, start, end time.Time, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config) (string, []any) {
	modelSQL, modelArgs := channelMonitorV2SampleModelClause(filter, cfg, "m", 6)
	args := []any{pq.Array(platforms), pq.Array(groupIDs), start, end, channelMonitorV2RecentSampleMinimum(cfg)}
	args = append(args, modelArgs...)
	return channelMonitorV2RecentSampleFactsSQL(modelSQL), args
}

// channelMonitorV2SampleModelClause mirrors channelMonitorV2ModelSelected so a
// model filter does not pull traffic the range query would have dropped.
// An empty clause means every model. "FALSE" means the filter matches nothing.
func channelMonitorV2SampleModelClause(filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, alias string, firstArg int) (string, []any) {
	if len(filter.Models) == 0 {
		return "", nil
	}
	selected := map[string]struct{}{}
	other := false
	for _, model := range filter.Models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if model == service.ChannelMonitorV2OtherModel {
			other = true
			continue
		}
		selected[model] = struct{}{}
	}
	selectedList := make([]string, 0, len(selected))
	for model := range selected {
		selectedList = append(selectedList, model)
	}
	sort.Strings(selectedList)

	platforms := channelMonitorV2EnabledPlatforms(cfg)
	if len(filter.Platforms) > 0 {
		platforms = intersectStrings(platforms, filter.Platforms)
	}
	parts := make([]string, 0, len(platforms))
	args := make([]any, 0, len(platforms)*2)
	arg := firstArg
	for _, platform := range platforms {
		allow := channelMonitorV2PlatformAllowList(cfg, platform)
		if len(allow) == 0 {
			if len(selectedList) == 0 {
				continue
			}
			args = append(args, platform, pq.Array(selectedList))
			parts = append(parts, fmt.Sprintf("(%s.platform = $%d AND %s.model = ANY($%d))", alias, arg, alias, arg+1))
			arg += 2
			continue
		}
		exact := intersectStrings(allow, selectedList)
		sub := make([]string, 0, 2)
		if len(exact) > 0 {
			args = append(args, platform, pq.Array(exact))
			sub = append(sub, fmt.Sprintf("(%s.platform = $%d AND %s.model = ANY($%d))", alias, arg, alias, arg+1))
			arg += 2
		}
		if other {
			args = append(args, platform, pq.Array(allow))
			sub = append(sub, fmt.Sprintf("(%s.platform = $%d AND NOT (%s.model = ANY($%d)))", alias, arg, alias, arg+1))
			arg += 2
		}
		if len(sub) > 0 {
			parts = append(parts, "("+strings.Join(sub, " OR ")+")")
		}
	}
	if len(parts) == 0 {
		return "FALSE", nil
	}
	return "(" + strings.Join(parts, " OR ") + ")", args
}

func channelMonitorV2PlatformAllowList(cfg service.ChannelMonitorV2Config, platform string) []string {
	for _, item := range cfg.Platforms {
		if item.Platform == platform {
			return append([]string(nil), item.Models...)
		}
	}
	return nil
}

func (r *channelMonitorV2Repository) attachRecentSampleBuckets(ctx context.Context, matrix *service.ChannelMonitorV2Matrix, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, admin bool) error {
	if matrix == nil {
		return nil
	}
	pairs := make([]channelMonitorV2SamplePair, 0, len(matrix.Items))
	seen := map[channelMonitorV2SamplePair]struct{}{}
	for _, item := range matrix.Items {
		if item.GroupID == nil || *item.GroupID <= 0 || item.Platform == "" {
			continue
		}
		pair := channelMonitorV2SamplePair{platform: item.Platform, groupID: *item.GroupID}
		if _, ok := seen[pair]; ok {
			continue
		}
		seen[pair] = struct{}{}
		pairs = append(pairs, pair)
	}
	loaded := map[channelMonitorV2MatrixKey][]service.ChannelMonitorV2TrendPoint{}
	if len(pairs) > 0 {
		var err error
		loaded, err = r.loadRecentSampleBuckets(ctx, pairs, filter, cfg, admin)
		if err != nil {
			return err
		}
	}
	for i := range matrix.Items {
		key := channelMonitorV2MatrixKey{platform: matrix.Items[i].Platform}
		if matrix.Items[i].GroupID != nil {
			key.groupID = *matrix.Items[i].GroupID
		}
		buckets := loaded[key]
		if buckets == nil {
			buckets = []service.ChannelMonitorV2TrendPoint{}
		}
		matrix.Items[i].SampleBuckets = &buckets
	}
	return nil
}

func (r *channelMonitorV2Repository) loadRecentSampleBuckets(ctx context.Context, pairs []channelMonitorV2SamplePair, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, admin bool) (map[channelMonitorV2MatrixKey][]service.ChannelMonitorV2TrendPoint, error) {
	platforms := make([]string, len(pairs))
	groupIDs := make([]int64, len(pairs))
	for i, pair := range pairs {
		platforms[i] = pair.platform
		groupIDs[i] = pair.groupID
	}
	start, end := channelMonitorV2RecentSampleWindow(filter, time.Now())
	query, args := channelMonitorV2RecentSampleQuery(platforms, groupIDs, start, end, filter, cfg)
	minimum := channelMonitorV2RecentSampleMinimum(cfg)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load recent monitor samples: %w", err)
	}
	defer func() { _ = rows.Close() }()

	byKey := map[channelMonitorV2MatrixKey]map[string]*channelMonitorV2SampleBucket{}
	var bucketStarts []time.Time
	seenStart := map[string]struct{}{}
	for rows.Next() {
		var bucket time.Time
		var fact channelMonitorV2Fact
		if err := rows.Scan(&bucket, &fact.Platform, &fact.GroupID, &fact.Success, &fact.Errors, &fact.UpstreamAffected, &fact.UpstreamAttempts, &fact.Input, &fact.Output, &fact.CacheCreation, &fact.CacheRead, &fact.TTFTSum, &fact.TTFTCount, &fact.DurationSum, &fact.DurationCount); err != nil {
			return nil, fmt.Errorf("scan recent monitor samples: %w", err)
		}
		// SQL already applies this bound. Keep it here so a row that does not
		// meet the minimum never becomes a card sample.
		if fact.Success+fact.Errors < minimum {
			continue
		}
		fact.BucketStart = bucket.UTC().Format(time.RFC3339Nano)
		key := channelMonitorV2MatrixKey{platform: fact.Platform, groupID: fact.GroupID}
		if byKey[key] == nil {
			byKey[key] = map[string]*channelMonitorV2SampleBucket{}
		}
		acc := newMetricAccumulator()
		acc.addFact(fact)
		byKey[key][fact.BucketStart] = &channelMonitorV2SampleBucket{at: bucket.UTC(), acc: acc}
		if _, ok := seenStart[fact.BucketStart]; !ok {
			seenStart[fact.BucketStart] = struct{}{}
			bucketStarts = append(bucketStarts, bucket.UTC())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recent monitor samples: %w", err)
	}
	if len(bucketStarts) == 0 {
		return map[channelMonitorV2MatrixKey][]service.ChannelMonitorV2TrendPoint{}, nil
	}
	startKeys := make([]string, len(bucketStarts))
	for i, bucket := range bucketStarts {
		startKeys[i] = bucket.UTC().Format(time.RFC3339Nano)
	}
	if err := r.loadRecentSampleHistogramsInto(ctx, byKey, startKeys, platforms, groupIDs, filter, cfg); err != nil {
		return nil, err
	}
	ignored, err := r.loadRecentSampleIgnored(ctx, startKeys, platforms, groupIDs, filter, cfg)
	if err != nil {
		return nil, err
	}
	minutes := service.ChannelMonitorV2RecentSampleBucket.Minutes()
	out := make(map[channelMonitorV2MatrixKey][]service.ChannelMonitorV2TrendPoint, len(byKey))
	for key, buckets := range byKey {
		points := make([]service.ChannelMonitorV2TrendPoint, 0, len(buckets))
		for bucketKey, bucket := range buckets {
			metrics := bucket.acc.metric(minutes, admin)
			applyIgnoredErrors(&metrics, ignored[key][bucketKey])
			metrics.HasSamples = metrics.RequestCount > 0
			points = append(points, service.ChannelMonitorV2TrendPoint{
				BucketStart: bucket.at,
				Metrics:     metrics,
				Health:      service.ChannelMonitorV2HealthForWithThresholds(metrics, cfg.HealthThresholds),
			})
		}
		sort.Slice(points, func(i, j int) bool { return points[i].BucketStart.Before(points[j].BucketStart) })
		out[key] = points
	}
	return out, nil
}

type channelMonitorV2SampleBucket struct {
	at  time.Time
	acc *metricAccumulator
}

func (r *channelMonitorV2Repository) loadRecentSampleHistogramsInto(ctx context.Context, byKey map[channelMonitorV2MatrixKey]map[string]*channelMonitorV2SampleBucket, bucketStarts []string, platforms []string, groupIDs []int64, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config) error {
	modelSQL, modelArgs := channelMonitorV2SampleModelClause(filter, cfg, "h", 4)
	modelFilter := ""
	if modelSQL != "" {
		modelFilter = " AND " + modelSQL
	}
	// user_id 0 is the group rollup. Per-user histogram rows must not be added again.
	// Bucket starts are text because lib/pq cannot encode a time.Time array.
	query := fmt.Sprintf(`SELECT h.bucket_start, h.platform, h.group_id, h.metric, h.upper_bound_ms, SUM(h.sample_count)::bigint
FROM channel_monitor_v2_latency_histograms_rollup h
WHERE h.bucket_seconds = %d
  AND h.user_id = 0
  AND h.bucket_start = ANY(SELECT unnest($1::text[])::timestamptz)
  AND h.group_id = ANY($2)
  AND h.platform = ANY($3)%s
GROUP BY h.bucket_start, h.platform, h.group_id, h.metric, h.upper_bound_ms`, int(service.ChannelMonitorV2RecentSampleBucket.Seconds()), modelFilter)
	args := []any{pq.Array(bucketStarts), pq.Array(groupIDs), pq.Array(platforms)}
	args = append(args, modelArgs...)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("load recent monitor sample histograms: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var bucket time.Time
		var platform, metric string
		var groupID, upper, count int64
		if err := rows.Scan(&bucket, &platform, &groupID, &metric, &upper, &count); err != nil {
			return fmt.Errorf("scan recent monitor sample histograms: %w", err)
		}
		key := channelMonitorV2MatrixKey{platform: platform, groupID: groupID}
		slot := byKey[key][bucket.UTC().Format(time.RFC3339Nano)]
		if slot == nil {
			continue
		}
		slot.acc.addHistogram(channelMonitorV2Histogram{Metric: metric, UpperBound: upper, Count: count})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read recent monitor sample histograms: %w", err)
	}
	return nil
}

func (r *channelMonitorV2Repository) loadRecentSampleIgnored(ctx context.Context, bucketStarts []string, platforms []string, groupIDs []int64, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config) (map[channelMonitorV2MatrixKey]map[string]int64, error) {
	out := map[channelMonitorV2MatrixKey]map[string]int64{}
	if len(cfg.IgnoredErrorCategories) == 0 {
		return out, nil
	}
	modelSQL, modelArgs := channelMonitorV2SampleModelClause(filter, cfg, "e", 6)
	modelFilter := ""
	if modelSQL != "" {
		modelFilter = " AND " + modelSQL
	}
	query := fmt.Sprintf(`SELECT e.bucket_start, e.platform, e.group_id, SUM(e.error_requests)::bigint
FROM channel_monitor_v2_error_metrics_rollup e
WHERE e.bucket_seconds = %d
  AND e.bucket_start = ANY(SELECT unnest($1::text[])::timestamptz)
  AND e.group_id = ANY($2)
  AND e.platform = ANY($3)
  AND e.error_category = ANY($4)
  AND e.taxonomy_version = $5%s
GROUP BY e.bucket_start, e.platform, e.group_id`, int(service.ChannelMonitorV2RecentSampleBucket.Seconds()), modelFilter)
	args := []any{pq.Array(bucketStarts), pq.Array(groupIDs), pq.Array(platforms), pq.Array(cfg.IgnoredErrorCategories), service.ChannelMonitorV2TaxonomyVersion}
	args = append(args, modelArgs...)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("load recent monitor sample ignored errors: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var bucket time.Time
		var platform string
		var groupID, count int64
		if err := rows.Scan(&bucket, &platform, &groupID, &count); err != nil {
			return nil, fmt.Errorf("scan recent monitor sample ignored errors: %w", err)
		}
		key := channelMonitorV2MatrixKey{platform: platform, groupID: groupID}
		if out[key] == nil {
			out[key] = map[string]int64{}
		}
		out[key][bucket.UTC().Format(time.RFC3339Nano)] += count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read recent monitor sample ignored errors: %w", err)
	}
	return out, nil
}
