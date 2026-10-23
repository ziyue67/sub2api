package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestOpenAIGatewayService_SelectAccountWithScheduler_EncryptedStickyKeepsTTFT(t *testing.T) {
	ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(`{"input":[{"type":"reasoning","encrypted_content":"synthetic"}]}`))
	groupID := int64(10101)
	accounts := []Account{
		{
			ID:          21101,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    0,
			GroupIDs:    []int64{groupID},
		},
		{
			ID:          21102,
			Platform:    PlatformOpenAI,
			Type:        AccountTypeAPIKey,
			Status:      StatusActive,
			Schedulable: true,
			Concurrency: 1,
			Priority:    1,
			GroupIDs:    []int64{groupID},
		},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_ttft": 21101}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	concurrencyCache := schedulerTestConcurrencyCache{acquireResults: map[int64]bool{21102: true}}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                cfg,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(concurrencyCache),
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
	}
	fastTTFT := 14999
	svc.openaiAccountStats.report(21101, true, &fastTTFT)
	stableTTFT := 14999
	svc.openaiAccountStats.report(21101, true, &stableTTFT)

	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_sticky_ttft", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(21101), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerSessionSticky, decision.Layer)
	require.True(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}

	slowTTFT := 20000
	for i := 0; i < 3; i++ {
		svc.openaiAccountStats.report(21101, true, &slowTTFT)
	}

	selection, decision, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_sticky_ttft", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(21101), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerSessionSticky, decision.Layer)
	require.True(t, decision.StickySessionHit)
	require.Equal(t, int64(21101), cache.sessionBindings["openai:session_hash_sticky_ttft"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_EncryptedStickyKeepsErrorRate(t *testing.T) {
	ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(`{"input":[{"type":"reasoning","encrypted_content":"synthetic"}]}`))
	groupID := int64(10102)
	accounts := []Account{
		{ID: 21201, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}},
		{ID: 21202, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_error_rate": 21201}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                cfg,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{21202: true}}),
		openaiAccountStats: newOpenAIAccountRuntimeStats(),
	}
	for i := 0; i < 3; i++ {
		svc.openaiAccountStats.report(21201, false, nil)
	}
	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_sticky_error_rate", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(21201), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerSessionSticky, decision.Layer)
	require.True(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
	for i := 0; i < 2; i++ {
		svc.openaiAccountStats.report(21201, false, nil)
	}

	selection, decision, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_sticky_error_rate", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(21201), selection.Account.ID)
	require.Equal(t, openAIAccountScheduleLayerSessionSticky, decision.Layer)
	require.True(t, decision.StickySessionHit)
	require.Equal(t, int64(21201), cache.sessionBindings["openai:session_hash_sticky_error_rate"])
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}

func TestOpenAIGatewayService_SelectAccountWithScheduler_EncryptedStickyBusyStillEscapes(t *testing.T) {
	ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(`{"input":[{"type":"reasoning","encrypted_content":"synthetic"}]}`))
	groupID := int64(10103)
	accounts := []Account{
		{ID: 21301, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0, GroupIDs: []int64{groupID}},
		{ID: 21302, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 1, GroupIDs: []int64{groupID}},
	}
	cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:session_hash_sticky_busy_escape": 21301}}
	cfg := &config.Config{}
	cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
	cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
	cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
	cfg.Gateway.Scheduling.StickySessionMaxWaiting = 2
	cfg.Gateway.Scheduling.StickySessionWaitTimeout = 45 * time.Second
	concurrencyCache := schedulerTestConcurrencyCache{
		acquireResults: map[int64]bool{21301: false, 21302: true},
		waitCounts:     map[int64]int{21301: 999},
		loadMap: map[int64]*AccountLoadInfo{
			21301: {AccountID: 21301, LoadRate: 95, WaitingCount: 9},
			21302: {AccountID: 21302, LoadRate: 1, WaitingCount: 0},
		},
	}
	svc := &OpenAIGatewayService{
		accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
		cache:              cache,
		cfg:                cfg,
		rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService("true"),
		concurrencyService: NewConcurrencyService(concurrencyCache),
	}

	selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "session_hash_sticky_busy_escape", "gpt-5.1", nil, OpenAIUpstreamTransportAny, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.NotNil(t, selection.Account)
	require.Equal(t, int64(21302), selection.Account.ID)
	require.Nil(t, selection.WaitPlan)
	require.Equal(t, openAIAccountScheduleLayerLoadBalance, decision.Layer)
	require.False(t, decision.StickySessionHit)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
func TestEncryptedPriorityStickyCapacityStillRebalances(t *testing.T) {
	for _, test := range []struct {
		name                        string
		preserve, disable, previous bool
		otherGroup                  int64
		expectEscape                bool
	}{
		{name: "movable", expectEscape: true},
		{name: "task_owner", preserve: true},
		{name: "escape_disabled", disable: true},
		{name: "response_owner", previous: true},
		{name: "outside_group", otherGroup: 99},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := DefaultPrioritySchedulingConfig()
			cfg.Enabled = true
			groupID := int64(11)
			accounts := []Account{
				{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 30, GroupIDs: []int64{3, 5, 11, 13, 15}},
				{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 20, GroupIDs: []int64{11, 15}},
			}
			if test.otherGroup != 0 {
				accounts[1].GroupIDs = []int64{test.otherGroup}
			}
			gateway := priorityGateway(cfg, &priorityReaderStub{})
			gateway.accountRepo = schedulerTestOpenAIAccountRepo{accounts: accounts}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:existing-session": 1}}
			gateway.cache = cache
			gateway.concurrencyService = NewConcurrencyService(schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: 18}, 2: {AccountID: 2}}})
			scheduler := &defaultOpenAIAccountScheduler{service: gateway, stats: newOpenAIAccountRuntimeStats()}
			req := OpenAIAccountScheduleRequest{EncryptedReasoning: true, Platform: PlatformOpenAI, GroupID: &groupID, SessionHash: "existing-session", StickyAccountID: 1, UseUpstreamTokenCost: true, PreserveStickyBinding: test.preserve, DisableStickyEscape: test.disable}
			if test.previous {
				req.PreviousResponseID = "resp-owner"
			}
			require.Equal(t, test.expectEscape, scheduler.shouldRebalancePrioritySticky(context.Background(), req, &accounts[0]))
			selected, escaped, err := scheduler.selectBySessionHash(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, test.expectEscape, escaped)
			if test.expectEscape {
				require.Nil(t, selected)
			} else {
				require.NotNil(t, selected)
				if selected.ReleaseFunc != nil {
					selected.ReleaseFunc()
				}
			}
			require.Equal(t, int64(1), cache.sessionBindings["openai:existing-session"])
		})
	}
}

