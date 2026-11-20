package service

import (
	"context"
	"encoding/hex"
	"fmt"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

func (s *PelicanGroupTestService) bindPersonalBank(ctx context.Context, plan, current *PelicanGroupTestPlan, input PelicanGroupTestPlanInput) error {
	if input.PersonalBankOwnerID <= 0 || s.personalQuestions == nil {
		return infraerrors.Forbidden("PELICAN_PERSONAL_ADMIN_REQUIRED", "An administrator session is required for a personal SVG bank")
	}
	owner := input.PersonalBankOwnerID
	// Quick pause/resume must not silently switch a shared plan to another admin's bank.
	if current != nil && current.PelicanConfig != nil && current.PelicanConfig.PromptMode == PelicanPromptModePersonal && !input.UseMyQuestionBank {
		owner = current.PelicanConfig.PersonalBankOwnerID
	}
	if owner <= 0 {
		return errPelicanPersonalInvalid
	}
	if plan.Enabled {
		items, err := s.personalQuestions.GetPelicanPersonalQuestions(ctx, owner)
		if err != nil {
			return err
		}
		enabled := false
		for _, item := range items {
			enabled = enabled || item.Enabled
		}
		if !enabled {
			return errPelicanPersonalEmpty
		}
	}
	plan.PelicanConfig.PersonalBankOwnerID = owner
	plan.PelicanConfig.Prompt = ""
	return nil
}

// Freeze once before starting parallel samples. Results keep this snapshot even
// when the owner edits the bank while a model is answering.
func (s *PelicanGroupTestService) prepareGroupQuestion(ctx context.Context, plan *PelicanGroupTestPlan, roundID string) (*PelicanTestConfig, error) {
	cfg := plan.PelicanConfig
	if cfg == nil || cfg.PromptMode != PelicanPromptModePersonal {
		return freezePelicanQuestion(cfg, roundID, nil), nil
	}
	if s.personalQuestions == nil || cfg.PersonalBankOwnerID <= 0 {
		return nil, errPelicanPersonalInvalid
	}
	previousID := ""
	// Preparation failures have no question. Look through retained history so a
	// temporary empty bank does not erase the last question when it is re-enabled.
	results, _, err := s.repo.ListResults(ctx, plan.ID, 0, pelicanGroupTestKeepResults)
	if err != nil {
		return nil, fmt.Errorf("load previous SVG round: %w", err)
	}
	for _, result := range results {
		if result == nil || result.PelicanConfig == nil {
			continue
		}
		previous := result.PelicanConfig
		if previous.PersonalBankOwnerID == cfg.PersonalBankOwnerID && previous.PelicanQuestion != nil {
			previousID = previous.PelicanQuestion.ID
			break
		}
	}
	drawn, err := s.personalQuestions.NewPelicanPersonalRound(ctx, cfg.PersonalBankOwnerID, previousID)
	if err != nil {
		return nil, err
	}
	snapshot := *cfg
	question := *drawn.PelicanQuestion
	question.RoundID = fmt.Sprintf("group:%d:%s", plan.ID, roundID)
	question.Proof = hex.EncodeToString(signPelicanPersonalQuestion(s.personalQuestions.pelicanPersonalSigningKey(), cfg.PersonalBankOwnerID, question))
	snapshot.Prompt, snapshot.PelicanQuestion = question.Prompt, &question
	snapshot.ActualPrompt = intelligenceTestPrompt(&snapshot)
	return &snapshot, nil
}
