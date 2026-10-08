package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/google/uuid"
)

type ControlledExperimentService struct {
	repo     ControlledExperimentRepository
	accounts AccountRepository
	executor ControlledExperimentExecutor
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func NewControlledExperimentService(repo ControlledExperimentRepository, accounts AccountRepository, executor ControlledExperimentExecutor) *ControlledExperimentService {
	ctx, cancel := context.WithCancel(context.Background())
	return &ControlledExperimentService{repo: repo, accounts: accounts, executor: executor, ctx: ctx, cancel: cancel}
}

func (s *ControlledExperimentService) Stop() { s.cancel(); s.wg.Wait() }

func (s *ControlledExperimentService) Create(ctx context.Context, input ControlledExperimentInput) (*ControlledExperiment, error) {
	bad := func(message string) (*ControlledExperiment, error) {
		return nil, infraerrors.BadRequest("INVALID_EXPERIMENT", message)
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Model = strings.TrimSpace(input.Model)
	if len(input.Name) == 0 || len(input.Name) > 100 || len(input.Model) == 0 || len(input.Model) > 100 || strings.Contains(input.Model, ":") {
		return bad("name and unsuffixed model are required (maximum 100 bytes)")
	}
	if input.Split != "screen" && input.Split != "confirm" {
		return bad("split must be screen or confirm")
	}
	switch input.ReasoningEffort {
	case "none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra":
	default:
		return bad("select a supported reasoning effort")
	}
	if len(input.Routes) < 1 || len(input.Routes) > 4 || input.Repetitions < 1 || input.Repetitions > 3 || input.MaxCalls < 1 || input.MaxCalls > 300 || input.TimeoutSeconds < 30 || input.TimeoutSeconds > 720 {
		return bad("limits: 1-4 routes, 1-3 repetitions, 1-300 calls, 30-720 seconds per call")
	}
	tasks, err := ControlledExperimentTasks()
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range input.TaskIDs {
		if selected[id] {
			return bad("duplicate task ID")
		}
		selected[id] = true
	}
	spec := ControlledExperimentSpec{SuiteVersion: ControlledSuiteVersion, Model: input.Model, ReasoningEffort: input.ReasoningEffort, Repetitions: input.Repetitions, TimeoutSeconds: input.TimeoutSeconds}
	for _, task := range tasks {
		if task.Split == input.Split && (len(input.TaskIDs) == 0 || selected[task.ID]) {
			spec.Tasks = append(spec.Tasks, task)
			delete(selected, task.ID)
		}
	}
	if len(selected) > 0 || len(spec.Tasks) == 0 {
		return bad("unknown task ID or task outside the selected split")
	}
	seen := map[string]bool{}
	for _, route := range input.Routes {
		switch route.Channel {
		case "native_http", "native_ws", "prism", "bps":
		default:
			return bad("invalid route channel")
		}
		key := fmt.Sprintf("%d/%s", route.AccountID, route.Channel)
		if seen[key] {
			return bad("duplicate account/channel route")
		}
		seen[key] = true
		account, err := s.accounts.GetByID(ctx, route.AccountID)
		if err != nil {
			return nil, err
		}
		if account == nil || account.Platform != PlatformOpenAI {
			return bad("experiments require an OpenAI account")
		}
		spec.Routes = append(spec.Routes, ControlledRoute{AccountID: account.ID, AccountName: account.Name, Channel: route.Channel, ParentAccountID: account.ParentAccountID, ProxyID: account.ProxyID, MappedModel: account.GetMappedModel(input.Model)})
	}
	spec.PlannedMaxCalls = len(spec.Routes)
	for _, task := range spec.Tasks {
		spec.PlannedMaxCalls += task.MaxTurns * spec.Repetitions * len(spec.Routes)
	}
	return s.repo.Create(ctx, &ControlledExperiment{Name: input.Name, MaxCalls: input.MaxCalls, Spec: spec, Status: "draft"})
}

func (s *ControlledExperimentService) List(ctx context.Context, before int64) ([]*ControlledExperiment, error) {
	if err := s.repo.RecoverExpired(ctx); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, before, 30)
}

func (s *ControlledExperimentService) Report(ctx context.Context, id int64) (*ControlledExperimentReport, error) {
	if err := s.repo.RecoverExpired(ctx); err != nil {
		return nil, err
	}
	run, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	attempts, err := s.repo.Attempts(ctx, id)
	if err != nil {
		return nil, err
	}
	preflight, err := s.repo.Preflight(ctx, id)
	if err != nil {
		return nil, err
	}
	return buildControlledReport(run, attempts, preflight), nil
}

func (s *ControlledExperimentService) Start(ctx context.Context, id int64) error {
	if s.ctx.Err() != nil {
		return ErrExperimentStopped
	}
	if err := s.repo.RecoverExpired(ctx); err != nil {
		return err
	}
	run, err := s.repo.Get(ctx, id)
	if err != nil {
		return err
	}
	ok, err := s.repo.Claim(ctx, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrExperimentBusy
	}
	s.wg.Add(1)
	go func() { defer s.wg.Done(); s.execute(run) }()
	return nil
}

func (s *ControlledExperimentService) RequestStop(ctx context.Context, id int64) error {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return err
	}
	_, err := s.repo.RequestStop(ctx, id)
	return err
}

