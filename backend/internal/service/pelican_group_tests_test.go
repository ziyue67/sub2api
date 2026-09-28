package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type groupTestClaim struct {
	planID int64
	until  time.Time
	next   *time.Time
}

type groupTestRepoFake struct {
	mu            sync.Mutex
	plans         map[int64]*PelicanGroupTestPlan
	due           []*PelicanGroupTestPlan
	claimOK       bool
	claims        []groupTestClaim
	finished      []time.Time
	results       []*PelicanGroupTestResult
	pruned        []int64
	expiredBefore time.Time
	listed        []*PelicanGroupTestResult
	listedOffset  int
	listedLimit   int
	nextID        int64
}

func newGroupTestRepoFake(plans ...*PelicanGroupTestPlan) *groupTestRepoFake {
	repo := &groupTestRepoFake{plans: map[int64]*PelicanGroupTestPlan{}, claimOK: true, nextID: 1000}
	for _, plan := range plans {
		repo.plans[plan.ID] = plan
	}
	return repo
}

func (r *groupTestRepoFake) ListPlans(context.Context) ([]*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]*PelicanGroupTestPlan, 0, len(r.plans))
	for _, plan := range r.plans {
		out = append(out, plan)
	}
	return out, nil
}
func (r *groupTestRepoFake) GetPlan(_ context.Context, id int64) (*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.plans[id], nil
}
func (r *groupTestRepoFake) CreatePlan(_ context.Context, plan *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := *plan
	r.nextID++
	saved.ID = r.nextID
	r.plans[saved.ID] = &saved
	return &saved, nil
}
func (r *groupTestRepoFake) UpdatePlan(_ context.Context, plan *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.plans[plan.ID] == nil {
		return nil, nil
	}
	saved := *plan
	r.plans[plan.ID] = &saved
	return &saved, nil
}
func (r *groupTestRepoFake) DeletePlan(_ context.Context, id int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.plans[id]
	delete(r.plans, id)
	return ok, nil
}
func (r *groupTestRepoFake) ListDue(context.Context, time.Time) ([]*PelicanGroupTestPlan, error) {
	return r.due, nil
}
func (r *groupTestRepoFake) Claim(_ context.Context, plan *PelicanGroupTestPlan, _, until time.Time, next *time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claims = append(r.claims, groupTestClaim{plan.ID, until, next})
	return r.claimOK, nil
}
func (r *groupTestRepoFake) Finish(_ context.Context, _ int64, until, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.finished = append(r.finished, until)
	return nil
}
func (r *groupTestRepoFake) CreateResult(_ context.Context, result *PelicanGroupTestResult) (*PelicanGroupTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	saved := *result
	r.nextID++
	saved.ID = r.nextID
	r.results = append(r.results, &saved)
	return &saved, nil
}
func (r *groupTestRepoFake) PruneResults(_ context.Context, planID int64, _ int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruned = append(r.pruned, planID)
	return nil
}
func (r *groupTestRepoFake) PruneExpiredResults(_ context.Context, before time.Time) error {
	r.expiredBefore = before
	return nil
}
func (r *groupTestRepoFake) ListResults(_ context.Context, _ int64, offset, limit int) ([]*PelicanGroupTestResult, int64, error) {
	r.listedOffset, r.listedLimit = offset, limit
	start := min(offset, len(r.listed))
	end := min(start+limit, len(r.listed))
	return r.listed[start:end], int64(len(r.listed)), nil
}
func (r *groupTestRepoFake) GetResult(_ context.Context, id int64) (*PelicanGroupTestResult, error) {
	for _, result := range r.results {
		if result.ID == id {
			return result, nil
		}
	}
	return nil, nil
}

type groupTestGroupsFake map[int64]*Group

func (g groupTestGroupsFake) GetByID(_ context.Context, id int64) (*Group, error) {
	if group, ok := g[id]; ok {
		return group, nil
	}
	return nil, ErrGroupNotFound
}

type routeStep struct {
	account *Account
	err     error
}

type groupTestRouterFake struct {
	mu       sync.Mutex
	steps    []routeStep
	excluded [][]int64
	released []int64
}

