package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/typesafe"
	"github.com/Wei-Shaw/sub2api/internal/pkg/xai"
)

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func qualityUnsupportedModelKey(model string) string {
	return fmt.Sprintf("quality_unsupported_model_%x", sha256.Sum256([]byte(model)))
}

// Discovery is shared with the account test picker. An unavailable catalog is
// an error, never evidence that the account does not support a model.
func (s *ScheduledTestService) accountQualityModels(ctx context.Context, id int64) ([]string, error) {
	account, err := s.accountTests.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if account == nil {
		return nil, ErrAccountNotFound
	}
	var models []string
	if account.IsOpenAI() {
		catalog, err := s.accountTests.FetchOpenAIAccountModels(ctx, account)
		if err != nil {
			return nil, err
		}
		for _, model := range catalog {
			models = append(models, model.ID)
		}
	} else if account.IsTypeSafe() {
		models = []string{typesafe.JevLatestModel}
	} else if mapping := account.GetModelMapping(); len(mapping) > 0 {
		for model := range mapping {
			models = append(models, model)
		}
	} else {
		switch account.Platform {
		case PlatformGemini:
			catalog := geminicli.DefaultModels
			if account.IsGeminiGoogleOne() {
				catalog = geminicli.GoogleOneModels
			}
			for _, model := range catalog {
				models = append(models, model.ID)
			}
		case PlatformGrok:
			for _, model := range xai.DefaultModels() {
				models = append(models, model.ID)
			}
		case PlatformAntigravity:
			for _, model := range antigravity.DefaultModels() {
				models = append(models, model.ID)
			}
		case PlatformDeepseek:
			models = defaultModelsListCandidateIDs(PlatformDeepseek)
		default:
			for _, model := range claude.DefaultModels {
				models = append(models, model.ID)
			}
		}
	}
	supported := models[:0]
	for _, model := range models {
		if account.Extra[qualityUnsupportedModelKey(model)] == nil {
			supported = append(supported, model)
		}
	}
	return supported, nil
}

func qualityPlanModels(plan *ScheduledTestPlan) []string {
	if plan.PelicanConfig != nil && len(plan.PelicanConfig.ModelIDs) > 0 {
		return plan.PelicanConfig.ModelIDs
	}
	return []string{plan.ModelID}
}

func (s *ScheduledTestService) supportedQualityModels(ctx context.Context, plan *ScheduledTestPlan) ([]string, []string, error) {
	requested := qualityPlanModels(plan)
	if plan.PelicanConfig == nil || plan.PelicanConfig.Quality == nil || s.qualityModels == nil {
		return requested, nil, nil
	}
	catalog, err := s.qualityModels(ctx, plan.AccountID)
	if err != nil {
		return nil, nil, fmt.Errorf("无法读取账号 #%d 的模型列表：%w", plan.AccountID, err)
	}
	var blocked map[string]any
	if s.accountTests != nil {
		account, err := s.accountTests.accountRepo.GetByID(ctx, plan.AccountID)
		if err != nil {
			return nil, nil, err
		}
		if account != nil {
			blocked = account.Extra
		}
	}
	var supported, unsupported []string
	for _, model := range requested {
		found := containsString(catalog, model)
		for _, pattern := range catalog {
			if strings.Contains(pattern, "*") && matchWildcard(pattern, model) {
				found = true
			}
		}
		if blocked[qualityUnsupportedModelKey(model)] != nil {
			found = false
		}
		if found {
			supported = append(supported, model)
		} else {
			unsupported = append(unsupported, model)
		}
	}
	return supported, unsupported, nil
}

func (s *ScheduledTestService) validateQualityAccountModels(ctx context.Context, plan *ScheduledTestPlan) error {
	_, unsupported, err := s.supportedQualityModels(ctx, plan)
	if err != nil {
		return err
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("账号 #%d 不支持检测模型：%s", plan.AccountID, strings.Join(unsupported, ", "))
	}
	return nil
}

// Work on a private snapshot: the group template retains the complete selection.
func (s *ScheduledTestService) filterQualityTemplatePlan(ctx context.Context, plan *ScheduledTestPlan) (bool, error) {
	supported, _, err := s.supportedQualityModels(ctx, plan)
	if err != nil {
		return false, err
	}
	if len(supported) == 0 {
		return false, nil
	}
	plan.PelicanConfig = cloneQualityPelicanConfig(plan.PelicanConfig)
	plan.PelicanConfig.ModelIDs = append([]string(nil), supported...)
	plan.ModelID = supported[0]
	return true, nil
}

// Match explicit model errors only; invalid tools, transient failures and
// authorization errors must never permanently suppress a model.
var qualityUnsupportedModelMessage = regexp.MustCompile(`(?i)\bmodel\b.{0,160}\b(?:not supported|not found|does not exist)\b`)

func qualityModelUnsupported(message string) bool {
	lower := strings.ToLower(message)
	for _, code := range []string{"model_not_found", "model_not_supported", "unsupported_model", "model_unsupported"} {
		if strings.Contains(lower, code) {
			return true
		}
	}
	if !strings.Contains(lower, "model") && !strings.Contains(lower, "模型") {
		return false
	}
	return qualityUnsupportedModelMessage.MatchString(lower) || strings.Contains(lower, "model is not supported") || strings.Contains(lower, "model not supported") ||
		strings.Contains(lower, "does not support this model") || strings.Contains(lower, "not supported with") ||
		strings.Contains(lower, "not supported by any configured account") || strings.Contains(lower, "model") && strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "不支持该模型") || strings.Contains(lower, "模型不支持") || strings.Contains(lower, "模型不存在")
}

func (s *ScheduledTestService) rememberUnsupportedQualityModel(ctx context.Context, id int64, model string) error {
	if s.accountTests == nil {
		return nil
	}
	// Independent keys use the native atomic Extra merge, so concurrent rules
	// cannot overwrite each other's observations or unrelated account settings.
	return s.accountTests.accountRepo.UpdateExtra(ctx, id, map[string]any{qualityUnsupportedModelKey(model): model})
}
