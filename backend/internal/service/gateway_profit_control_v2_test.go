//go:build unit

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func gatewayProfitTestGroup(id int64, platform string) *Group {
	return &Group{
		ID:                   id,
		Name:                 "profit-" + platform,
		Platform:             platform,
		Status:               StatusActive,
		Hydrated:             true,
		RateMultiplier:       0.5,
		SubscriptionType:     SubscriptionTypeStandard,
		ProfitControlEnabled: true,
		ProfitMinMargin:      0,
		ProfitSafetyBuffer:   0,
	}
}

func gatewayProfitTestContext(group *Group) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	ctx, _ = WithGatewayTokenRequestPricing(ctx)
	return ctx
}

func gatewayProfitTestAccount(id int64, platform string, rate float64, groupID int64) Account {
	return Account{
		ID:             id,
		Name:           "account",
		Platform:       platform,
		Type:           AccountTypeAPIKey,
		Status:         StatusActive,
		Schedulable:    true,
		Concurrency:    2,
		Priority:       1,
		RateMultiplier: &rate,
		AccountGroups:  []AccountGroup{{AccountID: id, GroupID: groupID}},
		GroupIDs:       []int64{groupID},
	}
}

func TestGatewayProfitControlInstallsForFivePlatformsOnlyOnTokenRequests(t *testing.T) {
	for _, platform := range []string{
		PlatformOpenAI,
		PlatformAnthropic,
		PlatformGemini,
		PlatformGrok,
		PlatformAntigravity,
	} {
		t.Run(platform, func(t *testing.T) {
			group := gatewayProfitTestGroup(101, platform)
			groupID := group.ID
			svc := &GatewayService{}

			tokenCtx := svc.withGatewayProfitControlGate(gatewayProfitTestContext(group), &groupID)
			gate, _ := tokenCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.NotNil(t, gate)
			require.Equal(t, platform, gate.platform)
			require.InDelta(t, 0.5, gate.threshold, 1e-12)

			metadataCtx := context.WithValue(context.Background(), ctxkey.Group, group)
			metadataCtx = svc.withGatewayProfitControlGate(metadataCtx, &groupID)
			gate, _ = metadataCtx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
			require.Nil(t, gate, "未显式标记为 token 请求的入口不得装门")
		})
	}
}

func TestGatewayProfitControlCompositeBillingUsesScheduledMemberConfig(t *testing.T) {
	billingGroup := &Group{
		ID:               201,
		Platform:         PlatformComposite,
		Status:           StatusActive,
		Hydrated:         true,
		RateMultiplier:   0.4,
		SubscriptionType: SubscriptionTypeStandard,
	}
	memberGroup := gatewayProfitTestGroup(202, PlatformAnthropic)
	memberGroup.RateMultiplier = 99
	memberGroup.ProfitMinMargin = 0.25

	ctx := context.WithValue(context.Background(), ctxkey.Group, billingGroup)
	ctx, pricingAt := WithGatewayTokenRequestPricing(ctx)
	svc := &GatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(
			nil,
			nil,
			nil,
			profitControlGroupRepo{group: memberGroup},
			nil,
		),
	}
	ctx = svc.withGatewayProfitControlGate(ctx, &memberGroup.ID)
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.NotNil(t, gate)
	require.Equal(t, memberGroup.ID, gate.groupID)
	require.Equal(t, PlatformAnthropic, gate.platform)
	require.Equal(t, pricingAt, gate.pricingAt)
	require.InDelta(t, 0.4*(1-0.25), gate.threshold, 1e-12, "D 必须取 composite 计费父分组，margin 取被调度成员分组")
}

