package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

// Pelican group tests ask a group the Pelican question on a schedule. For every sample
// the gateway scheduler picks the account exactly as it would for a user request to the
// group, so the showcase reflects the group rather than one hand-picked account.

const (
	// PelicanGroupTestMaxAttempts bounds the accounts one sample may try: like a user
	// request, a sample moves on only when the account fails before producing output.
	PelicanGroupTestMaxAttempts = 3
	pelicanGroupTestKeepResults = 100
	pelicanGroupTestHistoryAge  = 7 * 24 * time.Hour
	pelicanGroupTestLease       = 15 * time.Minute
	pelicanGroupTestRunTimeout  = 10 * time.Minute
	pelicanGroupTestSlotWait    = time.Minute
	pelicanGroupTestMaxRunning  = 4
	pelicanGroupTestDefaultCron = "*/30 * * * *"
)

var (
	ErrPelicanGroupTestPlanNotFound   = infraerrors.NotFound("PELICAN_GROUP_TEST_PLAN_NOT_FOUND", "pelican group test plan not found")
	ErrPelicanGroupTestResultNotFound = infraerrors.NotFound("PELICAN_GROUP_TEST_RESULT_NOT_FOUND", "pelican group test result not found")
	ErrPelicanGroupTestPlanRunning    = infraerrors.Conflict("PELICAN_GROUP_TEST_PLAN_RUNNING", "this group test is already running")
	// Plans carried over from the old showcase group list start without a model.
	ErrPelicanGroupTestModelRequired = infraerrors.BadRequest("PELICAN_GROUP_TEST_MODEL_REQUIRED", "set a model for this group test first")
	errPelicanGroupAccountBusy       = errors.New("every routed account stayed at its concurrency limit")
)

type PelicanGroupTestPlan struct {
	ID             int64              `json:"id"`
	GroupID        int64              `json:"group_id"`
	GroupName      string             `json:"group_name"`
	GroupPlatform  string             `json:"group_platform"`
	GroupStatus    string             `json:"group_status"`
	ModelID        string             `json:"model_id"`
	CronExpression string             `json:"cron_expression"`
	Enabled        bool               `json:"enabled"`
	PelicanConfig  *PelicanTestConfig `json:"pelican_config"`
	LastRunAt      *time.Time         `json:"last_run_at"`
	NextRunAt      *time.Time         `json:"next_run_at"`
	RunningUntil   *time.Time         `json:"running_until,omitempty"`
	// LastResult is the newest result without its HTML, for the admin overview.
	LastResult *PelicanGroupTestResult `json:"last_result,omitempty"`
	CreatedAt  time.Time               `json:"created_at"`
	UpdatedAt  time.Time               `json:"updated_at"`
}

// PelicanGroupTestAttempt is an account a sample left because it failed before any output.
type PelicanGroupTestAttempt struct {
	AccountID   int64  `json:"account_id"`
	AccountName string `json:"account_name"`
	Error       string `json:"error"`
}

// PelicanGroupTestResult is one sample. AccountID is the account that gave the final
// answer (0 when the scheduler found none); only admins see it.
type PelicanGroupTestResult struct {
	ID            int64                     `json:"id"`
	PlanID        int64                     `json:"plan_id"`
	GroupID       int64                     `json:"group_id"`
	GroupName     string                    `json:"group_name,omitempty"`
	AccountID     int64                     `json:"account_id"`
	AccountName   string                    `json:"account_name"`
	Attempts      []PelicanGroupTestAttempt `json:"attempts"`
	Status        string                    `json:"status"`
	ResponseText  string                    `json:"response_text,omitempty"`
	ErrorMessage  string                    `json:"error_message"`
	LatencyMs     int64                     `json:"latency_ms"`
	PelicanConfig *PelicanTestConfig        `json:"pelican_config,omitempty"`
	StartedAt     time.Time                 `json:"started_at"`
	FinishedAt    time.Time                 `json:"finished_at"`
	CreatedAt     time.Time                 `json:"created_at"`
}

