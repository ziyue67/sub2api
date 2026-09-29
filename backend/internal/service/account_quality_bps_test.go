package service

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func bpsQualityPlan() *ScheduledTestPlan {
	plan := pelicanPlan()
	plan.PelicanConfig = stateProbePlanConfig()
	plan.PelicanConfig.Quality = &QualityPolicy{Action: QualityActionEnableBPS, AutoRestore: true, BPS: &QualityBPSPolicy{
		FailureThreshold: 2, UsagePercent: 80, Models: []string{" gpt-6-astra ", "gpt-6-astra", "gpt-5.6-sol"},
		OmitUnsupportedTools: true, IgnoreEncryptedContent: true, TargetGroupID: 9, ProxySource: "ip_pool",
	}}
	return plan
}

func TestQualityBPSPolicyValidationNormalizes(t *testing.T) {
	plan := bpsQualityPlan()
	plan.PelicanConfig.Quality.RemoveGroupIDs = []int64{3}
	_, err := nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	b := plan.PelicanConfig.Quality.BPS
	require.Equal(t, []string{"gpt-6-astra", "gpt-5.6-sol"}, b.Models)
	require.Zero(t, b.TargetGroupID, "target group only applies with the 403 group action")
	require.Empty(t, b.ProxySource, "proxy source only applies with the session proxy")
	require.Equal(t, 1, b.PassThreshold, "rules saved without a healthy count turn BPS off after one pass")
	require.Nil(t, plan.PelicanConfig.Quality.RemoveGroupIDs)

	plan = bpsQualityPlan()
	b = plan.PelicanConfig.Quality.BPS
	b.AllModels, b.SessionProxy, b.ProxySource, b.AutoMoveOn403, b.TargetGroupID = true, true, "", true, 0
	_, err = nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	require.Nil(t, b.Models)
	require.Equal(t, ExcelBPSProxySourceMihomo, b.ProxySource)
	require.Zero(t, b.TargetGroupID, "0 = leave all groups on 403")

	plan = bpsQualityPlan()
	plan.PelicanConfig.Quality.Action = "disable_scheduling"
	_, err = nextPlanRun(plan, time.Now())
	require.NoError(t, err)
	require.Nil(t, plan.PelicanConfig.Quality.BPS, "other actions drop the BPS settings")
}

func TestQualityBPSPolicyValidationRejects(t *testing.T) {
	tooMany := make([]string, 51)
	for i := range tooMany {
		tooMany[i] = strings.Repeat("m", i+1)
	}
	for name, change := range map[string]func(*ScheduledTestPlan){
		"missing settings": func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS = nil },
		"candy question":   func(p *ScheduledTestPlan) { p.PelicanConfig.QuestionKind = "candy"; p.PelicanConfig.Prompt = "q" },
		"no trigger": func(p *ScheduledTestPlan) {
			p.PelicanConfig.Quality.BPS.FailureThreshold, p.PelicanConfig.Quality.BPS.UsagePercent = 0, 0
		},
		"negative count":      func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.FailureThreshold = -1 },
		"count too high":      func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.FailureThreshold = 101 },
		"usage too high":      func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.UsagePercent = 100.5 },
		"usage NaN":           func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.UsagePercent = math.NaN() },
		"no models":           func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.Models = []string{" "} },
		"too many models":     func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.Models = tooMany },
		"model too long":      func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.Models = []string{strings.Repeat("m", 101)} },
		"bad proxy source":    func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.SessionProxy = true },
		"negative pass count": func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.PassThreshold = -1 },
		"pass count too high": func(p *ScheduledTestPlan) { p.PelicanConfig.Quality.BPS.PassThreshold = 101 },
		"negative 403 group": func(p *ScheduledTestPlan) {
			p.PelicanConfig.Quality.BPS.AutoMoveOn403, p.PelicanConfig.Quality.BPS.TargetGroupID = true, -1
		},
	} {
		t.Run(name, func(t *testing.T) {
			plan := bpsQualityPlan()
			plan.PelicanConfig.Quality.BPS.ProxySource = "direct"
			change(plan)
			_, err := nextPlanRun(plan, time.Now())
			require.Error(t, err)
		})
	}
}

