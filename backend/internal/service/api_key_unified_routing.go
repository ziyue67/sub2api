package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	APIKeyRoutingLowestRate = "lowest_rate"
	APIKeyRoutingPriority   = "priority_weighted"
)

// Request-local admission inputs; never serialized into an auth snapshot.
type apiKeyRequestGroupRouting struct {
	models        []string
	kind          string
	subscriptions *SubscriptionService
	accepts       func(*APIKey) bool
	checkBilling  bool
	excluded      map[int64]struct{}
}

func cloneAPIKeyGroupRouting(p *domain.APIKeyGroupRouting) *domain.APIKeyGroupRouting {
	if p == nil {
		return nil
	}
	copy := *p
	copy.Routes = append([]domain.APIKeyGroupRoute(nil), p.Routes...)
	return &copy
}

func normalizeAPIKeyGroupRouting(p *domain.APIKeyGroupRouting) (*domain.APIKeyGroupRouting, error) {
	if p == nil {
		return nil, nil
	}
	if p.Strategy != APIKeyRoutingLowestRate && p.Strategy != APIKeyRoutingPriority {
		return nil, ErrAPIKeyGroupRouteInvalid
	}
	if len(p.Routes) == 0 || len(p.Routes) > maxAPIKeyGroups {
		return nil, ErrAPIKeyGroupRouteInvalid
	}
	copy := cloneAPIKeyGroupRouting(p)
	seen := map[int64]bool{}
	for _, r := range copy.Routes {
		if r.GroupID <= 0 || seen[r.GroupID] || r.Priority < 1 || r.Priority > 1000000 || r.Weight < 1 || r.Weight > 1000000 || r.CooldownSeconds < 0 || r.CooldownSeconds > 86400 {
			return nil, ErrAPIKeyGroupRouteInvalid
		}
		seen[r.GroupID] = true
	}
	return copy, nil
}

func routingGroupIDs(p *domain.APIKeyGroupRouting, enabledOnly bool) []int64 {
	var ids []int64
	for _, r := range p.Routes {
		if !enabledOnly || r.Enabled {
			ids = append(ids, r.GroupID)
		}
	}
	return ids
}

// Projections are for old readers/list filters only. The policy remains the
// authority, including when every route is disabled.
func applyAPIKeyGroupRouting(key *APIKey, p *domain.APIKeyGroupRouting) {
	key.GroupRouting = cloneAPIKeyGroupRouting(p)
	key.GroupRoutes = append([]domain.APIKeyGroupRoute(nil), p.Routes...)
	key.GroupIDs = routingGroupIDs(p, false)
	key.GroupID = &key.GroupIDs[0]
	key.Group = nil
}

func routingFieldsConflict(primary *int64, ids []int64, routes []domain.APIKeyGroupRoute) bool {
	return primary != nil || len(ids) > 0 || len(routes) > 0
}

var errAPIKeyRoutingConflict = infraerrors.BadRequest("API_KEY_ROUTING_CONFLICT", "Use group_routing alone; do not combine independent group selection settings")

// The same policy filters first attempts and failover. For legacy mixed keys,
// route restrictions take precedence over the old cheapest-group selector.
func (s *APIKeyService) requestGroupRoute(ctx context.Context, key *APIKey, id int64) (domain.APIKeyGroupRoute, bool, error) {
	route := domain.APIKeyGroupRoute{GroupID: id, Priority: 1, Weight: 1, Enabled: true}
	routes := key.GroupRoutes
	if key.GroupRouting != nil {
		routes = key.GroupRouting.Routes
	}
	if len(routes) > 0 {
		found := false
		for _, r := range routes {
			if r.GroupID == id {
				route, found = r, true
				break
			}
		}
		if !found || !route.Enabled {
			return route, false, nil
		}
	}
	var excluded map[int64]struct{}
	if key.requestGroupRouting != nil {
		excluded = key.requestGroupRouting.excluded
	}
	if s.groupRouteExcludedOrCooling(key.ID, id, excluded) {
		return route, false, nil
	}
	if key.GroupRouting != nil {
		if cache, ok := s.cache.(APIKeyRouteCooldownCache); ok {
			cooling, err := cache.APIKeyRouteCooling(ctx, key.ID, id)
			if err != nil {
				return route, false, infraerrors.ServiceUnavailable("GROUP_ROUTE_COOLDOWN_UNAVAILABLE", "Group route state unavailable")
			}
			if cooling {
				return route, false, nil
			}
		}
	}
	return route, true, nil
}

type APIKeyRouteCooldownCache interface {
	APIKeyRouteCooling(context.Context, int64, int64) (bool, error)
	SetAPIKeyRouteCooldown(context.Context, int64, int64, time.Duration) error
}

func (s *APIKeyService) MarkRequestGroupRouteFailed(ctx context.Context, key *APIKey, id int64) error {
	s.MarkGroupRouteFailed(key, id)
	if key == nil || key.GroupRouting == nil {
		return nil
	}
	if cache, ok := s.cache.(APIKeyRouteCooldownCache); ok {
		for _, route := range key.GroupRouting.Routes {
			if route.GroupID == id && route.CooldownSeconds > 0 {
				return cache.SetAPIKeyRouteCooldown(ctx, key.ID, id, time.Duration(route.CooldownSeconds)*time.Second)
			}
		}
	}
	return nil
}

func (k *APIKey) HasGroupFailover() bool {
	if k == nil {
		return false
	}
	if k.GroupRouting != nil {
		return k.GroupRouting.Failover
	}
	return len(k.GroupRoutes) > 0
}

func (k *APIKey) UsesRequestGroupSelection() bool {
	return k != nil && (k.GroupRouting != nil || len(k.ConfiguredGroupIDs()) > 1)
}

func (s *APIKeyService) nextUnifiedGroup(ctx context.Context, key *APIKey, attempted map[int64]struct{}) (bool, error) {
	if !key.HasGroupFailover() {
		return false, nil
	}
	// No request admission evidence means no safe cross-group forwarding.
	if key.requestGroupRouting == nil {
		return false, nil
	}
	params := *key.requestGroupRouting
	params.excluded = attempted
	// A handler has already been selected for the initial platform. Never
	// pass an incompatible group to that handler during an in-request retry.
	originalAccepts := params.accepts
	params.accepts = func(candidate *APIKey) bool {
		return key.Group != nil && candidate.Group.Platform == key.Group.Platform && (originalAccepts == nil || originalAccepts(candidate))
	}
	copy := *key
	copy.requestGroupRouting = &params
	next, err := s.selectRequestGroup(ctx, &copy, params.models, params.kind, params.subscriptions, params.accepts, params.checkBilling)
	if err != nil {
		if infraerrors.Reason(err) == "NO_ELIGIBLE_API_KEY_GROUP" || infraerrors.Reason(err) == "NO_AVAILABLE_API_KEY_GROUP" {
			return false, nil
		}
		return false, err
	}
	*key = *next
	return true, nil
}