// PelicanGroupTestPlanInput is what an admin edits; the question is always the HTML drawing kind.
type PelicanGroupTestPlanInput struct {
	GroupID         int64  `json:"group_id"`
	ModelID         string `json:"model_id"`
	CronExpression  string `json:"cron_expression"`
	Enabled         bool   `json:"enabled"`
	Prompt          string `json:"prompt"`
	ReasoningEffort string `json:"reasoning_effort"`
	ParallelCount   int    `json:"parallel_count"`
}

type PelicanGroupTestRepository interface {
	ListPlans(ctx context.Context) ([]*PelicanGroupTestPlan, error)
	// GetPlan returns nil when the plan does not exist.
	GetPlan(ctx context.Context, id int64) (*PelicanGroupTestPlan, error)
	CreatePlan(ctx context.Context, plan *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error)
	// UpdatePlan returns nil when the plan does not exist.
	UpdatePlan(ctx context.Context, plan *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error)
	DeletePlan(ctx context.Context, id int64) (bool, error)
	// ListDue returns enabled plans whose next run has come, of groups users can still reach.
	ListDue(ctx context.Context, now time.Time) ([]*PelicanGroupTestPlan, error)
	// Claim takes the run lease. A scheduled claim (next != nil) also needs the plan due and
	// unchanged since it was listed, and moves next_run_at; a manual claim only needs no run
	// in progress.
	Claim(ctx context.Context, plan *PelicanGroupTestPlan, now, until time.Time, next *time.Time) (bool, error)
	Finish(ctx context.Context, id int64, until, finished time.Time) error
	CreateResult(ctx context.Context, result *PelicanGroupTestResult) (*PelicanGroupTestResult, error)
	PruneResults(ctx context.Context, planID int64, keep int) error
	PruneExpiredResults(ctx context.Context, before time.Time) error
	// ListResults returns results newest first without HTML; planID 0 lists every plan.
	ListResults(ctx context.Context, planID int64, offset, limit int) ([]*PelicanGroupTestResult, int64, error)
	// GetResult returns nil when the result does not exist.
	GetResult(ctx context.Context, id int64) (*PelicanGroupTestResult, error)
}

// pelicanGroupRoute is the scheduler's pick for one sample. release frees the
// concurrency slot held while the sample runs.
type pelicanGroupRoute struct {
	account *Account
	model   string
	release func()
}

// pelicanGroupRouter picks the account a user request to the group would reach right now.
type pelicanGroupRouter interface {
	route(ctx context.Context, group *Group, model string, excluded map[int64]struct{}) (*pelicanGroupRoute, error)
}

type pelicanGroupTestGroups interface {
	GetByID(ctx context.Context, id int64) (*Group, error)
}

type PelicanGroupTestService struct {
	repo       PelicanGroupTestRepository
	groups     pelicanGroupTestGroups
	router     pelicanGroupRouter
	runAccount func(ctx context.Context, accountID int64, model string, cfg *PelicanTestConfig) (*ScheduledTestResult, error)
	showcase   *PelicanShowcaseService
	now        func() time.Time
	// runs tracks background runs so tests can wait for them.
	runs sync.WaitGroup
}

func NewPelicanGroupTestService(
	repo PelicanGroupTestRepository,
	groupRepo GroupRepository,
	gateway *GatewayService,
	openai *OpenAIGatewayService,
	concurrency *ConcurrencyService,
	accountTest *AccountTestService,
	showcase *PelicanShowcaseService,
) *PelicanGroupTestService {
	return &PelicanGroupTestService{
		repo:       repo,
		groups:     groupRepo,
		router:     &gatewayPelicanGroupRouter{gateway: gateway, openai: openai, concurrency: concurrency, slotWait: pelicanGroupTestSlotWait},
		runAccount: accountTest.RunPelicanBackground,
		showcase:   showcase,
		now:        time.Now,
	}
}

func (s *PelicanGroupTestService) ListPlans(ctx context.Context) ([]*PelicanGroupTestPlan, error) {
	return s.repo.ListPlans(ctx)
}

func (s *PelicanGroupTestService) CreatePlan(ctx context.Context, input PelicanGroupTestPlanInput) (*PelicanGroupTestPlan, error) {
	plan, err := s.planFromInput(ctx, input, nil)
	if err != nil {
		return nil, err
	}
	return s.repo.CreatePlan(ctx, plan)
}

