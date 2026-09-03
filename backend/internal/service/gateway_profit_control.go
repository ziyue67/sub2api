package service

import (
	"context"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// withGatewayProfitControlGate installs the gate only for explicitly marked
// token requests. This keeps media, metadata, and models-list paths outside
// the profit-control surface by construction.
func (s *GatewayService) withGatewayProfitControlGate(ctx context.Context, groupID *int64) context.Context {
	if _, ok := gatewayTokenRequestPricingAtFromContext(ctx); !ok || groupID == nil || *groupID <= 0 {
		return ctx
	}
	if existing, ok := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate); ok && existing != nil && existing.groupID == *groupID {
		return ctx
	}

	group, err := s.resolveProfitControlGroup(ctx, *groupID)
	if err != nil {
		slog.Warn("profit_control_group_load_failed", "group_id", *groupID, "error", err)
		return s.clearForeignProfitControlGate(ctx, groupID)
	}
	if group == nil || !group.ProfitControlEnabled || !profitControlPlatformSupported(group.Platform) {
		return s.clearForeignProfitControlGate(ctx, groupID)
	}

	pricingAt, _ := gatewayTokenRequestPricingAtFromContext(ctx)
	billingGroup := gatewayTokenRequestBillingGroupFromContext(ctx)
	if billingGroup == nil {
		if ctxGroup, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(ctxGroup) {
			billingGroup = ctxGroup
		} else {
			billingGroup = group
		}
	}

	downstream := billingGroup.RateMultiplier
	if userID, _ := ctx.Value(ctxkey.UserID).(int64); userID > 0 {
		downstream = s.ResolveUserGroupRateMultiplier(ctx, userID, billingGroup.ID, billingGroup.RateMultiplier)
	}
	downstream *= billingGroup.PeakMultiplierAt(pricingAt)
	threshold := clampProfitControlThreshold(downstream * (1 - group.ProfitMinMargin - group.ProfitSafetyBuffer))

	gate := &openAIProfitControlGate{
		groupID:   group.ID,
		platform:  group.Platform,
		threshold: threshold,
		pricingAt: pricingAt,
	}
	openAIProfitControlObserverInstance.recordInstall(gate.groupID, gate.platform, gate.threshold)
	return context.WithValue(ctx, openAIProfitControlGateCtxKey{}, gate)
}

func (s *GatewayService) clearForeignProfitControlGate(ctx context.Context, groupID *int64) context.Context {
	existing, ok := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	if !ok || existing == nil || groupID == nil || existing.groupID == *groupID {
		return ctx
	}
	return context.WithValue(ctx, openAIProfitControlGateCtxKey{}, (*openAIProfitControlGate)(nil))
}

func (s *GatewayService) resolveProfitControlGroup(ctx context.Context, groupID int64) (*Group, error) {
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == groupID {
		return group, nil
	}
	if s.schedulerSnapshot != nil {
		// Lite 读取：门只用平台/倍率/利润/高峰字段，不需要账号计数聚合。
		return s.schedulerSnapshot.GetGroupByIDLite(ctx, groupID)
	}
	return s.resolveGroupByID(ctx, groupID)
}

// GatewayProfitControlVetoLatest performs the terminal post-slot check against
// the latest scheduler snapshot. Legacy/profit-only snapshot read failures are
// deliberately fail-open to preserve availability, but a concrete proxy-lane
// affinity fails closed when its lifecycle cannot be authoritatively checked.
func (s *GatewayService) GatewayProfitControlVetoLatest(ctx context.Context, selected *Account) (*Account, bool, string) {
	return profitControlVetoLatest(ctx, selected, s.schedulerSnapshot)
}

