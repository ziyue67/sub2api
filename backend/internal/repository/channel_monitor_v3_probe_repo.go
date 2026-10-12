package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *channelMonitorV3Repository) UpdateIdleProbe(ctx context.Context, probe service.ChannelMonitorV3IdleProbe) error {
	if probe.Groups == nil {
		probe.Groups = []service.ChannelMonitorV3IdleProbeGroup{}
	}
	value, err := json.Marshal(probe)
	if err != nil {
		return err
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE channel_monitor_v3_config SET idle_probe = $1::jsonb WHERE id = 1`, value); err != nil {
		return fmt.Errorf("update channel monitor v3 idle probe: %w", err)
	}
	return nil
}

// SuccessCounts mirrors V2's success count: distinct requests with a charge,
// leaving out the request types V2 leaves out.
func (r *channelMonitorV3Repository) SuccessCounts(ctx context.Context, groupIDs []int64, from, to time.Time) ([]service.ChannelMonitorV3Fact, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ul.group_id, `+channelMonitorV2ModelSQL+`,
		       COUNT(DISTINCT COALESCE(NULLIF(ul.request_id, ''), 'usage:' || ul.id::text))
		FROM usage_logs ul
		WHERE ul.group_id = ANY($1) AND ul.created_at >= $2 AND ul.created_at < $3
		  AND COALESCE(ul.request_type, 0) NOT IN (4, 6) AND `+usageLogSuccessFilterUL+`
		GROUP BY 1, 2`, pq.Array(groupIDs), from, to)
	if err != nil {
		return nil, fmt.Errorf("count channel monitor v3 idle traffic: %w", err)
	}
	out := []service.ChannelMonitorV3Fact{}
	err = scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3Fact
		if err := rows.Scan(&f.GroupID, &f.Model, &f.Success); err != nil {
			return err
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

func (r *channelMonitorV3Repository) ClaimProbe(ctx context.Context, groupID int64, model string, slot time.Time, slotMinutes int) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx, `
		INSERT INTO channel_monitor_v3_probe_results (group_id, model, slot_start, slot_minutes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (group_id, model, slot_start) DO NOTHING
		RETURNING id`, groupID, model, slot, slotMinutes).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("claim channel monitor v3 probe: %w", err)
	}
	return id, nil
}

func (r *channelMonitorV3Repository) FinishProbe(ctx context.Context, result service.ChannelMonitorV3ProbeResult) error {
	var accountID any
	if result.AccountID > 0 {
		accountID = result.AccountID
	}
	if _, err := r.db.ExecContext(ctx, `
		UPDATE channel_monitor_v3_probe_results
		SET outcome = $2, latency_ms = $3, error_category = $4, error_message = $5, account_id = $6,
		    cost_usd = $7, finished_at = NOW()
		WHERE id = $1`,
		result.ID, result.Outcome, result.LatencyMs, result.Category, result.ErrorMessage, accountID, result.CostUSD); err != nil {
		return fmt.Errorf("finish channel monitor v3 probe: %w", err)
	}
	return nil
}

// channelMonitorV3ProbeAtSQL is when a probe was meant to run: 30 seconds
// before the end of the slot it was taken for. Binning by it places old probes
// in the right slot even after the slot length changes.
const channelMonitorV3ProbeAtSQL = `(p.slot_start + make_interval(mins => p.slot_minutes) - INTERVAL '30 seconds')`

func (r *channelMonitorV3Repository) ProbeFacts(ctx context.Context, groupIDs []int64, from, to time.Time, interval time.Duration) ([]service.ChannelMonitorV3ProbeFact, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	slot := `NULL::timestamptz`
	args := []any{pq.Array(groupIDs), from, to}
	if interval > 0 {
		slot = `date_bin($4::interval, ` + channelMonitorV3ProbeAtSQL + `, ` + channelMonitorV2DateBinOrigin + `)`
		args = append(args, fmt.Sprintf("%d seconds", int(interval.Seconds())))
	}
	// Slots are at most an hour long, so the slot_start bound keeps the index usable.
	rows, err := r.db.QueryContext(ctx, `
		SELECT p.group_id, p.model, `+slot+`, p.error_category, COUNT(*),
		       COALESCE(percentile_disc(0.5) WITHIN GROUP (ORDER BY p.latency_ms), 0)
		FROM channel_monitor_v3_probe_results p
		WHERE p.group_id = ANY($1) AND p.outcome <> 'running'
		  AND p.slot_start >= $2::timestamptz - INTERVAL '1 hour' AND p.slot_start < $3
		  AND `+channelMonitorV3ProbeAtSQL+` >= $2 AND `+channelMonitorV3ProbeAtSQL+` < $3
		GROUP BY 1, 2, 3, 4`, args...)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 probes: %w", err)
	}
	out := []service.ChannelMonitorV3ProbeFact{}
	err = scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var f service.ChannelMonitorV3ProbeFact
		var at sql.NullTime
		if err := rows.Scan(&f.GroupID, &f.Model, &at, &f.Category, &f.Count, &f.LatencyP50Ms); err != nil {
			return err
		}
		if at.Valid {
			f.Slot = at.Time.UTC()
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

func (r *channelMonitorV3Repository) ProbeStats(ctx context.Context, since time.Time) ([]service.ChannelMonitorV3IdleProbeStat, error) {
	rows, err := r.db.QueryContext(ctx, `
		WITH latest AS (
			SELECT DISTINCT ON (group_id) group_id, started_at, model, outcome, latency_ms, error_category, error_message
			FROM channel_monitor_v3_probe_results
			WHERE outcome <> 'running'
			ORDER BY group_id, slot_start DESC, id DESC
		), recent AS (
			SELECT group_id, COUNT(*) AS probes, COUNT(*) FILTER (WHERE outcome = 'failure') AS failures,
			       COALESCE(SUM(cost_usd), 0)::float8 AS cost
			FROM channel_monitor_v3_probe_results
			WHERE outcome <> 'running' AND started_at >= $1
			GROUP BY group_id
		)
		SELECT l.group_id, COALESCE(r.probes, 0), COALESCE(r.failures, 0), COALESCE(r.cost, 0),
		       l.started_at, l.model, l.outcome, l.latency_ms, l.error_category, l.error_message
		FROM latest l LEFT JOIN recent r ON r.group_id = l.group_id
		ORDER BY l.group_id`, since)
	if err != nil {
		return nil, fmt.Errorf("load channel monitor v3 probe stats: %w", err)
	}
	out := []service.ChannelMonitorV3IdleProbeStat{}
	err = scanChannelMonitorV3Rows(rows, func(rows *sql.Rows) error {
		var stat service.ChannelMonitorV3IdleProbeStat
		var at time.Time
		var latency sql.NullInt64
		if err := rows.Scan(&stat.GroupID, &stat.Probes, &stat.Failures, &stat.CostUSD, &at, &stat.LastModel,
			&stat.LastOutcome, &latency, &stat.LastCategory, &stat.LastError); err != nil {
			return err
		}
		at = at.UTC()
		stat.LastAt = &at
		if latency.Valid {
			stat.LastLatencyMs = &latency.Int64
		}
		out = append(out, stat)
		return nil
	})
	return out, err
}

func (r *channelMonitorV3Repository) PruneProbes(ctx context.Context, before time.Time) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM channel_monitor_v3_probe_results WHERE slot_start < $1`, before); err != nil {
		return fmt.Errorf("prune channel monitor v3 probes: %w", err)
	}
	return nil
}
