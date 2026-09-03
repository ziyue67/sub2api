package service

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"time"
)

// laneStickyCache returns the optional lane-aware extension without widening
// GatewayCache.  A nil result is expected during rolling upgrades where the
// process still talks to an adapter that only implements account sticky keys.
func laneStickyCache(cache GatewayCache) LaneStickyCache {
	if cache == nil {
		return nil
	}
	laneCache, _ := cache.(LaneStickyCache)
	return laneCache
}

// normalizeLaneStickyModel gives model-less requests their own stable bucket.
// The wildcard is intentionally not treated as a cross-model binding: callers
// that know a model always use that model in the key.
func normalizeLaneStickyModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "*"
	}
	return model
}

func laneStickyCacheModel(model string) string { return normalizeLaneStickyModel(model) }

func (s *OpenAIGatewayService) getStickySessionLane(ctx context.Context, groupID *int64, model, sessionHash string) (LaneStickyBinding, error) {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return LaneStickyBinding{}, ErrLaneStickySessionNotFound
	}
	cache := laneStickyCache(s.cache)
	if cache == nil {
		return LaneStickyBinding{}, ErrLaneStickySessionNotFound
	}
	return cache.GetSessionLane(ctx, derefGroupID(groupID), laneStickyCacheModel(model), strings.TrimSpace(sessionHash))
}

func (s *OpenAIGatewayService) setStickySessionLane(ctx context.Context, groupID *int64, model, sessionHash string, binding LaneStickyBinding, ttl time.Duration) error {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	cache := laneStickyCache(s.cache)
	if cache == nil {
		return nil
	}
	return cache.SetSessionLane(ctx, derefGroupID(groupID), laneStickyCacheModel(model), strings.TrimSpace(sessionHash), binding, ttl)
}

func (s *OpenAIGatewayService) refreshStickySessionLaneTTL(ctx context.Context, groupID *int64, model, sessionHash string, ttl time.Duration) error {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	cache := laneStickyCache(s.cache)
	if cache == nil {
		return nil
	}
	return cache.RefreshSessionLaneTTL(ctx, derefGroupID(groupID), laneStickyCacheModel(model), strings.TrimSpace(sessionHash), ttl)
}

func (s *OpenAIGatewayService) deleteStickySessionLane(ctx context.Context, groupID *int64, model, sessionHash string) error {
	if s == nil || s.cache == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	cache := laneStickyCache(s.cache)
	if cache == nil {
		return nil
	}
	return cache.DeleteSessionLane(ctx, derefGroupID(groupID), laneStickyCacheModel(model), strings.TrimSpace(sessionHash))
}

// bindSelectedLaneSticky records the lane actually selected for a request.
// It is deliberately best-effort: the Redis extension is an affinity hint and
// must never turn a successful upstream request into a gateway error.
func (s *OpenAIGatewayService) bindSelectedLaneSticky(ctx context.Context, groupID *int64, model, sessionHash string, account *Account, lane *AccountProxyLane, ttl time.Duration) {
	if account == nil || lane == nil || lane.ID <= 0 || account.ID <= 0 ||
		!account.HasProxyLanes() || lane.AccountID != account.ID {
		return
	}
	// Never persist a lane that is not present in the request's account
	// snapshot.  The scheduler normally hands us a lane copied from
	// account.ProxyLanes, but this guard protects the Redis affinity key when a
	// stale/corrupt selection result (or a test adapter) supplies an ID from a
	// different account.  A lane ID by itself is not a sufficient trust key.
	owned := false
	for i := range account.ProxyLanes {
		candidate := &account.ProxyLanes[i]
		if candidate.ID == lane.ID && candidate.AccountID == account.ID {
			owned = true
			break
		}
	}
	if !owned {
		return
	}
	if err := s.setStickySessionLane(ctx, groupID, model, sessionHash, LaneStickyBinding{
		AccountID: account.ID,
		LaneID:    lane.ID,
	}, ttl); err != nil {
		slogLaneStickyFailure("set", err, account.ID, lane.ID)
	}
}

// bindOpenAILaneStickyDuringSelection mirrors the legacy eager-binding gate.
// Profit-controlled and guardian-parent selections intentionally defer/avoid
// rewriting sticky state; the terminal admission path can call
// bindSelectedLaneSticky explicitly once it knows the final lane.
func (s *OpenAIGatewayService) bindOpenAILaneStickyDuringSelection(ctx context.Context, groupID *int64, model, sessionHash string, account *Account, lane *AccountProxyLane, ttl time.Duration) {
	if gatewayProfitControlGateActive(ctx) || preserveOpenAIGuardianParentBinding(ctx, sessionHash) {
		return
	}
	s.bindSelectedLaneSticky(ctx, groupID, model, sessionHash, account, lane, ttl)
}

