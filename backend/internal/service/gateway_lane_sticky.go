package service

import (
	"context"
	"strings"
	"time"
)

// The OpenAI gateway has its own scheduler type, while Anthropic/Gemini and
// the generic gateway use GatewayService.  Keep the lane-sticky cache adapter
// deliberately small and optional for both services so a rolling upgrade can
// continue to talk to an older Redis adapter.

func (s *GatewayService) getGatewayStickySessionLane(ctx context.Context, groupID *int64, model, sessionHash string) (LaneStickyBinding, error) {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return LaneStickyBinding{}, ErrLaneStickySessionNotFound
	}
	cache := laneStickyCache(s.cache)
	if cache == nil {
		return LaneStickyBinding{}, ErrLaneStickySessionNotFound
	}
	return cache.GetSessionLane(ctx, derefGroupID(groupID), laneStickyCacheModel(model), strings.TrimSpace(sessionHash))
}

func (s *GatewayService) setGatewayStickySessionLane(ctx context.Context, groupID *int64, model, sessionHash string, binding LaneStickyBinding, ttl time.Duration) error {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	cache := laneStickyCache(s.cache)
	if cache == nil {
		return nil
	}
	return cache.SetSessionLane(ctx, derefGroupID(groupID), laneStickyCacheModel(model), strings.TrimSpace(sessionHash), binding, ttl)
}

// bindGatewaySelectedLaneSticky records the lane actually admitted by the
// generic scheduler.  It is best effort: affinity is an optimization and a
// Redis extension failure must never turn a successful request into an error.
func (s *GatewayService) bindGatewaySelectedLaneSticky(ctx context.Context, groupID *int64, model, sessionHash string, account *Account, lane *AccountProxyLane, ttl time.Duration) {
	if account == nil || lane == nil || account.ID <= 0 || lane.ID <= 0 {
		return
	}
	if err := s.setGatewayStickySessionLane(ctx, groupID, model, sessionHash, LaneStickyBinding{
		AccountID: account.ID,
		LaneID:    lane.ID,
	}, ttl); err != nil {
		slogLaneStickyFailure("set", err, account.ID, lane.ID)
	}
}

func (s *GatewayService) bindGatewayLaneStickyDuringSelection(ctx context.Context, groupID *int64, model, sessionHash string, account *Account, lane *AccountProxyLane) {
	if gatewayProfitControlGateActive(ctx) {
		return
	}
	s.bindGatewaySelectedLaneSticky(ctx, groupID, model, sessionHash, account, lane, stickySessionTTL)
}