func TestEncryptedStickyHardGates(t *testing.T) {
	for _, gate := range []string{"excluded", "disabled", "wrong group", "wrong platform", "rate limit", "model", "transport"} {
		t.Run(gate, func(t *testing.T) {
			group := int64(9)
			acquired := []int64{}
			account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group}}
			req := OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, GroupID: &group, SessionHash: "hard-gate", StickyAccountID: 1, RequestedModel: "gpt-5.1", EncryptedReasoning: true}
			switch gate {
			case "excluded":
				req.ExcludedIDs = map[int64]struct{}{1: {}}
			case "disabled":
				account.Schedulable = false
			case "wrong group":
				account.GroupIDs = []int64{10}
			case "wrong platform":
				account.Platform = PlatformGrok
			case "rate limit":
				until := time.Now().Add(time.Hour)
				account.RateLimitResetAt = &until
			case "model":
				account.Credentials = map[string]any{"model_mapping": map[string]any{"other-model": "other-model"}}
			case "transport":
				req.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2
				account.Credentials = map[string]any{"openai_ws_force_http": true}
			}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cfg: &config.Config{}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired})}
			scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
			selection, escaped, err := scheduler.selectBySessionHash(context.Background(), req)
			require.NoError(t, err)
			require.Nil(t, selection)
			require.False(t, escaped)
			require.Empty(t, acquired, "quality affinity cannot bypass hard gate")
		})
	}
}