// applyStickyLaneBinding projects a persisted lane onto an account snapshot.
// It returns false when the account does not own the lane or the lane is not
// currently schedulable.  In either case callers should fall back to normal
// account/lane scheduling, not to a mismatched proxy.
func applyStickyLaneBinding(account *Account, binding LaneStickyBinding, now time.Time) bool {
	if account == nil || binding.AccountID <= 0 || binding.LaneID <= 0 || account.ID != binding.AccountID || !account.HasProxyLanes() {
		return false
	}
	for i := range account.ProxyLanes {
		lane := &account.ProxyLanes[i]
		// Lane IDs are expected to be globally unique, but keep the parent
		// relation authoritative as well.  A stale/corrupt scheduler snapshot
		// can otherwise project a lane row that happens to share the binding ID
		// while belonging to another account, causing the request to use an
		// egress it was never admitted against.
		if lane.ID != binding.LaneID || lane.AccountID != account.ID || !lane.IsSchedulableAt(now) {
			continue
		}
		// A sticky lane is only an affinity hint at this stage.  Keep the
		// account-level concurrency value intact until admission decides which
		// namespace owns the reservation; old cache adapters still use that
		// aggregate account bucket even though transport is lane-specific.
		aggregateConcurrency := account.Concurrency
		account.ApplySelectedProxyLane(lane)
		account.Concurrency = aggregateConcurrency
		return true
	}
	return false
}

// IsLaneStickyTransportFailure is intentionally narrow.  Lane affinity is
// cleared only when the selected egress cannot establish/maintain a transport
// connection; provider-level 4xx/5xx business responses must not cause an IP
// switch.  The status argument is retained for call-site symmetry, but a
// status alone is deliberately insufficient evidence: providers commonly use
// 502/503/504 for application/business outages too.
func IsLaneStickyTransportFailure(err error, statusCode int) bool {
	if err != nil {
		// Request cancellation/deadline is caused by the client or gateway
		// budget, not by the selected egress.  In particular,
		// context.DeadlineExceeded implements net.Error; checking it before the
		// net.Error branch prevents a local wait timeout from evicting a healthy
		// sticky lane.  Upstream HTTP timeouts are wrapped by the forwarding
		// layer with OpenAITransportFailureReason and still take the transport
		// path below when appropriate.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return false
		}
		var netErr net.Error
		if errors.As(err, &netErr) {
			return true
		}
		// UpstreamFailoverError carries the provider status separately from the
		// failure classification.  A bare 502/503 is not enough evidence to
		// move an IP (providers also use those codes for business outages); only
		// reasons explicitly naming the connection/transport chain qualify.
		var failoverErr *UpstreamFailoverError
		if errors.As(err, &failoverErr) && failoverErr != nil {
			reason := strings.ToLower(string(failoverErr.Reason))
			for _, marker := range []string{"transport", "connection", "dial", "proxy", "tls", "network", "timeout"} {
				if strings.Contains(reason, marker) {
					return true
				}
			}
		}
		msg := strings.ToLower(err.Error())
		for _, marker := range []string{
			"connection refused", "connection reset", "broken pipe", "unexpected eof",
			"no such host", "dial tcp", "tls handshake timeout", "network is unreachable",
			"i/o timeout", "upstream timeout", "transport error",
		} {
			if strings.Contains(msg, marker) {
				return true
			}
		}
	}
	// Do not clear affinity on a bare HTTP status.  Keep statusCode in the
	// signature for forward compatibility and to make the deliberate choice
	// explicit at the call site.
	_ = statusCode
	return false
}

// ClearLaneStickyOnTransportFailure is the public, guarded invalidation hook
// for forwarding paths.  It never touches the legacy account sticky key.
func (s *OpenAIGatewayService) ClearLaneStickyOnTransportFailure(ctx context.Context, groupID *int64, model, sessionHash string, err error, statusCode int) {
	if !IsLaneStickyTransportFailure(err, statusCode) {
		return
	}
	if deleteErr := s.deleteStickySessionLane(ctx, groupID, model, sessionHash); deleteErr != nil {
		slogLaneStickyFailure("delete", deleteErr, 0, 0)
	}
}

// slogLaneStickyFailure is kept tiny and dependency-free so cache failures do
// not obscure the original upstream result.  The standard logger is used by
// callers that want to inspect Redis extension health without leaking keys.
func slogLaneStickyFailure(operation string, err error, accountID, laneID int64) {
	if err == nil {
		return
	}
	slog.Debug("lane_sticky_cache_operation_failed",
		"operation", operation,
		"account_id", accountID,
		"lane_id", laneID,
		"error", err,
	)
}
