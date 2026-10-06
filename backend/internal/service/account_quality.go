package service

import (
	"context"
	"fmt"
	"strings"
)

const QualityActionObserveOnly = "observe_only"
const QualityActionRemoveModel = "remove_models"

// QualityPolicy is opt-in. Legacy connectivity/HTML tests never modify membership.
type QualityPolicy struct {
	TriggerOnUpstream5xx bool                `json:"trigger_on_upstream_5xx"`
	Judge                *QualityJudgeConfig `json:"judge,omitempty"`
	ExpectedAnswer       string              `json:"expected_answer"`
	Action               string              `json:"action"`
	RemoveGroupIDs       []int64             `json:"remove_group_ids"`
	RemoveModels         []string            `json:"remove_models,omitempty"`
	// RecoveryConcurrency is the temporary OAuth concurrency cap applied when
	// this rule quarantines an account. Zero keeps the safe default of five.
	RecoveryConcurrency int               `json:"recovery_concurrency,omitempty"`
	AutoRestore         bool              `json:"auto_restore"`
	BPS                 *QualityBPSPolicy `json:"bps,omitempty"`
}

func validateQualityPolicy(plan *ScheduledTestPlan) error {
	q := plan.PelicanConfig.Quality
	if q == nil {
		return nil
	}
	if j := q.Judge; j != nil {
		if j.GroupID <= 0 || strings.TrimSpace(j.ModelID) == "" || len(j.ModelID) > 100 || strings.TrimSpace(j.Prompt) == "" || len(j.Prompt) > 16000 {
			return fmt.Errorf("judge group, model and grading prompt are required (100/16000 byte limits)")
		}
	}
	if plan.AutoRecover {
		return fmt.Errorf("quality plans use auto_restore, not connectivity auto_recover")
	}
	if plan.PelicanConfig.QuestionKind != "candy" && plan.PelicanConfig.QuestionKind != OpenAICodexStateProbeQuestionKind {
		return fmt.Errorf("quality plans require a text answer question")
	}
	// 探针题型自带满血/降智判定，不需要参考答案与判题模型。
	if plan.PelicanConfig.QuestionKind != OpenAICodexStateProbeQuestionKind {
		if strings.TrimSpace(q.ExpectedAnswer) == "" || len(q.ExpectedAnswer) > 4000 {
			return fmt.Errorf("expected answer must be 1–4000 bytes")
		}
	} else if len(q.ExpectedAnswer) > 4000 {
		return fmt.Errorf("expected answer must be 1–4000 bytes")
	}
	if q.Action != "remove_groups" && q.Action != "disable_scheduling" && q.Action != QualityActionRemoveModel && q.Action != QualityActionEnableBPS && q.Action != QualityActionObserveOnly {
		return fmt.Errorf("invalid quality action")
	}
	if q.Action == QualityActionObserveOnly {
		q.AutoRestore = false
		q.RemoveGroupIDs = nil
		q.BPS = nil
	}
	if q.Action == QualityActionEnableBPS {
		// BPS 开启后糖果题会走 BPS 通道，判不出直连是否恢复；只有探针能绕开 BPS 继续探直连。
		if plan.PelicanConfig.QuestionKind != OpenAICodexStateProbeQuestionKind {
			return fmt.Errorf("the enable_bps action requires the state probe question")
		}
		if err := validateQualityBPSPolicy(q.BPS); err != nil {
			return err
		}
		q.RemoveGroupIDs = nil
	} else {
		q.BPS = nil
	}
	if q.Action == QualityActionRemoveModel {
		if len(q.RemoveModels) == 0 || len(q.RemoveModels) > 50 {
			return fmt.Errorf("select 1-50 models to cool down")
		}
		seen := map[string]bool{}
		for i, model := range q.RemoveModels {
			model = strings.TrimSpace(model)
			if model == "" || len(model) > 100 || strings.ContainsAny(model, "*\r\n\t") || seen[model] {
				return fmt.Errorf("invalid or duplicate cooldown model")
			}
			seen[model] = true
			q.RemoveModels[i] = model
		}
		q.RemoveGroupIDs = nil
	} else {
		q.RemoveModels = nil
	}
	if q.RecoveryConcurrency < 0 || q.RecoveryConcurrency > 10000 {
		return fmt.Errorf("recovery concurrency must be 0-10000")
	}
	if q.Action == "remove_groups" && len(q.RemoveGroupIDs) == 0 {
		return fmt.Errorf("select at least one group to remove")
	}
	if len(q.RemoveGroupIDs) > 100 {
		return fmt.Errorf("select at most 100 groups")
	}
	seen := map[int64]bool{}
	for _, id := range q.RemoveGroupIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("invalid or duplicate group ID")
		}
		seen[id] = true
	}
	return nil
}