func (s *PelicanGroupTestService) UpdatePlan(ctx context.Context, id int64, input PelicanGroupTestPlanInput) (*PelicanGroupTestPlan, error) {
	current, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return nil, err
	}
	if current == nil {
		return nil, ErrPelicanGroupTestPlanNotFound
	}
	plan, err := s.planFromInput(ctx, input, current)
	if err != nil {
		return nil, err
	}
	updated, err := s.repo.UpdatePlan(ctx, plan)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		return nil, ErrPelicanGroupTestPlanNotFound
	}
	return updated, nil
}

func (s *PelicanGroupTestService) DeletePlan(ctx context.Context, id int64) error {
	deleted, err := s.repo.DeletePlan(ctx, id)
	if err != nil {
		return err
	}
	if !deleted {
		return ErrPelicanGroupTestPlanNotFound
	}
	return nil
}

// planFromInput validates an edit. A plan keeps its group: moving it would mix two
// groups' history, so changing the group means a new plan.
func (s *PelicanGroupTestService) planFromInput(ctx context.Context, input PelicanGroupTestPlanInput, current *PelicanGroupTestPlan) (*PelicanGroupTestPlan, error) {
	plan := &PelicanGroupTestPlan{GroupID: input.GroupID}
	if current != nil {
		plan.ID = current.ID
		plan.GroupID = current.GroupID
	}
	if plan.GroupID <= 0 {
		return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", "group is required")
	}
	if current == nil {
		group, err := s.groups.GetByID(ctx, plan.GroupID)
		if err != nil && !errors.Is(err, ErrGroupNotFound) {
			return nil, err
		}
		if err != nil || group == nil {
			return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", fmt.Sprintf("group %d does not exist", plan.GroupID))
		}
	}
	plan.ModelID = strings.TrimSpace(input.ModelID)
	if plan.ModelID == "" || len(plan.ModelID) > 100 {
		return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", "model is required (maximum 100 bytes)")
	}
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" || len(prompt) > 32000 {
		return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", "prompt is required (maximum 32000 bytes)")
	}
	effort := normalizePelicanReasoningEffort(input.ReasoningEffort)
	if effort == "" {
		return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", "invalid reasoning effort")
	}
	parallel := input.ParallelCount
	if parallel == 0 {
		parallel = 1
	}
	if parallel < 1 || parallel > 8 {
		return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", "parallel count must be 1–8")
	}
	plan.CronExpression = strings.TrimSpace(input.CronExpression)
	if plan.CronExpression == "" {
		plan.CronExpression = pelicanGroupTestDefaultCron
	}
	next, err := computeNextRun(plan.CronExpression, s.now())
	if err != nil {
		return nil, infraerrors.BadRequest("INVALID_PELICAN_GROUP_TEST", fmt.Sprintf("invalid schedule: %v", err))
	}
	plan.NextRunAt = &next
	plan.Enabled = input.Enabled
	plan.PelicanConfig = &PelicanTestConfig{QuestionKind: "pelican", Prompt: prompt, ReasoningEffort: effort, ParallelCount: parallel, ModelID: plan.ModelID}
	return plan, nil
}

// RunNow starts a run right away, outside the schedule. It fails when a run is in progress.
func (s *PelicanGroupTestService) RunNow(ctx context.Context, id int64) error {
	plan, err := s.repo.GetPlan(ctx, id)
	if err != nil {
		return err
	}
	if plan == nil {
		return ErrPelicanGroupTestPlanNotFound
	}
	if strings.TrimSpace(plan.ModelID) == "" {
		return ErrPelicanGroupTestModelRequired
	}
	now := s.now()
	until := now.Add(pelicanGroupTestLease).Truncate(time.Microsecond)
	claimed, err := s.repo.Claim(ctx, plan, now, until, nil)
	if err != nil {
		return err
	}
	if !claimed {
		return ErrPelicanGroupTestPlanRunning
	}
	s.runs.Add(1)
	go func() {
		defer s.runs.Done()
		s.execute(plan, until)
	}()
	return nil
}