func TestGatewayProfitControlGroupLoadFailureClearsForeignGate(t *testing.T) {
	billingGroup := &Group{
		ID:               211,
		Platform:         PlatformComposite,
		Status:           StatusActive,
		Hydrated:         true,
		RateMultiplier:   0.4,
		SubscriptionType: SubscriptionTypeStandard,
	}
	targetGroupID := int64(212)
	ctx := gatewayProfitTestContext(billingGroup)
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   210,
		platform:  PlatformAnthropic,
		threshold: 0.1,
	})
	svc := &GatewayService{
		schedulerSnapshot: NewSchedulerSnapshotService(
			nil,
			nil,
			nil,
			profitControlFailingGroupRepo{},
			nil,
		),
	}

	ctx = svc.withGatewayProfitControlGate(ctx, &targetGroupID)
	gate, ok := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	require.True(t, ok)
	require.Nil(t, gate, "加载新分组失败时必须清除其他分组遗留的门")

	account := gatewayProfitTestAccount(213, PlatformAnthropic, 0.8, targetGroupID)
	require.True(t, svc.isGatewayAccountProfitEligible(ctx, &account), "配置读取失败按既定语义 fail-open")
}

type profitControlFailingGroupRepo struct {
	GroupRepository
}

func (profitControlFailingGroupRepo) GetByIDLite(context.Context, int64) (*Group, error) {
	return nil, errors.New("group cache unavailable")
}

// 见 profitControlGroupRepo.GetByID：利润门必须走不带账号计数聚合的 lite 读取。
func (profitControlFailingGroupRepo) GetByID(context.Context, int64) (*Group, error) {
	panic("profit control gate must read groups via GetByIDLite (no account-count aggregation)")
}

func TestGatewayProfitControlLegacyMixedAndRoutedSelection(t *testing.T) {
	t.Run("legacy single-platform selection", func(t *testing.T) {
		group := gatewayProfitTestGroup(111, PlatformGrok)
		cheap := gatewayProfitTestAccount(1, PlatformGrok, 0.2, group.ID)
		expensive := gatewayProfitTestAccount(2, PlatformGrok, 0.8, group.ID)
		repo := &mockAccountRepoForPlatform{
			accounts:     []Account{expensive, cheap},
			accountsByID: map[int64]*Account{cheap.ID: &cheap, expensive.ID: &expensive},
		}
		svc := &GatewayService{
			accountRepo: repo,
			cache:       &mockGatewayCacheForPlatform{},
			cfg:         testConfig(),
		}

		selected, err := svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "", nil,
		)
		require.NoError(t, err)
		require.Equal(t, cheap.ID, selected.ID)

		_, err = svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "", map[int64]struct{}{cheap.ID: {}},
		)
		require.Error(t, err)
		require.ErrorIs(t, err, ErrNoAvailableAccounts)
	})

	t.Run("mixed routing filters the routed account", func(t *testing.T) {
		group := gatewayProfitTestGroup(112, PlatformAnthropic)
		group.ModelRoutingEnabled = true
		group.ModelRouting = map[string][]int64{"claude-test": {2, 1}}
		cheap := gatewayProfitTestAccount(1, PlatformAntigravity, 0.2, group.ID)
		cheap.Extra = map[string]any{"mixed_scheduling": true}
		cheap.Credentials = map[string]any{"model_mapping": map[string]any{"claude-test": "claude-test"}}
		expensive := gatewayProfitTestAccount(2, PlatformAnthropic, 0.8, group.ID)
		repo := &mockAccountRepoForPlatform{
			accounts:     []Account{expensive, cheap},
			accountsByID: map[int64]*Account{cheap.ID: &cheap, expensive.ID: &expensive},
		}
		svc := &GatewayService{
			accountRepo: repo,
			cache:       &mockGatewayCacheForPlatform{},
			cfg:         testConfig(),
		}

		selected, err := svc.SelectAccountForModelWithExclusions(
			gatewayProfitTestContext(group), &group.ID, "", "claude-test", nil,
		)
		require.NoError(t, err)
		require.Equal(t, cheap.ID, selected.ID)
	})
}

