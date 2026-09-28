//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Groups outside the OpenAI-compatible family go through the /v1/messages scheduler.
func TestPelicanGroupRouterRoutesThroughTheGatewayScheduler(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	repo := &mockAccountRepoForPlatform{
		accounts: []Account{
			{ID: 1, Name: "claude-a", Platform: PlatformAnthropic, Priority: 1, Status: StatusActive, Schedulable: true, Concurrency: 5, AccountGroups: []AccountGroup{{GroupID: groupID}}},
			{ID: 2, Name: "claude-b", Platform: PlatformAnthropic, Priority: 2, Status: StatusActive, Schedulable: true, Concurrency: 5, AccountGroups: []AccountGroup{{GroupID: groupID}}},
		},
		accountsByID: map[int64]*Account{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].ID] = &repo.accounts[i]
	}
	groupRepo := &mockGroupRepoForGateway{groups: map[int64]*Group{
		groupID: {ID: groupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true},
	}}
	cfg := testConfig()
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	concurrencyCache := &mockConcurrencyCache{}
	gateway := &GatewayService{
		accountRepo:        repo,
		groupRepo:          groupRepo,
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}
	router := &gatewayPelicanGroupRouter{gateway: gateway, concurrency: gateway.concurrencyService, slotWait: 50 * time.Millisecond}
	group := groupRepo.groups[groupID]
	model := "claude-3-5-sonnet-20241022"

	first, err := router.route(ctx, group, model, map[int64]struct{}{})
	require.NoError(t, err)
	require.Equal(t, int64(1), first.account.ID, "the scheduler's priority decides, not the test")
	require.Positive(t, concurrencyCache.acquireAccountCalls, "the sample holds a concurrency slot like a user request")
	first.release()

	second, err := router.route(ctx, group, model, map[int64]struct{}{1: {}})
	require.NoError(t, err)
	require.Equal(t, int64(2), second.account.ID, "a failed account is excluded like in failover")
	second.release()

	_, err = router.route(ctx, group, model, map[int64]struct{}{1: {}, 2: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}