func TestEncryptedStickyDetection(t *testing.T) {
	for _, tc := range []struct {
		body      string
		encrypted bool
	}{
		{`{"input":[{"type":"reasoning","encrypted_content":"synthetic"}]}`, true},
		{`{"input":{"type":"reasoning","encrypted_content":"synthetic"}}`, true},
		{`{"input":[{"type":"reasoning","encrypted_content":null}]}`, false},
		{`{"input":[{"type":"reasoning","encrypted_content":""}]}`, false},
		{`{"input":[{"type":"compaction","encrypted_content":"history"}]}`, false},
		{`{"input":[{"type":"agent_message","encrypted_content":"task"}]}`, false},
		{`{"input":[{"role":"user","content":"encrypted_content"}]}`, false},
	} {
		ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(tc.body))
		require.Equal(t, tc.encrypted, ctx.Value(openAIEncryptedReasoningAffinityKey{}))
	}
}

func TestEncryptedStickyRPMHardGate(t *testing.T) {
	ctx := WithOpenAIEncryptedReasoningAffinity(context.Background(), []byte(`{"input":[{"type":"reasoning","encrypted_content":"synthetic"}]}`))
	group := int64(9)
	acquired := []int64{}
	account := Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true, Concurrency: 1, GroupIDs: []int64{group}, Extra: map[string]any{"base_rpm": 1, "rpm_strategy": "sticky_exempt"}}
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cfg: &config.Config{}, rpmCache: &openAIRPMTestCache{counts: map[int64]int{1: 1}}, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{acquiredIDs: &acquired})}
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	selection, escaped, err := scheduler.selectBySessionHash(ctx, OpenAIAccountScheduleRequest{Platform: PlatformOpenAI, GroupID: &group, SessionHash: "rpm-gate", StickyAccountID: 1, RequestedModel: "gpt-5.1", EncryptedReasoning: true})
	require.NoError(t, err)
	require.Nil(t, selection)
	require.False(t, escaped)
	require.Empty(t, acquired)
}

func TestEncryptedStickyProfitVetoKeepsBinding(t *testing.T) {
	ctx := WithOpenAIEncryptedReasoningAffinity(profitControlTestCtx(profitControlTestGroup(23, 0.5, 0)), []byte(`{"input":[{"type":"reasoning","encrypted_content":"synthetic"}]}`))
	groupID := int64(23)
	now := time.Now()
	expensive := profitControlWSAccount(31, 0.8, now)

	cache := &stubGatewayCache{}
	store := NewOpenAIWSStateStore(cache)
	svc := &OpenAIGatewayService{
		accountRepo:        stubOpenAIAccountRepo{accounts: []Account{expensive}},
		cache:              cache,
		cfg:                newOpenAIWSV2TestConfig(),
		concurrencyService: NewConcurrencyService(stubConcurrencyCache{}),
		openaiWSStateStore: store,
	}
	require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_profit", expensive.ID, time.Hour))

	selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_profit", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.Nil(t, selection, "上游倍率 0.8 超过阈值 0.5 的账号不应继续命中 previous_response_id 粘连")

	// 利润不合格与 quota auto-pause 同为暂时状态：绑定必须保留。
	boundAccountID, getErr := store.GetResponseAccount(ctx, groupID, "resp_profit")
	require.NoError(t, getErr)
	require.Equal(t, expensive.ID, boundAccountID)

	// 上游倍率回落（探测刷新）后同一绑定重新可用。
	recovered := profitControlWSAccount(31, 0.3, time.Now())
	svc.accountRepo = stubOpenAIAccountRepo{accounts: []Account{recovered}}
	selection, err = svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_profit", "gpt-5.1", nil, false)
	require.NoError(t, err)
	require.NotNil(t, selection)
	require.Equal(t, recovered.ID, selection.Account.ID)
	if selection.ReleaseFunc != nil {
		selection.ReleaseFunc()
	}
}