func profitControlVetoLatest(ctx context.Context, selected *Account, snapshot *SchedulerSnapshotService) (*Account, bool, string) {
	if ctx == nil {
		ctx = context.Background()
	}
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	if selected == nil {
		return selected, false, ""
	}
	latest := selected
	// A lane is part of the request's transport contract, not merely a profit
	// control concern.  Refresh lane-enabled selections even when no profit
	// gate is installed so a lane deleted/paused between scheduling and
	// forwarding cannot silently fall back to Account.Proxy (or direct).  The
	// snapshot read remains gated for legacy accounts to preserve the historical
	// no-extra-read behavior on non-lane traffic.
	refreshLane := selected.HasProxyLanes() || selected.SelectedProxyLane != nil
	if snapshot != nil && (gate != nil || refreshLane) {
		// A concrete lane is an egress/lifecycle contract.  The scheduler cache
		// can contain the exact same (stale) lane row and account UpdatedAt as the
		// request-local selection because lane writes have their own updated_at
		// column and do not necessarily advance accounts.updated_at.  Therefore a
		// terminal check must bypass Redis and read the authoritative account row
		// whenever a concrete lane affinity is present.  Otherwise a deleted,
		// paused, or rebound lane could pass structural validation and be forwarded
		// over an old proxy.  Accounts without a concrete lane keep the cheaper
		// cache-first/profit-only path below.
		authoritative := refreshLane && selected.SelectedProxyLane != nil
		var refreshed *Account
		var err error
		if authoritative {
			refreshed, err = snapshot.getAccountAuthoritative(ctx, selected.ID)
		} else {
			refreshed, err = snapshot.GetAccount(ctx, selected.ID)
		}
		if err != nil || refreshed == nil {
			// GetAccount already falls back to the repository on a cache miss.
			// A concrete lane affinity cannot safely continue when that
			// authoritative read failed: the old request-local lane may have
			// been deleted or paused.  Legacy accounts and lane-enabled accounts
			// without a concrete affinity retain the historical profit-control
			// fail-open behavior below.
			if refreshLane && selected.SelectedProxyLane != nil {
				slog.Warn("proxy_lane_account_refresh_unavailable", "account_id", selected.ID, "error", err)
				return selected, true, openAIProfitFilterReasonLaneUnavailable
			}
			// A snapshot refresh is best effort for profit checks.  Keep the
			// existing account on read failure, but avoid dereferencing a nil gate
			// on the lane-only refresh path.
			if gate != nil {
				slog.Warn("profit_control_account_refresh_failed", "group_id", gate.groupID, "platform", gate.platform, "account_id", selected.ID, "error", err)
				openAIProfitControlObserverInstance.recordRefreshFailure(gate.groupID, gate.platform, gate.threshold)
			} else {
				slog.Warn("proxy_lane_account_refresh_failed", "account_id", selected.ID, "error", err)
			}
		} else {
			freshEnough := !refreshed.UpdatedAt.Before(selected.UpdatedAt)

			if refreshLane {
				// Scheduler payloads used by some rolling-upgrade readers are
				// intentionally compact: they contain lane IDs but omit the
				// referenced Proxy credentials.  A structural lane match is not
				// enough here because AccountProxyURL would interpret ProxyID-only
				// metadata as direct traffic.  Reconcile the request-local lane
				// against the hydrated row and, when needed, use the authoritative
				// repository before applying the terminal freshness check.
				if selected.SelectedProxyLane != nil {
					// In the concrete-lane path `refreshed` already came from the
					// authoritative repository.  Do not issue a second fallback query
					// when that row is malformed/paused; a second read could observe a
					// different state and makes the terminal decision non-deterministic.
					// Cache-first (non-authoritative) callers retain the repository
					// hydration fallback for compact payloads.
					laneRepo := snapshot.accountRepo
					if authoritative {
						laneRepo = nil
					}
					candidate, hydrateErr := ensureSelectedAccountProxyLaneHydrated(ctx, selected, refreshed, laneRepo)
					if hydrateErr != nil {
						slog.Warn("proxy_lane_terminal_hydration_failed", "account_id", selected.ID, "error", hydrateErr)
						// Return a scrubbed candidate rather than the stale request
						// object.  The caller logs/serializes the returned account on
						// failure; leaving SelectedProxyLane and the old Proxy reachable
						// would make a fail-closed veto look like a usable direct/proxy
						// route to downstream retry code.
						scrubbed := applyLaneToHydratedAccount(selected, refreshed)
						if scrubbed == nil {
							scrubbed = selected
						}
						return scrubbed, true, openAIProfitFilterReasonLaneUnavailable
					}
					refreshed = candidate
				}
				// A lane-enabled account must retain the exact lane selected before
				// hydration.  applyLaneToHydratedAccount clears all legacy proxy
				// fields when that lane no longer exists/is schedulable, and the
				// explicit check below turns that state into a fail-closed signal for
				// handlers instead of allowing a direct fallback.
				candidate := applyLaneToHydratedAccount(selected, refreshed)
				if selected.SelectedProxyLane != nil &&
					(candidate == nil || candidate.SelectedProxyLane == nil ||
						candidate.SelectedProxyLane.ID != selected.SelectedProxyLane.ID) {
					return candidate, true, openAIProfitFilterReasonLaneUnavailable
				}
				// A cache payload older than selected is useful only for lane
				// validation.  Keep the scheduler's newer account object unless the
				// direct repository read above established an authoritative value.
				if freshEnough || authoritative {
					latest = candidate
				}
			} else if freshEnough {
				// 选号路径可能已做过 DB recheck，selected 比缓存快照更新鲜；只有
				// 快照不落后时才替换，避免终检把新鲜账号换回较旧的缓存对象。
				latest = refreshed
			}
		}
	}
	if gate == nil {
		return latest, false, ""
	}
	vetoed, reason := openAIProfitControlVetoReason(ctx, latest)
	return latest, vetoed, reason
}

func (s *GatewayService) isGatewayAccountProfitEligible(ctx context.Context, account *Account) bool {
	vetoed, _ := openAIProfitControlVetoReason(ctx, account)
	return !vetoed
}

func gatewayProfitControlGateActive(ctx context.Context) bool {
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	return gate != nil
}
