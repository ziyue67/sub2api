package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const accountProxyLaneBatchSelectSQL = `
SELECT id, account_id, proxy_id, name, transport, concurrency, weight,
       priority, status, schedulable, cooldown_until, created_at, updated_at
FROM account_proxy_lanes
WHERE account_id = ANY($1)
ORDER BY account_id ASC, priority ASC, id ASC`

// isMissingAccountProxyLaneTable keeps rolling deployments compatible with a
// binary that starts before the account-proxy-lanes migration has been applied.
func isMissingAccountProxyLaneTable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "relation \"account_proxy_lanes\" does not exist") ||
		(strings.Contains(msg, "no such table") && strings.Contains(msg, "account_proxy_lanes"))
}

// ListProxyLanes returns the active (not deleted) lane configuration for an
// account. It is deliberately an optional repository capability: callers can
// type-assert service.AccountProxyLaneRepository without changing the legacy
// AccountRepository contract.
func (r *accountRepository) ListProxyLanes(ctx context.Context, accountID int64) ([]service.AccountProxyLane, error) {
	if r == nil || r.sql == nil || accountID <= 0 {
		return nil, nil
	}
	rows, err := r.sql.QueryContext(ctx, `
		SELECT id, account_id, proxy_id, name, transport, concurrency, weight,
		       priority, status, schedulable, cooldown_until, created_at, updated_at
		FROM account_proxy_lanes
		WHERE account_id = $1
		ORDER BY priority ASC, id ASC`, accountID)
	if err != nil {
		if isMissingAccountProxyLaneTable(err) {
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	lanes := make([]service.AccountProxyLane, 0)
	proxyIDs := make([]int64, 0)
	for rows.Next() {
		lane, err := scanAccountProxyLane(rows)
		if err != nil {
			return nil, err
		}
		lanes = append(lanes, *lane)
		if lane.ProxyID != nil {
			proxyIDs = append(proxyIDs, *lane.ProxyID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(proxyIDs) > 0 && r.client != nil {
		proxies, err := r.loadProxies(ctx, proxyIDs)
		if err != nil {
			return nil, err
		}
		for i := range lanes {
			if lanes[i].ProxyID != nil {
				lanes[i].Proxy = proxies[*lanes[i].ProxyID]
			}
		}
	}
	return lanes, nil
}

// loadProxyLanesBatch is used by account list/get paths to avoid an N+1 query.
// It intentionally returns an empty map when the account-proxy-lanes migration
// is not present.
func (r *accountRepository) loadProxyLanesBatch(ctx context.Context, accountIDs []int64) (map[int64][]service.AccountProxyLane, error) {
	out := make(map[int64][]service.AccountProxyLane)
	if r == nil || r.sql == nil || len(accountIDs) == 0 {
		return out, nil
	}
	rows, err := r.sql.QueryContext(ctx, accountProxyLaneBatchSelectSQL, pq.Array(accountIDs))
	if err != nil {
		if isMissingAccountProxyLaneTable(err) {
			return out, nil
		}
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	proxyIDs := make([]int64, 0)
	for rows.Next() {
		lane, err := scanAccountProxyLane(rows)
		if err != nil {
			return nil, err
		}
		out[lane.AccountID] = append(out[lane.AccountID], *lane)
		if lane.ProxyID != nil {
			proxyIDs = append(proxyIDs, *lane.ProxyID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(proxyIDs) > 0 && r.client != nil {
		proxies, err := r.loadProxies(ctx, proxyIDs)
		if err != nil {
			return nil, err
		}
		for accountID, lanes := range out {
			for i := range lanes {
				if lanes[i].ProxyID != nil {
					lanes[i].Proxy = proxies[*lanes[i].ProxyID]
				}
			}
			out[accountID] = lanes
		}
	}
	return out, nil
}

// CreateProxyLane persists one lane. Parent-account and proxy FK validation
// are left to PostgreSQL; service validation runs first for useful errors.
func (r *accountRepository) CreateProxyLane(ctx context.Context, lane *service.AccountProxyLane) error {
	if lane == nil {
		return fmt.Errorf("account proxy lane cannot be nil")
	}
	if r == nil || r.sql == nil {
		return service.ErrAccountProxyLanesUnavailable
	}
	laneCopy := lane.Normalize()
	if err := laneCopy.Validate(); err != nil {
		return err
	}
	var proxyID any
	if laneCopy.ProxyID != nil {
		proxyID = *laneCopy.ProxyID
	}
	rows, err := r.sql.QueryContext(ctx, `
		INSERT INTO account_proxy_lanes
		(account_id, proxy_id, name, transport, concurrency, weight, priority, status, schedulable, cooldown_until)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, account_id, proxy_id, name, transport, concurrency, weight, priority, status,
		          schedulable, cooldown_until, created_at, updated_at`,
		laneCopy.AccountID, proxyID, laneCopy.Name, laneCopy.Transport, laneCopy.Concurrency,
		laneCopy.Weight, laneCopy.Priority, laneCopy.Status, laneCopy.Schedulable, laneCopy.CooldownUntil)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return fmt.Errorf("inserted account proxy lane did not return a row")
	}
	created, err := scanAccountProxyLane(rows)
	if err != nil {
		return err
	}
	*lane = *created
	if err := enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &lane.AccountID, nil, nil); err != nil {
		// The lane write is already durable.  Match the existing account
		// repository semantics: cache propagation is best effort and the caller
		// must not retry a successful write merely because Redis/outbox is down.
		logger.LegacyPrintf("repository.account_proxy_lane", "[SchedulerOutbox] enqueue create failed: account=%d lane=%d err=%v", lane.AccountID, lane.ID, err)
	}
	r.syncSchedulerAccountSnapshot(ctx, lane.AccountID)
	return nil
}

// UpdateProxyLane replaces mutable lane settings while scoping by account ID
// to prevent cross-account edits through an admin route.
func (r *accountRepository) UpdateProxyLane(ctx context.Context, lane *service.AccountProxyLane) error {
	if lane == nil {
		return fmt.Errorf("account proxy lane cannot be nil")
	}
	if r == nil || r.sql == nil {
		return service.ErrAccountProxyLanesUnavailable
	}
	laneCopy := lane.Normalize()
	if err := laneCopy.Validate(); err != nil {
		return err
	}
	var proxyID any
	if laneCopy.ProxyID != nil {
		proxyID = *laneCopy.ProxyID
	}
	rows, err := r.sql.QueryContext(ctx, `
		UPDATE account_proxy_lanes
		SET proxy_id=$1, name=$2, transport=$3, concurrency=$4, weight=$5, priority=$6,
		    status=$7, schedulable=$8, cooldown_until=$9, updated_at=NOW()
		WHERE id=$10 AND account_id=$11
		RETURNING id, account_id, proxy_id, name, transport, concurrency, weight, priority, status,
		          schedulable, cooldown_until, created_at, updated_at`,
		proxyID, laneCopy.Name, laneCopy.Transport, laneCopy.Concurrency, laneCopy.Weight,
		laneCopy.Priority, laneCopy.Status, laneCopy.Schedulable, laneCopy.CooldownUntil,
		laneCopy.ID, laneCopy.AccountID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrAccountNotFound
	}
	updated, err := scanAccountProxyLane(rows)
	if err != nil {
		return err
	}
	*lane = *updated
	if err := enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &lane.AccountID, nil, nil); err != nil {
		logger.LegacyPrintf("repository.account_proxy_lane", "[SchedulerOutbox] enqueue update failed: account=%d lane=%d err=%v", lane.AccountID, lane.ID, err)
	}
	r.syncSchedulerAccountSnapshot(ctx, lane.AccountID)
	return nil
}

func (r *accountRepository) DeleteProxyLane(ctx context.Context, accountID, laneID int64) error {
	if accountID <= 0 || laneID <= 0 {
		return fmt.Errorf("account and lane ids must be positive")
	}
	if r == nil || r.sql == nil {
		return service.ErrAccountProxyLanesUnavailable
	}
	result, err := r.sql.ExecContext(ctx, `DELETE FROM account_proxy_lanes WHERE id=$1 AND account_id=$2`, laneID, accountID)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err == nil && affected == 0 {
		return service.ErrAccountProxyLaneNotFound
	}
	if err := enqueueSchedulerOutbox(ctx, r.sql, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		logger.LegacyPrintf("repository.account_proxy_lane", "[SchedulerOutbox] enqueue delete failed: account=%d lane=%d err=%v", accountID, laneID, err)
	}
	r.syncSchedulerAccountSnapshot(ctx, accountID)
	return nil
}

type accountProxyLaneScanner interface{ Scan(dest ...any) error }

func scanAccountProxyLane(row accountProxyLaneScanner) (*service.AccountProxyLane, error) {
	var lane service.AccountProxyLane
	var proxyID sql.NullInt64
	var cooldown sql.NullTime
	if err := row.Scan(&lane.ID, &lane.AccountID, &proxyID, &lane.Name, &lane.Transport,
		&lane.Concurrency, &lane.Weight, &lane.Priority, &lane.Status, &lane.Schedulable,
		&cooldown, &lane.CreatedAt, &lane.UpdatedAt); err != nil {
		return nil, err
	}
	if proxyID.Valid {
		lane.ProxyID = &proxyID.Int64
	}
	if cooldown.Valid {
		lane.CooldownUntil = &cooldown.Time
	}
	lane.Name = strings.TrimSpace(lane.Name)
	return &lane, nil
}
