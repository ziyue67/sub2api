package service

import "context"

type codexWebSocketCapabilityAccountBatchLister interface {
	ListSchedulableByGroupIDsAndPlatforms(ctx context.Context, groupIDs []int64, platforms []string) ([]Account, error)
}

// GroupCodexSupportsWebSockets reports whether the group currently has at least
// one schedulable account that can serve Codex Responses WebSocket ingress.
// Model-specific composite routing is still enforced when the first frame is
// resolved; this value is a conservative provider-level capability for config
// generation, not an authorization decision.
func (s *OpenAIGatewayService) GroupCodexSupportsWebSockets(ctx context.Context, group *Group) bool {
	if s == nil || s.accountRepo == nil || group == nil || group.ID <= 0 {
		return false
	}
	if group.Platform == PlatformComposite {
		return s.compositeGroupCodexSupportsWebSockets(ctx, group.ID)
	}
	return s.groupPlatformCodexSupportsWebSockets(ctx, group.ID, group.Platform)
}

// GroupsCodexSupportsWebSockets resolves the runtime capability for a group
// collection with one schedulable-account load and one composite-route load.
func (s *OpenAIGatewayService) GroupsCodexSupportsWebSockets(ctx context.Context, groups []*Group) map[int64]bool {
	result := make(map[int64]bool, len(groups))
	if s == nil || s.accountRepo == nil || len(groups) == 0 {
		return result
	}

	groupsByID := make(map[int64]*Group, len(groups))
	groupIDs := make([]int64, 0, len(groups))
	compositeGroupIDs := make([]int64, 0)
	for _, group := range groups {
		if group == nil || group.ID <= 0 {
			continue
		}
		if _, exists := groupsByID[group.ID]; exists {
			continue
		}
		groupsByID[group.ID] = group
		groupIDs = append(groupIDs, group.ID)
		result[group.ID] = false
		if group.Platform == PlatformComposite {
			compositeGroupIDs = append(compositeGroupIDs, group.ID)
		}
	}
	if len(groupIDs) == 0 {
		return result
	}

	platforms := []string{PlatformOpenAI, PlatformDeepSeek}
	var accounts []Account
	var err error
	if batchRepo, ok := s.accountRepo.(codexWebSocketCapabilityAccountBatchLister); ok {
		accounts, err = batchRepo.ListSchedulableByGroupIDsAndPlatforms(ctx, groupIDs, platforms)
	} else {
		accounts, err = s.accountRepo.ListSchedulableByPlatforms(ctx, platforms)
	}
	if err != nil {
		return result
	}
	platformSupport := make(map[int64]map[string]bool, len(groupsByID))
	deepSeekBridgeEnabled := DeepSeekResponsesWSHTTPBridgeEnabled(s.cfg)
	for i := range accounts {
		account := &accounts[i]
		capable := false
		switch account.Platform {
		case PlatformDeepSeek:
			capable = account.ResolveDeepSeekResponsesWebSocketMode(deepSeekBridgeEnabled) == DeepSeekResponsesWebSocketModeHTTPBridge
		case PlatformOpenAI:
			capable = s.openAIAccountSupportsCodexWebSocket(account)
		}
		if !capable {
			continue
		}
		accountGroupIDs := account.GroupIDs
		if len(accountGroupIDs) == 0 && len(account.AccountGroups) > 0 {
			accountGroupIDs = make([]int64, 0, len(account.AccountGroups))
			for _, binding := range account.AccountGroups {
				accountGroupIDs = append(accountGroupIDs, binding.GroupID)
			}
		}
		for _, groupID := range accountGroupIDs {
			if _, requested := groupsByID[groupID]; !requested {
				continue
			}
			if platformSupport[groupID] == nil {
				platformSupport[groupID] = make(map[string]bool, 2)
			}
			platformSupport[groupID][account.Platform] = true
		}
	}

	for groupID, group := range groupsByID {
		if group.Platform != PlatformComposite {
			result[groupID] = platformSupport[groupID][group.Platform]
		}
	}
	if len(compositeGroupIDs) == 0 || s.codexCompositeResolver == nil {
		return result
	}
	routesByGroup, err := s.codexCompositeResolver.ListActiveRoutesByGroups(ctx, compositeGroupIDs)
	if err != nil {
		return result
	}
	for _, groupID := range compositeGroupIDs {
		targets := make(map[string]struct{})
		for _, route := range routesByGroup[groupID] {
			if route.Endpoint != CompositeRouteEndpointResponses && route.Endpoint != CompositeRouteEndpointAny {
				continue
			}
			targets[route.TargetPlatform] = struct{}{}
		}
		if len(targets) == 0 {
			continue
		}
		capable := true
		for platform := range targets {
			if !platformSupport[groupID][platform] {
				capable = false
				break
			}
		}
		result[groupID] = capable
	}
	return result
}