func (r *groupTestRouterFake) route(_ context.Context, _ *Group, model string, excluded map[int64]struct{}) (*pelicanGroupRoute, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := []int64{}
	for id := range excluded {
		seen = append(seen, id)
	}
	r.excluded = append(r.excluded, seen)
	if len(r.steps) == 0 {
		return nil, ErrNoAvailableAccounts
	}
	step := r.steps[0]
	r.steps = r.steps[1:]
	if step.err != nil {
		return nil, step.err
	}
	id := step.account.ID
	return &pelicanGroupRoute{account: step.account, model: model, release: func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.released = append(r.released, id)
	}}, nil
}

var groupTestNow = time.Date(2026, 9, 28, 4, 0, 0, 0, time.UTC)

func groupTestPlan(parallel int) *PelicanGroupTestPlan {
	return &PelicanGroupTestPlan{ID: 7, GroupID: 4, ModelID: "gpt-6-astra", CronExpression: "*/30 * * * *", Enabled: true,
		PelicanConfig: &PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw", ReasoningEffort: "high", ParallelCount: parallel, ModelID: "gpt-6-astra"}}
}

func newGroupTestService(repo *groupTestRepoFake, router pelicanGroupRouter,
	run func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error)) (*PelicanGroupTestService, *showcaseRepoStub) {
	showcaseRepo := &showcaseRepoStub{}
	return &PelicanGroupTestService{
		repo:       repo,
		groups:     groupTestGroupsFake{4: {ID: 4, Name: "GPT PRO", Platform: PlatformOpenAI, Status: StatusActive}},
		router:     router,
		runAccount: run,
		showcase:   &PelicanShowcaseService{repo: showcaseRepo, settings: enabledShowcase()},
		now:        func() time.Time { return groupTestNow },
	}, showcaseRepo
}

func account(id int64, name string) *Account { return &Account{ID: id, Name: name} }

// answers scripts the account test: each account answers with the given result.
func answers(byAccount map[int64]ScheduledTestResult) func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
	return func(_ context.Context, accountID int64, _ string, _ *PelicanTestConfig) (*ScheduledTestResult, error) {
		result := byAccount[accountID]
		return &result, nil
	}
}

