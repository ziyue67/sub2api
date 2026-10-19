package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/modeltrace"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/gin-gonic/gin"
)

type modelTraceContextKey struct{}

type modelTraceEnvironment struct {
	Protocol     string `json:"protocol"`
	SystemPrompt string `json:"system_prompt"`
	OAuth        bool   `json:"oauth"`
}

type ModelTraceSample struct {
	Challenge      modeltrace.Challenge  `json:"challenge"`
	Text           string                `json:"text"`
	MappedModel    string                `json:"mapped_model"`
	Environment    modelTraceEnvironment `json:"environment"`
	ParsedNumbers  int                   `json:"parsed_numbers"`
	MinimumNumbers int                   `json:"minimum_numbers"`
	Accepted       bool                  `json:"accepted"`
	Error          string                `json:"error,omitempty"`
	Analysis       *modeltrace.Analysis  `json:"analysis,omitempty"`
	StartedAt      time.Time             `json:"started_at"`
	LatencyMs      int64                 `json:"latency_ms"`
}

type ModelTraceResult struct {
	RequestedModel  string               `json:"requested_model"`
	ReasoningEffort string               `json:"reasoning_effort"`
	Bank            modeltrace.Metadata  `json:"bank"`
	Samples         []ModelTraceSample   `json:"samples"`
	Analysis        *modeltrace.Analysis `json:"analysis,omitempty"`
	Status          string               `json:"status"`
	Disagreement    bool                 `json:"disagreement"`
	CreatedAt       time.Time            `json:"created_at"`
}

func modelTraceEnvironmentForAccount(account *Account, model string) (modelTraceEnvironment, error) {
	unsupported := func() (modelTraceEnvironment, error) {
		return modelTraceEnvironment{}, infraerrors.BadRequest("MODELTRACE_UNSUPPORTED", "ModelTrace supports native OpenAI and Anthropic text accounts only; Excel/BPS, Prism, Agent Identity, shadow and service accounts are not supported")
	}
	if account == nil || account.IsSyntheticUITest() || account.IsCredentialShadow() || account.IsBedrock() || (account.Type != AccountTypeAPIKey && !account.IsOAuth()) {
		return unsupported()
	}
	environment := modelTraceEnvironment{OAuth: account.IsOAuth(), SystemPrompt: "none"}
	switch account.Platform {
	case PlatformOpenAI:
		if account.IsExcelOAuth() || account.IsExcelBPSEnabled() || account.IsPrismBrowserEnabledForModel(model) || account.IsOpenAIAgentIdentity() || isOpenAIImageModel(account.GetMappedModel(model)) {
			return unsupported()
		}
		environment.Protocol = "responses"
		if account.IsOAuth() {
			environment.SystemPrompt = "required_responses_instructions"
		}
		if account.Type == AccountTypeAPIKey && !openai_compat.ShouldUseResponsesAPI(account.Extra) {
			environment.Protocol = "chat_completions"
			environment.SystemPrompt = "none"
		}
	case PlatformAnthropic:
		environment.Protocol = "messages"
		if account.IsOAuth() {
			environment.SystemPrompt = "required_claude_code"
		}
	default:
		return unsupported()
	}
	return environment, nil
}

func modelTraceRequested(ctx context.Context) bool {
	_, ok := ctx.Value(modelTraceContextKey{}).(modelTraceEnvironment)
	return ok
}

// Only the new diagnostic opts into these payload changes. OAuth-required
// instructions remain in place and are disclosed with every stored sample.
func applyModelTracePayload(ctx context.Context, payload map[string]any) {
	environment, ok := ctx.Value(modelTraceContextKey{}).(modelTraceEnvironment)
	if !ok {
		return
	}
	delete(payload, "tools")
	delete(payload, "tool_choice")
	delete(payload, "temperature")
	if environment.Protocol == "messages" {
		payload["max_tokens"] = 4096
		if environment.SystemPrompt == "none" {
			delete(payload, "system")
		}
	} else if environment.Protocol == "responses" && !environment.OAuth {
		delete(payload, "instructions")
		payload["max_output_tokens"] = 4096
	}
}