func TestGatewayProfitControlLoadAwareSelectionAndFailover(t *testing.T) {
	group := gatewayProfitTestGroup(121, PlatformGrok)
	cheap := gatewayProfitTestAccount(1, PlatformGrok, 0.2, group.ID)
	expensive := gatewayProfitTestAccount(2, PlatformGrok, 0.8, group.ID)
	repo := &mockAccountRepoForPlatform{
		accounts:     []Account{expensive, cheap},
		accountsByID: map[int64]*Account{cheap.ID: &cheap, expensive.ID: &expensive},
	}
	cfg := &config.Config{RunMode: config.RunModeStandard}
	cfg.Gateway.Scheduling.LoadBatchEnabled = true
	svc := &GatewayService{
		accountRepo:        repo,
		cache:              &mockGatewayCacheForPlatform{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
	}

	result, err := svc.SelectAccountWithLoadAwareness(
		gatewayProfitTestContext(group), &group.ID, "", "", nil, "", 0,
	)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, cheap.ID, result.Account.ID)
	if result.ReleaseFunc != nil {
		result.ReleaseFunc()
	}

	result, err = svc.SelectAccountWithLoadAwareness(
		gatewayProfitTestContext(group),
		&group.ID,
		"",
		"",
		map[int64]struct{}{cheap.ID: {}},
		"",
		0,
	)
	require.Nil(t, result)
	require.Error(t, err)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
}

func TestGatewayProfitControlStickyVetoKeepsBindingUntilRateRecovers(t *testing.T) {
	group := gatewayProfitTestGroup(131, PlatformAnthropic)
	expensive := gatewayProfitTestAccount(1, PlatformAnthropic, 0.8, group.ID)
	cheap := gatewayProfitTestAccount(2, PlatformAnthropic, 0.2, group.ID)
	repo := &mockAccountRepoForPlatform{
		accounts:     []Account{expensive, cheap},
		accountsByID: map[int64]*Account{expensive.ID: &expensive, cheap.ID: &cheap},
	}
	cache := &mockGatewayCacheForPlatform{
		sessionBindings: map[string]int64{"sticky-profit": expensive.ID},
	}
	svc := &GatewayService{
		accountRepo: repo,
		cache:       cache,
		cfg:         testConfig(),
	}
	ctx := gatewayProfitTestContext(group)

	selected, err := svc.SelectAccountForModelWithExclusions(ctx, &group.ID, "sticky-profit", "", nil)
	require.NoError(t, err)
	require.Equal(t, cheap.ID, selected.ID)
	require.Equal(t, expensive.ID, cache.sessionBindings["sticky-profit"], "候选过滤不得覆盖旧粘性绑定")

	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(
		svc.withGatewayProfitControlGate(ctx, &group.ID),
		&group.ID,
		"sticky-profit",
		cheap.ID,
	))
	require.Equal(t, expensive.ID, cache.sessionBindings["sticky-profit"], "终检通过的 fallback 账号也不得覆盖旧绑定")
	require.Zero(t, cache.deletedSessions["sticky-profit"])

	recovered := expensive
	recoveredRate := 0.2
	recovered.RateMultiplier = &recoveredRate
	repo.accounts[0] = recovered
	repo.accountsByID[recovered.ID] = &repo.accounts[0]

	selected, err = svc.SelectAccountForModelWithExclusions(ctx, &group.ID, "sticky-profit", "", nil)
	require.NoError(t, err)
	require.Equal(t, recovered.ID, selected.ID, "倍率恢复后应重新命中原粘性账号")
	require.Zero(t, cache.deletedSessions["sticky-profit"])
}

type gatewayProfitSnapshotCache struct {
	SchedulerCache
	account *Account
	err     error
	calls   *int
}

func (c *gatewayProfitSnapshotCache) GetAccount(context.Context, int64) (*Account, error) {
	if c.calls != nil {
		(*c.calls)++
	}
	return c.account, c.err
}

type gatewayProfitAccountRepo struct {
	AccountRepository
	account *Account
	err     error
	calls   *int
}

func (r gatewayProfitAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	if r.calls != nil {
		(*r.calls)++
	}
	return r.account, r.err
}

func TestGatewayProfitControlTerminalRefreshUsesReplacementObject(t *testing.T) {
	selected := gatewayProfitTestAccount(141, PlatformGemini, 0.2, 1)
	replacement := selected
	expensiveRate := 0.8
	replacement.RateMultiplier = &expensiveRate

	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: &replacement},
		nil,
		gatewayProfitAccountRepo{},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformGemini,
		threshold: 0.5,
	})

	latest, vetoed, reason := profitControlVetoLatest(ctx, &selected, snapshot)
	require.Same(t, &replacement, latest)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	require.InDelta(t, 0.2, *selected.RateMultiplier, 1e-12, "测试必须替换缓存对象，不能原地修改旧指针")
}