func (s *ControlledExperimentService) execute(run *ControlledExperiment) {
	status, reason := "completed", ""
	defer func() {
		if recover() != nil {
			status, reason = "interrupted", "runner_panic_no_replay"
			slog.Error("controlled experiment runner panicked", "run_id", run.ID)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := s.repo.Finish(ctx, run.ID, status, reason); err != nil {
			slog.Error("controlled experiment final status save failed", "run_id", run.ID, "error", err)
			return
		}
		// Converts any unresolved reservation after a save failure into unknown.
		if err := s.repo.RecoverExpired(ctx); err != nil {
			slog.Error("controlled experiment reservation recovery failed", "run_id", run.ID, "error", err)
		}
	}()
	stop := func(err error) {
		status, reason = "interrupted", "storage_or_execution_failure_no_replay"
		if errors.Is(err, ErrExperimentBudget) {
			status, reason = "budget_exhausted", "submission_limit"
		}
		if errors.Is(err, ErrExperimentStopped) {
			status, reason = "cancelled", "administrator_stop"
		}
	}
	eligible := make([]bool, len(run.Spec.Routes))
	preflight := make([]ControlledPreflight, len(eligible))
	for i, route := range run.Spec.Routes {
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
		preflight[i] = s.executor.Preflight(ctx, route, run.Spec)
		preflight[i].RouteIndex = i
		cancel()
		run.Spec.Routes[i].identityReference = preflight[i].identityReference
	}
	if err := s.repo.SavePreflight(s.ctx, run.ID, preflight); err != nil {
		stop(err)
		return
	}
	for i := range eligible {
		if !preflight[i].Available {
			continue
		}
		nonce := uuid.NewString()
		task := ControlledTask{ID: "eligibility", Prompt: "Reply with exactly this string and nothing else: " + nonce, MaxTurns: 1}
		attempt, _, err := s.submit(run, i, "eligibility", task, 0, 1, nil, uuid.NewString())
		if err != nil {
			stop(err)
			return
		}
		eligible[i] = attempt.Status == "completed" && strings.TrimSpace(attempt.Answer) == nonce
		if attempt.Status == "completed" {
			attempt.Grade = &ControlledGrade{Passed: eligible[i], Reason: "eligibility_answer_mismatch"}
			if eligible[i] {
				attempt.Grade.Score = 1
				attempt.Grade.Reason = "generation_verified"
			}
		}
		if err := s.saveAttempt(attempt); err != nil {
			stop(err)
			return
		}
	}
	// Alternate route order for each frozen task/repetition. No account failover,
	// repeated first attempts, or skipped tasks replaced by easier tasks.
	for repeat := 1; repeat <= run.Spec.Repetitions; repeat++ {
		for index, task := range run.Spec.Tasks {
			for offset := range eligible {
				i := (index + repeat - 1 + offset) % len(eligible)
				if !eligible[i] {
					continue
				}
				if err := s.executeTask(run, i, repeat, task); err != nil {
					stop(err)
					return
				}
			}
		}
	}
	if latest, err := s.repo.Get(s.ctx, run.ID); err != nil {
		stop(err)
	} else if latest.Status == "stop_requested" {
		stop(ErrExperimentStopped)
	}
}

func (s *ControlledExperimentService) submit(run *ControlledExperiment, index int, phase string, task ControlledTask, repeat, turn int, input []json.RawMessage, session string) (*ControlledAttempt, *ControlledTurn, error) {
	if s.ctx.Err() != nil {
		return nil, nil, s.ctx.Err()
	}
	body, err := controlledPayload(run.Spec, task, input)
	if err != nil {
		return nil, nil, err
	}
	attempt := &ControlledAttempt{RunID: run.ID, RouteIndex: index, Phase: phase, TaskID: task.ID, Repetition: repeat, Turn: turn}
	// Reservation commits before ANY network generation; never refund/retry an
	// uncertain submission. A cancelled browser cannot cancel this background run.
	reserveCtx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	err = s.repo.Reserve(reserveCtx, attempt)
	cancel()
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(s.ctx, time.Duration(run.Spec.TimeoutSeconds)*time.Second)
	response, diagnostic := s.executor.Execute(ctx, run.Spec.Routes[index], run.Spec, body, session)
	cancel()
	attempt.Diagnostic = diagnostic
	now := time.Now()
	attempt.FinishedAt = &now
	attempt.Status = "completed"
	if diagnostic.Code != "ok" {
		attempt.Status = "protocol_failed"
		if diagnostic.Code == "transport_unknown" || diagnostic.Code == "timeout_unknown" {
			attempt.Status = "unknown"
		}
		if diagnostic.Submissions == 0 {
			attempt.Status = "not_sent"
		}
		if diagnostic.UpstreamStatus == 401 || diagnostic.UpstreamStatus == 403 || diagnostic.UpstreamStatus == 429 {
			attempt.Status = "rejected"
		}
	}
	if response != nil {
		attempt.Answer = response.Text
	}
	return attempt, response, nil
}

func (s *ControlledExperimentService) saveAttempt(a *ControlledAttempt) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.repo.Resolve(ctx, a)
}

