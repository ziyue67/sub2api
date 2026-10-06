package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *settingRepository) RecordAstraGateway(ctx context.Context, row service.AstraGatewayHistoryRecord, passed bool) error {
	if !astraGatewayHostPattern.MatchString(row.Gateway) || row.SourceAccountID <= 0 || row.TargetAccountID < 0 {
		return errors.New("invalid gateway observation")
	}
	// Never persist arbitrary upstream strings in the observation table.
	if len(row.LastAnswer) > 6 || strings.Trim(row.LastAnswer, "0123456789") != "" {
		row.LastAnswer = ""
	}
	switch row.LastReason {
	case "qualified", "response_not_qualified", "target_probe_passed", "target_probe_degraded", "target_probe_failed", "target_route_changed":
	default:
		row.LastReason = "response_not_qualified"
	}
	if row.LastSeen.IsZero() {
		row.LastSeen = time.Now()
	}
	_, err := r.client.ExecContext(ctx, `INSERT INTO astra_gateway_history
 (gateway,source_account_id,target_account_id,passes,failures,first_seen,last_seen,last_pass,last_failure,last_answer,last_reason)
 VALUES ($1,$2,$3,CASE WHEN $4 THEN 1 ELSE 0 END,CASE WHEN $4 THEN 0 ELSE 1 END,$5,$5,
 CASE WHEN $4 THEN $5::timestamptz ELSE NULL END,CASE WHEN $4 THEN NULL ELSE $5::timestamptz END,$6,$7)
 ON CONFLICT (gateway,source_account_id,target_account_id) DO UPDATE SET
 passes=astra_gateway_history.passes+EXCLUDED.passes,failures=astra_gateway_history.failures+EXCLUDED.failures,
 first_seen=LEAST(astra_gateway_history.first_seen,EXCLUDED.first_seen),
 last_seen=GREATEST(astra_gateway_history.last_seen,EXCLUDED.last_seen),
 last_pass=GREATEST(astra_gateway_history.last_pass,EXCLUDED.last_pass),
 last_failure=GREATEST(astra_gateway_history.last_failure,EXCLUDED.last_failure),
 last_answer=CASE WHEN EXCLUDED.last_seen>=astra_gateway_history.last_seen THEN EXCLUDED.last_answer ELSE astra_gateway_history.last_answer END,
 last_reason=CASE WHEN EXCLUDED.last_seen>=astra_gateway_history.last_seen THEN EXCLUDED.last_reason ELSE astra_gateway_history.last_reason END`,
		row.Gateway, row.SourceAccountID, row.TargetAccountID, passed, row.LastSeen, row.LastAnswer, row.LastReason)
	return err
}
func (r *settingRepository) ListAstraGateways(ctx context.Context, host string, passed bool, offset, limit int) (service.AstraGatewayHistoryPage, error) {
	page := service.AstraGatewayHistoryPage{Items: []service.AstraGatewayHistoryRecord{}}
	if len(host) > 200 || offset < 0 || limit < 1 || limit > 100 {
		return page, errors.New("invalid gateway query")
	}
	// Parameterized literal substring search: % and _ are not wildcard input.
	where := ` WHERE position($1 in gateway)>0 AND (NOT $2 OR (passes>0 AND target_account_id>0))`
	counts, err := r.client.QueryContext(ctx, `SELECT count(*),count(DISTINCT gateway) FROM astra_gateway_history`+where, host, passed)
	if err != nil {
		return page, err
	}
	if counts.Next() {
		err = counts.Scan(&page.Total, &page.UniqueGateways)
	}
	if err == nil {
		err = counts.Err()
	}
	_ = counts.Close()
	if err != nil {
		return page, err
	}
	rows, err := r.client.QueryContext(ctx, `SELECT gateway,source_account_id,target_account_id,passes,failures,first_seen,last_seen,last_pass,last_failure,last_answer,last_reason FROM astra_gateway_history`+where+` ORDER BY last_seen DESC,gateway,source_account_id,target_account_id LIMIT $3 OFFSET $4`, host, passed, limit, offset)
	if err != nil {
		return page, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var row service.AstraGatewayHistoryRecord
		if err = rows.Scan(&row.Gateway, &row.SourceAccountID, &row.TargetAccountID, &row.Passes, &row.Failures, &row.FirstSeen, &row.LastSeen, &row.LastPass, &row.LastFailure, &row.LastAnswer, &row.LastReason); err != nil {
			return page, err
		}
		page.Items = append(page.Items, row)
	}
	return page, rows.Err()
}
