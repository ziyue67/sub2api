//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestCompositeDeepSeekAliasSchedulingUsesEndpointRouteAndUpstreamModel(t *testing.T) {
	groupID := int64(7)
	group := &Group{ID: groupID, Platform: PlatformComposite, Status: StatusActive, Hydrated: true}
	routes := []CompositeModelRoute{
		{ID: 1, GroupID: groupID, PublicModel: "gpt-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-flash", Endpoint: CompositeRouteEndpointChatCompletions, Priority: 100, Enabled: true},
		{ID: 2, GroupID: groupID, PublicModel: "grok-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: CompositeRouteEndpointResponses, Priority: 100, Enabled: true},
		{ID: 3, GroupID: groupID, PublicModel: "claude-deepseek-alias", MatchType: CompositeRouteMatchExact, TargetPlatform: PlatformDeepSeek, UpstreamModel: "deepseek-v4-pro", Endpoint: CompositeRouteEndpointMessages, Priority: 100, Enabled: true},
	}

	for _, route := range routes {
		t.Run(route.Endpoint, func(t *testing.T) {
			account := Account{
				ID:          99,
				Platform:    PlatformDeepSeek,
				Type:        AccountTypeAPIKey,
				Priority:    1,
				Status:      StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"api_key": "sk-test",
					"model_mapping": map[string]any{
						route.UpstreamModel: route.UpstreamModel,
					},
				},
			}
			accountRepo := &mockAccountRepoForPlatform{
				accounts:     []Account{account},
				accountsByID: map[int64]*Account{account.ID: &account},
			}
			svc := &GatewayService{
				accountRepo:       accountRepo,
				groupRepo:         &mockGroupRepoForGateway{groups: map[int64]*Group{groupID: group}},
				cfg:               testConfig(),
				compositeResolver: NewCompositeRouteResolver(compositeRouteRepoStub{routes: routes}),
			}
			detectedPlatform, ok := DetectModelPlatform(route.PublicModel)
			require.True(t, ok)
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			ctx = WithResolvedTargetPlatform(ctx, detectedPlatform)
			ctx = WithCompositeRouteEndpoint(ctx, route.Endpoint)

			selection, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", route.PublicModel, nil, "", 0)

			require.NoError(t, err)
			require.NotNil(t, selection)
			require.NotNil(t, selection.Account)
			require.Equal(t, PlatformDeepSeek, selection.Account.Platform)
			require.True(t, selection.Account.IsModelSupported(route.UpstreamModel))
			require.False(t, selection.Account.IsModelSupported(route.PublicModel), "selection must have used the mapped upstream model")
		})
	}
}
