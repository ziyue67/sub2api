package service

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/qualityqueue"
	"github.com/Wei-Shaw/sub2api/internal/pkg/qualityqueue/queuetest"
	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"
)

func TestQuality5xxSignalsFilterAndCoalesceAcrossReplicas(t *testing.T) {
	r := miniredis.RunT(t)
	client := queuetest.NewClient(r.Addr())
	defer func() { _ = client.Close() }()
	trigger := &quality5xxTrigger{queue: qualityqueue.NewRedis(client)}
	t.Cleanup(func() { _ = trigger.queue.Close() })
	ctx := context.Background()
	oauth := &Account{ID: 42, Type: AccountTypeOAuth}
	trigger.Observe(ctx, oauth, 429)
	trigger.Observe(ctx, &Account{ID: 43, Type: AccountTypeAPIKey}, 503)
	trigger.Observe(ctx, &Account{ID: 44, Type: AccountTypeSetupToken}, 503)
	trigger.Observe(context.WithValue(ctx, qualityProbeContextKey{}, true), oauth, 503)
	require.EqualValues(t, 0, client.ZCard(ctx, quality5xxPendingKey).Val())
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); trigger.Observe(ctx, oauth, 502) }()
	}
	wg.Wait()
	require.Equal(t, []string{"42"}, client.ZRange(ctx, quality5xxPendingKey, 0, -1).Val())
	client.ZRem(ctx, quality5xxPendingKey, "42")
	trigger.Observe(ctx, oauth, 500)
	require.EqualValues(t, 1, client.ZCard(ctx, quality5xxPendingKey).Val(), "a new failure after completion must run immediately")
	r.FastForward(61 * time.Second)
	trigger.Observe(ctx, oauth, 599)
	require.EqualValues(t, 1, client.ZCard(ctx, quality5xxPendingKey).Val())
}

type qualityTriggerPlans struct {
	pelicanPlanRepo
	next     time.Time
	outcomes int
}

func (r *qualityTriggerPlans) ClaimPelican(ctx context.Context, p *ScheduledTestPlan, now, until, next time.Time) (bool, error) {
	r.next = next
	return r.pelicanPlanRepo.ClaimPelican(ctx, p, now, until, next)
}
func (r *qualityTriggerPlans) ApplyQualityOutcome(context.Context, *ScheduledTestPlan, time.Time, string) (string, error) {
	r.outcomes++
	return "inconclusive", nil
}

func TestQuality5xxRunPreservesFutureScheduleAndUsesNormalResults(t *testing.T) {
	future := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	plan := pelicanPlan()
	plan.NextRunAt = &future
	plan.TriggerSource = quality5xxSource
	plan.PelicanConfig.QuestionKind = "candy"
	plan.PelicanConfig.Quality = &QualityPolicy{TriggerOnUpstream5xx: true, ExpectedAnswer: "21", Action: "disable_scheduling"}
	repo := &qualityTriggerPlans{}
	results := &pelicanResults{}
	runner := &ScheduledTestRunnerService{planRepo: repo, scheduledSvc: NewScheduledTestService(repo, results)}
	runner.runPelican = func(ctx context.Context, _ int64, _ string, cfg *PelicanTestConfig) (*ScheduledTestResult, error) {
		require.Equal(t, true, ctx.Value(qualityProbeContextKey{}))
		return &ScheduledTestResult{Status: "failed", ErrorMessage: "upstream_503", PelicanConfig: cfg}, nil
	}
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, future, repo.next)
	require.Equal(t, 1, repo.outcomes)
	require.Len(t, results.results, 2)
	for _, r := range results.results {
		require.Equal(t, quality5xxSource, r.PelicanConfig.TriggerSource)
		require.Equal(t, "inconclusive", r.QualityAction)
	}
	require.True(t, repo.finished)
}

func (r *qualityTriggerPlans) FinishTriggeredQuality(ctx context.Context, p *ScheduledTestPlan, until, finished, next time.Time) error {
	return r.FinishPelican(ctx, p.ID, until, finished)
}