// RunDue runs every due plan; the scheduled-test runner calls it once a minute.
func (s *PelicanGroupTestService) RunDue(ctx context.Context, now time.Time) {
	if s == nil {
		return
	}
	plans, err := s.repo.ListDue(ctx, now)
	if err != nil {
		logger.LegacyPrintf("service.pelican_group_test", "list due plans failed: %v", err)
		return
	}
	sem := make(chan struct{}, pelicanGroupTestMaxRunning)
	var wg sync.WaitGroup
	for _, plan := range plans {
		sem <- struct{}{}
		wg.Add(1)
		go func(plan *PelicanGroupTestPlan) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runScheduled(ctx, plan, now)
		}(plan)
	}
	wg.Wait()
}

func (s *PelicanGroupTestService) runScheduled(ctx context.Context, plan *PelicanGroupTestPlan, now time.Time) {
	defer func() {
		if recover() != nil {
			logger.LegacyPrintf("service.pelican_group_test", "plan=%d group=%d panicked", plan.ID, plan.GroupID)
		}
	}()
	if strings.TrimSpace(plan.ModelID) == "" {
		logger.LegacyPrintf("service.pelican_group_test", "plan=%d skipped: no model", plan.ID)
		return
	}
	next, err := computeNextRun(plan.CronExpression, now)
	if err != nil {
		logger.LegacyPrintf("service.pelican_group_test", "plan=%d invalid schedule: %v", plan.ID, err)
		return
	}
	// The persisted lease keeps ticks and server replicas from running a plan twice and
	// expires by itself after a crash.
	until := now.Add(pelicanGroupTestLease).Truncate(time.Microsecond)
	claimed, err := s.repo.Claim(ctx, plan, now, until, &next)
	if err != nil {
		logger.LegacyPrintf("service.pelican_group_test", "plan=%d claim failed: %v", plan.ID, err)
	}
	if err != nil || !claimed {
		return
	}
	s.execute(plan, until)
}

// execute runs the plan's samples and stores them. It owns its contexts: a run started
// from an admin request must outlive that request.
func (s *PelicanGroupTestService) execute(plan *PelicanGroupTestPlan, until time.Time) {
	runCtx, cancel := context.WithTimeout(context.Background(), pelicanGroupTestRunTimeout)
	defer cancel()
	results := s.runSamples(runCtx, plan)
	// Persist with a fresh context so timeout failures are still recorded.
	saveCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	defer stop()
	for _, result := range results {
		saved, err := s.repo.CreateResult(saveCtx, result)
		if err != nil {
			logger.LegacyPrintf("service.pelican_group_test", "plan=%d save failed: %v", plan.ID, err)
			continue
		}
		s.showcase.PublishGroupResult(saveCtx, plan.GroupID, saved)
	}
	if err := s.repo.PruneResults(saveCtx, plan.ID, pelicanGroupTestKeepResults); err != nil {
		logger.LegacyPrintf("service.pelican_group_test", "plan=%d prune failed: %v", plan.ID, err)
	}
	if err := s.repo.Finish(saveCtx, plan.ID, until, s.now()); err != nil {
		logger.LegacyPrintf("service.pelican_group_test", "plan=%d finish failed: %v", plan.ID, err)
	}
}

func (s *PelicanGroupTestService) runSamples(ctx context.Context, plan *PelicanGroupTestPlan) []*PelicanGroupTestResult {
	group, err := s.groups.GetByID(ctx, plan.GroupID)
	if err == nil && group != nil && group.Status != StatusActive {
		err = fmt.Errorf("group is %s", group.Status)
	}
	if err != nil || group == nil {
		if err == nil {
			err = ErrGroupNotFound
		}
		now := s.now()
		return []*PelicanGroupTestResult{s.newResult(plan, now, now, fmt.Sprintf("group_unavailable: %v", err))}
	}
	count := 1
	if plan.PelicanConfig != nil && plan.PelicanConfig.ParallelCount > 1 {
		count = plan.PelicanConfig.ParallelCount
	}
	results := make([]*PelicanGroupTestResult, count)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results[index] = s.runSample(ctx, plan, group)
		}(i)
	}
	wg.Wait()
	return results
}