func TestPelicanGroupTestPlanInputValidation(t *testing.T) {
	ctx := context.Background()
	repo := newGroupTestRepoFake()
	svc, _ := newGroupTestService(repo, &groupTestRouterFake{}, nil)
	valid := PelicanGroupTestPlanInput{GroupID: 4, ModelID: " gpt-6-astra ", Prompt: " draw a pelican ", ReasoningEffort: "HIGH", Enabled: true}

	for name, input := range map[string]PelicanGroupTestPlanInput{
		"no group":          {ModelID: "m", Prompt: "p", ReasoningEffort: "high"},
		"unknown group":     {GroupID: 99, ModelID: "m", Prompt: "p", ReasoningEffort: "high"},
		"no model":          {GroupID: 4, Prompt: "p", ReasoningEffort: "high"},
		"long model":        {GroupID: 4, ModelID: strings.Repeat("m", 101), Prompt: "p", ReasoningEffort: "high"},
		"no prompt":         {GroupID: 4, ModelID: "m", Prompt: "  ", ReasoningEffort: "high"},
		"long prompt":       {GroupID: 4, ModelID: "m", Prompt: strings.Repeat("p", 32001), ReasoningEffort: "high"},
		"unknown effort":    {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "extreme"},
		"too many parallel": {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", ParallelCount: 9},
		"negative parallel": {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", ParallelCount: -1},
		"bad schedule":      {GroupID: 4, ModelID: "m", Prompt: "p", ReasoningEffort: "high", CronExpression: "every minute"},
	} {
		_, err := svc.CreatePlan(ctx, input)
		require.Error(t, err, name)
	}
	require.Empty(t, repo.plans)

	plan, err := svc.CreatePlan(ctx, valid)
	require.NoError(t, err)
	require.Equal(t, "gpt-6-astra", plan.ModelID)
	require.Equal(t, "*/30 * * * *", plan.CronExpression, "the default schedule is every 30 minutes")
	require.True(t, plan.Enabled)
	require.Equal(t, &PelicanTestConfig{QuestionKind: "pelican", Prompt: "draw a pelican", ReasoningEffort: "high", ParallelCount: 1, ModelID: "gpt-6-astra"}, plan.PelicanConfig)
	require.Equal(t, groupTestNow.Add(30*time.Minute), *plan.NextRunAt)

	// An edit keeps the plan's group: moving it would mix two groups' history.
	updated, err := svc.UpdatePlan(ctx, plan.ID, PelicanGroupTestPlanInput{GroupID: 99, ModelID: "claude-opus-5-5", Prompt: "p", ReasoningEffort: "low", CronExpression: "0 * * * *", ParallelCount: 3})
	require.NoError(t, err)
	require.EqualValues(t, 4, updated.GroupID)
	require.Equal(t, "claude-opus-5-5", updated.ModelID)
	require.False(t, updated.Enabled)
	require.Equal(t, 3, updated.PelicanConfig.ParallelCount)
	require.Equal(t, groupTestNow.Add(time.Hour), *updated.NextRunAt)

	_, err = svc.UpdatePlan(ctx, 404, valid)
	require.ErrorIs(t, err, ErrPelicanGroupTestPlanNotFound)
	require.NoError(t, svc.DeletePlan(ctx, plan.ID))
	require.ErrorIs(t, svc.DeletePlan(ctx, plan.ID), ErrPelicanGroupTestPlanNotFound)
}

func TestPelicanGroupTestSampleFailsOverLikeAUserRequest(t *testing.T) {
	a, b, c := account(11, "pool-a"), account(12, "pool-b"), account(13, "pool-c")
	upstream429 := ScheduledTestResult{Status: "failed", ErrorMessage: "API returned 429: rate limited"}
	html := ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>", LatencyMs: 4200}
	cases := []struct {
		name         string
		steps        []routeStep
		answers      map[int64]ScheduledTestResult
		status       string
		accountID    int64
		errorMessage string
		response     string
		attempts     []PelicanGroupTestAttempt
		excluded     [][]int64
	}{
		{
			name: "moves on when the account fails before any output", steps: []routeStep{{account: a}, {account: b}},
			answers: map[int64]ScheduledTestResult{11: upstream429, 12: html},
			status:  "success", accountID: 12, response: "<svg></svg>",
			attempts: []PelicanGroupTestAttempt{{AccountID: 11, AccountName: "pool-a", Error: upstream429.ErrorMessage}},
			excluded: [][]int64{{}, {11}},
		},
		{
			name: "keeps an answer that is not HTML", steps: []routeStep{{account: a}, {account: b}},
			answers: map[int64]ScheduledTestResult{11: {Status: "failed", ResponseText: "Sure! Here is", ErrorMessage: "Model did not return HTML or SVG"}},
			status:  "failed", accountID: 11, response: "Sure! Here is", errorMessage: "Model did not return HTML or SVG",
			attempts: []PelicanGroupTestAttempt{}, excluded: [][]int64{{}},
		},
		{
			name: "keeps an empty answer", steps: []routeStep{{account: a}, {account: b}},
			answers: map[int64]ScheduledTestResult{11: {Status: "failed", ErrorMessage: pelicanErrEmptyOutput}},
			status:  "failed", accountID: 11, errorMessage: pelicanErrEmptyOutput,
			attempts: []PelicanGroupTestAttempt{}, excluded: [][]int64{{}},
		},
		{
			name: "keeps an oversized answer", steps: []routeStep{{account: a}, {account: b}},
			answers: map[int64]ScheduledTestResult{11: {Status: "failed", ErrorMessage: pelicanErrCaptureLimit}},
			status:  "failed", accountID: 11, errorMessage: pelicanErrCaptureLimit,
			attempts: []PelicanGroupTestAttempt{}, excluded: [][]int64{{}},
		},
		{
			name: "gives up after three accounts", steps: []routeStep{{account: a}, {account: b}, {account: c}, {account: account(14, "pool-d")}},
			answers: map[int64]ScheduledTestResult{11: upstream429, 12: upstream429, 13: {Status: "failed", ErrorMessage: "API returned 502"}},
			status:  "failed", accountID: 13, errorMessage: "API returned 502",
			attempts: []PelicanGroupTestAttempt{
				{AccountID: 11, AccountName: "pool-a", Error: upstream429.ErrorMessage},
				{AccountID: 12, AccountName: "pool-b", Error: upstream429.ErrorMessage},
			},
			excluded: [][]int64{{}, {11}, {11, 12}},
		},
		{
			name: "reports that the scheduler found no account", steps: nil,
			status: "failed", errorMessage: "no_available_account: no available accounts",
			attempts: []PelicanGroupTestAttempt{}, excluded: [][]int64{{}},
		},
		{
			name: "reports the last account when nothing is left to fail over to", steps: []routeStep{{account: a}},
			answers: map[int64]ScheduledTestResult{11: upstream429},
			status:  "failed", accountID: 11, errorMessage: upstream429.ErrorMessage,
			attempts: []PelicanGroupTestAttempt{}, excluded: [][]int64{{}, {11}},
		},
		{
			name: "passes a scheduler error through", steps: []routeStep{{err: errPelicanGroupAccountBusy}},
			status: "failed", errorMessage: "no_available_account: " + errPelicanGroupAccountBusy.Error(),
			attempts: []PelicanGroupTestAttempt{}, excluded: [][]int64{{}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := &groupTestRouterFake{steps: tc.steps}
			svc, _ := newGroupTestService(newGroupTestRepoFake(), router, answers(tc.answers))
			result := svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4, Status: StatusActive})
			require.Equal(t, tc.status, result.Status)
			require.Equal(t, tc.accountID, result.AccountID)
			require.Equal(t, tc.errorMessage, result.ErrorMessage)
			require.Equal(t, tc.response, result.ResponseText)
			require.Equal(t, tc.attempts, result.Attempts)
			require.Len(t, router.excluded, len(tc.excluded))
			for i, ids := range tc.excluded {
				require.ElementsMatch(t, ids, router.excluded[i], "excluded accounts on route %d", i)
			}
			require.Len(t, router.released, len(tc.excluded)-countRouteErrors(tc.steps, len(tc.excluded)), "every routed slot is released")
			require.Equal(t, "gpt-6-astra", result.PelicanConfig.ModelID, "results carry the model users ask for")
			require.EqualValues(t, 4, result.GroupID)
		})
	}
}