func TestQuality5xxOnlyObservesActualHTTPFailures(t *testing.T) {
	r := miniredis.RunT(t)
	client := queuetest.NewClient(r.Addr())
	defer func() { _ = client.Close() }()
	limiter := &RateLimitService{qualityTrigger: &quality5xxTrigger{queue: qualityqueue.NewRedis(client)}}
	t.Cleanup(func() { _ = limiter.qualityTrigger.queue.Close() })
	ctx := context.Background()
	account := &Account{ID: 21, Type: AccountTypeOAuth}
	limiter.observeQualityResponse(ctx, account, nil, errors.New("network"))
	limiter.observeQualityResponse(ctx, account, &http.Response{StatusCode: 200}, nil)
	limiter.observeQualityResponse(ctx, account, &http.Response{StatusCode: 502}, errors.New("transport error"))
	require.Zero(t, client.ZCard(ctx, quality5xxPendingKey).Val())
	limiter.observeQualityResponse(ctx, account, &http.Response{StatusCode: 503}, nil)
	limiter.observeQualityResponse(ctx, account, &http.Response{StatusCode: 200}, nil)
	require.Equal(t, []string{"21"}, client.ZRange(ctx, quality5xxPendingKey, 0, -1).Val(), "successful retry must not erase initial 503")
}

type quality5xxTempUnschedRepo struct {
	AccountRepository
	account *Account
	until   time.Time
	reason  string
}

func (r *quality5xxTempUnschedRepo) SetTempUnschedulable(_ context.Context, id int64, until time.Time, reason string) error {
	r.account.ID = id
	r.account.TempUnschedulableUntil = &until
	r.account.TempUnschedulableReason = reason
	r.until = until
	r.reason = reason
	return nil
}

type quality5xxRuntimeBlocker struct {
	account *Account
	until   time.Time
	reason  string
}

func (b *quality5xxRuntimeBlocker) BlockAccountScheduling(account *Account, until time.Time, reason string) {
	b.account = account
	b.until = until
	b.reason = reason
}
func (*quality5xxRuntimeBlocker) ClearAccountSchedulingBlock(int64) {}

func TestQuality5xxTemporarilyUnschedulesOAuthAccountBeforeProbe(t *testing.T) {
	account := &Account{ID: 21, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	repo := &quality5xxTempUnschedRepo{account: account}
	blocker := &quality5xxRuntimeBlocker{}
	limiter := &RateLimitService{accountRepo: repo, runtimeBlocker: blocker}

	limiter.temporarilyUnscheduleQuality5xx(context.Background(), account)

	require.True(t, repo.until.After(time.Now()))
	require.Equal(t, quality5xxTempUnschedReason, repo.reason)
	require.Same(t, account, blocker.account)
	require.Equal(t, quality5xxTempUnschedReason, blocker.reason)
	expired := time.Now().Add(-time.Minute)
	account.TempUnschedulableUntil = &expired
	require.False(t, limiter.temporarilyUnscheduleQuality5xx(context.Background(), account), "an expired quality cooldown must allow the recovery probe")
}

func TestQuality5xxTriggeredPlanDoesNotProbeDuringTempUnschedulableWindow(t *testing.T) {
	account := &Account{ID: 42, Type: AccountTypeOAuth, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true}
	until := time.Now().Add(time.Minute)
	account.TempUnschedulableUntil = &until
	plan := pelicanPlan()
	plan.AccountID = account.ID
	plan.TriggerSource = quality5xxSource
	plan.PelicanConfig.QuestionKind = OpenAICodexStateProbeQuestionKind
	plan.PelicanConfig.Quality = &QualityPolicy{TriggerOnUpstream5xx: true, Action: "disable_scheduling"}
	repo := &qualityClaimFailureRepo{plan: plan}
	accountRepo := &stateProbeAccountRepo{account: account}
	limiter := &RateLimitService{accountRepo: &quality5xxTempUnschedRepo{account: account}}
	runner := &ScheduledTestRunnerService{
		planRepo:       repo,
		accountTestSvc: &AccountTestService{accountRepo: accountRepo},
		rateLimitSvc:   limiter,
	}
	runner.runPelican = func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		t.Fatal("quality probe must wait for the official temp-unschedulable window")
		return nil, nil
	}

	require.Error(t, runner.runQualityTriggeredAccount(context.Background(), account.ID, time.Now()))
	require.NotNil(t, account.TempUnschedulableUntil)
	require.True(t, account.TempUnschedulableUntil.After(time.Now()))
}