func TestQualityBPSExtraMatchesAccountOptions(t *testing.T) {
	extra := QualityBPSExtra(&QualityBPSPolicy{Models: []string{"gpt-6-astra"}, OmitUnsupportedTools: true, IgnoreEncryptedContent: true})
	require.Equal(t, true, extra["openai_excel_bps"])
	require.Equal(t, []string{"gpt-6-astra"}, extra["openai_excel_bps_models"])
	require.Equal(t, true, extra[ExcelBPSOmitUnsupportedToolsKey])
	require.Equal(t, false, extra[ExcelBPSIgnoreImagesKey])
	require.Equal(t, true, extra[ExcelBPSIgnoreEncryptedContentKey])
	require.Equal(t, false, extra["openai_excel_bps_mihomo"])
	require.Nil(t, extra[ExcelBPS403TargetGroupIDKey])
	for _, key := range QualityBPSManagedKeys {
		require.Contains(t, extra, key, "every managed key is written or removed")
	}
	require.Len(t, extra, len(QualityBPSManagedKeys))

	extra = QualityBPSExtra(&QualityBPSPolicy{AllModels: true, AutoMoveOn403: true, TargetGroupID: 5, SessionProxy: true, ProxySource: ExcelBPSProxySourceIPPool})
	require.Nil(t, extra["openai_excel_bps_models"], "all models = no model scope key")
	require.Equal(t, int64(5), extra[ExcelBPS403TargetGroupIDKey])
	require.Equal(t, ExcelBPSProxySourceIPPool, extra[ExcelBPSProxySourceKey])

	// 写入后的账号按规则的模型范围走 BPS。
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{}}
	for key, value := range QualityBPSExtra(&QualityBPSPolicy{Models: []string{"gpt-6-astra"}}) {
		if value != nil {
			account.Extra[key] = value
		}
	}
	require.True(t, account.IsExcelBPSEnabledForModel("gpt-6-astra"))
	require.False(t, account.IsExcelBPSEnabledForModel("gpt-5.6-sol"))
}

func TestQualityBPSTriggerModes(t *testing.T) {
	either := &QualityBPSPolicy{FailureThreshold: 3, UsagePercent: 80}
	require.Equal(t, "", QualityBPSTrigger(either, 2, 79, true))
	require.Equal(t, "degraded", QualityBPSTrigger(either, 3, 0, false))
	require.Equal(t, "usage", QualityBPSTrigger(either, 0, 80, true))
	require.Equal(t, "", QualityBPSTrigger(either, 0, 99, false), "no usage snapshot never triggers")

	all := &QualityBPSPolicy{FailureThreshold: 3, UsagePercent: 80, RequireAll: true}
	require.Equal(t, "", QualityBPSTrigger(all, 3, 50, true))
	require.Equal(t, "", QualityBPSTrigger(all, 1, 90, true))
	require.Equal(t, "degraded", QualityBPSTrigger(all, 3, 90, true))

	countOnly := &QualityBPSPolicy{FailureThreshold: 1, RequireAll: true}
	require.Equal(t, "degraded", QualityBPSTrigger(countOnly, 1, 0, false), "require_all with one trigger behaves like that trigger")
	require.Equal(t, "", QualityBPSTrigger(nil, 10, 100, true))

	require.False(t, QualityBPSHoldForUsage(either, 85, true), "holding for usage is opt-in")
	either.HoldOnUsage, all.HoldOnUsage = true, true
	require.True(t, QualityBPSHoldForUsage(either, 85, true))
	require.False(t, QualityBPSHoldForUsage(either, 20, true))
	require.False(t, QualityBPSHoldForUsage(either, 99, false), "no usage snapshot never holds")
	require.False(t, QualityBPSHoldForUsage(all, 85, true), "require_all rules restore once the probe passes")
	require.False(t, QualityBPSHoldForUsage(&QualityBPSPolicy{FailureThreshold: 2, HoldOnUsage: true}, 100, true))

	require.Equal(t, 1, QualityBPSPassThreshold(nil), "a rule that no longer enables BPS restores after one pass")
	require.Equal(t, 1, QualityBPSPassThreshold(&QualityBPSPolicy{}))
	require.Equal(t, 3, QualityBPSPassThreshold(&QualityBPSPolicy{PassThreshold: 3}))
}

func TestQualityUsagePercentIgnoresResetWindows(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	used, ok := QualityUsagePercent(map[string]any{}, now)
	require.False(t, ok)
	require.Zero(t, used)

	used, ok = QualityUsagePercent(map[string]any{
		"codex_5h_used_percent": 91.5, "codex_5h_reset_at": now.Add(-time.Minute).Format(time.RFC3339),
		"codex_7d_used_percent": 40.0, "codex_7d_reset_at": now.Add(time.Hour).Format(time.RFC3339),
	}, now)
	require.True(t, ok)
	require.Equal(t, 40.0, used, "an expired 5h window counts as 0")

	used, _ = QualityUsagePercent(map[string]any{"codex_5h_used_percent": 91.5, "codex_7d_used_percent": 40.0}, now)
	require.Equal(t, 91.5, used)
}