// countRouteErrors counts the routes among the first n that did not hand out an account.
func countRouteErrors(steps []routeStep, n int) int {
	errs := 0
	for i := 0; i < n; i++ {
		if i >= len(steps) || steps[i].err != nil {
			errs++
		}
	}
	return errs
}

func TestPelicanGroupTestSampleSurvivesAccountTestFailures(t *testing.T) {
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}}}
	svc, _ := newGroupTestService(newGroupTestRepoFake(), router, func(context.Context, int64, string, *PelicanTestConfig) (*ScheduledTestResult, error) {
		panic("adapter bug")
	})
	result := svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4})
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "pelican_group_test_panic: sample failed", result.ErrorMessage)
	require.Equal(t, []int64{11}, router.released, "panic must release the account concurrency slot")

	router = &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
	calls := 0
	svc, _ = newGroupTestService(newGroupTestRepoFake(), router, func(_ context.Context, id int64, _ string, _ *PelicanTestConfig) (*ScheduledTestResult, error) {
		calls++
		if id == 11 {
			return nil, errors.New("dial tcp: timeout")
		}
		return &ScheduledTestResult{Status: "success", ResponseText: "<svg></svg>"}, nil
	})
	result = svc.runSample(context.Background(), groupTestPlan(1), &Group{ID: 4})
	require.Equal(t, "success", result.Status)
	require.Equal(t, 2, calls, "an error from the account test counts as a failure before output")
	require.Equal(t, []PelicanGroupTestAttempt{{AccountID: 11, AccountName: "a", Error: "dial tcp: timeout"}}, result.Attempts)
}

func TestPelicanGroupTestExecuteSavesPublishesAndFinishes(t *testing.T) {
	repo := newGroupTestRepoFake()
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(12, "b")}}}
	svc, showcase := newGroupTestService(repo, router, answers(map[int64]ScheduledTestResult{
		11: {Status: "success", ResponseText: "<svg data-a></svg>", LatencyMs: 900},
		12: {Status: "failed", ResponseText: "not html", ErrorMessage: "Model did not return HTML or SVG"},
	}))
	until := groupTestNow.Add(15 * time.Minute)
	svc.execute(groupTestPlan(2), until)

	require.Len(t, repo.results, 2, "one result per parallel sample")
	require.Len(t, showcase.published, 1, "only the HTML answer reaches the gallery")
	published := showcase.published[0]
	require.EqualValues(t, 4, published.snapshot.GroupID)
	require.Equal(t, "<svg data-a></svg>", published.snapshot.ResponseText)
	require.Equal(t, "gpt-6-astra", published.snapshot.ModelID)
	require.Equal(t, "high", published.snapshot.ReasoningEffort)
	require.EqualValues(t, 900, published.snapshot.LatencyMs)
	require.Equal(t, 12, published.maxItems)
	for _, result := range repo.results {
		if result.Status == "success" {
			require.Equal(t, result.ID, published.snapshot.SourceResultID)
		}
	}
	require.Equal(t, []int64{7}, repo.pruned)
	require.Equal(t, []time.Time{until}, repo.finished)
}