func TestGatewayProfitControlTerminalRefreshFallsBackFromCacheToDatabase(t *testing.T) {
	selected := gatewayProfitTestAccount(145, PlatformAnthropic, 0.2, 1)
	replacement := selected
	expensiveRate := 0.8
	replacement.RateMultiplier = &expensiveRate

	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{err: errors.New("cache unavailable")},
		nil,
		gatewayProfitAccountRepo{account: &replacement},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformAnthropic,
		threshold: 0.5,
	})

	latest, vetoed, reason := profitControlVetoLatest(ctx, &selected, snapshot)
	require.Same(t, &replacement, latest)
	require.True(t, vetoed, "缓存读取失败时必须继续从数据库重读，不能直接使用选号旧对象")
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
}

func TestGatewayProfitControlTerminalRefreshFailureFallsBackToSelectedObject(t *testing.T) {
	selected := gatewayProfitTestAccount(151, PlatformAntigravity, 0.2, 1)
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{err: errors.New("cache unavailable")},
		nil,
		gatewayProfitAccountRepo{err: errors.New("database unavailable")},
		nil,
		nil,
	)
	ctx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{
		groupID:   1,
		platform:  PlatformAntigravity,
		threshold: 0.5,
	})

	latest, vetoed, reason := profitControlVetoLatest(ctx, &selected, snapshot)
	require.Same(t, &selected, latest)
	require.False(t, vetoed)
	require.Empty(t, reason)
}

func TestGatewayProfitControlTerminalRefreshRejectsStaleProxyLane(t *testing.T) {
	proxyID := int64(320)
	selectedLane := AccountProxyLane{
		ID: 321, AccountID: 151, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 2,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "old-lane.example", Port: 8080, Status: StatusActive},
	}
	selected := &Account{
		ID:          151,
		Concurrency: 2, ProxyLanes: []AccountProxyLane{selectedLane},
		SelectedProxyLane: &selectedLane,
	}
	// The fresh scheduler snapshot no longer contains the selected lane.  This
	// is the race we must fail closed: returning the refreshed account as a
	// normal success would make AccountProxyURL fall back to its legacy proxy.
	refreshed := &Account{ID: selected.ID, Concurrency: 4, ProxyID: ptrInt64(999), Proxy: &Proxy{ID: 999, Protocol: "http", Host: "legacy.example", Port: 8081, Status: StatusActive}}
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: refreshed},
		nil,
		gatewayProfitAccountRepo{account: refreshed},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonLaneUnavailable, reason)
	require.NotNil(t, latest)
	require.Nil(t, latest.SelectedProxyLane)
	require.Nil(t, latest.ProxyID)
	require.Nil(t, latest.Proxy)
	require.Empty(t, AccountProxyURL(latest), "stale lane must never degrade to direct/legacy proxy")
}

func TestGatewayProfitControlTerminalRefreshUpdatesLaneCapWithoutProfitGate(t *testing.T) {
	proxyID := int64(330)
	oldLane := AccountProxyLane{
		ID: 331, AccountID: 152, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 2,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "edge.example", Port: 8080, Status: StatusActive},
	}
	selected := &Account{ID: 152, Concurrency: 2, ProxyLanes: []AccountProxyLane{oldLane}, SelectedProxyLane: &oldLane}
	newLane := oldLane
	newLane.Concurrency = 7
	refreshed := &Account{ID: selected.ID, Concurrency: 7, ProxyLanes: []AccountProxyLane{newLane}}
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: refreshed},
		nil,
		gatewayProfitAccountRepo{account: refreshed},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.False(t, vetoed)
	require.Empty(t, reason)
	require.Same(t, refreshed, latest)
	require.NotNil(t, latest.SelectedProxyLane)
	require.Equal(t, 7, latest.SelectedProxyLane.Concurrency,
		"lane cap must be refreshed even when no profit gate is installed")
}

