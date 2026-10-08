package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const ControlledSuiteVersion = "fenjue-v1-20261007"

var (
	ErrExperimentNotFound = infraerrors.NotFound("EXPERIMENT_NOT_FOUND", "experiment not found")
	ErrExperimentBusy     = infraerrors.Conflict("EXPERIMENT_BUSY", "another experiment is running, or this run has already started")
	ErrExperimentBudget   = errors.New("experiment submission budget exhausted")
	ErrExperimentStopped  = errors.New("experiment stopped or lease expired")
)

type ControlledTask struct {
	ID       string            `json:"id"`
	Family   string            `json:"family"`
	Split    string            `json:"split"`
	Category string            `json:"category"`
	Prompt   string            `json:"prompt"`
	Tools    []json.RawMessage `json:"tools"`
	MaxTurns int               `json:"max_turns"`
	Grader   ControlledGrader  `json:"grader"`
}

type ControlledGrader struct {
	Kind              string                     `json:"kind"`
	Expected          json.RawMessage            `json:"expected,omitempty"`
	Records           map[string]json.RawMessage `json:"records,omitempty"`
	RequiredRecordIDs []string                   `json:"required_record_ids,omitempty"`
	Cases             []ControlledSQLCase        `json:"cases,omitempty"`
}

type ControlledSQLCase struct {
	Tables       map[string][][]any `json:"tables"`
	ExpectedRows json.RawMessage    `json:"expected_rows"`
}

type ControlledRoute struct {
	identityReference []string
	AccountID         int64  `json:"account_id"`
	Channel           string `json:"channel"`
	AccountName       string `json:"account_name"`
	ParentAccountID   *int64 `json:"parent_account_id,omitempty"`
	ProxyID           *int64 `json:"proxy_id,omitempty"`
	MappedModel       string `json:"mapped_model"`
}