func TestPelicanGroupTestUnavailableGroupRecordsOneFailure(t *testing.T) {
	repo := newGroupTestRepoFake()
	router := &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}}}
	svc, showcase := newGroupTestService(repo, router, answers(nil))
	svc.groups = groupTestGroupsFake{4: {ID: 4, Status: StatusDisabled}}
	svc.execute(groupTestPlan(3), groupTestNow)
	require.Len(t, repo.results, 1)
	require.Equal(t, "group_unavailable: group is disabled", repo.results[0].ErrorMessage)
	require.Empty(t, router.excluded, "the scheduler is not asked for a disabled group")
	require.Empty(t, showcase.published)

	svc.groups = groupTestGroupsFake{}
	svc.execute(groupTestPlan(1), groupTestNow)
	require.Contains(t, repo.results[1].ErrorMessage, "group_unavailable")
}

func TestPelicanGroupTestRunNowAndRunDue(t *testing.T) {
	ctx := context.Background()
	plan := groupTestPlan(1)
	repo := newGroupTestRepoFake(plan)
	run := answers(map[int64]ScheduledTestResult{11: {Status: "success", ResponseText: "<svg></svg>"}})
	svc, _ := newGroupTestService(repo, &groupTestRouterFake{steps: []routeStep{{account: account(11, "a")}, {account: account(11, "a")}}}, run)

	require.ErrorIs(t, svc.RunNow(ctx, 404), ErrPelicanGroupTestPlanNotFound)
	noModel := groupTestPlan(1)
	noModel.ID, noModel.ModelID = 8, ""
	repo.plans[noModel.ID] = noModel
	require.ErrorIs(t, svc.RunNow(ctx, noModel.ID), ErrPelicanGroupTestModelRequired, "a carried-over plan needs a model first")
	repo.due = []*PelicanGroupTestPlan{noModel}
	svc.RunDue(ctx, groupTestNow)
	require.Empty(t, repo.claims, "neither path claims a plan without a model")
	repo.claimOK = false
	require.ErrorIs(t, svc.RunNow(ctx, plan.ID), ErrPelicanGroupTestPlanRunning)
	repo.claimOK = true
	require.NoError(t, svc.RunNow(ctx, plan.ID))
	svc.runs.Wait()
	require.Nil(t, repo.claims[1].next, "a manual run keeps the schedule")
	require.Len(t, repo.results, 1)

	repo.due = []*PelicanGroupTestPlan{plan}
	svc.RunDue(ctx, groupTestNow)
	require.Len(t, repo.claims, 3)
	require.Equal(t, groupTestNow.Add(30*time.Minute), *repo.claims[2].next, "a scheduled claim moves to the next slot")
	require.Equal(t, groupTestNow.Add(pelicanGroupTestLease), repo.claims[2].until)
	require.Len(t, repo.results, 2)

	repo.claimOK = false
	svc.RunDue(ctx, groupTestNow)
	require.Len(t, repo.results, 2, "a plan claimed elsewhere is skipped")

	var nilService *PelicanGroupTestService
	require.NotPanics(t, func() {
		nilService.RunDue(ctx, groupTestNow)
		nilService.Cleanup(ctx, groupTestNow)
	})
}