func (s *OpenAIGatewayService) SetCodexCompositeRouteResolver(resolver *CompositeRouteResolver) {
	if s != nil {
		s.codexCompositeResolver = resolver
	}
}

func (s *OpenAIGatewayService) compositeGroupCodexSupportsWebSockets(ctx context.Context, groupID int64) bool {
	if s.codexCompositeResolver == nil {
		return false
	}
	routes, err := s.codexCompositeResolver.ListActiveRoutes(ctx, groupID)
	if err != nil {
		return false
	}
	targets := make(map[string]struct{})
	for _, route := range routes {
		if route.Endpoint != CompositeRouteEndpointResponses && route.Endpoint != CompositeRouteEndpointAny {
			continue
		}
		targets[route.TargetPlatform] = struct{}{}
	}
	if len(targets) == 0 {
		return false
	}
	for platform := range targets {
		if !s.groupPlatformCodexSupportsWebSockets(ctx, groupID, platform) {
			return false
		}
	}
	return true
}

func (s *OpenAIGatewayService) groupPlatformCodexSupportsWebSockets(ctx context.Context, groupID int64, platform string) bool {
	if platform != PlatformOpenAI && platform != PlatformDeepSeek {
		return false
	}
	accounts, err := s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, groupID, platform)
	if err != nil {
		return false
	}
	deepSeekBridgeEnabled := DeepSeekResponsesWSHTTPBridgeEnabled(s.cfg)
	for i := range accounts {
		account := &accounts[i]
		switch account.Platform {
		case PlatformDeepSeek:
			if account.ResolveDeepSeekResponsesWebSocketMode(deepSeekBridgeEnabled) == DeepSeekResponsesWebSocketModeHTTPBridge {
				return true
			}
		case PlatformOpenAI:
			if s.openAIAccountSupportsCodexWebSocket(account) {
				return true
			}
		}
	}
	return false
}

func (s *OpenAIGatewayService) openAIAccountSupportsCodexWebSocket(account *Account) bool {
	if s == nil || s.cfg == nil || account == nil || !account.IsOpenAI() {
		return false
	}
	wsCfg := s.cfg.Gateway.OpenAIWS
	if !wsCfg.Enabled {
		return false
	}
	if wsCfg.ModeRouterV2Enabled {
		switch account.ResolveOpenAIResponsesWebSocketV2Mode(wsCfg.IngressModeDefault) {
		case OpenAIWSIngressModeHTTPBridge:
			return wsCfg.HTTPBridgeEnabled
		case OpenAIWSIngressModeCtxPool, OpenAIWSIngressModePassthrough:
			return !wsCfg.ForceHTTP && (wsCfg.ResponsesWebsocketsV2 || wsCfg.ResponsesWebsockets)
		default:
			return false
		}
	}
	if wsCfg.ForceHTTP {
		return false
	}
	decision := s.getOpenAIWSProtocolResolver().Resolve(account)
	return decision.Transport == OpenAIUpstreamTransportResponsesWebsocketV2 ||
		decision.Transport == OpenAIUpstreamTransportResponsesWebsocket
}
