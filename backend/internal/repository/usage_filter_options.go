package repository

import (
	"context"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *usageLogRepository) OwnUsageFilterOptions(ctx context.Context, userID int64, kind, query string, includeErrors bool) ([]service.UsageFilterOption, error) {
	var table, column string
	limit := 30
	switch kind {
	case "api_key":
		table, column = "api_keys", "api_key_id"
	case "account":
		table, column = "accounts", "account_id"
	case "group":
		table, column, limit = "groups", "group_id", 1000
	default:
		return nil, fmt.Errorf("invalid usage filter kind")
	}
	// SQL identifiers come exclusively from the whitelist above. Each history
	// branch independently enforces ownership, including failed-only requests.
	scope := fmt.Sprintf("EXISTS (SELECT 1 FROM usage_logs ul WHERE ul.user_id=$1 AND ul.%s=o.id)", column)
	if kind == "api_key" {
		scope = "o.user_id=$1"
	} else if includeErrors {
		scope += fmt.Sprintf(" OR EXISTS (SELECT 1 FROM ops_error_logs e WHERE e.user_id=$1 AND e.%s=o.id AND e.status_code>=400 AND COALESCE(e.is_count_tokens,false)=false)", column)
	}
	rows, err := r.sql.QueryContext(ctx, fmt.Sprintf("SELECT o.id,o.name FROM %s o WHERE (%s) AND o.name ILIKE $2 ORDER BY o.name,o.id LIMIT $3", table, scope), userID, "%"+query+"%", limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.UsageFilterOption{}
	for rows.Next() {
		var item service.UsageFilterOption
		if err := rows.Scan(&item.ID, &item.Name); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
