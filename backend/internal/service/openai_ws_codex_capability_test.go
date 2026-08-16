package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexCapabilityAccountRepoStub struct {
	AccountRepository
	accounts                []Account
	listPlatformsCalls      int
	listGroupPlatformsCalls int
}

func (s *codexCapabilityAccountRepoStub) ListSchedulableByGroupIDAndPlatform(_ context.Context, groupID int64, platform string) ([]Account, error) {
	result := make([]Account, 0)
	for _, account := range s.accounts {
		if account.Platform != platform || !account.IsSchedulable() {
			continue
		}
		for _, accountGroup := range account.AccountGroups {
			if accountGroup.GroupID == groupID {
				result = append(result, account)
				break
			}
		}
	}
	return result, nil
}

func (s *codexCapabilityAccountRepoStub) ListSchedulableByPlatforms(_ context.Context, platforms []string) ([]Account, error) {
	s.listPlatformsCalls++
	allowed := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowed[platform] = struct{}{}
	}
	result := make([]Account, 0)
	for _, account := range s.accounts {
		if _, ok := allowed[account.Platform]; ok && account.IsSchedulable() {
			result = append(result, account)
		}
	}
	return result, nil
}

func (s *codexCapabilityAccountRepoStub) ListSchedulableByGroupIDsAndPlatforms(_ context.Context, groupIDs []int64, platforms []string) ([]Account, error) {
	s.listGroupPlatformsCalls++
	allowedGroups := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		allowedGroups[groupID] = struct{}{}
	}
	allowedPlatforms := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		allowedPlatforms[platform] = struct{}{}
	}
	result := make([]Account, 0)
	for _, account := range s.accounts {
		if _, ok := allowedPlatforms[account.Platform]; !ok || !account.IsSchedulable() {
			continue
		}
		for _, groupID := range account.GroupIDs {
			if _, ok := allowedGroups[groupID]; ok {
				result = append(result, account)
				break
			}
		}
	}
	return result, nil
}

type codexCapabilityRouteRepoStub struct {
	CompositeModelRouteRepository
	routes            []CompositeModelRoute
	listByGroupCalls  int
	listByGroupsCalls int
}

func (s *codexCapabilityRouteRepoStub) ListByGroup(_ context.Context, groupID int64, includeDisabled bool) ([]CompositeModelRoute, error) {
	s.listByGroupCalls++
	result := make([]CompositeModelRoute, 0)
	for _, route := range s.routes {
		if route.GroupID == groupID && (includeDisabled || route.Enabled) {
			result = append(result, route)
		}
	}
	return result, nil
}

func (s *codexCapabilityRouteRepoStub) ListByGroups(_ context.Context, groupIDs []int64, includeDisabled bool) ([]CompositeModelRoute, error) {
	s.listByGroupsCalls++
	allowed := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		allowed[groupID] = struct{}{}
	}
	result := make([]CompositeModelRoute, 0)
	for _, route := range s.routes {
		if _, ok := allowed[route.GroupID]; ok && (includeDisabled || route.Enabled) {
			result = append(result, route)
		}
	}
	return result, nil
}

func TestOpenAIAccountSupportsCodexWebSocketModes(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	svc := &OpenAIGatewayService{cfg: cfg}
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1, Extra: map[string]any{
		"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool,
	}}

	require.True(t, svc.openAIAccountSupportsCodexWebSocket(account))
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeHTTPBridge
	require.True(t, svc.openAIAccountSupportsCodexWebSocket(account))
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = false
	require.False(t, svc.openAIAccountSupportsCodexWebSocket(account))
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	account.Extra["openai_apikey_responses_websockets_v2_mode"] = OpenAIWSIngressModeOff
	require.False(t, svc.openAIAccountSupportsCodexWebSocket(account))

	cfg.Gateway.OpenAIWS.Enabled = false
	require.False(t, svc.openAIAccountSupportsCodexWebSocket(account))
}

func TestDeepSeekResponsesWSHTTPBridgeEnabledRequiresRouterAndBridge(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	require.True(t, DeepSeekResponsesWSHTTPBridgeEnabled(cfg))

	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = false
	require.False(t, DeepSeekResponsesWSHTTPBridgeEnabled(cfg))
	require.False(t, DeepSeekResponsesWSHTTPBridgeEnabled(nil))
}

