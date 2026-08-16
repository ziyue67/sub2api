package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexCapabilityAccountRepoStub struct {
	AccountRepository
	accounts []Account
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

type codexCapabilityRouteRepoStub struct {
	CompositeModelRouteRepository
	routes []CompositeModelRoute
}

func (s *codexCapabilityRouteRepoStub) ListByGroup(_ context.Context, groupID int64, includeDisabled bool) ([]CompositeModelRoute, error) {
	result := make([]CompositeModelRoute, 0)
	for _, route := range s.routes {
		if route.GroupID == groupID && (includeDisabled || route.Enabled) {
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