func TestGatewayProfitControlTerminalRefreshHydratesCompactLaneFromRepository(t *testing.T) {
	proxyID := int64(335)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "authoritative.example", Port: 8080, Status: StatusActive}
	lane := AccountProxyLane{
		ID: 336, AccountID: 1521, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 2,
		Proxy: proxy,
	}
	selected := &Account{ID: 1521, Concurrency: 2, ProxyLanes: []AccountProxyLane{lane}, SelectedProxyLane: &lane}
	// The scheduler payload deliberately carries only lane metadata.  The
	// terminal check must not interpret this compact shape as direct traffic;
	// it has to complete the exact lane/proxy relation from the repository.
	compactLane := lane
	compactLane.Proxy = nil
	compact := &Account{ID: selected.ID, Concurrency: 2, ProxyLanes: []AccountProxyLane{compactLane}}
	database := &Account{ID: selected.ID, Concurrency: 2, ProxyLanes: []AccountProxyLane{lane}}
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: compact},
		nil,
		gatewayProfitAccountRepo{account: database},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.False(t, vetoed)
	require.Empty(t, reason)
	require.NotNil(t, latest.SelectedProxyLane)
	require.Same(t, proxy, latest.SelectedProxyLane.Proxy)
	require.Equal(t, proxy.URL(), AccountProxyURL(latest))
}

func TestGatewayProfitControlTerminalRefreshDoesNotRejectUnprojectedLaneAccount(t *testing.T) {
	proxyID := int64(340)
	lane := AccountProxyLane{
		ID: 341, AccountID: 153, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 2,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "edge.example", Port: 8080, Status: StatusActive},
	}
	selected := &Account{ID: 153, Concurrency: 2, ProxyLanes: []AccountProxyLane{lane}}
	refreshed := &Account{ID: selected.ID, Concurrency: 2, ProxyLanes: []AccountProxyLane{lane}}
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: refreshed},
		nil,
		gatewayProfitAccountRepo{},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.False(t, vetoed, "没有 request-scoped lane affinity 时不应伪造 stale-lane veto")
	require.Empty(t, reason)
	require.Same(t, refreshed, latest)
}

func TestGatewayProfitControlTerminalRefreshBypassesOlderCacheForLaneLifecycle(t *testing.T) {
	proxyID := int64(350)
	lane := AccountProxyLane{
		ID: 351, AccountID: 154, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 3,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "current.example", Port: 8080, Status: StatusActive},
	}
	now := time.Now().UTC()
	selected := &Account{
		ID: 154, UpdatedAt: now,
		Concurrency: 3, ProxyLanes: []AccountProxyLane{lane}, SelectedProxyLane: &lane,
	}
	// Redis contains an older account payload from before the lane was created.
	// The account row timestamp is intentionally older as well: lane writes have
	// their own updated_at and do not have to bump accounts.updated_at.
	oldSnapshot := &Account{ID: selected.ID, UpdatedAt: now.Add(-time.Minute)}
	database := *selected
	database.UpdatedAt = now.Add(-time.Minute)
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: oldSnapshot},
		nil,
		gatewayProfitAccountRepo{account: &database},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.False(t, vetoed)
	require.Empty(t, reason)
	require.NotNil(t, latest.SelectedProxyLane)
	require.Equal(t, lane.ID, latest.SelectedProxyLane.ID,
		"older cache must not bypass authoritative lane validation")
}

func TestGatewayProfitControlTerminalRefreshOlderCacheRejectsDeletedLane(t *testing.T) {
	proxyID := int64(360)
	lane := AccountProxyLane{
		ID: 361, AccountID: 155, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 3,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "old.example", Port: 8080, Status: StatusActive},
	}
	now := time.Now().UTC()
	selected := &Account{
		ID: 155, UpdatedAt: now,
		Concurrency: 3, ProxyLanes: []AccountProxyLane{lane}, SelectedProxyLane: &lane,
	}
	oldSnapshot := &Account{ID: selected.ID, UpdatedAt: now.Add(-time.Minute)}
	// The authoritative DB row no longer contains the selected lane.
	database := &Account{ID: selected.ID, UpdatedAt: now.Add(-time.Minute), ProxyID: ptrInt64(999), Proxy: &Proxy{ID: 999, Protocol: "http", Host: "legacy.example", Port: 8081, Status: StatusActive}}
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: oldSnapshot},
		nil,
		gatewayProfitAccountRepo{account: database},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonLaneUnavailable, reason)
	require.NotNil(t, latest)
	require.Nil(t, latest.SelectedProxyLane)
	require.Nil(t, latest.ProxyID)
	require.Nil(t, latest.Proxy)
}

