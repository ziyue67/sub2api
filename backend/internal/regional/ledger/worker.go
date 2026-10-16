package ledger

import (
	"context"
	"errors"
	"time"
)

// SettlementClient must authenticate the authority and return only after its
// transaction committed. An idempotent replay is also a successful confirmation.
type SettlementClient interface {
	Settle(context.Context, Event) error
}

// Flush is safe to retry after either endpoint restarts: it retains the exact
// original identity/content and acknowledges only after durable confirmation.
func (l *Local) Flush(ctx context.Context, client SettlementClient, limit int) (int, error) {
	if client == nil {
		return 0, errors.New("settlement client required")
	}
	events, err := l.Pending(ctx, limit)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, e := range events {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		if err := client.Settle(ctx, e); err != nil {
			return count, err
		}
		if err := l.Acknowledge(ctx, e); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// RunOutbox operates outside model requests. Retries never release reserved
// funds or change the original event. Per-cycle deadlines bound disconnects.
func (l *Local) RunOutbox(ctx context.Context, client SettlementClient, interval time.Duration) error {
	if client == nil || interval < time.Second || interval > time.Minute {
		return errors.New("invalid outbox worker")
	}
	delay := interval
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cycle, cancel := context.WithTimeout(ctx, 15*time.Second)
		_, err := l.Flush(cycle, client, 100)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil {
			delay = interval
		} else {
			delay = min(delay*2, 5*time.Minute)
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
