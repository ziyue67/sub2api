package service

import "context"

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