type qualityClaimFailureRepo struct {
	ScheduledTestPlanRepository
	plan   *ScheduledTestPlan
	claims atomic.Int32
}

func (r *qualityClaimFailureRepo) ListByAccountID(context.Context, int64) ([]*ScheduledTestPlan, error) {
	p := *r.plan
	return []*ScheduledTestPlan{&p}, nil
}
func (r *qualityClaimFailureRepo) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	p := *r.plan
	return &p, nil
}
func (r *qualityClaimFailureRepo) ClaimPelican(context.Context, *ScheduledTestPlan, time.Time, time.Time, time.Time) (bool, error) {
	r.claims.Add(1)
	return false, errors.New("database unavailable")
}
func TestQuality5xxWorkerRetainsSignalAfterClaimFailure(t *testing.T) {
	r := miniredis.RunT(t)
	client := queuetest.NewClient(r.Addr())
	defer func() { _ = client.Close() }()
	plan := pelicanPlan()
	future := time.Now().Add(time.Hour)
	plan.NextRunAt = &future
	plan.PelicanConfig.QuestionKind = "candy"
	plan.PelicanConfig.Quality = &QualityPolicy{TriggerOnUpstream5xx: true, Action: "disable_scheduling", ExpectedAnswer: "21"}
	repo := &qualityClaimFailureRepo{plan: plan}
	runner := &ScheduledTestRunnerService{planRepo: repo, qualityTrigger: &quality5xxTrigger{queue: qualityqueue.NewRedis(client)}}
	t.Cleanup(func() { _ = runner.qualityTrigger.queue.Close() })
	runner.qualityTrigger.Observe(context.Background(), &Account{ID: 42, Type: AccountTypeOAuth}, 503)
	runner.startQualityTriggers()
	defer func() { runner.triggerCancel(); runner.triggerWG.Wait() }()
	require.Eventually(t, func() bool { return repo.claims.Load() > 0 }, 3*time.Second, 20*time.Millisecond)
	require.Equal(t, []string{"42"}, client.ZRange(context.Background(), quality5xxPendingKey, 0, -1).Val())
}

func TestQuality5xxQueueFencesNewerEpisode(t *testing.T) {
	r := miniredis.RunT(t)
	client := queuetest.NewClient(r.Addr())
	defer func() { _ = client.Close() }()
	trigger := &quality5xxTrigger{queue: qualityqueue.NewRedis(client)}
	t.Cleanup(func() { _ = trigger.queue.Close() })
	ctx := context.Background()
	observedAt := time.Now().UnixMilli()
	require.NoError(t, trigger.queue.Enqueue(ctx, 9, time.UnixMilli(observedAt)))
	first := client.ZScore(ctx, quality5xxPendingKey, "9").Val()
	// Reuse the exact clock value to prove same-millisecond events are distinct.
	require.NoError(t, trigger.queue.Enqueue(ctx, 9, time.UnixMilli(observedAt)))
	second := client.ZScore(ctx, quality5xxPendingKey, "9").Val()
	require.Greater(t, second, first)
	require.False(t, trigger.signalIsCurrent(9, time.UnixMilli(int64(first))))
	require.True(t, trigger.signalIsCurrent(9, time.UnixMilli(int64(second))))
	removed, err := client.Eval(ctx, `if tonumber(redis.call('ZSCORE',KEYS[1],ARGV[1])) == tonumber(ARGV[2]) then return redis.call('ZREM',KEYS[1],ARGV[1]) end return 0`, []string{quality5xxPendingKey}, "9", first).Int()
	require.NoError(t, err)
	require.Zero(t, removed, "an older completion cannot remove the newer signal")
	require.Equal(t, second, client.ZScore(ctx, quality5xxPendingKey, "9").Val())
}

func TestQuality5xxKeepsSignalWhileAnExistingLeaseCouldBeInterrupted(t *testing.T) {
	plan := pelicanPlan()
	until := time.Now().Add(time.Minute)
	plan.RunningUntil = &until
	plan.PelicanConfig.Quality = &QualityPolicy{TriggerOnUpstream5xx: true}
	runner := &ScheduledTestRunnerService{planRepo: &qualityClaimFailureRepo{plan: plan}}
	require.Error(t, runner.runQualityTriggeredAccount(context.Background(), plan.AccountID, time.Now()))
}
