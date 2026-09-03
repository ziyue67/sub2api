package service

import (
	"context"
	"fmt"
	"time"
)

// runtimeSchedulableAccountProxyLanes is the runtime counterpart of the
// public/pure lane filters.  The latter intentionally accepts zero IDs so
// callers can use temporary in-memory lanes in deterministic unit tests.  A
// scheduler, however, is about to address Redis and forward traffic, so a
// lane without a persisted positive ID (or with a mismatched parent account)
// is malformed and must never participate in admission.  Keeping this check
// at the runtime boundary also avoids changing the historical semantics of
// SelectAccountProxyLane for non-persisted callers.
func runtimeSchedulableAccountProxyLanes(account *Account, now time.Time) []AccountProxyLane {
	if account == nil || account.ID <= 0 || !account.HasProxyLanes() {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}

	eligible := FilterSchedulableAccountProxyLanes(account.ProxyLanes, now)
	filtered := make([]AccountProxyLane, 0, len(eligible))
	for _, lane := range eligible {
		// IDs and ownership are authoritative at the runtime boundary.  A
		// scheduler cache can briefly contain a partially decoded row; treating
		// it as unavailable is safer than reserving a zero/foreign Redis key.
		if lane.ID <= 0 || lane.AccountID <= 0 || lane.AccountID != account.ID {
			continue
		}
		transport := normalizeAccountProxyLaneTransport(lane.Transport)
		switch transport {
		case AccountProxyLaneTransportDirect:
			// Direct lanes cannot carry a proxy relation.  Reject malformed rows
			// here instead of allowing ApplySelectedProxyLane to erase fields and
			// hide a data/configuration error.
			if lane.ProxyID != nil || lane.Proxy != nil {
				continue
			}
		case AccountProxyLaneTransportProxy:
			// Proxy credentials may be lazy (Proxy can be nil in scheduler
			// metadata), but the relation itself must be a positive ID.
			if lane.ProxyID == nil || *lane.ProxyID <= 0 {
				continue
			}
		default:
			continue
		}
		filtered = append(filtered, lane)
	}
	return filtered
}

// runtimeLaneByID returns a copy of a currently usable persisted lane.  It is
// intentionally linear: lane sets are small (normally 1-3), and preserving
// the source order keeps tie handling deterministic.
func runtimeLaneByID(account *Account, laneID int64, now time.Time) *AccountProxyLane {
	if laneID <= 0 {
		return nil
	}
	for _, lane := range runtimeSchedulableAccountProxyLanes(account, now) {
		if lane.ID == laneID {
			copyLane := lane
			return &copyLane
		}
	}
	return nil
}

// ensureSelectedAccountProxyLaneHydrated makes the request-local lane safe to
// hand to a forwarding client.  Scheduler bucket payloads intentionally carry
// only proxy IDs; if such a payload is projected directly, AccountProxyURL
// returns an empty string and the HTTP client treats the request as a direct
// connection.  The normal cache hit therefore gets one chance to validate the
// relation, followed by an authoritative repository read when the proxy object
// is missing or mismatched.
//
// `selected` is the request-local account that owns the affinity decision and
// `hydrated` is the account snapshot currently being returned to the caller.
// The helper never borrows a proxy by ID from another lane and never falls back
// to the legacy Account.Proxy field when a concrete lane cannot be validated.
func ensureSelectedAccountProxyLaneHydrated(
	ctx context.Context,
	selected *Account,
	hydrated *Account,
	repo AccountRepository,
) (*Account, error) {
	return ensureSelectedAccountProxyLaneHydratedMode(ctx, selected, hydrated, repo, false)
}

// ensureSelectedAccountProxyLaneHydratedAuthoritative is the terminal
// forwarding variant of ensureSelectedAccountProxyLaneHydrated.  A scheduler
// snapshot is intentionally cache-first for throughput, but a concrete lane
// carries an egress/lifecycle contract that must be checked against the
// account repository immediately before forwarding.  Lane rows have their own
// updated_at and therefore an account snapshot can look complete while the
// lane was deleted, paused, or rebound.  When a repository is available this
// helper reads it first; only rolling-upgrade/test configurations without a
// repository may use the already hydrated snapshot.
func ensureSelectedAccountProxyLaneHydratedAuthoritative(
	ctx context.Context,
	selected *Account,
	hydrated *Account,
	repo AccountRepository,
) (*Account, error) {
	return ensureSelectedAccountProxyLaneHydratedMode(ctx, selected, hydrated, repo, true)
}

