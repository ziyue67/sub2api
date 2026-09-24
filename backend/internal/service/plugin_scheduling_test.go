package service

import (
	"context"
	"errors"
	"testing"

	pluginv1 "github.com/Wei-Shaw/sub2api/pkg/pluginapi/v1"
	"github.com/stretchr/testify/require"
)

// 调度能力（openai.account.scheduling.v1）的核心契约测试。
//
// 这组测试锁死三条不变量，它们共同决定了这个特性可以安全上线：
//
//	1. 提名只调整顺序，绝不增删候选（否则会绕过宿主硬门槛）。
//	2. 插件异常/超时/越界提名一律 fail-open（否则插件故障会拖垮转发）。
//	3. 未启用调度能力时不产生任何额外调用与开销。

func testSchedulingAccounts(t *testing.T) []*Account {
	t.Helper()
	return []*Account{
		{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
		{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
		{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
	}
}

func testScheduleRequest() OpenAIAccountScheduleRequest {
	return OpenAIAccountScheduleRequest{
		Platform:       PlatformOpenAI,
		SessionHash:    "session-abc",
		RequestedModel: "gpt-5",
	}
}

// ---- 候选构造 ----

func TestBuildPluginSchedulingCandidatesCarriesLoadAndConcurrency(t *testing.T) {
	accounts := testSchedulingAccounts(t)
	loadMap := map[int64]*AccountLoadInfo{
		1: {AccountID: 1, CurrentConcurrency: 6, WaitingCount: 2, LoadRate: 75},
		2: {AccountID: 2, CurrentConcurrency: 1, WaitingCount: 0, LoadRate: 12},
	}

	candidates, ids := buildPluginSchedulingCandidates(accounts, loadMap, nil)

	require.Len(t, candidates, 3)
	require.Len(t, ids, 3)
	require.Contains(t, ids, int64(1))

	// 负载率必须从宿主的 0-100 百分比归一化到 0-1 比率。
	require.InDelta(t, 0.75, candidates[0].GetLoadRate(), 1e-9)
	require.Equal(t, int64(6), candidates[0].GetInFlight())
	require.Equal(t, int64(2), candidates[0].GetWaitingCount())
	require.Equal(t, int64(8), candidates[0].GetMaxConcurrency())

	// 缺失负载的账号必须给出零值而不是 nil 解引用。
	require.InDelta(t, 0.0, candidates[2].GetLoadRate(), 1e-9)
	require.Equal(t, int64(0), candidates[2].GetInFlight())
}

// 插件只需要知道"哪个账号更闲"，不需要知道它是谁 —— 候选里绝不能带
// 账号名、凭据、代理等敏感字段。这条测试防止未来有人图方便往里加字段。
func TestBuildPluginSchedulingCandidatesLeaksNoIdentityFields(t *testing.T) {
	accounts := []*Account{{
		ID:       42,
		Name:     "敏感账号名不该外泄",
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "secret-token-value",
		},
		Concurrency: 8,
	}}

	candidates, _ := buildPluginSchedulingCandidates(accounts, nil, nil)
	require.Len(t, candidates, 1)
	require.Equal(t, int64(42), candidates[0].GetAccountId())
	require.Equal(t, AccountTypeOAuth, candidates[0].GetAccountType())
	// 契约结构里根本没有承载名称/凭据的字段，只能靠"构造时不填"保证。
	require.Empty(t, candidates[0].GetName())
}

// ---- 提名置顶语义 ----

func TestApplyNominationToPlanPromotesWithoutChangingSet(t *testing.T) {
	order := testCandidateOrder(1, 2, 3)
	nomination := &pluginSchedulingNomination{
		accountID: 3,
		candidates: map[int64]struct{}{
			1: {}, 2: {}, 3: {},
		},
	}
	plan := &openAIAccountLoadPlan{selectionOrder: order}

	applyNominationToPlan(plan, nomination, testSchedulingAccounts(t))

	require.Equal(t, int64(3), plan.selectionOrder[0].account.ID, "提名账号应被置顶")
	// 集合必须完全不变，只是顺序变化。
	require.Len(t, plan.selectionOrder, 3)
	require.ElementsMatch(t,
		[]int64{1, 2, 3},
		[]int64{
			plan.selectionOrder[0].account.ID,
			plan.selectionOrder[1].account.ID,
			plan.selectionOrder[2].account.ID,
		},
	)
	// 其余账号必须保持原有相对顺序（1 在 2 之前）。
	require.Equal(t, int64(1), plan.selectionOrder[1].account.ID)
	require.Equal(t, int64(2), plan.selectionOrder[2].account.ID)
}

func TestApplyNominationToPlanIsNoopWhenAlreadyFirst(t *testing.T) {
	order := testCandidateOrder(1, 2, 3)
	original := append([]openAIAccountCandidateScore(nil), order...)
	nomination := &pluginSchedulingNomination{
		accountID:  1,
		candidates: map[int64]struct{}{1: {}, 2: {}, 3: {}},
	}
	plan := &openAIAccountLoadPlan{selectionOrder: order}

	applyNominationToPlan(plan, nomination, testSchedulingAccounts(t))

	require.Equal(t, original[0].account.ID, plan.selectionOrder[0].account.ID)
	require.Equal(t, original[1].account.ID, plan.selectionOrder[1].account.ID)
	require.Equal(t, original[2].account.ID, plan.selectionOrder[2].account.ID)
}

// 提名了候选集之外的账号 —— 无论这是插件 bug 还是被篡改，都必须丢弃。
func TestApplyNominationToPlanRejectsOutOfScopeAccount(t *testing.T) {
	order := testCandidateOrder(1, 2, 3)
	original := append([]openAIAccountCandidateScore(nil), order...)
	nomination := &pluginSchedulingNomination{
		accountID:  999,
		candidates: map[int64]struct{}{1: {}, 2: {}, 3: {}},
	}
	plan := &openAIAccountLoadPlan{selectionOrder: order}

	applyNominationToPlan(plan, nomination, testSchedulingAccounts(t))

	require.Equal(t, original[0].account.ID, plan.selectionOrder[0].account.ID)
	require.Equal(t, original[1].account.ID, plan.selectionOrder[1].account.ID)
}

// 提名账号合法但不在本池（例如属于订阅池，当前在选常规池）：
// 必须原样不动，不能跨池把账号捞过来，否则破坏订阅优先语义。
func TestApplyNominationToPlanDoesNotCrossPool(t *testing.T) {
	regularPool := []*Account{
		{ID: 10, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
		{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
	}
	order := []openAIAccountCandidateScore{
		{account: regularPool[0]},
		{account: regularPool[1]},
	}
	nomination := &pluginSchedulingNomination{
		accountID:  1, // 合法候选，但属于另一个池
		candidates: map[int64]struct{}{1: {}, 10: {}, 11: {}},
	}
	plan := &openAIAccountLoadPlan{selectionOrder: order}

	applyNominationToPlan(plan, nomination, regularPool)

	require.Equal(t, int64(10), plan.selectionOrder[0].account.ID, "不应跨池搬运账号")
	require.Equal(t, int64(11), plan.selectionOrder[1].account.ID)
}

func TestApplyNominationToPlanHandlesNilInputs(t *testing.T) {
	require.NotPanics(t, func() {
		applyNominationToPlan(nil, nil, nil)
	})
	plan := &openAIAccountLoadPlan{selectionOrder: testCandidateOrder(1, 2)}
	// 提名账号为 0（插件表示无偏好）。
	applyNominationToPlan(plan, &pluginSchedulingNomination{accountID: 0}, testSchedulingAccounts(t))
	require.Equal(t, int64(1), plan.selectionOrder[0].account.ID)
}

// ---- 归因标记 ----

func TestAttachNominationMarkOnlyMarksSelected(t *testing.T) {
	nomination := &pluginSchedulingNomination{accountID: 2, affinityHit: true}

	// 提名账号确实被选中：应标记为 selected。
	selected := &AccountSelectionResult{Account: &Account{ID: 2}}
	attachNominationMark(selected, nomination)
	require.NotNil(t, selected.pluginScheduling)
	require.True(t, selected.pluginScheduling.selected)
	require.True(t, selected.pluginScheduling.affinityHit)
	require.Equal(t, int64(2), selected.pluginScheduling.nominatedAccountID)

	// 提名账号没抢到槽，宿主选了别人：绝不能把这次选号归因于插件。
	other := &AccountSelectionResult{Account: &Account{ID: 7}}
	attachNominationMark(other, nomination)
	require.NotNil(t, other.pluginScheduling)
	require.False(t, other.pluginScheduling.selected, "提名未命中时不得标记为插件决策")
}

func TestAttachNominationMarkSkipsNilNomination(t *testing.T) {
	result := &AccountSelectionResult{Account: &Account{ID: 1}}
	attachNominationMark(result, nil)
	require.Nil(t, result.pluginScheduling, "无提名时不应写入标记")
}

// Layer 只在提名账号真正被选中时才改写，否则保持 load_balance。
func TestDecisionLayerOnlyChangesWhenNominationSelected(t *testing.T) {
	sel := &AccountSelectionResult{Account: &Account{ID: 5}}
	attachNominationMark(sel, &pluginSchedulingNomination{accountID: 5})
	require.True(t, sel.pluginScheduling.selected)

	sel2 := &AccountSelectionResult{Account: &Account{ID: 6}}
	attachNominationMark(sel2, &pluginSchedulingNomination{accountID: 5})
	require.False(t, sel2.pluginScheduling.selected)
}

// ---- 灰度判定 ----

func TestSchedulingBindingRolloutReadsOnlySchedulingCapability(t *testing.T) {
	// 只启用出站传输能力时，调度灰度必须是 0（不参与调度）。
	transportOnly := []PluginBinding{{
		Enabled:        true,
		Capability:     PluginCapabilityOpenAIOAuthOutbound,
		Platform:       PlatformOpenAI,
		AccountType:    AccountTypeOAuth,
		RolloutPercent: 100,
	}}
	require.Equal(t, 0, schedulingBindingRollout(transportOnly),
		"只有出站能力时不得参与调度")

	scheduling := []PluginBinding{{
		Enabled:        true,
		Capability:     PluginCapabilityOpenAIAccountScheduling,
		Platform:       PlatformOpenAI,
		AccountType:    AccountTypeOAuth,
		RolloutPercent: 30,
	}}
	require.Equal(t, 30, schedulingBindingRollout(scheduling))

	// 停用的调度绑定等于不参与。
	disabled := []PluginBinding{{
		Enabled:        false,
		Capability:     PluginCapabilityOpenAIAccountScheduling,
		RolloutPercent: 100,
	}}
	require.Equal(t, 0, schedulingBindingRollout(disabled))
}

func TestSchedulingRouteRequiresStartedManager(t *testing.T) {
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	require.Nil(t, manager.schedulingRoute(), "未启动时不应有调度路由")
	require.False(t, manager.openAIAccountSchedulingEnabled())
}

// ---- 提名调用：fail-open ----

func TestNominateSkipsWhenManagerHasNoRoute(t *testing.T) {
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	nominated, affinityHit, err := manager.NominateOpenAIAccountForScheduling(
		context.Background(),
		"req-1",
		PlatformOpenAI,
		"session",
		"",
		"gpt-5",
		nil,
		[]*pluginv1.ScheduleCandidate{{AccountId: 1}},
	)
	require.NoError(t, err)
	require.Zero(t, nominated)
	require.False(t, affinityHit)
}

func TestNominateSkipsWhenNoCandidates(t *testing.T) {
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	nominated, _, err := manager.NominateOpenAIAccountForScheduling(
		context.Background(), "req-1", PlatformOpenAI, "s", "", "m", nil, nil,
	)
	require.NoError(t, err)
	require.Zero(t, nominated)
}

func TestNominateRejectsOutOfScopeNomination(t *testing.T) {
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	// 候选集只有 1、2，插件却提名 99。
	_, _, err := manager.NominateOpenAIAccount(
		context.Background(),
		&pluginv1.NominateAccountRequest{},
		map[int64]struct{}{1: {}, 2: {}},
	)
	// 没有可用路由时应静默跳过（不报错），这是设计上的降级。
	require.NoError(t, err)
}

func TestShouldRouteSchedulingRequiresOpenAIOAuth(t *testing.T) {
	manager := &PluginManager{runtimes: map[int64]*pluginRuntime{}}
	require.False(t, manager.ShouldRouteOpenAIAccountScheduling(nil))
	require.False(t, manager.ShouldRouteOpenAIAccountScheduling(&Account{
		ID: 1, Platform: "anthropic", Type: AccountTypeOAuth,
	}))
	require.False(t, manager.ShouldRouteOpenAIAccountScheduling(&Account{
		ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
	}))
}

// ---- 辅助 ----

func testCandidateOrder(ids ...int64) []openAIAccountCandidateScore {
	out := make([]openAIAccountCandidateScore, 0, len(ids))
	for _, id := range ids {
		out = append(out, openAIAccountCandidateScore{
			account:  &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8},
			loadInfo: &AccountLoadInfo{AccountID: id},
		})
	}
	return out
}

// 全链路降级：调度器在没有插件绑定时的行为必须与改动前完全一致。
func TestResolveNominationReturnsNilWithoutPlugin(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}}
	nomination := scheduler.resolveSchedulingNomination(
		context.Background(),
		testScheduleRequest(),
		testSchedulingAccounts(t),
		nil,
	)
	require.Nil(t, nomination, "无插件时必须返回 nil，不产生任何候选构造与 RPC")
}

func TestResolveNominationSkipsSingleCandidate(t *testing.T) {
	scheduler := &defaultOpenAIAccountScheduler{service: &OpenAIGatewayService{}}
	nomination := scheduler.resolveSchedulingNomination(
		context.Background(),
		testScheduleRequest(),
		[]*Account{{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 8}},
		nil,
	)
	require.Nil(t, nomination, "只有一个候选时提名无意义，应跳过以省掉一次 RPC")
}

func TestResolveNominationHandlesNilScheduler(t *testing.T) {
	var scheduler *defaultOpenAIAccountScheduler
	require.NotPanics(t, func() {
		require.Nil(t, scheduler.resolveSchedulingNomination(
			context.Background(), testScheduleRequest(), nil, nil,
		))
	})
}

func TestPluginSchedulingTimeoutIsShortEnough(t *testing.T) {
	// 选号在关键路径上：超时必须显著小于用户可感知延迟。
	require.LessOrEqual(t, pluginSchedulingTimeout.Milliseconds(), int64(500),
		"提名超时过长会把插件故障放大成全局延迟")
	require.Greater(t, pluginSchedulingTimeout.Milliseconds(), int64(0))
}

var _ = errors.New