func TestGatewayProfitControlTerminalRefreshOlderCacheFailsClosedWhenAuthoritativeReadFails(t *testing.T) {
	proxyID := int64(370)
	lane := AccountProxyLane{
		ID: 371, AccountID: 156, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 3,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "old.example", Port: 8080, Status: StatusActive},
	}
	now := time.Now().UTC()
	selected := &Account{
		ID: 156, UpdatedAt: now,
		Concurrency: 3, ProxyLanes: []AccountProxyLane{lane}, SelectedProxyLane: &lane,
	}
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: &Account{ID: selected.ID, UpdatedAt: now.Add(-time.Minute)}},
		nil,
		gatewayProfitAccountRepo{err: errors.New("database unavailable")},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonLaneUnavailable, reason)
	require.Same(t, selected, latest)
}

func TestGatewayProfitControlTerminalRefreshAuthoritativeLaneIgnoresMatchingStaleCache(t *testing.T) {
	proxyID := int64(380)
	now := time.Now().UTC()
	lane := AccountProxyLane{
		ID: 381, AccountID: 157, ProxyID: &proxyID,
		Transport: AccountProxyLaneTransportProxy,
		Status:    AccountProxyLaneStatusActive, Schedulable: true, Weight: 1, Concurrency: 3,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
		Proxy: &Proxy{ID: proxyID, Protocol: "http", Host: "old.example", Port: 8080, Status: StatusActive},
	}
	selected := &Account{
		ID: 157, UpdatedAt: now,
		Concurrency: 3, ProxyLanes: []AccountProxyLane{lane}, SelectedProxyLane: &lane,
	}

	// Redis and the request-local selection deliberately contain the same
	// timestamps and an otherwise valid lane.  A cache-only terminal check
	// would accept this payload forever when account.updated_at did not move
	// with a lane edit.  The authoritative row has since paused the lane.
	cached := *selected
	cachedLane := lane
	cached.ProxyLanes = []AccountProxyLane{cachedLane}
	databaseLane := lane
	databaseLane.Status = AccountProxyLaneStatusPaused
	database := &Account{ID: selected.ID, UpdatedAt: now, ProxyLanes: []AccountProxyLane{databaseLane}}
	cacheCalls, repoCalls := 0, 0
	snapshot := NewSchedulerSnapshotService(
		&gatewayProfitSnapshotCache{account: &cached, calls: &cacheCalls},
		nil,
		gatewayProfitAccountRepo{account: database, calls: &repoCalls},
		nil,
		nil,
	)

	latest, vetoed, reason := profitControlVetoLatest(context.Background(), selected, snapshot)
	require.True(t, vetoed)
	require.Equal(t, openAIProfitFilterReasonLaneUnavailable, reason)
	require.Equal(t, 0, cacheCalls, "concrete lane terminal checks must bypass a potentially stale cache")
	require.Equal(t, 1, repoCalls, "authoritative lane validation should use one repository read")
	require.NotNil(t, latest)
	require.Nil(t, latest.SelectedProxyLane)
	require.Nil(t, latest.ProxyID)
	require.Nil(t, latest.Proxy)
}

