package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/regional"
	"github.com/Wei-Shaw/sub2api/internal/regional/control"
	"github.com/Wei-Shaw/sub2api/internal/regional/ledger"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type RegionalFundsTarget struct {
	BillingMode string          `json:"billing_mode"`
	UserID      int64           `json:"user_id"`
	APIKeyID    int64           `json:"api_key_id"`
	AccountID   int64           `json:"account_id"`
	Amount      regional.Amount `json:"amount"`
}
type RegionalFundsRuntimeConfig struct {
	Mode, Region  string
	Prices        []ledger.Price
	Targets       []RegionalFundsTarget
	MaximumOutbox int
}

func readRegionalFundsConfig(path string) (RegionalFundsRuntimeConfig, error) {
	var c RegionalFundsRuntimeConfig
	f, err := os.Open(path)
	if err != nil {
		return c, errors.New("funds config unavailable")
	}
	defer func() { _ = f.Close() }()
	d := json.NewDecoder(io.LimitReader(f, 65537))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, errors.New("invalid funds config")
	}
	if d.Decode(new(any)) != io.EOF {
		return c, errors.New("trailing funds config")
	}
	if c.Mode != "primary" && c.Mode != "regional" || len(c.Prices) == 0 || len(c.Targets) == 0 || c.MaximumOutbox < 1 || c.MaximumOutbox > 10000 {
		return c, errors.New("funds mode, prices, targets and outbox limit required")
	}
	seen := map[string]bool{}
	for _, p := range c.Prices {
		if err = p.Validate(); err != nil {
			return c, err
		}
		if seen[p.Model] {
			return c, errors.New("duplicate funds model")
		}
		seen[p.Model] = true
	}
	targets := make(map[[3]int64]bool)
	for _, target := range c.Targets {
		identity := [3]int64{target.UserID, target.APIKeyID, target.AccountID}
		if targets[identity] {
			return c, errors.New("duplicate funds target")
		}
		targets[identity] = true
		if target.BillingMode != "" && target.BillingMode != "subscription" {
			return c, errors.New("invalid funds billing mode")
		}
		if target.UserID <= 0 || target.APIKeyID <= 0 || target.AccountID <= 0 || target.Amount <= 0 {
			return c, errors.New("invalid funded target")
		}
		if _, err = ledger.USD(target.Amount); err != nil {
			return c, err
		}
	}
	return c, nil
}