type ControlledExperimentInput struct {
	Name            string            `json:"name"`
	Split           string            `json:"split"`
	TaskIDs         []string          `json:"task_ids"`
	Routes          []ControlledRoute `json:"routes"`
	Model           string            `json:"model"`
	ReasoningEffort string            `json:"reasoning_effort"`
	Repetitions     int               `json:"repetitions"`
	MaxCalls        int               `json:"max_calls"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
}

// Only server-owned tasks enter this snapshot. It cannot be updated after creation.
type ControlledExperimentSpec struct {
	SuiteVersion    string            `json:"suite_version"`
	Model           string            `json:"model"`
	ReasoningEffort string            `json:"reasoning_effort"`
	Repetitions     int               `json:"repetitions"`
	TimeoutSeconds  int               `json:"timeout_seconds"`
	PlannedMaxCalls int               `json:"planned_max_calls"`
	Routes          []ControlledRoute `json:"routes"`
	Tasks           []ControlledTask  `json:"tasks"`
}

type ControlledExperiment struct {
	ID            int64                    `json:"id"`
	Name          string                   `json:"name"`
	Status        string                   `json:"status"`
	MaxCalls      int                      `json:"max_calls"`
	ReservedCalls int                      `json:"reserved_calls"`
	Spec          ControlledExperimentSpec `json:"spec"`
	CreatedAt     time.Time                `json:"created_at"`
	StartedAt     *time.Time               `json:"started_at,omitempty"`
	FinishedAt    *time.Time               `json:"finished_at,omitempty"`
	LeaseUntil    *time.Time               `json:"lease_until,omitempty"`
	StopReason    string                   `json:"stop_reason"`
}

type ControlledToolTrace struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	RecordID  string `json:"record_id"`
	OK        bool   `json:"ok"`
}

type ControlledGrade struct {
	Passed         bool     `json:"passed"`
	Score          float64  `json:"score"`
	Reason         string   `json:"reason"`
	PassedCases    int      `json:"passed_cases,omitempty"`
	TotalCases     int      `json:"total_cases,omitempty"`
	MissingRecords []string `json:"missing_records,omitempty"`
	RejectedCalls  int      `json:"rejected_calls,omitempty"`
}

// Route evidence is selected-account evidence, not a claim to independently
// verify the identity of a provider behind a third-party API endpoint.
type ControlledDiagnostic struct {
	Code             string      `json:"code"`
	HTTPStatus       int         `json:"http_status"`
	UpstreamStatus   int         `json:"upstream_status,omitempty"`
	ForwardingStatus int         `json:"forwarding_status,omitempty"`
	ActualChannel    string      `json:"actual_channel"`
	UpstreamEndpoint string      `json:"upstream_endpoint"`
	UpstreamModel    string      `json:"upstream_model"`
	ResponseModel    string      `json:"response_model"`
	EffectiveEffort  string      `json:"effective_effort"`
	EffortEvidence   string      `json:"effort_evidence"`
	Terminal         string      `json:"terminal"`
	IdentityStable   bool        `json:"identity_stable"`
	RequestID        string      `json:"request_id,omitempty"`
	ResponseID       string      `json:"response_id,omitempty"`
	OutputTypes      []string    `json:"output_types"`
	DurationMs       int64       `json:"duration_ms"`
	Submissions      int         `json:"submissions"`
	UsageSource      string      `json:"usage_source"`
	Usage            OpenAIUsage `json:"usage"`
	CostUSD          *float64    `json:"cost_usd,omitempty"`
	CostIncomplete   bool        `json:"cost_incomplete"`
}

type ControlledAttempt struct {
	RunID      int64                 `json:"run_id"`
	Sequence   int                   `json:"sequence"`
	RouteIndex int                   `json:"route_index"`
	Phase      string                `json:"phase"`
	TaskID     string                `json:"task_id"`
	Repetition int                   `json:"repetition"`
	Turn       int                   `json:"turn"`
	Status     string                `json:"status"`
	StartedAt  time.Time             `json:"started_at"`
	FinishedAt *time.Time            `json:"finished_at,omitempty"`
	Answer     string                `json:"answer"`
	Diagnostic ControlledDiagnostic  `json:"diagnostic"`
	Grade      *ControlledGrade      `json:"grade,omitempty"`
	ToolTrace  []ControlledToolTrace `json:"tool_trace,omitempty"`
}

type ControlledPreflight struct {
	identityReference []string
	RouteIndex        int    `json:"route_index"`
	Available         bool   `json:"available"`
	Reason            string `json:"reason"`
	Catalog           string `json:"catalog"`
}

type ControlledRouteSummary struct {
	RouteIndex       int      `json:"route_index"`
	Eligibility      string   `json:"eligibility"`
	Calls            int      `json:"calls"`
	CompletedCalls   int      `json:"completed_calls"`
	ProtocolFailures int      `json:"protocol_failures"`
	UnknownCalls     int      `json:"unknown_calls"`
	GradedTasks      int      `json:"graded_tasks"`
	PassedTasks      int      `json:"passed_tasks"`
	MeanScore        *float64 `json:"mean_score"`
	CostUSD          float64  `json:"cost_usd"`
	CostIncomplete   bool     `json:"cost_incomplete"`
}

type ControlledComparison struct {
	Left           int      `json:"left"`
	Right          int      `json:"right"`
	PairedTasks    int      `json:"paired_tasks"`
	ExcludedTasks  int      `json:"excluded_tasks"`
	LeftMean       *float64 `json:"left_mean"`
	RightMean      *float64 `json:"right_mean"`
	MeanDifference *float64 `json:"mean_difference"`
}

type ControlledExperimentReport struct {
	Run         *ControlledExperiment    `json:"run"`
	Attempts    []*ControlledAttempt     `json:"attempts"`
	Preflight   []ControlledPreflight    `json:"preflight"`
	Routes      []ControlledRouteSummary `json:"routes"`
	Comparisons []ControlledComparison   `json:"comparisons"`
}

type ControlledExperimentRepository interface {
	Create(context.Context, *ControlledExperiment) (*ControlledExperiment, error)
	Get(context.Context, int64) (*ControlledExperiment, error)
	List(context.Context, int64, int) ([]*ControlledExperiment, error)
	Claim(context.Context, int64) (bool, error)
	SavePreflight(context.Context, int64, []ControlledPreflight) error
	Preflight(context.Context, int64) ([]ControlledPreflight, error)
	Reserve(context.Context, *ControlledAttempt) error
	Resolve(context.Context, *ControlledAttempt) error
	Attempts(context.Context, int64) ([]*ControlledAttempt, error)
	Finish(context.Context, int64, string, string) error
	RequestStop(context.Context, int64) (bool, error)
	RecoverExpired(context.Context) error
}

type ControlledExperimentExecutor interface {
	Preflight(context.Context, ControlledRoute, ControlledExperimentSpec) ControlledPreflight
	Execute(context.Context, ControlledRoute, ControlledExperimentSpec, []byte, string) (*ControlledTurn, ControlledDiagnostic)
}

type ControlledTurn struct {
	usagePresent bool
	Output       []json.RawMessage `json:"output"`
	Text         string            `json:"text"`
}
