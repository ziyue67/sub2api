package service

import (
	"context"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/stretchr/testify/require"
)

type mixedProviderDiagnosticRepo struct {
	AccountRepository
	accounts map[int64][]Account
}

func (r mixedProviderDiagnosticRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, id int64, platform string) ([]Account, error) {
	var result []Account
	for _, a := range r.accounts[id] {
		if a.Platform == platform {
			result = append(result, a)
		}
	}
	return result, nil
}

// Exercise the real model eligibility probe, rather than a canned availability
// result. These fixtures contain no live credentials or upstream requests.
func TestDeepSeekOpenAIMixedGroupDiagnostic(t *testing.T) {
	for _, primary := range []int64{1, 2} {
		for _, strategy := range []string{"legacy", APIKeyRoutingLowestRate, APIKeyRoutingPriority} {
			t.Run(fmt.Sprintf("primary-%d/%s", primary, strategy), func(t *testing.T) {
				groups := map[int64]*Group{
					1: {ID: 1, Platform: PlatformDeepseek, Status: StatusActive, Hydrated: true, RateMultiplier: 0.06},
					2: {ID: 2, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.085},
				}
				account := func(id int64, platform, model string) Account {
					return Account{ID: id, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
						Credentials: map[string]any{"model_mapping": map[string]any{model: model}}}
				}
				repo := mixedProviderDiagnosticRepo{accounts: map[int64][]Account{
					1: {account(101, PlatformDeepseek, "deepseek-v4.1-flash")},
					2: {account(102, PlatformOpenAI, "gpt-5.4")},
				}}
				probe := &GatewayService{accountRepo: repo, groupRepo: multiGroupRepoStub{groups: groups}, cfg: &config.Config{RunMode: config.RunModeStandard}}
				key := &APIKey{ID: 1, UserID: 1, GroupID: &primary, Group: groups[primary], GroupIDs: []int64{primary, 3 - primary}, User: &User{ID: 1, Balance: 10}}
				if strategy != "legacy" {
					key.GroupRouting = &domain.APIKeyGroupRouting{Strategy: strategy, Routes: []domain.APIKeyGroupRoute{
						{GroupID: primary, Priority: 1, Weight: 1, Enabled: true},
						{GroupID: 3 - primary, Priority: 2, Weight: 1, Enabled: true},
					}}
				}
				svc := &APIKeyService{groupRepo: multiGroupRepoStub{groups: groups}, groupRouter: probe}
				for _, request := range []struct {
					model string
					group int64
				}{{"deepseek-v4.1-flash", 1}, {"gpt-5.4", 2}} {
					selected, err := svc.SelectRequestGroup(context.Background(), key, []string{request.model}, "token", nil, nil, true)
					require.NoError(t, err, request.model)
					require.Equal(t, request.group, *selected.GroupID, request.model)
					require.Equal(t, groups[request.group].Platform, selected.Group.Platform)
					require.Equal(t, groups[request.group].RateMultiplier, selected.Group.RateMultiplier)
					require.Equal(t, primary, *key.GroupID, "shared auth state must remain unchanged")
				}
				_, err := svc.SelectRequestGroup(context.Background(), key, []string{"unsupported-model"}, "token", nil, nil, true)
				require.Error(t, err)
			})
		}
	}
}

// A regression target for the reported inability to fall back across compatible
// provider groups. Both groups explicitly support the SAME requested model.
func TestDeepSeekOpenAICrossPlatformFailoverRepro(t *testing.T) {
	for _, pair := range [][2]string{{PlatformDeepseek, PlatformDeepseek}, {PlatformOpenAI, PlatformOpenAI}, {PlatformDeepseek, PlatformOpenAI}, {PlatformOpenAI, PlatformDeepseek}} {
		t.Run(pair[0]+"-to-"+pair[1], func(t *testing.T) {
			groups := map[int64]*Group{
				1: {ID: 1, Platform: pair[0], Status: StatusActive, Hydrated: true, RateMultiplier: 0.06},
				2: {ID: 2, Platform: pair[1], Status: StatusActive, Hydrated: true, RateMultiplier: 0.065},
			}
			repo := mixedProviderDiagnosticRepo{accounts: map[int64][]Account{}}
			for id, g := range groups {
				repo.accounts[id] = []Account{{ID: id + 100, Platform: g.Platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"deepseek-v4.1-flash": "deepseek-v4.1-flash"}}}}
			}
			groupRepo := multiGroupRepoStub{groups: groups}
			svc := &APIKeyService{groupRepo: groupRepo, groupRouter: &GatewayService{accountRepo: repo, groupRepo: groupRepo, cfg: &config.Config{RunMode: config.RunModeStandard}}}
			key := &APIKey{ID: 7, UserID: 7, User: &User{ID: 7, Balance: 10}}
			applyAPIKeyGroupRouting(key, &domain.APIKeyGroupRouting{Strategy: APIKeyRoutingLowestRate, Failover: true, Routes: []domain.APIKeyGroupRoute{
				{GroupID: 1, Priority: 1, Weight: 1, Enabled: true}, {GroupID: 2, Priority: 1, Weight: 1, Enabled: true},
			}})
			selected, err := svc.SelectRequestGroup(context.Background(), key, []string{"deepseek-v4.1-flash"}, "token", nil, nil, true)
			require.NoError(t, err)
			require.Equal(t, int64(1), *selected.GroupID)
			ctx := WithOpenAITextGroupFailover(context.Background(), []byte(`{"model":"deepseek-v4.1-flash","messages":[{"role":"user","content":"hello"}]}`))
			switched, err := svc.ApplyNextGroupRoute(ctx, selected, map[int64]struct{}{1: {}})
			require.NoError(t, err)
			require.True(t, switched, "a healthy compatible fallback supports the same model, and failover is enabled")
			require.Equal(t, int64(2), *selected.GroupID)
		})
	}
}