// TestModelTraceAccountConnection runs exactly three independent prompts, with
// no attribution-driven retries, failover, recovery or scheduling decisions.
// Validation happens before committing SSE headers so normal HTTP errors work.
func (s *AccountTestService) TestModelTraceAccountConnection(c *gin.Context, accountID int64, model, effort string) error {
	account, err := s.accountRepo.GetByID(c.Request.Context(), accountID)
	if err != nil {
		return err
	}
	if _, err := modelTraceEnvironmentForAccount(account, model); err != nil {
		return err
	}
	if _, loaded := s.modelTraceAccounts.LoadOrStore(accountID, struct{}{}); loaded {
		return infraerrors.Conflict("MODELTRACE_BUSY", "A ModelTrace test is already running for this account")
	}
	defer s.modelTraceAccounts.Delete(accountID)
	challenges, err := modeltrace.Challenges()
	if err != nil {
		return err
	}
	bank, err := modeltrace.BankMetadata()
	if err != nil {
		return err
	}
	result := ModelTraceResult{RequestedModel: model, ReasoningEffort: effort, Bank: bank, CreatedAt: time.Now(), Status: "insufficient"}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("X-Accel-Buffering", "no")
	stop := startAccountTestSSEKeepalive(c, 10*time.Second)
	defer stop()
	s.sendEvent(c, TestEvent{Type: "modeltrace_start", Data: gin.H{"challenges": challenges, "bank": bank}})
	err = runModelTraceSamples(c.Request.Context(), challenges, func(ctx context.Context, challenge modeltrace.Challenge) ModelTraceSample {
		return s.runModelTraceSample(ctx, accountID, model, effort, challenge)
	}, func(sample ModelTraceSample) {
		result.Samples = append(result.Samples, sample)
		s.sendEvent(c, TestEvent{Type: "modeltrace_sample", Data: sample})
	})
	if err != nil {
		return err
	}
	completeModelTraceResult(&result)
	s.sendEvent(c, TestEvent{Type: "modeltrace_complete", Data: result, Success: true})
	return c.Request.Context().Err()
}

func runModelTraceSamples(ctx context.Context, challenges []modeltrace.Challenge, run func(context.Context, modeltrace.Challenge) ModelTraceSample, emit func(ModelTraceSample)) error {
	for _, challenge := range challenges {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The total round is bounded by three per-sample deadlines. Each sample
		// completes before the next one starts, including releasing its body.
		sampleCtx, cancel := context.WithTimeout(ctx, 240*time.Second)
		sample := run(sampleCtx, challenge)
		cancel()
		if err := ctx.Err(); err != nil {
			return err
		}
		emit(sample)
	}
	return nil
}

func completeModelTraceResult(result *ModelTraceResult) {
	var outputs []modeltrace.Output
	predictions := make(map[string]bool)
	for _, sample := range result.Samples {
		if sample.Accepted {
			outputs = append(outputs, modeltrace.Output{Text: sample.Text, ExpectedCount: sample.Challenge.ExpectedCount})
			if sample.Analysis != nil {
				predictions[sample.Analysis.Prediction] = true
			}
		}
	}
	result.Analysis, _ = modeltrace.Analyze(outputs)
	result.Status = "insufficient"
	if result.Analysis != nil {
		result.Status = "partial"
		if result.Analysis.UsedOutputs == 3 {
			result.Status = "complete"
		}
	}
	result.Disagreement = len(predictions) > 1
}

func (s *AccountTestService) runModelTraceSample(ctx context.Context, accountID int64, model, effort string, challenge modeltrace.Challenge) (sample ModelTraceSample) {
	started := time.Now()
	sample = ModelTraceSample{Challenge: challenge, StartedAt: started, MinimumNumbers: modeltrace.MinimumNumbers(challenge.ExpectedCount)}
	defer func() { sample.LatencyMs = time.Since(started).Milliseconds() }()
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil {
		sample.Error = "account_unavailable"
		return sample
	}
	environment, err := modelTraceEnvironmentForAccount(account, model)
	if err != nil {
		sample.Error = "unsupported"
		return sample
	}
	sample.Environment = environment
	ctx = context.WithValue(ctx, modelTraceContextKey{}, environment)
	ctx = withPelicanTestOptions(ctx, pelicanTestOptions{observeOnly: true})
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &pelicanRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	c, _ := gin.CreateTestContext(w)
	c.Request = (&http.Request{Header: make(http.Header)}).WithContext(ctx)
	err = s.TestPelicanAccountConnection(c, accountID, model, challenge.Prompt, effort)
	text, message := parsePelicanOutput(w.Body.String())
	if w.overflow || len(text) > 64<<10 {
		sample.Error = "output_limit"
		return sample
	}
	// Only normal completion is eligible; partial output remains visible but
	// never enters the aggregate, even if it contains enough integers.
	sample.Text = text
	for _, line := range strings.Split(w.Body.String(), "\n") {
		var event TestEvent
		if data, ok := strings.CutPrefix(line, "data:"); ok && json.Unmarshal([]byte(data), &event) == nil && event.Type == "test_start" {
			sample.MappedModel = event.Model
		}
	}
	sample.ParsedNumbers = len(modeltrace.ParseNumbers(text))
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		sample.Error = "timeout"
	case err != nil || message != "":
		// Do not persist provider error bodies, which may echo credentials.
		sample.Error = "generation_failed"
	case sample.ParsedNumbers < sample.MinimumNumbers:
		sample.Error = "too_few_numbers"
	default:
		sample.Analysis, err = modeltrace.Analyze([]modeltrace.Output{{Text: text, ExpectedCount: challenge.ExpectedCount}})
		if err != nil {
			sample.Error = "analysis_failed"
		} else {
			sample.Accepted = true
		}
	}
	return sample
}