func TestPelicanGroupTestCleanupAndHistoryPaging(t *testing.T) {
	ctx := context.Background()
	repo := newGroupTestRepoFake()
	svc, showcase := newGroupTestService(repo, &groupTestRouterFake{}, nil)
	svc.Cleanup(ctx, groupTestNow)
	require.Equal(t, groupTestNow.Add(-7*24*time.Hour), repo.expiredBefore)
	require.Len(t, showcase.pruned, 1, "the gallery limits are applied on the same tick")

	for id := int64(10); id > 0; id-- {
		repo.listed = append(repo.listed, &PelicanGroupTestResult{ID: id})
	}
	items, total, err := svc.ListResults(ctx, 0, 1, 3)
	require.NoError(t, err)
	require.Len(t, items, 3)
	require.EqualValues(t, 10, total)
	require.EqualValues(t, 10, items[0].ID)
	items, total, err = svc.ListResults(ctx, 0, 2, 3)
	require.NoError(t, err)
	require.EqualValues(t, 7, items[0].ID)
	require.EqualValues(t, 10, total)
	items, total, err = svc.ListResults(ctx, 0, 4, 3)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.EqualValues(t, 10, total, "the last page still reports the full total")
	require.EqualValues(t, 1, items[0].ID)
	items, total, err = svc.ListResults(ctx, 0, 5, 3)
	require.NoError(t, err)
	require.Empty(t, items)
	require.EqualValues(t, 10, total, "an empty page still reports the total")
	_, _, err = svc.ListResults(ctx, 0, 0, 0)
	require.NoError(t, err)
	require.Equal(t, 0, repo.listedOffset)
	require.Equal(t, 20, repo.listedLimit)
	_, _, err = svc.ListResults(ctx, 0, 2, 2000)
	require.NoError(t, err)
	require.Equal(t, 1000, repo.listedOffset)
	require.Equal(t, 1000, repo.listedLimit)

	_, err = svc.GetResult(ctx, 12345)
	require.ErrorIs(t, err, ErrPelicanGroupTestResultNotFound)
}

func TestPelicanGroupRouterRoutesThroughTheOpenAIScheduler(t *testing.T) {
	ctx := context.Background()
	accounts := []Account{
		{ID: 11, Name: "pool-a", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 0},
		{ID: 12, Name: "pool-b", Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1, Priority: 5},
	}
	newRouter := func(cache schedulerTestConcurrencyCache) *gatewayPelicanGroupRouter {
		cfg := &config.Config{}
		cfg.Gateway.Scheduling.LoadBatchEnabled = false
		concurrency := NewConcurrencyService(cache)
		openai := &OpenAIGatewayService{
			accountRepo:        schedulerTestOpenAIAccountRepo{accounts: accounts},
			cache:              &schedulerTestGatewayCache{},
			cfg:                cfg,
			concurrencyService: concurrency,
		}
		return &gatewayPelicanGroupRouter{openai: openai, concurrency: concurrency, slotWait: 50 * time.Millisecond}
	}
	group := &Group{ID: 4, Platform: PlatformOpenAI, Status: StatusActive}

	acquired, released := []int64{}, []int64{}
	router := newRouter(schedulerTestConcurrencyCache{acquiredIDs: &acquired, releasedIDs: &released})
	first, err := router.route(ctx, group, "gpt-6-astra", map[int64]struct{}{})
	require.NoError(t, err)
	require.Equal(t, int64(11), first.account.ID, "the scheduler's priority decides, not the test")
	require.Equal(t, "gpt-6-astra", first.model)
	require.Equal(t, []int64{11}, acquired, "the sample holds a concurrency slot like a user request")
	first.release()
	first.release()
	require.Equal(t, []int64{11}, released, "the slot is released exactly once")

	second, err := router.route(ctx, group, "gpt-6-astra", map[int64]struct{}{11: {}})
	require.NoError(t, err)
	require.Equal(t, int64(12), second.account.ID, "a failed account is excluded like in failover")
	second.release()

	_, err = router.route(ctx, group, "gpt-6-astra", map[int64]struct{}{11: {}, 12: {}})
	require.ErrorIs(t, err, ErrNoAvailableAccounts)

	busy := newRouter(schedulerTestConcurrencyCache{acquireResults: map[int64]bool{11: false, 12: false}})
	_, err = busy.route(ctx, group, "gpt-6-astra", map[int64]struct{}{})
	require.ErrorIs(t, err, errPelicanGroupAccountBusy, "a sample waits for a slot like a queued request, then gives up")
}

func TestPelicanGroupRouterPlatformsMirrorGatewayRoutes(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo} {
		require.True(t, isOpenAIResponsesGatewayPlatform(platform), platform)
	}
	for _, platform := range []string{PlatformAnthropic, PlatformGemini, PlatformAntigravity, PlatformComposite, ""} {
		require.False(t, isOpenAIResponsesGatewayPlatform(platform), platform)
	}
}