func ensureSelectedAccountProxyLaneHydratedMode(
	ctx context.Context,
	selected *Account,
	hydrated *Account,
	repo AccountRepository,
	forceAuthoritative bool,
) (*Account, error) {
	if hydrated == nil {
		return nil, fmt.Errorf("%w: hydrated account is nil", ErrNoSchedulableAccountProxyLane)
	}
	if selected == nil {
		selected = hydrated
	}
	lane := selected.SelectedProxyLaneOrNil()
	if lane == nil {
		// A caller may have selected the lane on the hydrated object itself.
		lane = hydrated.SelectedProxyLaneOrNil()
		if lane == nil {
			return hydrated, nil
		}
		selected = hydrated
	}

	// Terminal forwarding must not trust a full scheduler cache payload.  Read
	// the complete account (including current lane rows and proxy relations)
	// before attempting to project the request-local affinity.  If the optional
	// repository is absent, retain the existing cache-only behaviour so a
	// rolling upgrade can still serve already-hydrated lanes.
	if forceAuthoritative && repo != nil {
		accountID := selected.ID
		if accountID <= 0 {
			accountID = hydrated.ID
		}
		if accountID <= 0 {
			return nil, fmt.Errorf("%w: selected account id is invalid", ErrNoSchedulableAccountProxyLane)
		}
		if ctx == nil {
			ctx = context.Background()
		}
		fresh, err := repo.GetByID(ctx, accountID)
		if err != nil {
			return nil, fmt.Errorf("%w: authoritative account hydration failed: %v", ErrNoSchedulableAccountProxyLane, err)
		}
		if fresh == nil {
			return nil, fmt.Errorf("%w: account %d disappeared during lane hydration", ErrNoSchedulableAccountProxyLane, accountID)
		}
		if !applyHydratedSelectedProxyLane(selected, fresh) {
			return nil, fmt.Errorf("%w: selected lane %d is no longer schedulable or its proxy relation changed", ErrNoSchedulableAccountProxyLane, lane.ID)
		}
		if err := ValidateSelectedAccountProxyLane(fresh); err != nil {
			return nil, err
		}
		return fresh, nil
	}

	// Never trust a request-local SelectedProxyLane merely because the refreshed
	// snapshot happens to carry the same lane ID. Lane lifecycle state and the
	// proxy relation live on the lane row itself; a stale snapshot can otherwise
	// make a paused/deleted/rebound lane look valid and send traffic through the
	// wrong egress. Always reconcile against hydrated.ProxyLanes first. This also
	// deliberately fails for compact metadata rows whose Proxy object was
	// stripped; those rows must be completed from the authoritative repository.
	if applyHydratedSelectedProxyLane(selected, hydrated) {
		if err := ValidateSelectedAccountProxyLane(hydrated); err == nil {
			return hydrated, nil
		}
	}
	if repo == nil {
		if err := ValidateSelectedAccountProxyLane(hydrated); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: selected lane %d could not be reconciled", ErrNoSchedulableAccountProxyLane, lane.ID)
	}

	accountID := selected.ID
	if accountID <= 0 {
		accountID = hydrated.ID
	}
	if accountID <= 0 {
		return nil, fmt.Errorf("%w: selected account id is invalid", ErrNoSchedulableAccountProxyLane)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	fresh, err := repo.GetByID(ctx, accountID)
	if err != nil {
		return nil, fmt.Errorf("%w: authoritative account hydration failed: %v", ErrNoSchedulableAccountProxyLane, err)
	}
	if fresh == nil {
		return nil, fmt.Errorf("%w: account %d disappeared during lane hydration", ErrNoSchedulableAccountProxyLane, accountID)
	}
	if !applyHydratedSelectedProxyLane(selected, fresh) {
		return nil, fmt.Errorf("%w: selected lane %d is no longer schedulable or its proxy relation changed", ErrNoSchedulableAccountProxyLane, lane.ID)
	}
	if err := ValidateSelectedAccountProxyLane(fresh); err != nil {
		return nil, err
	}
	return fresh, nil
}

// orderedRuntimeAccountProxyLanesForSession is the strict runtime ordering
// used by acquisition and wait-plan construction.  It preserves the public
// selector's priority/weight semantics while excluding malformed persisted
// rows before any Redis namespace is touched.
func orderedRuntimeAccountProxyLanesForSession(account *Account, sessionKey string, now time.Time) ([]AccountProxyLane, error) {
	lanes := runtimeSchedulableAccountProxyLanes(account, now)
	if len(lanes) == 0 {
		return nil, ErrNoSchedulableAccountProxyLane
	}
	ordered, err := OrderedAccountProxyLanesForSession(lanes, sessionKey, now)
	if err != nil {
		return nil, err
	}
	return ordered, nil
}

// laneConcurrencySupported reports whether the configured cache implements
// the optional lane namespace.  During a rolling upgrade old cache adapters
// are still allowed to serve legacy account slots; they must not silently
// bypass concurrency for a lane-enabled account.
func laneConcurrencySupported(concurrency *ConcurrencyService) bool {
	if concurrency == nil || concurrency.cache == nil {
		return false
	}
	_, ok := concurrency.cache.(LaneConcurrencyCache)
	return ok
}

// acquireAccountProxyLaneSlot selects and acquires one egress for account.
// Legacy accounts continue to use the account slot. Lane-enabled accounts use
// both the parent account slot (the shared aggregate ceiling) and a lane slot
// (the per-egress ceiling). This is what makes total=20 with IP1=10/IP2=10 a
// real two-level limit instead of merely an implied sum of lane capacities.
//
// The preferred lane is deterministic for a supplied session key. If it is
// full, another eligible lane is tried in stable order; this is the only
// intentional spill-over point and does not rewrite the account-level sticky
// binding. The selected lane is projected onto the request-local Account copy
// for forwarding code.
func acquireAccountProxyLaneSlot(
	ctx context.Context,
	concurrency *ConcurrencyService,
	account *Account,
	sessionKey string,
	requestedMaxConcurrency ...int,
) (*AcquireResult, error) {
	if account == nil {
		return nil, ErrAccountNotFound
	}
	// Callers normally pass the account-level limit explicitly.  Keeping the
	// argument variadic preserves compatibility with older internal/test
	// callers while ensuring a lane projection cannot accidentally replace the
	// aggregate limit on the legacy account bucket.
	legacyMaxConcurrency := account.Concurrency
	if len(requestedMaxConcurrency) > 0 {
		legacyMaxConcurrency = requestedMaxConcurrency[0]
	}
	// Legacy accounts still use the account-scoped slot.  A lane-enabled
	// account, however, must project an egress even when the caller disabled
	// concurrency accounting (for example in a lightweight/admin process):
	// otherwise the request silently falls back to account.Proxy and ignores
	// the configured lane transport.  Select the lane locally in that case;
	// there is no Redis reservation to release.
	if !account.HasProxyLanes() {
		if concurrency == nil {
			return &AcquireResult{Acquired: true, ReleaseFunc: func() {}, MaxConcurrency: legacyMaxConcurrency, MaxConcurrencySet: true}, nil
		}
		return concurrency.AcquireAccountSlot(ctx, account.ID, legacyMaxConcurrency)
	}
	// Resolve the current eligible lanes before choosing the admission namespace.
	// Even when Redis only supports the legacy account bucket, a configured lane
	// set with no healthy egress must fail closed rather than forwarding through
	// the account-level proxy.
	now := time.Now()
	lanes, err := orderedRuntimeAccountProxyLanesForSession(account, sessionKey, now)
	if err != nil {
		return &AcquireResult{Acquired: false, MaxConcurrency: legacyMaxConcurrency, MaxConcurrencySet: true}, nil
	}
	if concurrency != nil && !laneConcurrencySupported(concurrency) {
		// During a rolling upgrade an old cache has no lane namespace. Preserve
		// the legacy account bucket semantics instead of bypassing concurrency,
		// but still project a deterministic healthy lane for transport routing.
		// This keeps the egress choice correct while the account-level bucket is
		// used as the temporary admission guard.
		preferred := lanes[0]
		if selected := account.SelectedProxyLaneOrNil(); selected != nil {
			for i := range lanes {
				if lanes[i].ID == selected.ID {
					preferred = lanes[i]
					break
				}
			}
		}
		account.ApplySelectedProxyLane(&preferred)
		result, err := concurrency.AcquireAccountSlot(ctx, account.ID, legacyMaxConcurrency)
		// Account-level wait plans must retain the persisted aggregate limit;
		// SelectedProxyLane still carries the lane-specific limit for routing.
		account.Concurrency = legacyMaxConcurrency
		if result != nil {
			result.Lane = account.SelectedProxyLaneOrNil()
		}
		return result, err
	}

	// No concurrency service means selection is still required, but all lane
	// rows are otherwise healthy. Apply the deterministic preferred lane and
	// return a no-op release; this keeps direct/proxy transport routing correct
	// without introducing an in-memory limit that could diverge across workers.
	if concurrency == nil {
		preferred := lanes[0]
		if selected := account.SelectedProxyLaneOrNil(); selected != nil {
			for i := range lanes {
				if lanes[i].ID != selected.ID {
					continue
				}
				preferred = lanes[i]
				break
			}
		}
		account.ApplySelectedProxyLane(&preferred)
		return &AcquireResult{
			Acquired:          true,
			ReleaseFunc:       func() {},
			MaxConcurrency:    preferred.Concurrency,
			MaxConcurrencySet: true,
			Lane:              account.SelectedProxyLaneOrNil(),
		}, nil
	}

	// Reserve the shared account ceiling before trying individual lanes.  If the
	// account is full, no lane can be used even when that lane has spare room.
	// This reservation is released again when every lane is full or when a lane
	// acquire fails, so a failed probe never leaks aggregate capacity.
	aggregateResult, err := concurrency.AcquireAccountSlot(ctx, account.ID, legacyMaxConcurrency)
	if err != nil {
		return nil, err
	}
	if aggregateResult == nil {
		return &AcquireResult{Acquired: false, MaxConcurrency: lanes[0].Concurrency, MaxConcurrencySet: true, AggregateMaxConcurrency: legacyMaxConcurrency, AggregateMaxConcurrencySet: true, Lane: account.SelectedProxyLaneOrNil()}, nil
	}
	if !aggregateResult.Acquired {
		preferred := lanes[0]
		account.ApplySelectedProxyLane(&preferred)
		aggregateResult.MaxConcurrency = preferred.Concurrency
		aggregateResult.MaxConcurrencySet = true
		aggregateResult.AggregateMaxConcurrency = legacyMaxConcurrency
		aggregateResult.AggregateMaxConcurrencySet = true
		aggregateResult.Lane = account.SelectedProxyLaneOrNil()
		return aggregateResult, nil
	}
	aggregateRelease := aggregateResult.ReleaseFunc

	releaseAggregate := func() {
		if aggregateRelease != nil {
			aggregateRelease()
			aggregateRelease = nil
		}
	}
	// A persisted lane-sticky binding is projected onto SelectedProxyLane by
	// the scheduler before admission.  Prefer it over the hash bucket while
	// retaining the normal deterministic spill-over order when that lane is
	// full.  Invalid/stale selections are ignored; the account's current lane
	// rows remain authoritative.
	if selected := account.SelectedProxyLaneOrNil(); selected != nil {
		for i := range lanes {
			if lanes[i].ID != selected.ID {
				continue
			}
			ordered := make([]AccountProxyLane, 0, len(lanes))
			ordered = append(ordered, lanes[i])
			for j := range lanes {
				if j != i {
					ordered = append(ordered, lanes[j])
				}
			}
			lanes = ordered
			break
		}
	}
	for i := range lanes {
		lane := lanes[i]
		result, err := concurrency.AcquireLaneSlot(ctx, lane.ID, lane.Concurrency)
		if err != nil {
			releaseAggregate()
			return nil, err
		}
		if result == nil || !result.Acquired {
			continue
		}
		account.ApplySelectedProxyLane(&lane)
		laneRelease := result.ReleaseFunc
		return &AcquireResult{
			Acquired:                   true,
			MaxConcurrency:             lane.Concurrency,
			MaxConcurrencySet:          true,
			AggregateMaxConcurrency:    legacyMaxConcurrency,
			AggregateMaxConcurrencySet: true,
			Lane:                       account.SelectedProxyLaneOrNil(),
			ReleaseFunc: func() {
				if laneRelease != nil {
					laneRelease()
					laneRelease = nil
				}
				releaseAggregate()
			},
		}, nil
	}

	// Preserve the preferred lane in the failed result so callers can build a
	// lane-specific wait plan without selecting a different egress mid-session.
	releaseAggregate()
	preferred := lanes[0]
	account.ApplySelectedProxyLane(&preferred)
	return &AcquireResult{
		Acquired:                   false,
		MaxConcurrency:             lanes[0].Concurrency,
		MaxConcurrencySet:          true,
		AggregateMaxConcurrency:    legacyMaxConcurrency,
		AggregateMaxConcurrencySet: true,
		Lane:                       account.SelectedProxyLaneOrNil(),
	}, nil
}

// selectAccountProxyLaneForWait returns the lane that should own a wait plan.
// It mirrors the acquisition ordering but does not touch Redis.
func selectAccountProxyLaneForWait(account *Account, sessionKey string) *AccountProxyLane {
	if account == nil || !account.HasProxyLanes() {
		return nil
	}
	lanes, err := orderedRuntimeAccountProxyLanesForSession(account, sessionKey, time.Now())
	if err != nil || len(lanes) == 0 {
		return nil
	}
	// Selecting a lane for a wait plan is only a routing/admission hint.  In
	// particular, an old cache has no lane namespace and must continue to use
	// the persisted aggregate account bucket.  ApplySelectedProxyLane projects
	// the lane onto the request account and (for historical forwarding callers)
	// also replaces Account.Concurrency with the lane cap; restore the account
	// value here so callers that pass account.Concurrency into admission do not
	// accidentally turn an aggregate limit into the first lane's limit.
	aggregateConcurrency := account.Concurrency
	account.ApplySelectedProxyLane(&lanes[0])
	account.Concurrency = aggregateConcurrency
	return account.SelectedProxyLaneOrNil()
}

// applyLaneToHydratedAccount preserves request-scoped lane selection when a
// scheduler snapshot is refreshed to restore credentials. Snapshot hydration
// intentionally returns a new Account pointer, so copy the lane explicitly.
//
// A lane ID in a request is only an affinity hint.  The hydrated snapshot is
// authoritative for ownership, status, cooldown and proxy relation.  Older
// code copied the selected lane unconditionally, which meant that deleting or
// pausing a lane could be undone by the next hydration and, worse, a direct
// lane could silently fall back to the account's legacy proxy.  Invalid
// selections are therefore cleared and the legacy proxy fields are blanked;
// callers can then re-run normal account/lane selection instead of forwarding
// over an egress that no longer belongs to the request.
func applyLaneToHydratedAccount(original, hydrated *Account) *Account {
	if hydrated == nil {
		return nil
	}
	if original == nil || original.SelectedProxyLane == nil {
		return hydrated
	}

	if applyHydratedSelectedProxyLane(original, hydrated) {
		return hydrated
	}

	// Do not leave the old account proxy reachable through AccountProxyURL's
	// legacy fallback after a stale lane was rejected.  Keep the account's
	// concurrency and credentials intact; the scheduler may select a fresh
	// lane/account on its next pass.
	hydrated.SelectedProxyLane = nil
	hydrated.ProxyID = nil
	hydrated.Proxy = nil
	return hydrated
}

// applyHydratedSelectedProxyLane validates and projects the selected lane
// against a freshly hydrated account.  It returns false when the lane was
// deleted, belongs to another account, is no longer schedulable, or its proxy
// relation changed/missing.  The helper is intentionally kept separate from
// applyLaneToHydratedAccount so scheduler callers that own an acquired slot
// can make a release/reselect decision without inspecting mutable fields.
func applyHydratedSelectedProxyLane(original, hydrated *Account) bool {
	if original == nil || hydrated == nil || original.SelectedProxyLane == nil || hydrated.ID <= 0 {
		return false
	}
	selected := original.SelectedProxyLane
	if selected.ID <= 0 || (selected.AccountID > 0 && selected.AccountID != hydrated.ID) {
		return false
	}

	now := time.Now()
	for i := range hydrated.ProxyLanes {
		lane := &hydrated.ProxyLanes[i]
		if lane.ID != selected.ID || lane.AccountID != hydrated.ID || !lane.IsSchedulableAt(now) {
			continue
		}

		transport := normalizeAccountProxyLaneTransport(lane.Transport)
		switch transport {
		case AccountProxyLaneTransportDirect:
			// A direct lane must never carry a proxy relation.  ApplySelected...
			// also clears the account-level proxy fields as a belt-and-suspenders
			// guard against stale snapshots.
			if lane.ProxyID != nil || lane.Proxy != nil {
				return false
			}
		case AccountProxyLaneTransportProxy:
			// The refreshed lane, not the stale request copy, owns the proxy
			// relation.  Requiring both the ID and hydrated object prevents a
			// deleted/reassigned proxy from being resurrected from the old lane.
			if lane.ProxyID == nil || *lane.ProxyID <= 0 || lane.Proxy == nil || lane.Proxy.ID != *lane.ProxyID {
				return false
			}
			if !lane.Proxy.IsActive() {
				return false
			}
			if lane.Proxy.IsExpired(now) {
				return false
			}
			if selected.ProxyID != nil && *selected.ProxyID != *lane.ProxyID {
				return false
			}
		default:
			return false
		}

		hydrated.ApplySelectedProxyLane(lane)
		return hydrated.SelectedProxyLane != nil && hydrated.SelectedProxyLane.ID == lane.ID
	}
	return false
}