// ProvideRegionalMetering attaches finance to the guarded concurrency authority.
// Issuance requires the live deployment fence and committed legacy quota import.
func ProvideRegionalMetering(db *sql.DB, cfg *config.Config, cache service.ConcurrencyCache, rdb *redis.Client) (*service.RegionalMetering, error) {
	if cfg == nil || cfg.Runtime.RegionalFundsFile == "" {
		return nil, nil
	}
	if cfg.BatchImage.QueueEnabled {
		return nil, errors.New("batch image queue must be drained and disabled before coordinated funds")
	}
	c, err := readRegionalFundsConfig(cfg.Runtime.RegionalFundsFile)
	if err != nil {
		return nil, err
	}
	capacity, ok := cache.(*regionalConcurrencyCache)
	if !ok || capacity.runtimeConfig == nil || capacity.runtimeConfig.Mode != c.Mode || capacity.runtimeConfig.Region != c.Region {
		return nil, errors.New("funds require matching guarded concurrency runtime")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prices := map[string]ledger.Price{}
	for _, price := range c.Prices {
		if err = ledger.InstallPrice(ctx, db, price); err != nil {
			return nil, err
		}
		prices[price.Model] = price
	}
	var requests ledger.RequestLedger
	var stoppedFlag atomic.Bool
	var failed atomic.Bool
	failed.Store(true)
	var local *ledger.Local
	var broker *control.Broker
	if c.Mode == "primary" {
		guard := func(ctx context.Context, tx *sql.Tx) error {
			if err := capacity.primary.GrantGuard(ctx, tx); err != nil {
				return err
			}
			var imported bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_funds_imports WHERE epoch=$1)", capacity.runtimeConfig.Epoch).Scan(&imported); err != nil {
				return err
			}
			if !imported {
				return errors.New("funds migration not committed")
			}
			return nil
		}
		primary, holds, verify := NewRegionalFundsAuthorities(db, guard)
		handler, err := control.NewHandler(capacity.runtimeConfig.Scope, primary, capacity.primary, verify)
		if err != nil {
			return nil, err
		}
		capacity.fundsHandler.Store(&handler)
		requests = holds
	} else {
		local = &ledger.Local{DB: db, Region: c.Region}
		requests = local
		if capacity.brokerClient == nil {
			return nil, errors.New("funds broker client missing")
		}
		broker = &control.Broker{DB: db, Remote: capacity.brokerClient, Quota: local, Slots: capacity.local}
	}
	meter := &service.RegionalMetering{Ledger: requests}
	meter.Admit = func(ctx context.Context, key *service.APIKey, account *service.Account, body []byte) (ledger.Reservation, ledger.Price, error) {
		r := ledger.Reservation{}
		if err := meter.Check(ctx); err != nil {
			return r, ledger.Price{}, err
		}
		if key == nil || account == nil {
			return r, ledger.Price{}, ledger.ErrConflict
		}
		var request struct{ Model string }
		if err := json.Unmarshal(body, &request); err != nil {
			return r, ledger.Price{}, err
		}
		price, ok := prices[request.Model]
		if !ok {
			return r, ledger.Price{}, errors.New("immutable price unavailable")
		}
		allowed := false
		for _, target := range c.Targets {
			if target.UserID == key.UserID && target.APIKeyID == key.ID && target.AccountID == account.ID {
				allowed = true
				break
			}
		}
		if !allowed {
			return r, price, ledger.ErrConflict
		}
		var admitted time.Time
		if err := db.QueryRowContext(ctx, "SELECT clock_timestamp()").Scan(&admitted); err != nil {
			return r, price, err
		}
		r = ledger.Reservation{RequestID: uuid.NewString(), RegionID: c.Region, UserID: key.UserID, APIKeyID: key.ID, AccountID: account.ID, AdmittedAt: admitted.UTC().Truncate(time.Microsecond)}
		if c.Mode == "regional" {
			var raw []byte
			err := db.QueryRowContext(ctx, `SELECT lease FROM regional_local_leases WHERE region_id=$1 AND user_id=$2 AND state='open' AND (lease->>'api_key_id')::bigint=$3 AND (lease->>'account_id')::bigint=$4 AND (lease->>'expires_at')::timestamptz>clock_timestamp() AND granted-spent-reserved>0 ORDER BY lease_id LIMIT 1`, c.Region, key.UserID, key.ID, account.ID).Scan(&raw)
			if err != nil {
				return r, price, err
			}
			var lease regional.Lease
			if err := json.Unmarshal(raw, &lease); err != nil {
				return r, price, err
			}
			r.LeaseID = lease.LeaseID
		}
		return r, price, nil
	}
	// Observe the durable cutover, worker health, outbox and active grants.
	meter.Readiness = func(ctx context.Context) error {
		if err := db.PingContext(ctx); err != nil {
			return err
		}
		var unresolved int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM regional_usage_outbox WHERE NOT acknowledged").Scan(&unresolved); err != nil {
			return err
		}
		if unresolved >= c.MaximumOutbox {
			return errors.New("regional usage outbox at admission limit")
		}
		if stoppedFlag.Load() {
			return errors.New("funds runtime stopped")
		}
		if c.Mode == "primary" {
			var unattributed bool
			if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_budget_allocations WHERE state='held' AND group_id IS NULL)").Scan(&unattributed); err != nil {
				return err
			}
			if unattributed {
				return errors.New("legacy held allocation requires billing attribution drain")
			}
		}
		if capacity.runtimeFailed.Load() {
			return errors.New("capacity runtime failed")
		}
		if failed.Load() {
			return errors.New("funds reconciliation unavailable")
		}
		if c.Mode == "primary" {
			var imported bool
			if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_funds_imports WHERE epoch=$1)", capacity.runtimeConfig.Epoch).Scan(&imported); err != nil {
				return err
			}
			if !imported {
				return errors.New("funds cutover not ready")
			}
			return nil
		}
		for _, target := range c.Targets {
			var ready bool
			if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM regional_local_leases WHERE region_id=$1 AND user_id=$2 AND state='open' AND (lease->>'api_key_id')::bigint=$3 AND (lease->>'account_id')::bigint=$4 AND (lease->>'expires_at')::timestamptz>clock_timestamp() AND granted-spent-reserved>0)", c.Region, target.UserID, target.APIKeyID, target.AccountID).Scan(&ready); err != nil {
				return err
			}
			if !ready {
				return ledger.ErrExhausted
			}
		}
		return nil
	}
	var lifecycle sync.Mutex
	var runCancel context.CancelFunc
	var running bool
	done := make(chan struct{})
	meter.Run = func(ctx context.Context) {
		lifecycle.Lock()
		if stoppedFlag.Load() || running {
			lifecycle.Unlock()
			return
		}
		ctx, runCancel = context.WithCancel(ctx)
		running = true
		lifecycle.Unlock()
		defer close(done)
		defer runCancel()
		defer failed.Store(true)
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			cycle, cancel := context.WithTimeout(ctx, 5*time.Second)
			var cycleErr error
			if c.Mode == "primary" {
				_, cycleErr = db.ExecContext(cycle, "INSERT INTO regional_funds_members(epoch,instance_id) VALUES($1,$2) ON CONFLICT(epoch,instance_id) DO UPDATE SET heartbeat_at=clock_timestamp()", capacity.runtimeConfig.Epoch, capacity.runtimeConfig.Instance)
				if cycleErr == nil {
					cycleErr = importRegionalFundsUsage(cycle, db, rdb, capacity)
				}
			} else {
				if _, err := local.Flush(cycle, capacity.brokerClient, 100); err != nil {
					cycleErr = errors.Join(cycleErr, err)
				}
				if err := drainRegionalFunds(cycle, db, broker, local); err != nil {
					cycleErr = errors.Join(cycleErr, err)
				}
				if err := broker.RunOnce(cycle); err != nil {
					cycleErr = errors.Join(cycleErr, err)
				}
				for _, target := range c.Targets {
					if err := ensureRegionalFunds(cycle, db, broker, c.Region, target, time.Duration(capacity.runtimeConfig.LifetimeSeconds)*time.Second); err != nil {
						cycleErr = errors.Join(cycleErr, err)
					}
				}
				if err := broker.RunOnce(cycle); err != nil {
					cycleErr = errors.Join(cycleErr, err)
				}
				if _, err := local.Flush(cycle, capacity.brokerClient, 100); err != nil {
					cycleErr = errors.Join(cycleErr, err)
				}
			}
			failed.Store(cycleErr != nil)
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}
	meter.Stop = func() {
		lifecycle.Lock()
		stoppedFlag.Store(true)
		failed.Store(true)
		wait := running
		if runCancel != nil {
			runCancel()
		}
		lifecycle.Unlock()
		if wait {
			<-done
		}
	}
	return meter, nil
}