// runSample routes one sample like a user request. It moves to another account only when
// the account failed before producing any output, which is when a user request fails over
// too; an answer that is not HTML is the group's real answer and is kept. Each sample runs
// in its own goroutine outside Gin's recovery, so a panic becomes a failed result.
func (s *PelicanGroupTestService) runSample(ctx context.Context, plan *PelicanGroupTestPlan, group *Group) (result *PelicanGroupTestResult) {
	started := s.now()
	defer func() {
		if recover() != nil {
			result = s.newResult(plan, started, s.now(), "pelican_group_test_panic: sample failed")
			logger.LegacyPrintf("service.pelican_group_test", "plan=%d group=%d sample panicked", plan.ID, plan.GroupID)
		}
	}()
	excluded := make(map[int64]struct{})
	attempts := []PelicanGroupTestAttempt{}
	for {
		route, err := s.router.route(ctx, group, plan.ModelID, excluded)
		if err != nil {
			if len(attempts) == 0 {
				return s.newResult(plan, started, s.now(), fmt.Sprintf("no_available_account: %v", err))
			}
			// Nothing left to fail over to: the last account's error is what a user would get.
			last := attempts[len(attempts)-1]
			result = s.newResult(plan, started, s.now(), last.Error)
			result.AccountID, result.AccountName, result.Attempts = last.AccountID, last.AccountName, attempts[:len(attempts)-1]
			return result
		}
		var sample *ScheduledTestResult
		func() {
			defer route.release()
			sample, err = s.runAccount(ctx, route.account.ID, route.model, plan.PelicanConfig)
		}()
		if err != nil || sample == nil {
			sample = &ScheduledTestResult{Status: "failed", ErrorMessage: fmt.Sprint(err)}
			if err == nil {
				sample.ErrorMessage = "pelican_group_test_empty_result: account test returned no result"
			}
		}
		if sample.Status == "success" || !pelicanFailedBeforeOutput(sample) || len(attempts)+1 >= PelicanGroupTestMaxAttempts {
			result = s.newResult(plan, started, s.now(), sample.ErrorMessage)
			result.Status = sample.Status
			result.ResponseText = sample.ResponseText
			result.LatencyMs = sample.LatencyMs
			result.AccountID, result.AccountName, result.Attempts = route.account.ID, route.account.Name, attempts
			return result
		}
		attempts = append(attempts, PelicanGroupTestAttempt{AccountID: route.account.ID, AccountName: route.account.Name, Error: sample.ErrorMessage})
		excluded[route.account.ID] = struct{}{}
	}
}

// newResult is a failed result carrying the plan's config snapshot, labelled with the
// public model the plan asks for (composite groups may send an upstream model instead).
func (s *PelicanGroupTestService) newResult(plan *PelicanGroupTestPlan, started, finished time.Time, message string) *PelicanGroupTestResult {
	result := &PelicanGroupTestResult{
		PlanID:       plan.ID,
		GroupID:      plan.GroupID,
		Attempts:     []PelicanGroupTestAttempt{},
		Status:       "failed",
		ErrorMessage: message,
		LatencyMs:    finished.Sub(started).Milliseconds(),
		StartedAt:    started,
		FinishedAt:   finished,
	}
	if plan.PelicanConfig != nil {
		snapshot := *plan.PelicanConfig
		snapshot.ModelID = plan.ModelID
		result.PelicanConfig = &snapshot
	}
	return result
}

// pelicanFailedBeforeOutput reports an account-side failure with no model output, such as
// an upstream HTTP error or a missing token. Output problems (empty, oversized) are the
// model's answer and do not trigger a retry on another account.
func pelicanFailedBeforeOutput(result *ScheduledTestResult) bool {
	if result.Status == "success" || result.ResponseText != "" {
		return false
	}
	switch result.ErrorMessage {
	case pelicanErrEmptyOutput, pelicanErrCaptureLimit, pelicanErrHistoryLimit:
		return false
	}
	return true
}

func (s *PelicanGroupTestService) ListResults(ctx context.Context, planID int64, page, pageSize int) ([]*PelicanGroupTestResult, int64, error) {
	params := pagination.PaginationParams{Page: page, PageSize: pageSize}
	return s.repo.ListResults(ctx, planID, params.Offset(), params.Limit())
}

func (s *PelicanGroupTestService) GetResult(ctx context.Context, id int64) (*PelicanGroupTestResult, error) {
	result, err := s.repo.GetResult(ctx, id)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, ErrPelicanGroupTestResultNotFound
	}
	return result, nil
}

