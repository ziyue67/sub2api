package service

import (
	"context"
	"errors"
)

var ErrStickySessionAdminUnsupported = errors.New("sticky session administration unsupported")

// StickySessionAdminCache is an optional capability implemented by the Redis
// gateway cache. Keeping it separate from GatewayCache lets existing test
// doubles and alternative deployments omit the bulk-scan operations.
type StickySessionAdminCache interface {
	CountStickySessionBindings(ctx context.Context, groupID int64) (map[int64]int, error)
	ClearStickySessionBindings(ctx context.Context, groupID int64, accountIDs map[int64]struct{}) (int, error)
}

func (s *OpenAIGatewayService) StickySessionBindingCounts(ctx context.Context, groupID int64) (map[int64]int, error) {
	if s == nil || s.cache == nil {
		return nil, ErrStickySessionAdminUnsupported
	}
	cache, ok := s.cache.(StickySessionAdminCache)
	if !ok {
		return nil, ErrStickySessionAdminUnsupported
	}
	return cache.CountStickySessionBindings(ctx, groupID)
}

// ClearStickySessionBindings removes only session-hash bindings. Response
// continuation and response-owner bindings are deliberately preserved.
func (s *OpenAIGatewayService) ClearStickySessionBindings(ctx context.Context, groupID int64, accountIDs []int64) (int, error) {
	if s == nil || s.cache == nil {
		return 0, ErrStickySessionAdminUnsupported
	}
	cache, ok := s.cache.(StickySessionAdminCache)
	if !ok {
		return 0, ErrStickySessionAdminUnsupported
	}
	var filter map[int64]struct{}
	if len(accountIDs) > 0 {
		filter = make(map[int64]struct{}, len(accountIDs))
		for _, id := range accountIDs {
			if id > 0 {
				filter[id] = struct{}{}
			}
		}
		// An explicit filter that resolves to no valid account must not degrade
		// into the "clear all" sentinel (an empty map/nil).
		if len(filter) == 0 {
			return 0, nil
		}
	}
	return cache.ClearStickySessionBindings(ctx, groupID, filter)
}