// 选号结果携带门：门安装在调度栈局部 ctx 上，handler 必须经
// ContextWithSelectionProfitGate 重放后终检与准入后绑定才可见（评审修复回归）。
func TestGatewayProfitControlSelectionCarriesGateToHandlerContext(t *testing.T) {
	group := gatewayProfitTestGroup(1, PlatformAnthropic)
	svc := &GatewayService{}
	expensive := gatewayProfitTestAccount(161, PlatformAnthropic, 0.9, group.ID)

	gateCtx := svc.withGatewayProfitControlGate(gatewayProfitTestContext(group), &group.ID)
	selection, err := svc.newSelectionResult(gateCtx, &expensive, true, nil, nil)
	require.NoError(t, err)
	require.True(t, selection.ProfitGateActive(), "选号结果必须携带调度栈内生效的门")

	// 修复前的缺陷形态：handler 原始 ctx 不含门，终检退化为空操作。
	_, vetoed, _ := svc.GatewayProfitControlVetoLatest(context.Background(), &expensive)
	require.False(t, vetoed, "对照组：不重放门时终检确实看不到门")

	handlerCtx := ContextWithSelectionProfitGate(context.Background(), selection)
	latest, vetoed, reason := svc.GatewayProfitControlVetoLatest(handlerCtx, &expensive)
	require.True(t, vetoed, "重放门后终检必须真实生效")
	require.Equal(t, openAIProfitFilterReasonThreshold, reason)
	require.NotNil(t, latest)

	// 无门选号不携带门，重放为无操作。
	plain, err := svc.newSelectionResult(context.Background(), &expensive, true, nil, nil)
	require.NoError(t, err)
	require.False(t, plain.ProfitGateActive())
	require.Equal(t, context.Background(), ContextWithSelectionProfitGate(context.Background(), plain))
}

// 生图意图不关门（H1/H2 回归锚点）：/v1/responses 混合请求即使带生图声明，
// token 定价上下文照常装配，共享门照常安装并否决越线账号。
func TestGatewayProfitControlImageIntentDoesNotDisableGate(t *testing.T) {
	group := gatewayProfitTestGroup(2, PlatformAnthropic)
	svc := &GatewayService{}
	expensive := gatewayProfitTestAccount(162, PlatformAnthropic, 0.9, group.ID)

	ctx := gatewayProfitTestContext(group)
	ctx = WithOpenAIImageGenerationIntent(ctx)
	gateCtx := svc.withGatewayProfitControlGate(ctx, &group.ID)
	require.False(t, svc.isGatewayAccountProfitEligible(gateCtx, &expensive),
		"请求体里的生图声明（含被动 image_gen namespace）不得关闭利润门")
}

// 无门时准入后绑定回退官方 eager 语义；门下读失败保守不写（评审 M-Bind 回归）。
func TestGatewayProfitControlAfterAdmissionBindSemantics(t *testing.T) {
	groupID := int64(3)
	expensiveID := int64(171)
	cheapID := int64(172)

	t.Run("eager without gate", func(t *testing.T) {
		cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"s": expensiveID}}
		svc := &GatewayService{cache: cache}
		require.NoError(t, svc.BindStickySessionAfterProfitAdmission(context.Background(), &groupID, "s", cheapID))
		require.Equal(t, cheapID, cache.sessionBindings["s"], "无门时保持既有 eager 绑定行为")
	})

	t.Run("gated read failure is conservative", func(t *testing.T) {
		// mock 的 miss 返回非 sentinel 错误，等价于 Redis 读失败：门下保守不写。
		cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}
		svc := &GatewayService{cache: cache}
		gate := &openAIProfitControlGate{groupID: groupID, platform: PlatformAnthropic, threshold: 0.5}
		gateCtx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
		require.NoError(t, svc.BindStickySessionAfterProfitAdmission(gateCtx, &groupID, "absent", cheapID))
		require.NotContains(t, cache.sessionBindings, "absent")
	})

	t.Run("gated sentinel miss binds", func(t *testing.T) {
		cache := &sentinelMissGatewayCache{mockGatewayCacheForPlatform: &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{}}}
		svc := &GatewayService{cache: cache}
		gate := &openAIProfitControlGate{groupID: groupID, platform: PlatformAnthropic, threshold: 0.5}
		gateCtx := context.WithValue(context.Background(), openAIProfitControlGateCtxKey{}, gate)
		require.NoError(t, svc.BindStickySessionAfterProfitAdmission(gateCtx, &groupID, "fresh", cheapID))
		require.Equal(t, cheapID, cache.sessionBindings["fresh"], "门下无既有绑定（sentinel miss）应建立粘性")
	})
}

// sentinelMissGatewayCache 让 miss 返回与真实仓库一致的 ErrStickySessionNotFound。
type sentinelMissGatewayCache struct {
	*mockGatewayCacheForPlatform
}

func (c *sentinelMissGatewayCache) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := c.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, ErrStickySessionNotFound
}