// Cleanup trims the admin history by age and applies the showcase limits; the
// scheduled-test runner calls it once a minute.
func (s *PelicanGroupTestService) Cleanup(ctx context.Context, now time.Time) {
	if s == nil {
		return
	}
	if err := s.repo.PruneExpiredResults(ctx, now.Add(-pelicanGroupTestHistoryAge)); err != nil {
		logger.LegacyPrintf("service.pelican_group_test", "history cleanup failed: %v", err)
	}
	s.showcase.Cleanup(ctx, now)
}

// gatewayPelicanGroupRouter selects through the same scheduler entry points as the
// /v1/responses and /v1/messages handlers, holding the account's concurrency slot while
// the sample runs.
type gatewayPelicanGroupRouter struct {
	gateway     *GatewayService
	openai      *OpenAIGatewayService
	concurrency *ConcurrencyService
	slotWait    time.Duration
}

func (r *gatewayPelicanGroupRouter) route(ctx context.Context, group *Group, model string, excluded map[int64]struct{}) (*pelicanGroupRoute, error) {
	platform, upstreamModel := group.Platform, model
	if platform == PlatformComposite {
		decision, matched, err := r.gateway.resolveCompositeRouteDecision(ctx, group, model, CompositeRouteEndpointAny)
		if err != nil {
			return nil, err
		}
		if !matched {
			return nil, fmt.Errorf("%w supporting model: %s (composite target platform unknown)", ErrNoAvailableAccounts, model)
		}
		ctx = WithCompositeRouteDecision(ctx, decision)
		platform = decision.TargetPlatform
		if rewritten := strings.TrimSpace(decision.UpstreamModel); rewritten != "" {
			upstreamModel = rewritten
		}
	}
	var selection *AccountSelectionResult
	var err error
	if isOpenAIResponsesGatewayPlatform(platform) {
		selection, _, err = r.openai.SelectAccountWithSchedulerForCapability(ctx, &group.ID, "", "", upstreamModel, excluded,
			OpenAIUpstreamTransportAny, OpenAIEndpointCapabilityChatCompletions, false, false, true, NormalizeOpenAICompatiblePlatform(platform))
	} else {
		selection, err = r.gateway.SelectAccountWithLoadAwareness(ctx, &group.ID, "", upstreamModel, excluded, "", 0)
	}
	if err != nil {
		return nil, err
	}
	if selection == nil || selection.Account == nil {
		return nil, ErrNoAvailableAccounts
	}
	release, err := r.holdSlot(ctx, selection)
	if err != nil {
		return nil, err
	}
	return &pelicanGroupRoute{account: selection.Account, model: upstreamModel, release: release}, nil
}

// holdSlot returns the slot the scheduler acquired, or waits for one like a queued user
// request would, up to slotWait.
func (r *gatewayPelicanGroupRouter) holdSlot(ctx context.Context, selection *AccountSelectionResult) (func(), error) {
	if selection.Acquired || selection.WaitPlan == nil || r.concurrency == nil {
		return releaseOnce(selection.ReleaseFunc), nil
	}
	plan := selection.WaitPlan
	wait := r.slotWait
	if plan.Timeout > 0 && plan.Timeout < wait {
		wait = plan.Timeout
	}
	deadline := time.Now().Add(wait)
	for {
		acquired, err := r.concurrency.AcquireAccountSlot(ctx, plan.AccountID, plan.MaxConcurrency)
		if err == nil && acquired != nil && acquired.Acquired {
			return releaseOnce(acquired.ReleaseFunc), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, errPelicanGroupAccountBusy
		}
		if remaining > 500*time.Millisecond {
			remaining = 500 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(remaining):
		}
	}
}

func releaseOnce(release func()) func() {
	if release == nil {
		return func() {}
	}
	var once sync.Once
	return func() { once.Do(release) }
}

// isOpenAIResponsesGatewayPlatform mirrors the /v1/responses routing in server/routes/gateway.go:
// these platforms are served by the OpenAI-compatible handlers and scheduler.
func isOpenAIResponsesGatewayPlatform(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek, PlatformMiniMax, PlatformOpenCodeGo:
		return true
	default:
		return false
	}
}