// One completed wrong answer quarantines; restoration requires every probe to pass.
// Transport errors alone are inconclusive, never evidence of degradation.
// Multi-model rules pass only when every selected model/sample passed.
// Preserve the legacy single-model transport-error policy for existing rules.
func qualityModelOutcomes(results []*ScheduledTestResult, targets []string) map[string]string {
	outcomes := make(map[string]string, len(targets))
	for _, model := range targets {
		var samples []*ScheduledTestResult
		for _, result := range results {
			if result != nil && result.PelicanConfig != nil && result.PelicanConfig.ModelID == model {
				samples = append(samples, result)
			}
		}
		if len(samples) == 0 || !qualityRoundHasResults(samples) {
			outcomes[model] = "skipped"
		} else {
			outcomes[model] = qualityRoundOutcome(samples, false)
		}
	}
	return outcomes
}

func qualityRoundHasResults(results []*ScheduledTestResult) bool {
	for _, result := range results {
		if result == nil || result.Status != "skipped" {
			return true
		}
	}
	return false
}

func qualityRoundOutcome(results []*ScheduledTestResult, multipleModels bool) string {
	effective := make([]*ScheduledTestResult, 0, len(results))
	for _, result := range results {
		if result == nil || result.Status != "skipped" {
			effective = append(effective, result)
		}
	}
	if len(effective) == 0 {
		return "inconclusive"
	}
	outcome := qualityOutcome(effective)
	if multipleModels && outcome != "passed" {
		return "failed"
	}
	return outcome
}

func qualityOutcome(results []*ScheduledTestResult) string {
	allPassed := len(results) > 0
	for _, r := range results {
		if r != nil && r.QualityJudgment != nil && r.QualityJudgment.Verdict == "incorrect" && r.Status == "failed" &&
			(r.ErrorMessage == "answer_mismatch" || r.ErrorMessage == openAICodexStateDegradedError) {
			return "failed"
		}
		if r == nil || r.Status != "success" || r.QualityJudgment == nil || r.QualityJudgment.Verdict != "correct" {
			allPassed = false
		}
	}
	if allPassed {
		return "passed"
	}
	return "inconclusive"
}

func (s *ScheduledTestService) ListQualityPlans(ctx context.Context) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListQualityPlans(ctx)
}
func (s *ScheduledTestService) TriggerQuality(ctx context.Context, id int64) error {
	return s.planRepo.TriggerQuality(ctx, id)
}

func (s *ScheduledTestService) ListQualityHistory(ctx context.Context, beforeID int64) (*QualityHistoryPage, error) {
	items, err := s.resultRepo.ListQualityHistory(ctx, beforeID, 101)
	if err != nil {
		return nil, err
	}
	// Results are persisted individually after all model requests have ended.
	// During those writes (or after a failed write), never label a partial round
	// as passing simply because its first saved sample passed.
	for _, item := range items {
		expected := item.TotalCount
		if cfg := item.PelicanConfig; cfg != nil && len(cfg.ModelIDs) > 1 {
			if planned := len(cfg.ModelIDs) * cfg.ParallelCount; planned > expected {
				expected = planned
			}
		}
		item.TotalCount = expected - item.SkippedCount
		if item.TotalCount <= 0 {
			item.TotalCount = 0
			item.Status = "skipped"
		} else if item.PassedCount == item.TotalCount {
			item.Status = "success"
		} else {
			item.Status = "failed"
		}
	}
	page := &QualityHistoryPage{Items: items}
	if len(items) > 100 {
		page.Items = items[:100]
		page.NextCursor = items[99].ID
	}
	return page, nil
}
