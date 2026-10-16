package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/regional/slots"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (c *regionalConcurrencyCache) database() *sql.DB {
	if c.primary != nil {
		return c.primary.DB
	}
	return c.local.DB
}
func (c *regionalConcurrencyCache) region() string {
	if c.local != nil {
		return c.local.Region
	}
	return ""
}
func (c *regionalConcurrencyCache) acquireTx(ctx context.Context, tx *sql.Tx, r slots.Resource, id string) (bool, error) {
	if c.primary != nil {
		return c.primary.AcquireTx(ctx, tx, r, id)
	}
	return c.local.AcquireResourceTx(ctx, tx, r, id)
}
func (c *regionalConcurrencyCache) releaseTx(ctx context.Context, tx *sql.Tx, r slots.Resource, id string) error {
	if c.primary != nil {
		return c.primary.ReleaseTx(ctx, tx, r, id)
	}
	return c.local.ReleaseResourceTx(ctx, tx, r, id)
}

// The same resource lock guards FIFO order, cancellation, and slot admission.
func (c *regionalConcurrencyCache) queueTx(ctx context.Context, id int64) (*sql.Tx, int64, error) {
	if c.runtimeFailed.Load() {
		return nil, 0, errors.New("regional runtime unavailable")
	}
	if id <= 0 {
		return nil, 0, errors.New("invalid regional queue key")
	}
	tx, err := c.database().BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	fail := func(err error) (*sql.Tx, int64, error) { _ = tx.Rollback(); return nil, 0, err }
	if _, err = tx.ExecContext(ctx, "INSERT INTO regional_slot_budgets(kind,resource_id) VALUES('api_key',$1) ON CONFLICT DO NOTHING", id); err != nil {
		return fail(err)
	}
	var locked int64
	if err = tx.QueryRowContext(ctx, "SELECT resource_id FROM regional_slot_budgets WHERE kind='api_key' AND resource_id=$1 FOR UPDATE", id).Scan(&locked); err != nil {
		return fail(err)
	}
	var now int64
	if err = tx.QueryRowContext(ctx, "SELECT floor(extract(epoch FROM clock_timestamp())*1000)::bigint").Scan(&now); err != nil {
		return fail(err)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE regional_key_queue SET state='timeout' WHERE region_id=$1 AND key_id=$2 AND state='waiting' AND deadline_ms<=$3", c.region(), id, now); err != nil {
		return fail(err)
	}
	return tx, now, nil
}
func (c *regionalConcurrencyCache) AcquireAPIKeySlotWithTime(ctx context.Context, id int64, limit int, request string) (bool, int64, error) {
	tx, now, err := c.queueTx(ctx, id)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var waiting bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_key_queue WHERE region_id=$1 AND key_id=$2 AND state='waiting')", c.region(), id).Scan(&waiting)
	if err != nil {
		return false, now, err
	}
	if waiting {
		return false, now, tx.Commit()
	}
	ok, err := c.acquireTx(ctx, tx, slots.Resource{Kind: "api_key", ID: id}, request)
	if err != nil {
		return false, now, err
	}
	return ok, now, tx.Commit()
}
func (c *regionalConcurrencyCache) AdvanceAPIKeyQueue(ctx context.Context, id int64, request string, mode service.APIKeyQueueMode, deadline int64, limit, maxWaiting int) (service.APIKeyQueueOutcome, error) {
	if request == "" || len(request) > 128 || limit <= 0 || maxWaiting < 0 || (mode != service.APIKeyQueueModeEnter && mode != service.APIKeyQueueModePoll) {
		return 0, errors.New("invalid regional queue attempt")
	}
	tx, now, err := c.queueTx(ctx, id)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	finish := func(out service.APIKeyQueueOutcome) (service.APIKeyQueueOutcome, error) { return out, tx.Commit() }
	if c.primary != nil {
		current, err := c.primary.Limit(ctx, tx, slots.Resource{Kind: "api_key", ID: id})
		if err != nil {
			return 0, err
		}
		if current != limit {
			return finish(service.APIKeyQueueOutcomePolicyChanged)
		}
	}

	var state string
	var oldDeadline int64
	var oldLimit int
	err = tx.QueryRowContext(ctx, "SELECT state,deadline_ms,key_limit FROM regional_key_queue WHERE region_id=$1 AND key_id=$2 AND request_id=$3", c.region(), id, request).Scan(&state, &oldDeadline, &oldLimit)
	if err == nil {
		if state == "aborted" {
			return finish(service.APIKeyQueueOutcomeAborted)
		}
		if oldDeadline != deadline || oldLimit != limit {
			return finish(service.APIKeyQueueOutcomePolicyChanged)
		}
		switch state {
		case "aborted":
			return finish(service.APIKeyQueueOutcomeAborted)
		case "timeout":
			return finish(service.APIKeyQueueOutcomeTimeout)
		case "acquired":
			return finish(service.APIKeyQueueOutcomeAcquired)
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	} else {
		if mode == service.APIKeyQueueModePoll {
			return finish(service.APIKeyQueueOutcomeTicketLost)
		}
		if deadline <= now {
			return finish(service.APIKeyQueueOutcomeTimeout)
		}
		var n int
		if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM regional_key_queue WHERE region_id=$1 AND key_id=$2 AND state='waiting'", c.region(), id).Scan(&n); err != nil {
			return 0, err
		}
		if n >= maxWaiting {
			return finish(service.APIKeyQueueOutcomeQueueFull)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO regional_key_queue(region_id,key_id,request_id,deadline_ms,key_limit,state) VALUES($1,$2,$3,$4,$5,'waiting')", c.region(), id, request, deadline, limit); err != nil {
			return 0, err
		}
	}
	var first string
	if err = tx.QueryRowContext(ctx, "SELECT request_id FROM regional_key_queue WHERE region_id=$1 AND key_id=$2 AND state='waiting' ORDER BY sequence LIMIT 1", c.region(), id).Scan(&first); err != nil {
		return 0, err
	}
	if first != request {
		return finish(service.APIKeyQueueOutcomeWaiting)
	}
	ok, err := c.acquireTx(ctx, tx, slots.Resource{Kind: "api_key", ID: id}, request)
	if err != nil {
		return 0, err
	}
	if !ok {
		return finish(service.APIKeyQueueOutcomeWaiting)
	}
	if _, err = tx.ExecContext(ctx, "UPDATE regional_key_queue SET state='acquired' WHERE region_id=$1 AND key_id=$2 AND request_id=$3", c.region(), id, request); err != nil {
		return 0, err
	}
	return finish(service.APIKeyQueueOutcomeAcquired)
}
func (c *regionalConcurrencyCache) AbortAPIKeyQueueAttempt(ctx context.Context, id int64, request string, deadline int64, remove bool) error {
	tx, _, err := c.queueTx(ctx, id)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "INSERT INTO regional_key_queue(region_id,key_id,request_id,deadline_ms,key_limit,state) VALUES($1,$2,$3,$4,0,'aborted') ON CONFLICT(region_id,key_id,request_id) DO UPDATE SET state=CASE WHEN regional_key_queue.state='acquired' AND NOT $5 THEN 'acquired' ELSE 'aborted' END", c.region(), id, request, deadline, remove); err != nil {
		return err
	}
	if remove {
		if err = c.releaseTx(ctx, tx, slots.Resource{Kind: "api_key", ID: id}, request); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (c *regionalConcurrencyCache) GetAPIKeyQueueStats(ctx context.Context, id int64) (int, int, error) {
	tx, _, err := c.queueTx(ctx, id)
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var active, waiting int
	if c.primary != nil {
		err = tx.QueryRowContext(ctx, "SELECT primary_used+granted FROM regional_slot_budgets WHERE kind='api_key' AND resource_id=$1", id).Scan(&active)
	} else {
		err = tx.QueryRowContext(ctx, "SELECT COALESCE(sum(used),0) FROM regional_local_slot_grants WHERE region_id=$1 AND kind='api_key' AND resource_id=$2", c.region(), id).Scan(&active)
	}
	if err != nil {
		return 0, 0, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM regional_key_queue WHERE region_id=$1 AND key_id=$2 AND state='waiting'", c.region(), id).Scan(&waiting); err != nil {
		return 0, 0, err
	}
	return active, waiting, tx.Commit()
}
func (c *regionalConcurrencyCache) GetAPIKeyQueueStatsBatch(ctx context.Context, ids []int64) (map[int64]service.APIKeyQueueCounts, error) {
	out := make(map[int64]service.APIKeyQueueCounts, len(ids))
	for _, id := range ids {
		active, waiting, err := c.GetAPIKeyQueueStats(ctx, id)
		if err != nil {
			return nil, err
		}
		out[id] = service.APIKeyQueueCounts{Active: active, Waiting: waiting}
	}
	return out, nil
}

var _ service.APIKeySlotQueueCache = (*regionalConcurrencyCache)(nil)
