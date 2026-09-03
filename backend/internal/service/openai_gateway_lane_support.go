package service

import (
	"context"
	"time"
)

// openAIWaitingCountForAccount returns the queue depth for the admission
// domain that the account will actually use.  A lane-enabled account must not
// consult the legacy account queue: that queue is intentionally unused once
// lane-capable Redis admission is active.  The selected lane (when present)
// remains authoritative; otherwise use the same deterministic lane used by a
// fallback wait plan.
func (s *OpenAIGatewayService) openAIWaitingCountForAccount(ctx context.Context, account *Account) int {
	if s == nil || s.concurrencyService == nil || account == nil {
		return 0
	}
	if account.HasProxyLanes() && laneConcurrencySupported(s.concurrencyService) {
		lane := account.SelectedProxyLaneOrNil()
		if lane == nil {
			lane = selectAccountProxyLaneForWait(account, AccountProxyLaneSessionFromContext(ctx))
		}
		if lane == nil || lane.ID <= 0 {
			return 0
		}
		waiting, err := s.concurrencyService.GetLaneWaitingCount(ctx, lane.ID)
		if err != nil || waiting < 0 {
			return 0
		}
		return waiting
	}
	waiting, err := s.concurrencyService.GetAccountWaitingCount(ctx, account.ID)
	if err != nil || waiting < 0 {
		return 0
	}
	return waiting
}

// openAIWaitPlanForAccount mirrors the advanced scheduler's wait-plan helper
// for the legacy load-awareness entry point.  Keeping the capability check in
// one place prevents a lane-enabled account from accidentally falling back to
// the old account namespace during a rolling cache upgrade.
func (s *OpenAIGatewayService) openAIWaitPlanForAccount(
	ctx context.Context,
	account *Account,
	timeout time.Duration,
	maxWaiting int,
) (*AccountWaitPlan, bool) {
	if account == nil {
		return nil, false
	}
	plan := &AccountWaitPlan{
		AccountID:                  account.ID,
		MaxConcurrency:             account.Concurrency,
		AggregateMaxConcurrency:    account.Concurrency,
		AggregateMaxConcurrencySet: true,
		Timeout:                    timeout,
		MaxWaiting:                 maxWaiting,
	}
	if !account.HasProxyLanes() {
		return plan, true
	}
	lane := account.SelectedProxyLaneOrNil()
	if lane != nil {
		// SelectedProxyLane is request-local and may outlive a lane lifecycle
		// update.  Validate it against the current account before reusing it;
		// otherwise a paused/deleted lane could become a bogus wait target.
		lane = findSchedulableOpenAILane(account, lane.ID)
	}
	if lane == nil {
		lane = selectAccountProxyLaneForWait(account, AccountProxyLaneSessionFromContext(ctx))
	}
	if lane == nil {
		// All configured lanes are paused, disabled, cooling down, or otherwise
		// unschedulable.  Never issue an account-level wait plan in that case.
		return nil, false
	}
	account.ApplySelectedProxyLane(lane)
	if s == nil || s.concurrencyService == nil || !laneConcurrencySupported(s.concurrencyService) {
		// The lane transport still needs to be projected during a rolling
		// upgrade, but an old cache has no lane wait namespace.  Keep the
		// account-level wait plan so the legacy account slot remains enforced.
		return plan, true
	}
	plan.LaneID = lane.ID
	plan.MaxConcurrency = lane.Concurrency
	return plan, true
}

// refreshOpenAILaneLoadMap replaces account-level load statistics with a
// lane-aware aggregate for accounts that have independently scheduled lanes.
// The aggregate is deliberately conservative for admission:
//   - CurrentConcurrency/WaitingCount are sums for observability;
//   - LoadRate is based on total capped capacity, so one full lane does not
//     hide another idle lane;
//   - an eligible unlimited lane makes the account effectively available and
//     therefore reports LoadRate=0.
//
// Redis errors leave the original account-level snapshot untouched.  That is
// the same fail-open behavior as the existing load batch path and lets the
// atomic lane acquire operation remain the source of truth.
func (s *OpenAIGatewayService) refreshOpenAILaneLoadMap(
	ctx context.Context,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) {
	if s == nil || s.concurrencyService == nil || len(accounts) == 0 ||
		!laneConcurrencySupported(s.concurrencyService) || loadMap == nil {
		return
	}

	now := time.Now()
	laneIDs := make([]int64, 0)
	lanesByAccount := make(map[int64][]AccountProxyLane)
	seenLane := make(map[int64]struct{})
	for _, account := range accounts {
		if account == nil || !account.HasProxyLanes() {
			continue
		}
		eligible := FilterSchedulableAccountProxyLanes(account.ProxyLanes, now)
		if len(eligible) == 0 {
			// Mark a lane-enabled account with no healthy egress as full.  The
			// eventual wait-plan path still performs the authoritative status
			// check, but this avoids needlessly probing a dead account first.
			loadMap[account.ID] = &AccountLoadInfo{AccountID: account.ID, LoadRate: 100}
			continue
		}
		lanesByAccount[account.ID] = eligible
		for _, lane := range eligible {
			if lane.ID <= 0 {
				continue
			}
			if _, ok := seenLane[lane.ID]; ok {
				continue
			}
			seenLane[lane.ID] = struct{}{}
			laneIDs = append(laneIDs, lane.ID)
		}
	}
	if len(laneIDs) == 0 {
		return
	}

	counts, err := s.concurrencyService.GetLaneConcurrencyBatch(ctx, laneIDs)
	if err != nil {
		// The legacy account bucket is not authoritative for lane-enabled
		// accounts.  Leaving a stale LoadRate=100 snapshot here would prevent
		// tryAcquireFromLoadMap from ever reaching the atomic lane acquire and
		// would turn a transient Redis read failure into a false outage.  Mark
		// these accounts as unknown/available so acquisition remains the final
		// arbiter; preserve legacy (non-lane) snapshots untouched.
		for accountID := range lanesByAccount {
			loadMap[accountID] = &AccountLoadInfo{AccountID: accountID, LoadRate: 0}
		}
		return
	}

	for accountID, lanes := range lanesByAccount {
		var current, waiting, capacity int
		unlimited := false
		for _, lane := range lanes {
			if lane.ID <= 0 {
				continue
			}
			count := counts[lane.ID]
			if count < 0 {
				count = 0
			}
			current += count
			if laneWaiting, waitErr := s.concurrencyService.GetLaneWaitingCount(ctx, lane.ID); waitErr == nil && laneWaiting > 0 {
				waiting += laneWaiting
			}
			if lane.Concurrency <= 0 {
				unlimited = true
				continue
			}
			capacity += lane.Concurrency
		}

		loadRate := 0
		if !unlimited && capacity > 0 {
			loadRate = (current + waiting) * 100 / capacity
			if loadRate < 0 {
				loadRate = 0
			}
		}
		loadMap[accountID] = &AccountLoadInfo{
			AccountID:          accountID,
			CurrentConcurrency: current,
			WaitingCount:       waiting,
			LoadRate:           loadRate,
		}
	}
}
