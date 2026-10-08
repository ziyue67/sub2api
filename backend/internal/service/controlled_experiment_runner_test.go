package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type controlledTestAccounts struct{ AccountRepository }

func (controlledTestAccounts) GetByID(_ context.Context, id int64) (*Account, error) {
	return &Account{ID: id, Name: "fixture account", Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive, Schedulable: true}, nil
}

type controlledMemoryRepository struct {
	ControlledExperimentRepository
	mu        sync.Mutex
	run       *ControlledExperiment
	attempts  []*ControlledAttempt
	preflight []ControlledPreflight
	failSave  bool
}

func (r *controlledMemoryRepository) Create(_ context.Context, run *ControlledExperiment) (*ControlledExperiment, error) {
	run.ID = 1
	run.CreatedAt = time.Now()
	r.run = run
	return run, nil
}
func (r *controlledMemoryRepository) Get(_ context.Context, _ int64) (*ControlledExperiment, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, _ := json.Marshal(r.run)
	var copy ControlledExperiment
	_ = json.Unmarshal(raw, &copy)
	return &copy, nil
}
func (r *controlledMemoryRepository) Claim(_ context.Context, _ int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run.Status != "draft" {
		return false, nil
	}
	r.run.Status = "running"
	return true, nil
}
func (r *controlledMemoryRepository) SavePreflight(_ context.Context, _ int64, p []ControlledPreflight) error {
	r.preflight = p
	return nil
}
func (r *controlledMemoryRepository) Preflight(_ context.Context, _ int64) ([]ControlledPreflight, error) {
	return r.preflight, nil
}
func (r *controlledMemoryRepository) Reserve(_ context.Context, a *ControlledAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run.Status != "running" {
		return ErrExperimentStopped
	}
	if r.run.ReservedCalls >= r.run.MaxCalls {
		return ErrExperimentBudget
	}
	r.run.ReservedCalls++
	a.Sequence = r.run.ReservedCalls
	a.StartedAt = time.Now()
	a.Status = "reserved"
	copy := *a
	r.attempts = append(r.attempts, &copy)
	return nil
}
func (r *controlledMemoryRepository) Resolve(_ context.Context, a *ControlledAttempt) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failSave {
		return errors.New("save unavailable")
	}
	copy := *a
	r.attempts[a.Sequence-1] = &copy
	return nil
}
func (r *controlledMemoryRepository) Attempts(_ context.Context, _ int64) ([]*ControlledAttempt, error) {
	return r.attempts, nil
}
func (r *controlledMemoryRepository) Finish(_ context.Context, _ int64, status, reason string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run.Status = status
	r.run.StopReason = reason
	return nil
}
func (r *controlledMemoryRepository) RequestStop(_ context.Context, _ int64) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.run.Status = "stop_requested"
	return true, nil
}
func (r *controlledMemoryRepository) RecoverExpired(_ context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.run != nil && r.run.Status == "interrupted" {
		for _, a := range r.attempts {
			if a.Status == "reserved" {
				a.Status = "unknown"
			}
		}
	}
	return nil
}

type controlledFixtureExecutor struct {
	t       *testing.T
	task    ControlledTask
	count   int
	unknown bool
	onSend  func()
}

func (e *controlledFixtureExecutor) Preflight(_ context.Context, _ ControlledRoute, _ ControlledExperimentSpec) ControlledPreflight {
	return ControlledPreflight{Available: true, Catalog: "listed"}
}
func (e *controlledFixtureExecutor) Execute(_ context.Context, route ControlledRoute, spec ControlledExperimentSpec, body []byte, _ string) (*ControlledTurn, ControlledDiagnostic) {
	e.count++
	if e.onSend != nil {
		e.onSend()
	}
	d := ControlledDiagnostic{Code: "ok", ActualChannel: route.Channel, IdentityStable: true, ResponseModel: spec.Model, EffectiveEffort: spec.ReasoningEffort, EffortEvidence: "upstream_declared", Submissions: 1}
	if e.unknown {
		d.Code = "transport_unknown"
		return nil, d
	}
	prompt := gjson.GetBytes(body, "input.0.content.0.text").String()
	if strings.HasPrefix(prompt, "Reply with exactly this string and nothing else: ") {
		return &ControlledTurn{Text: strings.TrimPrefix(prompt, "Reply with exactly this string and nothing else: ")}, d
	}
	require.Equal(e.t, e.task.Prompt, prompt)
	if len(gjson.GetBytes(body, "input").Array()) == 1 {
		output := []json.RawMessage{json.RawMessage(`{"type":"reasoning","encrypted_content":"opaque-state-in-memory-only"}`)}
		for _, id := range e.task.Grader.RequiredRecordIDs {
			call, _ := json.Marshal(map[string]any{"type": "function_call", "namespace": "fixture", "name": "read_record", "call_id": id, "arguments": "{\"record_id\":\"" + id + "\"}"})
			output = append(output, call)
		}
		return &ControlledTurn{Output: output}, d
	}
	require.Contains(e.t, string(body), "opaque-state-in-memory-only", "opaque reasoning must survive the tool continuation")
	return &ControlledTurn{Text: string(e.task.Grader.Expected)}, d
}