// gatewayWaitingCountForAccount reads the queue that will actually arbitrate
// this account.  Lane-enabled accounts must never consult the old aggregate
// account queue, otherwise an unrelated legacy counter can reject a healthy
// lane (or allow an already saturated one).
func (s *GatewayService) gatewayWaitingCountForAccount(ctx context.Context, account *Account) int {
	if s == nil || s.concurrencyService == nil || account == nil {
		return 0
	}
	if account.HasProxyLanes() && laneConcurrencySupported(s.concurrencyService) {
		lane := account.SelectedProxyLaneOrNil()
		if lane != nil {
			lane = runtimeLaneByID(account, lane.ID, time.Now())
		}
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

// gatewayWaitPlanForAccount builds a queue plan for the exact admission
// namespace used by the account. Lane-capable accounts carry both the lane
// ceiling and the parent aggregate ceiling; the handler acquires both before
// forwarding. If the cache extension is absent we retain the legacy account
// queue but still project a healthy lane onto the request so egress routing
// remains correct during rollout.
func (s *GatewayService) gatewayWaitPlanForAccount(ctx context.Context, account *Account, timeout time.Duration, maxWaiting int) (*AccountWaitPlan, bool) {
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
	// SelectedProxyLane is only a request-local affinity hint.  It may have
	// been copied from a sticky key just before a lane was paused/deleted.  Do
	// not build a wait plan for that stale ID: waiting on a dead lane can strand
	// the request even when another lane on the same account is healthy.
	lane := account.SelectedProxyLaneOrNil()
	if lane != nil {
		lane = runtimeLaneByID(account, lane.ID, time.Now())
	}
	if lane == nil {
		lane = selectAccountProxyLaneForWait(account, AccountProxyLaneSessionFromContext(ctx))
	}
	if lane == nil {
		return nil, false
	}
	aggregateConcurrency := account.Concurrency
	account.ApplySelectedProxyLane(lane)
	// ApplySelectedProxyLane projects the lane cap onto Concurrency for
	// forwarding.  The legacy account wait namespace still owns the aggregate
	// limit during a rolling cache upgrade, so restore the account value before
	// returning to callers that may reuse this request-local object.
	account.Concurrency = aggregateConcurrency
	if s != nil && s.concurrencyService != nil && laneConcurrencySupported(s.concurrencyService) {
		plan.LaneID = lane.ID
		plan.MaxConcurrency = lane.Concurrency
	}
	return plan, true
}

// refreshGatewayLaneLoadMap replaces stale account-level load metadata with
// an aggregate over independently schedulable lanes.  The aggregate is used
// only as a pre-filter; the atomic lane admission remains authoritative.
func refreshGatewayLaneLoadMap(ctx context.Context, concurrency *ConcurrencyService, accounts []*Account, loadMap map[int64]*AccountLoadInfo) {
	if concurrency == nil || loadMap == nil || len(accounts) == 0 || !laneConcurrencySupported(concurrency) {
		return
	}
	now := time.Now()
	laneIDs := make([]int64, 0)
	lanesByAccount := make(map[int64][]AccountProxyLane)
	seen := make(map[int64]struct{})
	for _, account := range accounts {
		if account == nil || !account.HasProxyLanes() {
			continue
		}
		eligible := FilterSchedulableAccountProxyLanes(account.ProxyLanes, now)
		if len(eligible) == 0 {
			loadMap[account.ID] = &AccountLoadInfo{AccountID: account.ID, LoadRate: 100}
			continue
		}
		lanesByAccount[account.ID] = eligible
		for _, lane := range eligible {
			if lane.ID <= 0 {
				continue
			}
			if _, ok := seen[lane.ID]; ok {
				continue
			}
			seen[lane.ID] = struct{}{}
			laneIDs = append(laneIDs, lane.ID)
		}
	}
	if len(laneIDs) == 0 {
		return
	}
	counts, err := concurrency.GetLaneConcurrencyBatch(ctx, laneIDs)
	if err != nil {
		// The legacy account bucket is not authoritative once lane scheduling is
		// enabled.  Do not leave a stale 100% account snapshot in place: that
		// would filter the account before the atomic lane acquire gets a chance
		// to make the real decision.  Mark lane accounts as available/unknown so
		// a transient Redis read error remains fail-open for admission.
		for accountID := range lanesByAccount {
			loadMap[accountID] = &AccountLoadInfo{AccountID: accountID, LoadRate: 0}
		}
		return
	}
	for accountID, lanes := range lanesByAccount {
		current, waiting, capacity := 0, 0, 0
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
			if laneWaiting, waitErr := concurrency.GetLaneWaitingCount(ctx, lane.ID); waitErr == nil && laneWaiting > 0 {
				waiting += laneWaiting
			}
			if lane.Concurrency <= 0 {
				unlimited = true
			} else {
				capacity += lane.Concurrency
			}
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

// projectGatewayLaneSelection hydrates a generic gateway account and projects
// one valid lane onto the request copy.  A lane binding is only an affinity
// hint; ownership/status/proxy relation always come from the freshly hydrated
// account.  If no valid lane exists, fail closed instead of silently inheriting
// the account's legacy proxy.
func (s *GatewayService) projectGatewayLaneSelection(ctx context.Context, groupID *int64, model, sessionHash string, account *Account) (*Account, error) {
	if account == nil {
		return nil, nil
	}
	hydrated, err := s.hydrateSelectedAccount(ctx, account)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, ErrNoSchedulableAccountProxyLane
	}
	// Keep the request-local affinity account separate from the refreshed
	// snapshot. Scheduler bucket payloads may contain lane IDs without proxy
	// credentials; the finalizer below obtains an authoritative account when
	// that compact shape is encountered instead of allowing a direct fallback.
	finalize := func(candidate *Account) (*Account, error) {
		repo := s.accountRepo
		if repo == nil && s.schedulerSnapshot != nil {
			repo = s.schedulerSnapshot.accountRepo
		}
		return ensureSelectedAccountProxyLaneHydratedAuthoritative(ctx, account, candidate, repo)
	}
	if !hydrated.HasProxyLanes() {
		// A stale/full-account cache can lose the lane collection entirely. If
		// the selected account opted into lanes, choose the request-local lane
		// from that account and let the finalizer reload the authoritative row;
		// never treat the missing collection as a legacy account.
		if account.HasProxyLanes() {
			if account.SelectedProxyLane == nil {
				_ = selectAccountProxyLaneForWait(account, sessionHash)
			}
			validated, validateErr := finalize(hydrated)
			if validateErr != nil || validated == nil || !validated.HasProxyLanes() || validated.SelectedProxyLane == nil {
				if validateErr != nil {
					return nil, validateErr
				}
				return nil, ErrNoSchedulableAccountProxyLane
			}
			s.bindGatewayLaneStickyDuringSelection(ctx, groupID, model, sessionHash, validated, validated.SelectedProxyLane)
			return validated, nil
		}
		return hydrated, nil
	}

	// Preserve an already selected lane only after validating it against the
	// refreshed account.  This is used by callers that acquired a lane before
	// hydration; a stale/deleted lane must not fall back to direct/legacy proxy.
	if account.SelectedProxyLane != nil {
		validated, validateErr := finalize(hydrated)
		if validateErr != nil {
			return nil, validateErr
		}
		s.bindGatewayLaneStickyDuringSelection(ctx, groupID, model, sessionHash, validated, validated.SelectedProxyLane)
		return validated, nil
	}

	// A persisted lane binding is preferred when it belongs to this account and
	// is still schedulable.  Invalid bindings are removed, but the account may
	// still be selected again using the deterministic lane hash below.
	if binding, bindErr := s.getGatewayStickySessionLane(ctx, groupID, model, sessionHash); bindErr == nil && binding.AccountID == hydrated.ID {
		if applyStickyLaneBinding(hydrated, binding, time.Now()) {
			validated, validateErr := finalize(hydrated)
			if validateErr != nil {
				return nil, validateErr
			}
			s.bindGatewayLaneStickyDuringSelection(ctx, groupID, model, sessionHash, validated, validated.SelectedProxyLane)
			return validated, nil
		}
		// Keep the stale affinity key until its normal TTL expires.  Lane
		// lifecycle changes are not transport failures; clearing here would make
		// a business/status change look like an IP health event.
	}

	lane := selectAccountProxyLaneForWait(hydrated, sessionHash)
	if lane == nil {
		return nil, ErrNoSchedulableAccountProxyLane
	}
	// selectAccountProxyLaneForWait mutates hydrated with the chosen lane. The
	// finalizer verifies that projection and, when the scheduler payload is
	// compact (ProxyID only), reloads the full lane/proxy relation from the DB.
	validated, validateErr := finalize(hydrated)
	if validateErr != nil {
		return nil, validateErr
	}
	s.bindGatewayLaneStickyDuringSelection(ctx, groupID, model, sessionHash, validated, validated.SelectedProxyLane)
	return validated, nil
}

// prepareGatewayLaneHint applies a persisted lane binding (or the stable
// session hash fallback) to an account snapshot before slot admission.  It
// returns whether a configured lane set had a usable egress.  The account
// object is intentionally mutated in place because it is request-local.
func (s *GatewayService) prepareGatewayLaneHint(ctx context.Context, groupID *int64, model, sessionHash string, account *Account, binding LaneStickyBinding) bool {
	if account == nil || !account.HasProxyLanes() {
		return true
	}
	if binding.LaneID <= 0 && strings.TrimSpace(sessionHash) != "" {
		if persisted, err := s.getGatewayStickySessionLane(ctx, groupID, model, sessionHash); err == nil {
			binding = persisted
		}
	}
	if binding.AccountID == account.ID && binding.LaneID > 0 {
		if applyStickyLaneBinding(account, binding, time.Now()) {
			return true
		}
		// A stale binding must not survive a lane delete/pause/rebind.  The
		// account itself may still have another healthy lane, so continue with
		// deterministic selection after deleting only the lane key.
		// Do not clear on a lifecycle/status miss.  Only the explicit transport
		// failure path is allowed to invalidate lane affinity.
	}
	return selectAccountProxyLaneForWait(account, sessionHash) != nil
}