func TestQualityBPSEligibleMatchesAccountRules(t *testing.T) {
	parent := int64(1)
	require.True(t, QualityBPSEligible(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}))
	require.False(t, QualityBPSEligible(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"plan_type": "free"}}))
	require.False(t, QualityBPSEligible(nil))
	require.False(t, QualityBPSEligible(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}))
	require.False(t, QualityBPSEligible(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, ParentAccountID: &parent}))
	require.False(t, QualityBPSEligible(&Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity}}))
}

// 「降智开 BPS」规则开启 BPS 后仍要探直连通道；普通探针遇到 BPS 模型照旧不适用。
func TestStateProbeQualityBPSRuleProbesPastBPS(t *testing.T) {
	account := stateProbeAccount()
	account.Extra = map[string]any{"openai_excel_bps": true}
	require.NotEmpty(t, openAICodexStateProbeUnsupportedReason(account, "gpt-6-astra", false))
	require.Empty(t, openAICodexStateProbeUnsupportedReason(account, "gpt-6-astra", true))

	upstream := &stateProbeUpstream{replies: []stateProbeReply{
		stateProbeMint("ticket-1"),
		{status: http.StatusOK, body: stateProbeCompletedStream},
	}}
	svc := stateProbeTestService(upstream)
	svc.accountRepo = &stateProbeAccountRepo{account: account}
	cfg := stateProbePlanConfig()
	cfg.Quality = &QualityPolicy{Action: QualityActionEnableBPS, BPS: &QualityBPSPolicy{FailureThreshold: 1, AllModels: true}}
	result, err := svc.RunPelicanBackground(context.Background(), 7, "gpt-6-astra", cfg)
	require.NoError(t, err)
	require.Equal(t, "success", result.Status)
	require.Len(t, upstream.calls, 2)

	cfg.Quality.Action = "disable_scheduling"
	result, err = svc.RunPelicanBackground(context.Background(), 7, "gpt-6-astra", cfg)
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Contains(t, result.ErrorMessage, openAICodexStateInconclusiveErrPrefix)
}

type bpsRecoveryPlanRepo struct {
	qualityPlanRepo
	ownsBPS bool
}

func (r *bpsRecoveryPlanRepo) ClaimPelican(ctx context.Context, plan *ScheduledTestPlan, now, until, next time.Time) (bool, error) {
	claimed, err := r.pelicanPlanRepo.ClaimPelican(ctx, plan, now, until, next)
	if claimed {
		plan.PelicanConfig.BPSRecoveryPending = r.ownsBPS
	}
	return claimed, err
}

func TestQualityBPSRecoveryProbeAfterActionChange(t *testing.T) {
	for _, action := range []string{"disable_scheduling", "remove_groups"} {
		for _, owned := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/owned=%t", action, owned), func(t *testing.T) {
				account := stateProbeAccount()
				account.Extra = map[string]any{"openai_excel_bps": true}
				upstream := &stateProbeUpstream{replies: []stateProbeReply{
					stateProbeMint("recovery-ticket"),
					{status: http.StatusOK, body: stateProbeCompletedStream},
				}}
				svc := stateProbeTestService(upstream)
				svc.accountRepo = &stateProbeAccountRepo{account: account}
				plans := &bpsRecoveryPlanRepo{ownsBPS: owned}
				results := &pelicanResults{}
				runner := &ScheduledTestRunnerService{planRepo: plans, scheduledSvc: NewScheduledTestService(plans, results), runPelican: svc.RunPelicanBackground}
				plan := pelicanPlan()
				plan.AccountID = account.ID
				plan.PelicanConfig = stateProbePlanConfig()
				plan.PelicanConfig.Quality = &QualityPolicy{Action: action, AutoRestore: true, RemoveGroupIDs: []int64{3}}
				runner.runOnePlan(context.Background(), plan)
				require.True(t, plans.finished)
				require.Len(t, results.results, 1)
				if owned {
					require.Equal(t, []string{"passed"}, plans.outcomes)
					require.Len(t, upstream.calls, 2)
				} else {
					require.Equal(t, []string{"inconclusive"}, plans.outcomes, "manual BPS remains unsupported")
					require.Empty(t, upstream.calls)
				}
			})
		}
	}
}

func TestQualityBPSRecoveryOwnershipIsRuntimeOnly(t *testing.T) {
	config := stateProbePlanConfig()
	config.BPSRecoveryPending = true
	data, err := json.Marshal(config)
	require.NoError(t, err)
	var decoded PelicanTestConfig
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.False(t, decoded.BPSRecoveryPending)
	data, err = json.Marshal(map[string]any{"BPSRecoveryPending": true, "bps_recovery_pending": true})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.False(t, decoded.BPSRecoveryPending)
}