func controlledRunnerFixture(t *testing.T, maxCalls, routes int) (*ControlledExperimentService, *controlledMemoryRepository, *controlledFixtureExecutor) {
	tasks, err := ControlledExperimentTasks()
	require.NoError(t, err)
	var task ControlledTask
	for _, candidate := range tasks {
		if candidate.ID == "screen-tools-01" {
			task = candidate
		}
	}
	r := &controlledMemoryRepository{}
	e := &controlledFixtureExecutor{t: t, task: task}
	s := NewControlledExperimentService(r, controlledTestAccounts{}, e)
	t.Cleanup(s.Stop)
	input := ControlledExperimentInput{Name: "fixture", Split: "screen", TaskIDs: []string{task.ID}, Model: "gpt-6.1-sol", ReasoningEffort: "high", Repetitions: 1, MaxCalls: maxCalls, TimeoutSeconds: 30}
	for i := 0; i < routes; i++ {
		input.Routes = append(input.Routes, ControlledRoute{AccountID: int64(i + 1), Channel: "native_http"})
	}
	_, err = s.Create(context.Background(), input)
	require.NoError(t, err)
	return s, r, e
}

func TestControlledExperimentRunnerCountsEligibilityAndToolContinuation(t *testing.T) {
	s, r, e := controlledRunnerFixture(t, 4, 2)
	require.NoError(t, s.Start(context.Background(), 1))
	s.wg.Wait()
	require.Equal(t, 4, e.count)
	require.Equal(t, 4, r.run.ReservedCalls)
	require.Equal(t, "budget_exhausted", r.run.Status)
	require.Len(t, r.attempts, 4)
	require.True(t, r.attempts[3].Grade.Passed)
	raw, err := json.Marshal(r.attempts)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "opaque-state-in-memory-only")
	report, err := s.Report(context.Background(), 1)
	require.NoError(t, err)
	require.Equal(t, 1, report.Comparisons[0].ExcludedTasks)
	require.Equal(t, 0, report.Comparisons[0].PairedTasks)
	require.ErrorIs(t, s.Start(context.Background(), 1), ErrExperimentBusy)
	require.Equal(t, 4, e.count)
}

func TestControlledExperimentRunnerDoesNotReplayUnknownOrSaveFailure(t *testing.T) {
	for _, failure := range []string{"unknown", "save_failure"} {
		t.Run(failure, func(t *testing.T) {
			s, r, e := controlledRunnerFixture(t, 10, 1)
			e.unknown = failure == "unknown"
			r.failSave = failure == "save_failure"
			require.NoError(t, s.Start(context.Background(), 1))
			s.wg.Wait()
			require.Equal(t, 1, e.count)
			require.Equal(t, 1, r.run.ReservedCalls)
			require.Len(t, r.attempts, 1)
			require.Equal(t, "unknown", r.attempts[0].Status)
			require.Nil(t, r.attempts[0].Grade, "protocol outcomes must not receive quality scores")
		})
	}
}

func TestControlledExperimentRunnerStopsAfterCurrentSubmission(t *testing.T) {
	s, r, e := controlledRunnerFixture(t, 20, 2)
	e.onSend = func() { _, _ = r.RequestStop(context.Background(), 1) }
	require.NoError(t, s.Start(context.Background(), 1))
	s.wg.Wait()
	require.Equal(t, 1, e.count)
	require.Equal(t, "cancelled", r.run.Status)
	require.Equal(t, "completed", r.attempts[0].Status)
	require.True(t, r.attempts[0].Grade.Passed)
}