func TestGroupCodexSupportsWebSocketsRequiresEveryCompositeResponsesTarget(t *testing.T) {
	const groupID = int64(42)
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	accounts := []Account{
		{
			ID: 1, Platform: PlatformDeepSeek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
			Extra:         map[string]any{DeepSeekResponsesWebSocketModeKey: DeepSeekResponsesWebSocketModeHTTPBridge},
			AccountGroups: []AccountGroup{{GroupID: groupID}},
		},
		{
			ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
			Extra:         map[string]any{"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool},
			AccountGroups: []AccountGroup{{GroupID: groupID}},
		},
	}
	routes := []CompositeModelRoute{
		{GroupID: groupID, TargetPlatform: PlatformDeepSeek, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{GroupID: groupID, TargetPlatform: PlatformOpenAI, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
	}
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: &codexCapabilityAccountRepoStub{accounts: accounts}}
	svc.SetCodexCompositeRouteResolver(NewCompositeRouteResolver(&codexCapabilityRouteRepoStub{routes: routes}))
	group := &Group{ID: groupID, Platform: PlatformComposite}

	require.True(t, svc.GroupCodexSupportsWebSockets(context.Background(), group))

	routes = append(routes, CompositeModelRoute{GroupID: groupID, TargetPlatform: PlatformAnthropic, Endpoint: CompositeRouteEndpointResponses, Enabled: true})
	svc.SetCodexCompositeRouteResolver(NewCompositeRouteResolver(&codexCapabilityRouteRepoStub{routes: routes}))
	require.False(t, svc.GroupCodexSupportsWebSockets(context.Background(), group))

	accounts[0].Extra[DeepSeekResponsesWebSocketModeKey] = DeepSeekResponsesWebSocketModeOff
	svc.accountRepo = &codexCapabilityAccountRepoStub{accounts: accounts}
	svc.SetCodexCompositeRouteResolver(NewCompositeRouteResolver(&codexCapabilityRouteRepoStub{routes: routes[:2]}))
	require.False(t, svc.GroupCodexSupportsWebSockets(context.Background(), group))
}

func TestGroupsCodexSupportsWebSocketsUsesBatchAccountAndRouteLoads(t *testing.T) {
	cfg := &config.Config{}
	cfg.Gateway.OpenAIWS.Enabled = true
	cfg.Gateway.OpenAIWS.ModeRouterV2Enabled = true
	cfg.Gateway.OpenAIWS.HTTPBridgeEnabled = true
	cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
	accounts := []Account{
		{
			ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
			Extra:    map[string]any{"openai_apikey_responses_websockets_v2_mode": OpenAIWSIngressModeCtxPool},
			GroupIDs: []int64{1, 3},
		},
		{
			ID: 2, Platform: PlatformDeepSeek, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
			Extra:    map[string]any{DeepSeekResponsesWebSocketModeKey: DeepSeekResponsesWebSocketModeHTTPBridge},
			GroupIDs: []int64{2, 3, 4},
		},
	}
	routes := []CompositeModelRoute{
		{GroupID: 3, TargetPlatform: PlatformOpenAI, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{GroupID: 3, TargetPlatform: PlatformDeepSeek, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{GroupID: 4, TargetPlatform: PlatformDeepSeek, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
		{GroupID: 4, TargetPlatform: PlatformAnthropic, Endpoint: CompositeRouteEndpointResponses, Enabled: true},
	}
	accountRepo := &codexCapabilityAccountRepoStub{accounts: accounts}
	routeRepo := &codexCapabilityRouteRepoStub{routes: routes}
	svc := &OpenAIGatewayService{cfg: cfg, accountRepo: accountRepo}
	svc.SetCodexCompositeRouteResolver(NewCompositeRouteResolver(routeRepo))

	result := svc.GroupsCodexSupportsWebSockets(context.Background(), []*Group{
		{ID: 1, Platform: PlatformOpenAI},
		{ID: 2, Platform: PlatformDeepSeek},
		{ID: 3, Platform: PlatformComposite},
		{ID: 4, Platform: PlatformComposite},
	})

	require.True(t, result[1])
	require.True(t, result[2])
	require.True(t, result[3])
	require.False(t, result[4])
	require.Equal(t, 1, accountRepo.listGroupPlatformsCalls)
	require.Zero(t, accountRepo.listPlatformsCalls)
	require.Equal(t, 1, routeRepo.listByGroupsCalls)
	require.Zero(t, routeRepo.listByGroupCalls)
}