func (s *ControlledExperimentService) executeTask(run *ControlledExperiment, index, repeat int, task ControlledTask) error {
	var input []json.RawMessage
	trace := make([]ControlledToolTrace, 0)
	callsSeen := map[string]bool{}
	session := uuid.NewString()
	for turn := 1; turn <= task.MaxTurns; turn++ {
		attempt, response, err := s.submit(run, index, "task", task, repeat, turn, input, session)
		if err != nil {
			return err
		}
		if attempt.Status != "completed" || response == nil {
			attempt.ToolTrace = trace
			return s.saveAttempt(attempt)
		}
		calls := controlledOutputCalls(response.Output)
		if len(calls) == 0 || len(task.Tools) == 0 || turn == task.MaxTurns {
			attempt.ToolTrace = trace
			grade := GradeControlledTask(s.ctx, task, attempt.Answer, trace)
			if len(calls) > 0 {
				grade.Passed = false
				grade.Score = 0
				grade.Reason = "tool_turn_limit_or_unexpected_call"
			}
			attempt.Grade = &grade
			return s.saveAttempt(attempt)
		}
		if input == nil {
			var initial struct {
				Input []json.RawMessage `json:"input"`
			}
			body, _ := controlledPayload(run.Spec, task, nil)
			_ = json.Unmarshal(body, &initial)
			input = initial.Input
		}
		// Preserve ALL output items, including opaque reasoning, until continuation
		// finishes. The persistent record contains text and local trace only.
		input = append(input, response.Output...)
		for _, call := range calls {
			result, entry, toolErr := executeControlledTool(task, call)
			var item struct {
				CallID string `json:"call_id"`
			}
			_ = json.Unmarshal(call, &item)
			if toolErr != nil || callsSeen[item.CallID] {
				attempt.Status = "protocol_failed"
				attempt.Diagnostic.Code = "invalid_tool_call"
				attempt.ToolTrace = trace
				return s.saveAttempt(attempt)
			}
			callsSeen[item.CallID] = true
			trace = append(trace, entry)
			input = append(input, result)
		}
		attempt.ToolTrace = append([]ControlledToolTrace(nil), trace...)
		if err := s.saveAttempt(attempt); err != nil {
			return err
		}
	}
	return nil
}
